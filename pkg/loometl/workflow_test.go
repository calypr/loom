package loometl

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type diagnosticCollector struct{ values []Diagnostic }

func (c *diagnosticCollector) Log(_ context.Context, value Diagnostic) {
	c.values = append(c.values, value)
}

type mockLoomServer struct {
	t                  *testing.T
	server             *httptest.Server
	mu                 sync.Mutex
	counts             map[string]int
	generation         SnapshotGeneration
	execution          MaterializationExecution
	release            ProjectRelease
	active             *ActiveRelease
	checksumConflict   bool
	publicationFailure bool
	activationConflict bool
	startRetryableOnce bool
}

func newMockLoomServer(t *testing.T) *mockLoomServer {
	t.Helper()
	m := &mockLoomServer{t: t, counts: map[string]int{}}
	m.generation = SnapshotGeneration{Dataset: DatasetRef{Project: "project-a", Generation: "commit-a"}, GitCommit: "commit-a", State: "LOADING", ExpectedResourceTypes: []string{"Patient"}}
	selector := DataframeSelector{Recipe: "default", TranslationVersion: "v1", Output: "Patient"}
	retryable := false
	m.execution = MaterializationExecution{ID: "execution-1", Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, SourceGeneration: "commit-a", State: "QUEUED", Outputs: []ExecutionOutput{{Name: "Patient", Selector: &selector, State: "QUEUED", ErrorRetryable: &retryable}}}
	m.release = ProjectRelease{ID: "release-1", Project: "project-a", GitCommit: "commit-a", Generation: "commit-a", Publications: []ReleasePublication{{Selector: selector, ExecutionID: "execution-1", Generation: "commit-a", Required: true}}}
	m.server = httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	return m
}

func (m *mockLoomServer) close() { m.server.Close() }

func (m *mockLoomServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := serverOperationKey(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "loom-"+key)
	m.counts[key]++
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-a/releases/active":
		if m.active == nil {
			writeError(w, http.StatusNotFound, "NO_ACTIVE_RELEASE", "no active release", false, "READ_ACTIVE_RELEASE", "", nil)
			return
		}
		_ = json.NewEncoder(w).Encode(m.active)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-a/generations/commit-a":
		_ = json.NewEncoder(w).Encode(m.generation)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/projects/project-a/generations/commit-a/resources/Patient":
		body, _ := io.ReadAll(r.Body)
		if m.checksumConflict {
			writeError(w, http.StatusConflict, "CHECKSUM_CONFLICT", "immutable checksum differs", false, "UPLOAD_RESOURCE", "Patient", map[string]any{"expected": "old", "received": r.Header.Get("X-Content-SHA256")})
			return
		}
		m.generation.Uploads = []ResourceUpload{{ResourceType: "Patient", SHA256: r.Header.Get("X-Content-SHA256"), Size: int64(len(body))}}
		_ = json.NewEncoder(w).Encode(m.generation)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-a/generations/commit-a/finalize":
		m.generation.State = "STAGED"
		_ = json.NewEncoder(w).Encode(FinalizeGenerationResult{Generation: m.generation})
	case r.Method == http.MethodPost && r.URL.Path == "/graphql/graph":
		var request graphqlRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "StartDataframeMaterialization") {
			if m.startRetryableOnce && m.counts[key] == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{"message": "backend temporarily unavailable", "extensions": map[string]any{"code": "BACKEND_UNAVAILABLE", "phase": "ENQUEUE", "details": map[string]any{"service": "publication-catalog"}, "requestId": "loom-gql-retry", "retryable": true}}}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"startDataframeMaterialization": m.execution}})
			return
		}
		if m.publicationFailure {
			retryable := false
			m.execution.State, m.execution.Phase = "FAILED", "VERIFY_OUTPUT"
			m.execution.Error, m.execution.ErrorCode, m.execution.ErrorRetryable, m.execution.LoomRequestID = "Patient output violated its contract", "PUBLICATION_FAILED", &retryable, "loom-publication-42"
			m.execution.Outputs[0].State, m.execution.Outputs[0].Phase = "FAILED", "VERIFY_OUTPUT"
			m.execution.Outputs[0].Error, m.execution.Outputs[0].ErrorCode, m.execution.Outputs[0].ErrorRetryable = "Patient output violated its contract", "RECIPE_CONTRACT_VIOLATION", &retryable
		} else {
			m.execution.State = "PUBLISHED"
			m.execution.Outputs[0].State = "PUBLISHED"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"dataframeRecipeExecution": m.execution}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-a/releases":
		_ = json.NewEncoder(w).Encode(m.release)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-a/releases/release-1/activate":
		m.active = &ActiveRelease{Release: m.release, Revision: 1}
		if m.activationConflict {
			writeError(w, http.StatusConflict, "RELEASE_ACTIVATION_CONFLICT", "activation response was ambiguous", false, "ACTIVATE_RELEASE", "", nil)
			return
		}
		_ = json.NewEncoder(w).Encode(m.active)
	default:
		http.NotFound(w, r)
	}
}

