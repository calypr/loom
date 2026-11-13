package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

// The harness drives BOTH arms exclusively through public HTTP routes:
// multipart generation upload, explorer-config workspace publication, and
// authoring-v2 preview execution. No back-door storage access: what the
// benchmark measures is what a client gets through Loom.

// Arm is one backend under test.
type Arm struct {
	Backend string
	BaseURL string
	Client  *http.Client
	Proc    *ServerProcess // nil for external arms; required for process-cold samples
}

func (a *Arm) authed(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", token)
	}
}

// UploadGeneration posts the NDJSON fixture files to the dataset route.
func (a *Arm) UploadGeneration(ctx context.Context, token, project, generation string, files map[string]string, authResourcePath string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, name := range sortedNDJSONNames(files) {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			return fmt.Errorf("benchmark: multipart %s: %w", name, err)
		}
		if _, err := part.Write([]byte(files[name])); err != nil {
			return fmt.Errorf("benchmark: multipart write %s: %w", name, err)
		}
	}
	_ = writer.WriteField("project", project)
	_ = writer.WriteField("generation", generation)
	_ = writer.WriteField("defer_activation", "false")
	if authResourcePath != "" {
		_ = writer.WriteField("auth_resource_path", authResourcePath)
	}
	if err := writer.Close(); err != nil {
		return err
	}
	endpoint := a.BaseURL + "/api/v1/datasets/" + url.PathEscape(project) + "/generations/" + url.PathEscape(generation)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	a.authed(req, token)
	payload, err := doArmRequest(a.Client, req)
	if err != nil {
		return fmt.Errorf("benchmark: upload generation on %s: %w", a.Backend, err)
	}
	if reused, _ := payload["reused"].(bool); reused {
		return nil
	}
	if summary, ok := payload["summary"].(map[string]any); ok {
		if _, ok := summary["resources"].(map[string]any); !ok {
			return fmt.Errorf("benchmark: %s upload omitted resource counts", a.Backend)
		}
	}
	return nil
}

// Publication identifies the published output for execution.
type Publication struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
	ExecutionID        string `json:"executionId"`
}

// PublishWorkspace posts the authored workspace for one workload.
func (a *Arm) PublishWorkspace(ctx context.Context, token, project, generation string, workspace []byte, sourceCommit string) (Publication, error) {
	endpoint := a.BaseURL + "/api/v1/projects/" + url.PathEscape(project) + "/generations/" + url.PathEscape(generation) + "/explorer-config"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(workspace))
	if err != nil {
		return Publication{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if sourceCommit != "" {
		req.Header.Set("X-Loom-Source-Commit", sourceCommit)
	}
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return Publication{}, fmt.Errorf("benchmark: publish workspace on %s: %w", a.Backend, err)
	}
	raw, _ := json.Marshal(value)
	var pub Publication
	if err := json.Unmarshal(raw, &pub); err != nil {
		return Publication{}, fmt.Errorf("benchmark: publication shape on %s: %w", a.Backend, err)
	}
	if pub.Output == "" {
		return Publication{}, fmt.Errorf("benchmark: publication on %s omitted output identity", a.Backend)
	}
	return pub, nil
}

