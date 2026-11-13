package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	loomexplorer "github.com/calypr/loom/internal/explorer"
)

// fakeArm serves the public routes the harness uses, with scripted rows per
// (backend, output) so the offline tests pin the client and protocol.
type fakeArm struct {
	server      *httptest.Server
	mu          sync.Mutex
	calls       []string // ordered "backend:route:output"
	rows        map[string][]map[string]any
	columns     []string
	sampled     bool
	leakScoped  bool
	failWith    map[string]int // output -> status
	uploads     int
	publish     int
	published   [][]byte // explorer-config bodies in publish order
	noSnapshot  bool
	scopedToken string // when the bearer equals this, deny like production: zero rows
	explorerID  string
	authoring   []string // ordered create/builder/reconcile calls
	handler     http.Handler
	extra       []*http.Server // serveOn listeners, closed with the fake
	// authoring publish + active-state readback scripts
	publishOutputs   []PublicationOutput
	publishState     string
	publishEchoBad   bool
	publishNoOutputs bool // answers with an empty outputs array
	activeDigest     string
	activeRevision   string
	// forceEmptyActiveState serves the server-legal state whose draft
	// digest is empty, so the fail-closed abort path is testable.
	forceEmptyActiveState bool
	activationBody        string // when set, publish/explorer-config answer 503 with this body
}

func (f *fakeArm) publishedBodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, 0, len(f.published))
	for _, body := range f.published {
		out = append(out, append([]byte(nil), body...))
	}
	return out
}

func (f *fakeArm) authoringCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.authoring...)
}