func serverOperationKey(request *http.Request) string {
	if request.URL.Path != "/graphql/graph" {
		return request.Method + " " + request.URL.Path
	}
	data, _ := io.ReadAll(request.Body)
	request.Body = io.NopCloser(strings.NewReader(string(data)))
	if strings.Contains(string(data), "StartDataframeMaterialization") {
		return "graphql-start"
	}
	return "graphql-poll"
}

func writeError(w http.ResponseWriter, status int, code, message string, retryable bool, phase, output string, details map[string]any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": retryable, "phase": phase, "output": output, "details": details, "requestId": "loom-error-1"}})
}

type loseFirstResponseTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	lost map[string]bool
}

func (t *loseFirstResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	key := operationKey(request)
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	t.mu.Lock()
	first := !t.lost[key]
	if first {
		t.lost[key] = true
	}
	t.mu.Unlock()
	if first {
		_ = response.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return response, nil
}

func operationKey(request *http.Request) string {
	if request.URL.Path != "/graphql/graph" {
		return request.Method + " " + request.URL.Path
	}
	if request.GetBody != nil {
		body, err := request.GetBody()
		if err == nil {
			data, _ := io.ReadAll(body)
			_ = body.Close()
			if strings.Contains(string(data), "StartDataframeMaterialization") {
				return "graphql-start"
			}
		}
	}
	return "graphql-poll"
}

func testClient(t *testing.T, server *mockLoomServer, transport http.RoundTripper) *Client {
	t.Helper()
	if transport == nil {
		transport = http.DefaultTransport
	}
	client, err := NewClient(ClientConfig{BaseURL: server.server.URL, HTTPClient: &http.Client{Transport: transport}, Retry: RetryPolicy{
		MaxAttempts: 3, Backoff: func(int) time.Duration { return 0 }, Sleep: func(context.Context, time.Duration) error { return nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testRequest(t *testing.T) RunRequest {
	t.Helper()
	resource, err := BytesResource("Patient", []byte("{\"resourceType\":\"Patient\",\"id\":\"p1\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	return RunRequest{Project: "project-a", GitCommit: "commit-a", Resources: []ResourceSource{resource}, RequiredSelectors: []DataframeSelector{{Recipe: "default", TranslationVersion: "v1", Output: "Patient"}}}
}

func testWorkflow(t *testing.T, api LoomAPI, sink DiagnosticSink) *Workflow {
	t.Helper()
	workflow, err := NewWorkflow(WorkflowConfig{API: api, Diagnostics: sink, PollInterval: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	return workflow
}

func TestWorkflowSafelyRetriesNetworkLossAtEveryStep(t *testing.T) {
	server := newMockLoomServer(t)
	defer server.close()
	transport := &loseFirstResponseTransport{base: http.DefaultTransport, lost: map[string]bool{}}
	client := testClient(t, server, transport)
	result, err := testWorkflow(t, client, nil).Run(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if result.Active.Release.ID != "release-1" || result.Active.Revision != 1 || result.Generation.State != "STAGED" {
		t.Fatalf("workflow result = %#v", result)
	}
	for _, key := range []string{
		"GET /api/v1/projects/project-a/releases/active",
		"POST /api/v1/projects/project-a/generations/commit-a",
		"PUT /api/v1/projects/project-a/generations/commit-a/resources/Patient",
		"POST /api/v1/projects/project-a/generations/commit-a/finalize",
		"graphql-start", "graphql-poll",
		"POST /api/v1/projects/project-a/releases",
		"POST /api/v1/projects/project-a/releases/release-1/activate",
	} {
		if server.counts[key] < 2 {
			t.Fatalf("operation %q count = %d, want retry after lost response", key, server.counts[key])
		}
	}
}

func TestChecksumConflictIsDetailedAndNeverActivates(t *testing.T) {
	server := newMockLoomServer(t)
	server.checksumConflict = true
	server.active = previousActiveRelease()
	defer server.close()
	sink := &diagnosticCollector{}
	_, err := testWorkflow(t, testClient(t, server, nil), sink).Run(context.Background(), testRequest(t))
	var workflowErr *WorkflowError
	var apiErr *APIError
	if !errors.As(err, &workflowErr) || !errors.As(err, &apiErr) || apiErr.Code != "CHECKSUM_CONFLICT" || apiErr.Retryable() {
		t.Fatalf("workflow error = %#v; API error = %#v", workflowErr, apiErr)
	}
	if server.counts["PUT /api/v1/projects/project-a/generations/commit-a/resources/Patient"] != 1 {
		t.Fatal("non-retryable checksum conflict was retried")
	}
	if server.counts["POST /api/v1/projects/project-a/releases"] != 0 || server.active.Release.ID != "release-old" || server.active.Revision != 7 {
		t.Fatal("checksum conflict reached release activation")
	}
	last := sink.values[len(sink.values)-1]
	if last.ErrorCode != "CHECKSUM_CONFLICT" || last.Output != "Patient" || last.LoomRequestID != "loom-error-1" || last.Details["expected"] != "old" || !last.PreviousReleasePreserved {
		t.Fatalf("diagnostic = %#v", last)
	}
}

func TestPublicationFailureSurfacesDurableDiagnostics(t *testing.T) {
	server := newMockLoomServer(t)
	server.publicationFailure = true
	server.active = previousActiveRelease()
	defer server.close()
	sink := &diagnosticCollector{}
	_, err := testWorkflow(t, testClient(t, server, nil), sink).Run(context.Background(), testRequest(t))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "RECIPE_CONTRACT_VIOLATION" || apiErr.Phase != "VERIFY_OUTPUT" || apiErr.Output != "Patient" || apiErr.RequestID != "loom-publication-42" {
		t.Fatalf("publication error = %#v", apiErr)
	}
	if server.counts["POST /api/v1/projects/project-a/releases"] != 0 || server.active.Release.ID != "release-old" || server.active.Revision != 7 {
		t.Fatal("failed publication reached release activation")
	}
	last := sink.values[len(sink.values)-1]
	if last.ErrorCode != "RECIPE_CONTRACT_VIOLATION" || last.Phase != "VERIFY_OUTPUT" || last.Output != "Patient" || last.LoomRequestID != "loom-publication-42" || !last.PreviousReleasePreserved {
		t.Fatalf("diagnostic = %#v", last)
	}
}

func previousActiveRelease() *ActiveRelease {
	return &ActiveRelease{Release: ProjectRelease{ID: "release-old", Project: "project-a", GitCommit: "commit-old", Generation: "commit-old"}, Revision: 7}
}

func TestActivationConflictIsConfirmedThroughActiveRelease(t *testing.T) {
	server := newMockLoomServer(t)
	server.activationConflict = true
	defer server.close()
	result, err := testWorkflow(t, testClient(t, server, nil), nil).Run(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if result.Active.Release.ID != "release-1" || result.Active.Revision != 1 {
		t.Fatalf("activation was not confirmed: %#v", result.Active)
	}
	if server.counts["GET /api/v1/projects/project-a/releases/active"] != 2 {
		t.Fatalf("active release reads = %d, want initial observation plus reconciliation", server.counts["GET /api/v1/projects/project-a/releases/active"])
	}
}

func TestSameCommitAndChecksumsAreIdempotent(t *testing.T) {
	server := newMockLoomServer(t)
	defer server.close()
	workflow := testWorkflow(t, testClient(t, server, nil), nil)
	first, err := workflow.Run(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	uploads := server.counts["PUT /api/v1/projects/project-a/generations/commit-a/resources/Patient"]
	second, err := workflow.Run(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if first.Active.Release.ID != second.Active.Release.ID || second.Active.Revision != 1 {
		t.Fatalf("idempotent results differ: first=%#v second=%#v", first.Active, second.Active)
	}
	if got := server.counts["PUT /api/v1/projects/project-a/generations/commit-a/resources/Patient"]; got != uploads {
		t.Fatalf("already staged checksum was uploaded again: before=%d after=%d", uploads, got)
	}
}

func TestLegacyMutableUploadDefaultsOff(t *testing.T) {
	t.Setenv(LegacyMutableUploadEnv, "")
	enabled, err := LegacyMutableUploadEnabled()
	if err != nil || enabled {
		t.Fatalf("legacy flag = %v, err = %v", enabled, err)
	}
	t.Setenv(LegacyMutableUploadEnv, "true")
	enabled, err = LegacyMutableUploadEnabled()
	if err != nil || !enabled {
		t.Fatalf("legacy flag = %v, err = %v", enabled, err)
	}
}

func TestExplicitlyRetryableGraphQLErrorIsRetried(t *testing.T) {
	server := newMockLoomServer(t)
	server.startRetryableOnce = true
	defer server.close()
	result, err := testWorkflow(t, testClient(t, server, nil), nil).Run(context.Background(), testRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if result.Active.Release.ID != "release-1" || server.counts["graphql-start"] != 2 {
		t.Fatalf("retryable GraphQL result = %#v, starts=%d", result, server.counts["graphql-start"])
	}
}