// PreviewResult is one executed output through the source-row path.
type PreviewResult struct {
	Columns  []string         `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	RowCount int              `json:"rowCount"`
	Sampled  bool             `json:"sampled"`
	Digest   string           `json:"-"`
}

// ExecuteOutput runs one output through the authoring-v2 preview route and
// canonicalizes it. sampled=true is surfaced, never silently accepted.
func (a *Arm) ExecuteOutput(ctx context.Context, token, project, explorer, receiptID, outputID string, limit int) (PreviewResult, error) {
	endpoint := a.BaseURL + "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(explorer) + "/authoring/v2/preview"
	body, _ := json.Marshal(map[string]any{"receiptId": receiptID, "outputId": outputID, "limit": limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return PreviewResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("benchmark: preview %s/%s on %s: %w", outputID, receiptID, a.Backend, err)
	}
	result, err := decodePreview(value)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("benchmark: preview decode on %s: %w", a.Backend, err)
	}
	return result, nil
}

func decodePreview(value map[string]any) (PreviewResult, error) {
	var result PreviewResult
	raw, _ := json.Marshal(value)
	if err := json.Unmarshal(raw, &result); err != nil {
		return PreviewResult{}, err
	}
	if result.Columns == nil {
		return PreviewResult{}, fmt.Errorf("preview response omitted columns")
	}
	rows, ok := value["rows"].([]any)
	if !ok {
		return PreviewResult{}, fmt.Errorf("preview response omitted rows")
	}
	result.Rows = make([]map[string]any, 0, len(rows))
	for i, rawRow := range rows {
		row, ok := rawRow.(map[string]any)
		if !ok {
			return PreviewResult{}, fmt.Errorf("preview row %d is not an object", i)
		}
		result.Rows = append(result.Rows, row)
	}
	result.Digest = Bag(CanonicalizeRows(result.Columns, result.Rows)).Digest()
	return result, nil
}

func sortedNDJSONNames(files map[string]string) []string {
	var names []string
	for name := range files {
		if strings.HasSuffix(name, ".ndjson") {
			names = append(names, name)
		}
	}
	// deterministic upload order for reproducible evidence
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return names
}

func doArmRequest(client *http.Client, req *http.Request) (map[string]any, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("route %s returned %d: %s", req.URL.Path, resp.StatusCode, truncateToken(string(data)))
	}
	var value map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("non-JSON response from %s: %w", req.URL.Path, err)
	}
	return value, nil
}

// RunOptions binds one full measured run.
type RunOptions struct {
	Project           string
	Generation        string
	Explorer          string
	AuthResourcePath  string
	UnrestrictedToken string
	ScopedToken       string // grant list EXCLUDES AuthResourcePath on the fence
	PreviewLimit      int    // must stay within the engine's MaxPreviewLimit
	Config            RunConfig
	SourceCommit      string
}

// MeasureArms runs the interleaved round-robin timing protocol for one
// workload on both arms. A non-nil coldRestart is invoked before the first
// measured iteration of each round to earn the process-cold label; otherwise
// samples are warm. Rounds interleave arm-by-arm so clock and host drift hit
// both sides alike.
func MeasureArms(ctx context.Context, clock Clock, workloadID string, arango, postgres *Arm, opts RunOptions, receiptIDs map[string]string, outputIDs map[string]string, coldRestart func(ctx context.Context, arm *Arm) error) ([]Sample, map[string]PreviewResult, error) {
	if err := opts.Config.Validate(); err != nil {
		return nil, nil, err
	}
	arms := map[string]*Arm{BackendArango: arango, BackendPostgres: postgres}
	var samples []Sample
	finals := map[string]PreviewResult{}
	itersPerRound := opts.Config.Iterations / opts.Config.Rounds
	runPhase := func(warmup bool, rounds, iters int) {
		iteration := 0
		for round := 1; round <= rounds; round++ {
			if !warmup && coldRestart != nil {
				for _, backend := range []string{BackendArango, BackendPostgres} {
					if err := coldRestart(ctx, arms[backend]); err != nil {
						samples = append(samples, Sample{WorkloadID: workloadID, Backend: backend, Outcome: OutcomeError, ErrorCode: "cold restart: " + err.Error()})
					}
				}
			}
			for i := 0; i < iters; i++ {
				iteration++
				for _, backend := range []string{BackendArango, BackendPostgres} { // fixed order: matched scheduling
					arm := arms[backend]
					started := clock.Now()
					result, err := arm.ExecuteOutput(ctx, tokenFor(backend, opts), opts.Project, opts.Explorer,
						receiptIDs[backend], outputIDs[backend], opts.PreviewLimit)
					sample := Sample{
						WorkloadID: workloadID, Backend: backend, Round: round, Iteration: iteration, Warmup: warmup,
						StartedAt: started,
						Cache: func() CacheLabel {
							if coldRestart != nil && !warmup {
								return CacheProcessCold
							}
							return CacheWarm
						}(),
					}
					if err != nil {
						sample.Outcome = classifyArmError(err)
						sample.ErrorCode = err.Error()
					} else {
						sample.Outcome = OutcomePassed
						sample.RowsSeen = result.RowCount
						sample.Truncated = result.Sampled
					}
					sample.Millis = ElapsedMillis(clock, started)
					samples = append(samples, sample)
					if err == nil && !warmup {
						finals[backend] = result
					}
				}
			}
		}
	}
	if opts.Config.Warmup > 0 {
		runPhase(true, 1, opts.Config.Warmup)
	}
	runPhase(false, opts.Config.Rounds, itersPerRound)
	return samples, finals, nil
}

func tokenFor(backend string, opts RunOptions) string {
	_ = backend
	// Timing runs as the unrestricted principal; the scoped read is only
	// the pre-timing authorization control (ruling iii).
	return opts.UnrestrictedToken
}

func classifyArmError(err error) Outcome {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "deadline"), strings.Contains(text, "timeout"), strings.Contains(text, "context canceled"):
		return OutcomeTimeout
	default:
		return OutcomeError
	}
}

// NegativeAuthControl executes the pre-timing authorization gate (ruling
// iii). The unrestricted principal must see data; the scoped principal,
// whose fence grant list excludes the dataset path, must see zero rows —
// via the restricted-scope intersection, which yields an empty path set and
// therefore an empty result, not an error.
func NegativeAuthControl(ctx context.Context, arango, postgres *Arm, opts RunOptions, receiptIDs, outputIDs map[string]string) WorkloadGateReport {
	baselineArango, errA := arango.ExecuteOutput(ctx, opts.UnrestrictedToken, opts.Project, opts.Explorer, receiptIDs[BackendArango], outputIDs[BackendArango], opts.PreviewLimit)
	baselinePostgres, errP := postgres.ExecuteOutput(ctx, opts.UnrestrictedToken, opts.Project, opts.Explorer, receiptIDs[BackendPostgres], outputIDs[BackendPostgres], opts.PreviewLimit)
	baseline := 0
	switch {
	case errA == nil && errP == nil && baselineArango.RowCount == baselinePostgres.RowCount:
		baseline = baselineArango.RowCount
	case errA == nil:
		baseline = baselineArango.RowCount
	case errP == nil:
		baseline = baselinePostgres.RowCount
	}
	scopedA, scopedErrA := arango.ExecuteOutput(ctx, opts.ScopedToken, opts.Project, opts.Explorer, receiptIDs[BackendArango], outputIDs[BackendArango], opts.PreviewLimit)
	scopedP, scopedErrP := postgres.ExecuteOutput(ctx, opts.ScopedToken, opts.Project, opts.Explorer, receiptIDs[BackendPostgres], outputIDs[BackendPostgres], opts.PreviewLimit)
	errText := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return ""
	}
	return EvaluateNegativeAuth("negative-auth/"+outputIDs[BackendArango], baseline, rowCountOr(scopedA, scopedErrA), rowCountOr(scopedP, scopedErrP), errText(scopedErrA), errText(scopedErrP))
}

func rowCountOr(result PreviewResult, err error) int {
	if err != nil {
		return -1
	}
	return result.RowCount
}

// authoring/v2 lifecycle routes, matching the handlers in
// internal/server/openapi_explorer_direct.go and the reconcile/preview
// direct handlers. Each workload is authored onto its own explorer cloned
// from the seeded default so drafts do not collide.

// BuilderState is the draft state the Builder GET returns: it carries the
// optimistic-concurrency tokens reconcile needs.
type BuilderState struct {
	DraftVersion int    `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	Snapshot     struct {
		SnapshotToken string `json:"snapshotToken"`
	} `json:"snapshot"`
	Catalog struct {
		SnapshotToken string `json:"snapshotToken"`
	} `json:"catalog"`
}