// routeCalls exposes the ordered "backend:route:key" call log, so driver
// tests can assert which routes a lane actually exercised.
func (f *fakeArm) routeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func newFakeArm(backend string) *fakeArm {
	f := &fakeArm{rows: map[string][]map[string]any{}, columns: []string{"patient_id", "gender"}, failWith: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/explorers") && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			name, _ := body["name"].(string)
			source, _ := body["sourceExplorerId"].(string)
			if source != "default" {
				http.Error(w, `{"error":"sourceExplorerId must be default"}`, 400)
				return
			}
			f.mu.Lock()
			f.authoring = append(f.authoring, "create:"+name)
			explorerID := f.explorerID
			if explorerID == "" {
				explorerID = "exp-created"
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"explorerId": explorerID})
			return
		case strings.Contains(path, "/authoring/v2/builder"):
			explorer := path[strings.LastIndex(path, "/explorers/")+len("/explorers/") : strings.Index(path, "/authoring")]
			f.mu.Lock()
			f.authoring = append(f.authoring, "builder:"+explorer)
			noSnap := f.noSnapshot
			f.mu.Unlock()
			state := map[string]any{"draftVersion": 1, "draftDigest": "dig-1",
				"catalog": contributorCatalogFixture()}
			if noSnap {
				state = map[string]any{"draftVersion": 1}
			}
			_ = json.NewEncoder(w).Encode(state)
			return
		case strings.Contains(path, "/explorer-config"):
			f.mu.Lock()
			activation := f.activationBody
			f.mu.Unlock()
			if activation != "" {
				http.Error(w, activation, http.StatusServiceUnavailable)
				return
			}
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.publish++
			f.published = append(f.published, body)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"recipe": "bench", "translationVersion": "t1", "receiptId": "revpub-1", "revisionId": "rev-1", "executionId": "exec-1", "activated": true})
			return
		case strings.HasSuffix(path, "/authoring/v2/publish") && r.Method == http.MethodPost:
			f.mu.Lock()
			activation := f.activationBody
			f.mu.Unlock()
			if activation != "" {
				http.Error(w, activation, http.StatusServiceUnavailable)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			receiptID, _ := body["receiptId"].(string)
			f.mu.Lock()
			f.calls = append(f.calls, fmt.Sprintf("%s:publish:%s", backend, receiptID))
			outs, state, echo, noOuts := f.publishOutputs, f.publishState, f.publishEchoBad, f.publishNoOutputs
			f.mu.Unlock()
			if state == "" {
				state = "ACTIVE"
			}
			answer := receiptID
			if echo {
				answer = "receipt-other"
			}
			if len(outs) == 0 && !noOuts {
				outs = []PublicationOutput{{OutputID: "out", State: "READY", MaterializationID: "mat-" + receiptID}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"receiptId": answer, "revisionId": "authoring-test", "state": state, "outputs": outs})
			return
		case r.Method == http.MethodGet && strings.Contains(path, "/explorers/") && !strings.Contains(path[strings.LastIndex(path, "/explorers/")+len("/explorers/"):], "/"):
			f.mu.Lock()
			digest, rev, forceEmpty := f.activeDigest, f.activeRevision, f.forceEmptyActiveState
			f.mu.Unlock()
			if !forceEmpty {
				if digest == "" {
					digest = "sha256:active-default"
				}
				if rev == "" {
					rev = "authoring-active-default"
				}
			}
			// The mock follows the server: it marshals the real
			// ExplorerStateV1 the route actually answers, so the
			// client and the mock cannot share a wrong shape.
			id := path[strings.LastIndex(path, "/explorers/")+len("/explorers/"):]
			state := loomexplorer.ExplorerStateV1{
				APIVersion: loomexplorer.ExplorerStateV1APIVersion,
				Kind:       loomexplorer.ExplorerStateV1Kind,
				Project:    "proj",
				ExplorerID: id,
				Management: loomexplorer.ManagementInteractive,
				Draft:      loomexplorer.ExplorerStateV1Draft{Version: 1, Digest: digest},
				Active:     loomexplorer.ExplorerStateV1Active{RevisionID: rev, Status: "PUBLISHED"},
			}
			state.ActiveURL = "/api/v1/projects/proj/explorers/" + id
			_ = json.NewEncoder(w).Encode(state)
			return
		case strings.Contains(path, "/authoring/v2/preview"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			output, _ := body["outputId"].(string)
			rows := f.rows[output]
			if f.scopedToken != "" && r.Header.Get("Authorization") == f.scopedToken && !f.leakScoped {
				// production denial shape: restricted intersection => rows: []
				rows = []map[string]any{}
			}
			f.mu.Lock()
			f.calls = append(f.calls, fmt.Sprintf("%s:preview:%s", backend, output))
			status := f.failWith[output]
			columns := f.columns
			sampled := f.sampled
			f.mu.Unlock()
			if status != 0 {
				http.Error(w, `{"error":"scripted failure"}`, status)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"columns": columns, "rows": rows, "rowCount": len(rows), "sampled": sampled})
			return
		case strings.Contains(path, "/authoring/v2/reconcile"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["snapshotToken"] != "snap-1" || body["draftVersion"].(float64) != 1 {
				http.Error(w, `{"error":"stale draft token"}`, 409)
				return
			}
			output, _ := body["outputId"].(string)
			explorer := path[strings.LastIndex(path, "/explorers/")+len("/explorers/") : strings.Index(path, "/authoring")]
			f.mu.Lock()
			f.authoring = append(f.authoring, "reconcile:"+explorer+":"+output)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"receiptId": "rcpt-" + output})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/api/v1/datasets/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.uploads++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"summary": map[string]any{"resources": map[string]any{"Patient": 12}}})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	f.server = httptest.NewServer(mux)
	f.handler = mux
	return f
}

// serveOn binds the fake's handler on an extra address, so a spawned arm's
// BaseURL (chosen by the driver from a free port) reaches the same routes
// the attached fake serves.
func (f *fakeArm) serveOn(listenAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: f.handler}
	f.mu.Lock()
	f.extra = append(f.extra, srv)
	f.mu.Unlock()
	go func() { _ = srv.Serve(ln) }()
	return nil
}

func (f *fakeArm) arm(backend string) *Arm {
	return &Arm{Backend: backend, BaseURL: f.server.URL, Client: f.server.Client()}
}

