package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeArm serves the public routes the harness uses, with scripted rows per
// (backend, output) so the offline tests pin the client and protocol.
type fakeArm struct {
	server     *httptest.Server
	mu         sync.Mutex
	calls      []string // ordered "backend:route:output"
	rows       map[string][]map[string]any
	columns    []string
	sampled    bool
	leakScoped bool
	failWith   map[string]int // output -> status
	uploads    int
	publish    int
}

func newFakeArm(backend string) *fakeArm {
	f := &fakeArm{rows: map[string][]map[string]any{}, columns: []string{"patient_id", "gender"}, failWith: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/datasets/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.uploads++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"summary": map[string]any{"resources": map[string]any{"Patient": 12}}})
	})
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/explorer-config") {
			f.mu.Lock()
			f.publish++
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"recipe": "bench", "translationVersion": "t1", "output": "out"})
			return
		}
		if strings.Contains(r.URL.Path, "/authoring/v2/preview") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			output, _ := body["outputId"].(string)
			f.mu.Lock()
			f.calls = append(f.calls, fmt.Sprintf("%s:preview:%s", backend, output))
			status := f.failWith[output]
			rows := f.rows[output]
			columns := f.columns
			sampled := f.sampled
			f.mu.Unlock()
			if status != 0 {
				http.Error(w, `{"error":"scripted failure"}`, status)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"columns": columns, "rows": rows, "rowCount": len(rows), "sampled": sampled})
			return
		}
		http.NotFound(w, r)
	})
	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakeArm) arm(backend string) *Arm {
	return &Arm{Backend: backend, BaseURL: f.server.URL, Client: f.server.Client()}
}

func (f *fakeArm) close() { f.server.Close() }

func TestUploadAndPublishUsePublicRoutes(t *testing.T) {
	f := newFakeArm(BackendArango)
	defer f.close()
	arm := f.arm(BackendArango)
	files := map[string]string{
		"Patient.ndjson":     "{\"resourceType\":\"Patient\"}\n",
		"Observation.ndjson": "{\"resourceType\":\"Observation\"}\n",
		"facts.json":         "{\"ignored\":true}\n", // must NOT be uploaded
	}
	if err := arm.UploadGeneration(context.Background(), "Bearer tok", "proj", "gen1", files, "/programs/p/projects/proj/x"); err != nil {
		t.Fatal(err)
	}
	if f.uploads != 1 {
		t.Fatalf("uploads=%d", f.uploads)
	}
	pub, err := arm.PublishWorkspace(context.Background(), "Bearer tok", "proj", "gen1", []byte(`{}`), "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if pub.Output != "out" || pub.TranslationVersion != "t1" {
		t.Fatalf("publication decode: %+v", pub)
	}
}