// SnapshotToken prefers the catalog token, falling back to the inline one.
func (b BuilderState) SnapshotToken() string {
	if b.Catalog.SnapshotToken != "" {
		return b.Catalog.SnapshotToken
	}
	return b.Snapshot.SnapshotToken
}

func (a *Arm) authoringURL(project, explorer string) string {
	return a.BaseURL + "/api/v1/projects/" + url.PathEscape(project) + "/explorers/" + url.PathEscape(explorer) + "/authoring/v2"
}

// CreateExplorer clones a new explorer from sourceExplorerID (usually the
// seeded "default") so each workload owns an independent draft.
func (a *Arm) CreateExplorer(ctx context.Context, token, project, name, title, sourceExplorerID string) (string, error) {
	endpoint := a.BaseURL + "/api/v1/projects/" + url.PathEscape(project) + "/explorers"
	body, _ := json.Marshal(map[string]any{"name": name, "title": title, "sourceExplorerId": sourceExplorerID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return "", fmt.Errorf("benchmark: create explorer on %s: %w", a.Backend, err)
	}
	explorerID, _ := value["explorerId"].(string)
	if strings.TrimSpace(explorerID) == "" {
		if summary, ok := value["explorer"].(map[string]any); ok {
			explorerID, _ = summary["explorerId"].(string)
		}
	}
	if strings.TrimSpace(explorerID) == "" {
		return "", fmt.Errorf("benchmark: create explorer on %s returned no explorerId", a.Backend)
	}
	return explorerID, nil
}

// FetchBuilderState reads the draft state and its optimistic-concurrency
// tokens. The token must be present: reconcile without it would race.
func (a *Arm) FetchBuilderState(ctx context.Context, token, project, explorer string) (BuilderState, error) {
	endpoint := a.authoringURL(project, explorer) + "/builder"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return BuilderState{}, err
	}
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return BuilderState{}, fmt.Errorf("benchmark: builder state on %s: %w", a.Backend, err)
	}
	raw, _ := json.Marshal(value)
	var state BuilderState
	if err := json.Unmarshal(raw, &state); err != nil {
		return BuilderState{}, fmt.Errorf("benchmark: builder state shape on %s: %w", a.Backend, err)
	}
	if strings.TrimSpace(state.SnapshotToken()) == "" {
		return BuilderState{}, fmt.Errorf("benchmark: builder state on %s omitted the snapshot token", a.Backend)
	}
	return state, nil
}