// contributorCatalogFixture is the builder catalog the fake serves: exactly
// the candidates the battery's aggregate predicates resolve against, plus
// the occurrence nodes the migration looks up by resource type.
func contributorCatalogFixture() map[string]any {
	return map[string]any{
		"snapshotToken": "snap-1", "generation": "gen1",
		"authorizationScopeDigest": "scope-digest-1",
		"complete":                 true, "truncated": false,
		"nodes": []map[string]any{
			{"nodeId": "patient", "resourceType": "Patient"},
			{"nodeId": "observation", "resourceType": "Observation"},
			{"nodeId": "specimen", "resourceType": "Specimen"},
			{"nodeId": "condition", "resourceType": "Condition"},
		},
		"candidates": []map[string]any{
			{"candidateId": "cand-obs-age-code", "nodeId": "observation", "fieldPath": "code.coding[].code",
				"logicalType": "code", "repeated": true,
				"repeatedBoundaries": []map[string]any{{"path": "code.coding[]", "maxItems": 20}}},
			{"candidateId": "cand-spec-type-code", "nodeId": "specimen", "fieldPath": "type.coding[].code",
				"logicalType": "code", "repeated": true,
				"repeatedBoundaries": []map[string]any{{"path": "type.coding[]", "maxItems": 20}}},
		},
	}
}

func (f *fakeArm) close() {
	f.server.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, srv := range f.extra {
		_ = srv.Close()
	}
}

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
	if pub.Recipe != "bench" || pub.TranslationVersion != "t1" || pub.ReceiptID != "revpub-1" || pub.RevisionID != "rev-1" {
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
		yaml := BuildServerConfigYAML(":18080", backend, "  url: http://127.0.0.1:8529\n", "http://127.0.0.1:9999", "bench-cursor-secret-0123456789ab", nil)
		if !strings.Contains(yaml, "clickhouse:") || !strings.Contains(yaml, "enabled: false") {
			t.Fatalf("%s arm config must disable ClickHouse: %s", backend, yaml)
		}
		if strings.Contains(yaml, "dataframer") {
			t.Fatalf("%s arm config must not name a recipe without ClickHouse: %s", backend, yaml)
		}
		if !strings.Contains(yaml, "mode: calypr") || !strings.Contains(yaml, "trusted_issuer") {
			t.Fatalf("%s arm config must wire scoped auth: %s", backend, yaml)
		}
	}
}

func TestBuildServerConfigYAMLEnablesChainWithOwnDatabase(t *testing.T) {
	ch := &ClickHouseSpec{URL: "clickhouse://127.0.0.1:9000", Database: "loom_bench_postgres", Username: "loom_bench", Password: "ci-only", Recipe: "/repo/testdata/devloop-fixture/recipe.json"}
	yaml := BuildServerConfigYAML(":18080", BackendPostgres, "  host: x\n", "http://127.0.0.1:9999", "bench-cursor-secret-0123456789ab", ch)
	for _, want := range []string{"enabled: true", `url: "clickhouse://127.0.0.1:9000"`, `database: "loom_bench_postgres"`, `username: "loom_bench"`, `password: "ci-only"`, "dataframer", `recipe: "/repo/testdata/devloop-fixture/recipe.json"`} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("enabled arm config missing %q: %s", want, yaml)
		}
	}
	if strings.Contains(yaml, "enabled: false") {
		t.Fatalf("enabled arm config must not also disable ClickHouse: %s", yaml)
	}
}

func TestAuthorWorkloadFlowPinsRoutesAndTokens(t *testing.T) {
	f := newFakeArm(BackendPostgres)
	defer f.close()
	arm := f.arm(BackendPostgres)
	explorerID, receiptID, err := arm.AuthorWorkload(context.Background(), "Bearer t", "proj", "gen1",
		[]byte(`{"kind":"ExplorerBuilderWorkspace"}`), "abc", "bench-wl", "out_1")
	if err != nil {
		t.Fatal(err)
	}
	if explorerID != "exp-created" || receiptID != "rcpt-out_1" {
		t.Fatalf("unexpected identities: %s %s", explorerID, receiptID)
	}
	if f.publish != 1 {
		t.Fatalf("workspace must be published exactly once, got %d", f.publish)
	}
	if got := f.authoringCalls(); len(got) != 3 ||
		got[0] != "create:bench-wl" || got[1] != "builder:exp-created" || got[2] != "reconcile:exp-created:out_1" {
		t.Fatalf("route order wrong: %v", got)
	}
}