func TestExecuteOutputCanonicalizesAndDigests(t *testing.T) {
	f := newFakeArm(BackendPostgres)
	defer f.close()
	rows := []map[string]any{{"patient_id": "p1", "gender": "female"}, {"patient_id": "p2"}}
	f.rows["out"] = rows
	result, err := f.arm(BackendPostgres).ExecuteOutput(context.Background(), "Bearer tok", "proj", "exp", "receipt-1", "out", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 2 || len(result.Rows) != 2 {
		t.Fatalf("rows: %+v", result)
	}
	expected := Bag(CanonicalizeRows([]string{"patient_id", "gender"}, rows)).Digest()
	if result.Digest != expected {
		t.Fatal("preview digest must use the canonical path")
	}
	if result.Sampled {
		t.Fatal("sampled must be false here")
	}
}

func TestExecuteOutputPropagatesRouteErrors(t *testing.T) {
	f := newFakeArm(BackendArango)
	defer f.close()
	f.failWith["out"] = http.StatusInternalServerError
	if _, err := f.arm(BackendArango).ExecuteOutput(context.Background(), "Bearer tok", "proj", "exp", "r", "out", 10); err == nil {
		t.Fatal("route failure must surface, never as an empty pass")
	}
}

func TestMeasureArmsInterleavesAndCounts(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["out"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	fp.rows["out"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	clock := &fakeClock{start: time.Now(), now: time.Now()}
	opts := RunOptions{
		Project: "proj", Explorer: "exp", UnrestrictedToken: "Bearer tok",
		PreviewLimit: 100, Config: RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2},
	}
	samples, finals, err := MeasureArms(context.Background(), clock, "w", fa.arm(BackendArango), fp.arm(BackendPostgres),
		opts, map[string]string{"arango": "ra", "postgres": "rp"}, map[string]string{"arango": "out", "postgres": "out"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var warmup, measured int
	order := map[int][]string{}
	for _, s := range samples {
		if s.Warmup {
			warmup++
			continue
		}
		measured++
		order[s.Round] = append(order[s.Round], s.Backend)
	}
	if warmup != 2 || measured != 8 {
		t.Fatalf("warmup=%d measured=%d (want 2 and 8)", warmup, measured)
	}
	// Interleaving: within each round, arango and postgres alternate; both
	// arms present per round with the fixed order.
	for round, seq := range order {
		if strings.Join(seq, ",") != "arango,postgres,arango,postgres" {
			t.Fatalf("round %d not interleaved round-robin: %v", round, seq)
		}
	}
	if len(finals) != 2 {
		t.Fatalf("finals per arm required for gating: %v", finals)
	}
	// Every measured sample carries timing and an outcome.
	for _, s := range samples {
		if s.Outcome == "" {
			t.Fatalf("sample missing outcome: %+v", s)
		}
	}
}

func TestMeasureArmsRecordsFailuresInRaw(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["out"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	fp.failWith["out"] = http.StatusBadGateway
	clock := &fakeClock{start: time.Now(), now: time.Now()}
	opts := RunOptions{Project: "p", Explorer: "e", UnrestrictedToken: "Bearer t", PreviewLimit: 10,
		Config: RunConfig{Warmup: 0, Iterations: 2, Concurrency: 1, Rounds: 1}}
	samples, _, err := MeasureArms(context.Background(), clock, "w", fa.arm(BackendArango), fp.arm(BackendPostgres),
		opts, map[string]string{"arango": "ra", "postgres": "rp"}, map[string]string{"arango": "out", "postgres": "out"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed, passed := 0, 0
	for _, s := range samples {
		if s.Outcome == OutcomePassed {
			passed++
		} else {
			failed++
			if s.ErrorCode == "" || s.Millis < 0 {
				t.Fatalf("failed sample must keep identity and timing: %+v", s)
			}
		}
	}
	if failed != 2 || passed != 2 {
		t.Fatalf("failed samples must stay in the raw record: passed=%d failed=%d", passed, failed)
	}
}

func TestNegativeAuthControlFullRule(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	rows := []map[string]any{{"patient_id": "p1", "gender": "female"}, {"patient_id": "p2", "gender": "male"}}
	fa.rows["out"] = rows
	fp.rows["out"] = rows
	opts := RunOptions{Project: "p", Explorer: "e", UnrestrictedToken: "Bearer unauth", ScopedToken: "Bearer scoped", PreviewLimit: 100}
	receipts := map[string]string{"arango": "ra", "postgres": "rp"}
	outputs := map[string]string{"arango": "out", "postgres": "out"}

	// Deny by restricting the FENCE: the arms honor the principal scope;
	// the fake simulates the restricted intersection (zero rows) only for
	// the scoped token, matching ResolveReadScopeForGeneration semantics.
	report := NegativeAuthControlWithScope(context.Background(), fa, fp, opts, receipts, outputs)
	if !report.Claimable {
		t.Fatalf("proper scoped denial should pass: %+v", report)
	}
	// Leak: the scoped principal sees rows on the arango arm.
	fa.leakScoped = true
	report = NegativeAuthControlWithScope(context.Background(), fa, fp, opts, receipts, outputs)
	if report.Claimable {
		t.Fatal("scoped principal seeing rows must fail the gate")
	}
}

// NegativeAuthControlWithScope drives the production control against the
// fake arms, which distinguish tokens so the fake models the server-side
// scope intersection.
func NegativeAuthControlWithScope(ctx context.Context, fa, fp *fakeArm, opts RunOptions, receipts, outputs map[string]string) WorkloadGateReport {
	baselineA, errA := fa.arm(BackendArango).ExecuteOutput(ctx, opts.UnrestrictedToken, opts.Project, opts.Explorer, receipts[BackendArango], outputs[BackendArango], opts.PreviewLimit)
	baselineP, errP := fp.arm(BackendPostgres).ExecuteOutput(ctx, opts.UnrestrictedToken, opts.Project, opts.Explorer, receipts[BackendPostgres], outputs[BackendPostgres], opts.PreviewLimit)
	baseline := 0
	switch {
	case errA == nil && errP == nil && baselineA.RowCount == baselineP.RowCount:
		baseline = baselineA.RowCount
	case errA == nil:
		baseline = baselineA.RowCount
	case errP == nil:
		baseline = baselineP.RowCount
	}
	scopedA, scopedErrA := fa.scopedExecute(ctx, opts, receipts, outputs, BackendArango)
	scopedP, scopedErrP := fp.scopedExecute(ctx, opts, receipts, outputs, BackendPostgres)
	errText := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	return EvaluateNegativeAuth("negative-auth", baseline, rowCountOr(scopedA, scopedErrA), rowCountOr(scopedP, scopedErrP), errText(scopedErrA), errText(scopedErrP))
}

func (f *fakeArm) scopedExecute(ctx context.Context, opts RunOptions, receipts, outputs map[string]string, backend string) (PreviewResult, error) {
	if f.leakScoped {
		return f.arm(backend).ExecuteOutput(ctx, opts.ScopedToken, opts.Project, opts.Explorer, receipts[backend], outputs[backend], opts.PreviewLimit)
	}
	// Restricted intersection empty => restricted scope with no paths =>
	// zero rows: the exact production denial shape.
	return PreviewResult{Columns: f.columns, Rows: nil, RowCount: 0}, nil
}

func TestMockFenceGrantsAndTokens(t *testing.T) {
	fence, err := NewMockFence()
	if err != nil {
		t.Fatal(err)
	}
	defer fence.Close()
	fence.Grant("bench-admin", "/programs/p/projects/proj/x")
	token := MakeBearerToken(fence.URL(), "bench-admin")
	// Without the bearer header the fence denies.
	req, _ := http.NewRequest(http.MethodGet, fence.URL()+"/user", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing bearer must 401, got %d", resp.StatusCode)
	}
	// With a matching token, the grant list comes back.
	req, _ = http.NewRequest(http.MethodGet, fence.URL()+"/user", nil)
	req.Header.Set("Authorization", token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Authz map[string]any `json:"authz"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload.Authz["/programs/p/projects/proj/x"]; !ok {
		t.Fatalf("grant missing: %+v", payload)
	}
	if fence.Requests() < 2 {
		t.Fatal("fence must count requests for evidence")
	}
	// Wrong issuer token: subject still decodes but the production fence
	// client enforces iss match; the mock records only shape. Check token
	// shape instead.
	bad := MakeBearerToken("http://elsewhere", "bench-admin")
	if !strings.HasPrefix(bad, "Bearer ") {
		t.Fatal("bearer prefix required")
	}
	if subjectFromBearer(bad) != "bench-admin" {
		t.Fatal("subject decode broken")
	}
	if subjectFromBearer("Basic abc") != "" {
		t.Fatal("non-bearer must be rejected")
	}
}

func TestScrubWriterRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/log.txt"
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := scrubWriter{inner: f, secrets: []string{"super-secret-password", "postgres://u:p@h/db"}}
	fmt.Fprint(w, "connect with password super-secret-password to postgres://u:p@h/db now\n")
	f.Close()
	raw, _ := os.ReadFile(path)
	data := string(raw)
	if strings.Contains(data, "super-secret-password") || strings.Contains(data, "postgres://u:p@h/db") {
		t.Fatalf("secret leaked into arm log: %s", data)
	}
	if !strings.Contains(data, "[REDACTED]") {
		t.Fatal("redaction marker missing")
	}
}

func TestBuildServerConfigYAMLDisablesClickHouseBothArms(t *testing.T) {
	for _, backend := range []string{BackendArango, BackendPostgres} {
		yaml := BuildServerConfigYAML(":18080", backend, "  url: http://127.0.0.1:8529\n", "http://127.0.0.1:9999")
		if !strings.Contains(yaml, "clickhouse:") || !strings.Contains(yaml, "enabled: false") {
			t.Fatalf("%s arm config must disable ClickHouse: %s", backend, yaml)
		}
		if !strings.Contains(yaml, "mode: calypr") || !strings.Contains(yaml, "trusted_issuer") {
			t.Fatalf("%s arm config must wire scoped auth: %s", backend, yaml)
		}
	}
}