// Reconcile compiles the current draft and returns its receipt id. The
// receipt is what ties a preview to an exact authored revision.
func (a *Arm) Reconcile(ctx context.Context, token, project, explorer, outputID string, state BuilderState) (string, error) {
	endpoint := a.authoringURL(project, explorer) + "/reconcile"
	body := map[string]any{
		"snapshotToken": state.SnapshotToken(),
		"draftVersion":  state.DraftVersion,
	}
	if state.DraftDigest != "" {
		body["draftDigest"] = state.DraftDigest
	}
	if outputID != "" {
		body["outputId"] = outputID
	}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	a.authed(req, token)
	value, err := doArmRequest(a.Client, req)
	if err != nil {
		return "", fmt.Errorf("benchmark: reconcile on %s: %w", a.Backend, err)
	}
	receiptID, _ := value["receiptId"].(string)
	if strings.TrimSpace(receiptID) == "" {
		if nested, ok := value["receipt"].(map[string]any); ok {
			receiptID, _ = nested["receiptId"].(string)
		}
	}
	if strings.TrimSpace(receiptID) == "" {
		return "", fmt.Errorf("benchmark: reconcile on %s returned no receiptId", a.Backend)
	}
	return receiptID, nil
}

// AuthorWorkload drives the four-route authoring flow for one workload on
// one arm and returns the (explorer, receipt) pair the preview route needs.
// Order is load-bearing: publish the workspace on the generation, clone an
// explorer from the seeded default so the draft equals the active revision,
// read the builder tokens, reconcile for a receipt bound to that draft.
func (a *Arm) AuthorWorkload(ctx context.Context, token, project, generation string, workspace []byte, sourceCommit, name, outputID string) (explorerID, receiptID string, err error) {
	if _, err := a.PublishWorkspace(ctx, token, project, generation, workspace, sourceCommit); err != nil {
		return "", "", err
	}
	explorerID, err = a.CreateExplorer(ctx, token, project, name, name, "default")
	if err != nil {
		return "", "", err
	}
	state, err := a.FetchBuilderState(ctx, token, project, explorerID)
	if err != nil {
		return "", "", err
	}
	receiptID, err = a.Reconcile(ctx, token, project, explorerID, outputID, state)
	if err != nil {
		return "", "", err
	}
	return explorerID, receiptID, nil
}