func TestFetchBuilderStateRefusesMissingToken(t *testing.T) {
	f := newFakeArm(BackendPostgres)
	defer f.close()
	f.noSnapshot = true
	arm := f.arm(BackendPostgres)
	if _, err := arm.FetchBuilderState(context.Background(), "Bearer t", "proj", "exp-1"); err == nil ||
		!strings.Contains(err.Error(), "snapshot token") {
		t.Fatalf("missing snapshot token must fail closed, got %v", err)
	}
}

// --- cache-label contract (review r4559430564) --------------------------------

// spawnSleeperProcess starts a real OS process (sh+sleep) that stands in
// for a spawned arm whose HTTP surface is the fakeArm server: restarts are
// genuine process restarts, observable by the harness without any backend.
func spawnSleeperProcess(t *testing.T, readyURL, backend string) *ServerProcess {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "dummy-arm.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := ServerSpec{
		Backend: backend, BinaryPath: script, ConfigYAML: "listen: unused",
		ReadyURL: readyURL, Timeout: 15 * time.Second, StderrDir: dir,
	}
	proc, err := SpawnServer(context.Background(), spec, nil, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("spawn dummy arm: %v", err)
	}
	t.Cleanup(proc.Stop)
	return proc
}

func TestMeasureArmsColdLabelContract(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["w"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	fp.rows["w"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	arango := fa.arm(BackendArango)
	arango.Proc = spawnSleeperProcess(t, ready.URL, BackendArango)
	postgres := fp.arm(BackendPostgres) // attached arm: Proc == nil, never cold

	restarts := 0
	cold := func(ctx context.Context, arm *Arm) error {
		if arm == nil || arm.Proc == nil {
			return nil
		}
		restarts++
		return arm.Proc.Restart(ctx)
	}
	clock := &scriptedClock{base: time.Unix(1700000000, 0)}
	opts := RunOptions{Project: "p", Generation: "g", Explorer: "w", UnrestrictedToken: "Bearer x", PreviewLimit: 100,
		Config: RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2}}
	samples, finals, err := MeasureArms(context.Background(), clock, "w", arango, postgres, opts,
		map[string]string{BackendArango: "r-a", BackendPostgres: "r-p"},
		map[string]string{BackendArango: "w", BackendPostgres: "w"}, cold)
	if err != nil {
		t.Fatal(err)
	}
	if restarts != 2 {
		t.Fatalf("restarts=%d want 2 (one per round, owned arms only)", restarts)
	}
	if _, ok := finals[BackendArango]; !ok {
		t.Fatal("arango final preview missing")
	}
	for _, s := range samples {
		if s.Outcome != OutcomePassed {
			t.Fatalf("sample outcome %s: %s", s.Outcome, s.ErrorCode)
		}
		switch s.Backend {
		case BackendArango:
			if s.Warmup {
				if s.Cache != CacheWarm {
					t.Fatalf("warmup sample on owned arm must be warm, got %s", s.Cache)
				}
				continue
			}
			// itersPerRound == 2: the first iteration of each round
			// directly followed a successful restart; the second is warm.
			firstOfRound := (s.Iteration-1)%2 == 0
			want := CacheWarm
			if firstOfRound {
				want = CacheProcessCold
			}
			if s.Cache != want {
				t.Fatalf("arango round %d iteration %d: cache %s want %s", s.Round, s.Iteration, s.Cache, want)
			}
		case BackendPostgres:
			if s.Cache != CacheServiceWarm {
				t.Fatalf("attached arm labeled %s on round %d iteration %d: attached arms are service-warm regardless of the peer being spawned",
					s.Cache, s.Round, s.Iteration)
			}
		}
	}
}

func TestMeasureArmsFailedColdRestartIsLabeled(t *testing.T) {
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["w"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	fp.rows["w"] = []map[string]any{{"patient_id": "p", "gender": "female"}}
	arango := fa.arm(BackendArango)
	// A process owner that never started: every Restart attempt fails with
	// "arm never started", the cheapest honest reproduction of a failed
	// cold restart.
	arango.Proc = &ServerProcess{}
	postgres := fp.arm(BackendPostgres)
	cold := func(ctx context.Context, arm *Arm) error {
		if arm == nil || arm.Proc == nil {
			return nil
		}
		return arm.Proc.Restart(ctx)
	}
	clock := &scriptedClock{base: time.Unix(1700000000, 0)}
	opts := RunOptions{Project: "p", Generation: "g", Explorer: "w", UnrestrictedToken: "Bearer x", PreviewLimit: 100,
		Config: RunConfig{Warmup: 1, Iterations: 4, Concurrency: 1, Rounds: 2}}
	samples, _, err := MeasureArms(context.Background(), clock, "w", arango, postgres, opts,
		map[string]string{BackendArango: "r-a", BackendPostgres: "r-p"},
		map[string]string{BackendArango: "w", BackendPostgres: "w"}, cold)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for _, s := range samples {
		if s.Backend != BackendArango || s.Warmup {
			continue
		}
		if s.Outcome == OutcomeError && strings.Contains(s.ErrorCode, "cold restart:") {
			failures++
			// The failed attempt must sit at its position with the
			// regime it attempted, never as a naked hole (review:
			// empty Cache, no Round/Iteration).
			if s.Cache != CacheProcessCold {
				t.Fatalf("cold-restart failure sample lacks its regime label: %q", s.Cache)
			}
			if s.Round < 1 || s.Iteration < 1 {
				t.Fatalf("cold-restart failure sample lacks Round/Iteration: %+v", s)
			}
			if s.Millis != 0 {
				t.Fatalf("failed restart must not report a duration: %v", s.Millis)
			}
			continue
		}
		if s.Cache != CacheWarm {
			t.Fatalf("arango sample after a failed restart attempt must be warm (no cold was earned), got %s", s.Cache)
		}
	}
	if failures != 2 {
		t.Fatalf("cold-restart failures=%d want 2 (one per round)", failures)
	}
}

func TestMeasureArmsRefusesSpawnedArmWithoutWarmIterationPerRound(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ready.Close()
	cold := func(ctx context.Context, arm *Arm) error {
		if arm == nil || arm.Proc == nil {
			return nil
		}
		return arm.Proc.Restart(ctx)
	}
	clock := &scriptedClock{base: time.Unix(1700000000, 0)}
	opts := RunOptions{Project: "p", Generation: "g", Explorer: "w", UnrestrictedToken: "Bearer x", PreviewLimit: 100,
		Config: RunConfig{Warmup: 1, Iterations: 2, Concurrency: 1, Rounds: 2}} // itersPerRound == 1
	rcpt := map[string]string{BackendArango: "r-a", BackendPostgres: "r-p"}
	outs := map[string]string{BackendArango: "w", BackendPostgres: "w"}

	// A spawned arm with one measured iteration per round yields only the
	// post-restart process-cold sample: no warm cell can ever back a claim
	// (review r4561031154). Refuse before measuring.
	fa, fp := newFakeArm(BackendArango), newFakeArm(BackendPostgres)
	defer fa.close()
	defer fp.close()
	fa.rows["w"] = []map[string]any{{"patient_id": "p"}}
	fp.rows["w"] = []map[string]any{{"patient_id": "p"}}
	arango := fa.arm(BackendArango)
	arango.Proc = spawnSleeperProcess(t, ready.URL, BackendArango)
	if _, _, err := MeasureArms(context.Background(), clock, "w", arango, fp.arm(BackendPostgres), opts, rcpt, outs, cold); err == nil ||
		!strings.Contains(err.Error(), "Iterations >= 2*Rounds") {
		t.Fatalf("spawned arm with one measured iteration per round must be refused, got %v", err)
	}

	// The same schedule is legal for attached arms: they never restart, so
	// every measured sample is service-warm and claimable.
	if _, _, err := MeasureArms(context.Background(), clock, "w", fa.arm(BackendArango), fp.arm(BackendPostgres), opts, rcpt, outs, cold); err != nil {
		t.Fatalf("attached arms keep the one-iteration-per-round schedule legal: %v", err)
	}
}
