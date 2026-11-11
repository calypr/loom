package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "test-bearer-secret"

var elevenResourceTypes = []string{
	"Patient", "Specimen", "Observation", "Encounter", "Condition", "Procedure",
	"Medication", "Organization", "Group", "DocumentReference", "DiagnosticReport",
}

func TestPublishStreamsElevenFilesThroughSerialLoadProtocol(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())

	var acquired, active, maximumActive atomic.Int32
	request := lazyRequest(elevenPaths())
	request.AcquireFile = func(_ context.Context, sourcePath string) (string, func(), error) {
		acquired.Add(1)
		current := active.Add(1)
		for maximum := maximumActive.Load(); current > maximum; maximum = maximumActive.Load() {
			if maximumActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		localPath := filepath.Join(t.TempDir(), path.Base(sourcePath))
		if err := os.WriteFile(localPath, []byte("resource "+resourceType(sourcePath)), 0o600); err != nil {
			active.Add(-1)
			return "", nil, err
		}
		return localPath, func() {
			if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove hydrated file: %v", err)
			}
			active.Add(-1)
		}, nil
	}

	receipt, err := client.Publish(context.Background(), request)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receipt.OperationID != "operation-1" || receipt.SourceRevision != 8 || receipt.ManifestHash != strings.Repeat("a", 64) || receipt.ExecutionID != "execution-8" || receipt.State != "PUBLISHED" {
		t.Fatalf("receipt = %#v", receipt)
	}
	if acquired.Load() != 11 || maximumActive.Load() != 1 || active.Load() != 0 {
		t.Fatalf("file acquisition counts: acquired=%d max-active=%d active=%d", acquired.Load(), maximumActive.Load(), active.Load())
	}
	if got, want := api.snapshotEvents(), append([]string{"begin", "status"}, append(uploadEvents(elevenResourceTypes), "publish")...); !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
	if api.badAuth.Load() || api.authRequests.Load() != 14 {
		t.Fatalf("authorization requests=%d bad-auth=%t, want 14 authenticated requests", api.authRequests.Load(), api.badAuth.Load())
	}
	if api.plan.AuthResourcePath != "/programs/org/projects/project" || api.plan.Repository != "https://github.com/example/data" || api.plan.Commit != strings.Repeat("b", 40) {
		t.Fatalf("source load plan identity = %#v", api.plan)
	}
	if len(api.plan.Files) != 11 {
		t.Fatalf("plan files = %d, want 11", len(api.plan.Files))
	}
	for _, file := range api.plan.Files {
		if file.SHA256 != "" {
			t.Fatalf("client sent a redundant file digest in plan: %#v", file)
		}
	}
	if got := api.snapshotUploadCounts(); len(got) != 11 {
		t.Fatalf("uploaded resource types = %v", got)
	}
}

func TestPublishValidationFailurePreventsLaterFilesAndFinalPublish(t *testing.T) {
	api := newLoadTestAPI(t, nil, "Specimen")
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	request := lazyRequest([]string{"META/Patient.ndjson", "META/Specimen.ndjson", "META/Encounter.ndjson"})
	var acquired atomic.Int32
	request.AcquireFile = func(_ context.Context, sourcePath string) (string, func(), error) {
		acquired.Add(1)
		return writeTempFile(t, path.Base(sourcePath), "resource"), func() {}, nil
	}

	_, err := client.Publish(context.Background(), request)
	var loomErr *Error
	if !errors.As(err, &loomErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if loomErr.StatusCode != http.StatusUnprocessableEntity || loomErr.Code != "INVALID_DATA" || loomErr.RequestID != "validation-request-17" || loomErr.Retryable {
		t.Fatalf("validation error = %#v", loomErr)
	}
	if len(loomErr.Issues) != 1 || loomErr.TotalErrors != 3 || !loomErr.Truncated {
		t.Fatalf("validation issues = %#v total=%d truncated=%t", loomErr.Issues, loomErr.TotalErrors, loomErr.Truncated)
	}
	message := err.Error()
	for _, part := range []string{"validation-request-17", "META/Specimen.ndjson", "row 8", "resource type Specimen", "required field \"subject\" is missing"} {
		if !strings.Contains(message, part) {
			t.Fatalf("error %q does not include %q", message, part)
		}
	}
	if acquired.Load() != 2 {
		t.Fatalf("acquired files = %d, want only Patient and rejected Specimen", acquired.Load())
	}
	if got, want := api.snapshotEvents(), []string{"begin", "status", "file:Patient", "file:Specimen"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
	if api.publishCount.Load() != 0 || api.snapshotUploadCounts()["Patient"] != 1 {
		t.Fatalf("publish count=%d upload counts=%v", api.publishCount.Load(), api.snapshotUploadCounts())
	}
}

func TestPublishLostFileAcknowledgementRetriesOnlyThatFile(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	defer api.server.Close()
	base := api.server.Client().Transport
	transport := &dropAcknowledgementTransport{base: base, target: "/files/Specimen"}
	client := newTestClient(t, api.server.URL, &http.Client{Transport: transport})
	request := lazyRequest([]string{"META/Patient.ndjson", "META/Specimen.ndjson", "META/Encounter.ndjson"})
	var acquired atomic.Int32
	request.AcquireFile = func(_ context.Context, sourcePath string) (string, func(), error) {
		acquired.Add(1)
		return writeTempFile(t, path.Base(sourcePath), "resource "+resourceType(sourcePath)), func() {}, nil
	}

	receipt, err := client.Publish(context.Background(), request)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receipt.State != "PUBLISHED" || !transport.dropped.Load() {
		t.Fatalf("receipt = %#v, acknowledgement dropped=%t", receipt, transport.dropped.Load())
	}
	if got, want := api.snapshotEvents(), []string{"begin", "status", "file:Patient", "file:Specimen", "file:Specimen", "file:Encounter", "publish"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
	if got := api.snapshotUploadCounts(); got["Patient"] != 1 || got["Specimen"] != 2 || got["Encounter"] != 1 {
		t.Fatalf("per-file upload attempts = %v", got)
	}
	if acquired.Load() != 3 || api.publishCount.Load() != 1 {
		t.Fatalf("acquisitions=%d publish-count=%d", acquired.Load(), api.publishCount.Load())
	}
}

func TestPublishRetriesPublicationInProgressAcrossProtocolSteps(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	api.busyResponses = map[string]int{"begin": 2, "file:Patient": 1, "publish": 2}
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	client.busyRetryInterval = time.Millisecond
	client.busyRetryBudget = 200 * time.Millisecond

	if _, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"})); err != nil {
		t.Fatalf("Publish() after temporary publication locks: %v", err)
	}
	want := []string{"begin", "begin", "begin", "status", "file:Patient", "file:Patient", "publish", "publish", "publish"}
	if got := api.snapshotEvents(); !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
}

func TestPublishCapsOrdinaryRetryableFailuresAtFourAttempts(t *testing.T) {
	var attempts atomic.Int32
	server := newLocalServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPut {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		attempts.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(writer, `{"error":{"code":"BACKEND_UNAVAILABLE","message":"try again","retryable":true}}`)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, server.Client())
	_, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"}))
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.Attempts != 4 || attempts.Load() != 4 {
		t.Fatalf("Publish() error=%#v HTTP attempts=%d, want four ordinary attempts", loomErr, attempts.Load())
	}
}

func TestPublishDoesNotRetryPermanentPublicationConflict(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	api.conflictBegin = true
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	_, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"}))
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.Code != "PUBLICATION_CONFLICT" || loomErr.Retryable {
		t.Fatalf("Publish() error = %v, want permanent PUBLICATION_CONFLICT", err)
	}
	if got, want := api.snapshotEvents(), []string{"begin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
}

func TestPublishBusyWaitInterruptsOnCancellation(t *testing.T) {
	firstBegin := make(chan struct{}, 1)
	server := newLocalServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, request.Body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(writer, `{"error":{"code":"PUBLICATION_IN_PROGRESS","message":"another publisher is recovering","retryable":true}}`)
			firstBegin <- struct{}{}
			return
		}
		http.Error(writer, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, server.Client())
	request := localRequest(t, []string{"META/Patient.ndjson"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Publish(ctx, request)
		done <- err
	}()
	<-firstBegin
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Publish() did not stop during the busy retry wait")
	}
}

func TestRetryStateFailsFastForPermanentConflictsAndStopsAtBusyDeadline(t *testing.T) {
	client, err := New("https://loom.example", testToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.busyRetryInterval != 15*time.Second || client.busyRetryBudget != 5*time.Minute {
		t.Fatalf("busy retry defaults = interval %s, budget %s", client.busyRetryInterval, client.busyRetryBudget)
	}

	state := retryState{}
	retry, err := state.wait(context.Background(), &Error{Code: "PUBLICATION_CONFLICT", StatusCode: http.StatusConflict}, time.Millisecond, time.Second)
	if err != nil || retry {
		t.Fatalf("permanent conflict retry = %t, err=%v, want fast failure", retry, err)
	}

	state = retryState{busyStarted: time.Now().Add(-time.Second)}
	retry, err = state.wait(context.Background(), &Error{Code: "PUBLICATION_IN_PROGRESS", StatusCode: http.StatusConflict, Retryable: true}, time.Millisecond, 10*time.Millisecond)
	if err != nil || retry {
		t.Fatalf("expired busy retry = %t, err=%v, want stop at deadline", retry, err)
	}
}

func TestPublishSkipsFilesAlreadyCompletedByPriorAttempt(t *testing.T) {
	completed := []SourceFileReceipt{receiptForPath("META/Patient.ndjson")}
	api := newLoadTestAPI(t, completed, "")
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	request := localRequest(t, []string{"META/Patient.ndjson", "META/Condition.ndjson"})
	request.Files[0].LocalPath = filepath.Join(t.TempDir(), "already-completed-file-is-gone")
	if _, err := client.Publish(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got, want := api.snapshotEvents(), []string{"begin", "status", "file:Condition", "publish"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("protocol events = %v, want %v", got, want)
	}
}

func TestPublishStopsOnSupersededLoad(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	api.statusState = "SUPERSEDED"
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	request := lazyRequest([]string{"META/Patient.ndjson"})
	var acquired atomic.Int32
	request.AcquireFile = func(_ context.Context, _ string) (string, func(), error) {
		acquired.Add(1)
		return writeTempFile(t, "Patient.ndjson", "resource"), func() {}, nil
	}
	_, err := client.Publish(context.Background(), request)
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.Code != "PUBLICATION_CONFLICT" || loomErr.StatusCode != http.StatusConflict {
		t.Fatalf("Publish() error = %v, want superseded publication conflict", err)
	}
	if acquired.Load() != 0 || api.publishCount.Load() != 0 {
		t.Fatalf("acquired=%d publish-count=%d, want no file requests", acquired.Load(), api.publishCount.Load())
	}
}

func TestNewBlocksBearerTokenOnRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := newLocalServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		targetRequests.Add(1)
		if request.Header.Get("Authorization") != "" {
			t.Errorf("bearer token forwarded to redirect target")
		}
	}))
	defer target.Close()
	redirect := newLocalServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("source request authorization = %q", request.Header.Get("Authorization"))
		}
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client := newTestClient(t, redirect.URL, redirect.Client())
	_, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"}))
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.StatusCode != http.StatusFound {
		t.Fatalf("Publish() error = %v, want redirect HTTP error", err)
	}
	if targetRequests.Load() != 0 {
		t.Fatalf("redirect target requests = %d, want 0", targetRequests.Load())
	}
}

func TestPublishValidationErrorRedactsTokenAcrossDetails(t *testing.T) {
	const token = "private-token-value"
	api := newLoadTestAPIWithValidation(t, "Patient", token)
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client(), token)
	_, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"}))
	if err == nil {
		t.Fatal("Publish() error = nil, want validation failure")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("bearer token leaked in worker error: %s", err)
	}
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.RequestID != "[redacted]" || len(loomErr.Issues) != 1 || loomErr.Issues[0].Message == token {
		t.Fatalf("structured validation error = %#v", loomErr)
	}
}

func TestPublishContextCancellationInterruptsRetryDelay(t *testing.T) {
	firstBegin := make(chan struct{}, 1)
	server := newLocalServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, request.Body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(writer, `{"error":{"code":"BACKEND_UNAVAILABLE","message":"retry","retryable":true}}`)
			firstBegin <- struct{}{}
			return
		}
		http.Error(writer, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, server.Client())
	request := localRequest(t, []string{"META/Patient.ndjson"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Publish(ctx, request)
		done <- err
	}()
	<-firstBegin
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Publish() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Publish() did not stop after cancellation")
	}
}

func TestPublishRejectsInvalidFinalReceipt(t *testing.T) {
	api := newLoadTestAPI(t, nil, "")
	api.wrongFinalOperation = true
	defer api.server.Close()
	client := newTestClient(t, api.server.URL, api.server.Client())
	_, err := client.Publish(context.Background(), localRequest(t, []string{"META/Patient.ndjson"}))
	var loomErr *Error
	if !errors.As(err, &loomErr) || loomErr.Code != "INVALID_RECEIPT" {
		t.Fatalf("Publish() error = %v, want INVALID_RECEIPT", err)
	}
	if api.publishCount.Load() != 1 {
		t.Fatalf("publish attempts = %d, want 1", api.publishCount.Load())
	}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	for _, test := range []struct{ baseURL, token string }{
		{baseURL: "", token: "secret"},
		{baseURL: "ftp://loom.example", token: "secret"},
		{baseURL: "https://user:pass@loom.example", token: "secret"},
		{baseURL: "https://loom.example", token: ""},
		{baseURL: "https://loom.example", token: "secret\nvalue"},
	} {
		if _, err := New(test.baseURL, test.token, nil); err == nil {
			t.Fatalf("New(%q, token) error = nil", test.baseURL)
		}
	}
}

type testLoadAPI struct {
	server              *httptest.Server
	mu                  sync.Mutex
	events              []string
	plan                sourceLoadPlan
	uploads             map[string]int
	completed           map[string]SourceFileReceipt
	failResource        string
	validationToken     string
	busyResponses       map[string]int
	conflictBegin       bool
	statusState         string
	wrongFinalOperation bool
	badAuth             atomic.Bool
	authRequests        atomic.Int32
	publishCount        atomic.Int32
}

func newLoadTestAPI(t *testing.T, completed []SourceFileReceipt, failResource string) *testLoadAPI {
	t.Helper()
	return newLoadTestAPIWithValidation(t, failResource, "", completed...)
}

func newLoadTestAPIWithValidation(t *testing.T, failResource, validationToken string, completed ...SourceFileReceipt) *testLoadAPI {
	t.Helper()
	api := &testLoadAPI{
		uploads:         make(map[string]int),
		completed:       make(map[string]SourceFileReceipt),
		failResource:    failResource,
		validationToken: validationToken,
	}
	for _, receipt := range completed {
		api.completed[receipt.Path] = receipt
	}
	api.server = newLocalServer(http.HandlerFunc(api.serveHTTP))
	return api
}

func (api *testLoadAPI) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	api.authRequests.Add(1)
	if got := request.Header.Get("Authorization"); got != "Bearer "+testToken {
		api.badAuth.Store(true)
	}
	if request.Header.Get("Idempotency-Key") != "operation-1" {
		api.badAuth.Store(true)
	}
	const basePath = "/api/v1/datasets/project-1/source-loads/operation-1"
	switch {
	case request.URL.Path == basePath && request.Method == http.MethodPut:
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "bad plan body", http.StatusBadRequest)
			return
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			http.Error(writer, "bad plan", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(body, &api.plan); err != nil {
			http.Error(writer, "bad plan payload", http.StatusBadRequest)
			return
		}
		if _, exists := raw["auth_resource_path"]; !exists {
			api.badAuth.Store(true)
		}
		api.addEvent("begin")
		if api.conflictBegin {
			api.writeConflict(writer)
			return
		}
		if api.takeBusy("begin") {
			api.writeBusy(writer)
			return
		}
		api.writeStatus(writer)
	case request.URL.Path == basePath && request.Method == http.MethodGet:
		api.addEvent("status")
		api.writeStatus(writer)
	case strings.HasPrefix(request.URL.Path, basePath+"/files/") && request.Method == http.MethodPut:
		resource := strings.TrimPrefix(request.URL.Path, basePath+"/files/")
		filename, content, err := readSingleUpload(request)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		api.addEvent("file:" + resource)
		if filename != "META/"+resource+".ndjson" || content != "resource "+resource && !strings.HasPrefix(content, "resource") {
			http.Error(writer, "unexpected file part", http.StatusBadRequest)
			return
		}
		api.mu.Lock()
		api.uploads[resource]++
		api.mu.Unlock()
		if api.takeBusy("file:" + resource) {
			api.writeBusy(writer)
			return
		}
		if resource == api.failResource {
			if api.validationToken != "" {
				api.writeValidationError(writer, api.validationToken)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(writer, `{"error":{"code":"INVALID_DATA","message":"source file validation failed","requestId":"validation-request-17","retryable":true,"details":{"issues":[{"file":"META/Specimen.ndjson","row":8,"resourceType":"Specimen","category":"validation","message":"required field \"subject\" is missing"}],"totalErrors":3,"truncated":true}}}`)
			return
		}
		receipt := receiptForPath("META/" + resource + ".ndjson")
		api.mu.Lock()
		api.completed[receipt.Path] = receipt
		api.mu.Unlock()
		writeJSON(writer, receipt)
	case request.URL.Path == basePath+"/publish" && request.Method == http.MethodPost:
		api.publishCount.Add(1)
		api.addEvent("publish")
		if api.takeBusy("publish") {
			api.writeBusy(writer)
			return
		}
		operationID := "operation-1"
		if api.wrongFinalOperation {
			operationID = "other-operation"
		}
		writeJSON(writer, Receipt{
			OperationID: operationID, SourceRevision: 8, ManifestHash: strings.Repeat("a", 64),
			ExecutionID: "execution-8", State: "PUBLISHED", Reused: false,
		})
	default:
		http.NotFound(writer, request)
	}
}

func (api *testLoadAPI) takeBusy(operation string) bool {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.busyResponses == nil || api.busyResponses[operation] == 0 {
		return false
	}
	api.busyResponses[operation]--
	return true
}

func (api *testLoadAPI) writeBusy(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusConflict)
	_, _ = io.WriteString(writer, `{"error":{"code":"PUBLICATION_IN_PROGRESS","message":"another publisher is recovering","retryable":true}}`)
}

func (api *testLoadAPI) writeConflict(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusConflict)
	_, _ = io.WriteString(writer, `{"error":{"code":"PUBLICATION_CONFLICT","message":"base revision is stale","retryable":true}}`)
}

func (api *testLoadAPI) writeValidationError(writer http.ResponseWriter, token string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = fmt.Fprintf(writer, `{"error":{"code":"INVALID_DATA","message":"invalid %s","requestId":"%s","details":{"issues":[{"file":"META/Patient.ndjson","row":2,"resourceType":"Patient","category":"validation","message":"%s"}],"totalErrors":1,"truncated":false}}}`, token, token, token)
}

func newLocalServer(handler http.Handler) *httptest.Server {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	server.Start()
	return server
}

func (api *testLoadAPI) writeStatus(writer http.ResponseWriter) {
	api.mu.Lock()
	files := make([]SourceFileReceipt, 0, len(api.completed))
	for _, receipt := range api.completed {
		files = append(files, receipt)
	}
	api.mu.Unlock()
	state := api.statusState
	if state == "" {
		state = "UPLOADING"
	}
	writeJSON(writer, SourceLoadStatus{OperationID: "operation-1", Generation: "candidate-1", State: state, BaseRevision: 7, Files: files})
}

func (api *testLoadAPI) addEvent(event string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.events = append(api.events, event)
}

func (api *testLoadAPI) snapshotEvents() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]string(nil), api.events...)
}

func (api *testLoadAPI) snapshotUploadCounts() map[string]int {
	api.mu.Lock()
	defer api.mu.Unlock()
	result := make(map[string]int, len(api.uploads))
	for resource, count := range api.uploads {
		result[resource] = count
	}
	return result
}

func readSingleUpload(request *http.Request) (filename, content string, err error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return "", "", err
	}
	part, err := reader.NextPart()
	if err != nil {
		return "", "", err
	}
	if part.FormName() != "file" || part.FileName() == "" {
		return "", "", errors.New("multipart file part must use the file field")
	}
	_, parameters, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return "", "", err
	}
	body, err := io.ReadAll(part)
	if err != nil {
		return "", "", err
	}
	if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
		return "", "", errors.New("multipart request must contain exactly one file part")
	}
	return parameters["filename"], string(body), nil
}

type dropAcknowledgementTransport struct {
	base    http.RoundTripper
	target  string
	dropped atomic.Bool
}

func (transport *dropAcknowledgementTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil || !strings.HasSuffix(request.URL.Path, transport.target) || !transport.dropped.CompareAndSwap(false, true) {
		return response, err
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return nil, errors.New("injected lost file acknowledgement")
}

func newTestClient(t *testing.T, baseURL string, httpClient *http.Client, token ...string) *Client {
	t.Helper()
	bearer := testToken
	if len(token) > 0 {
		bearer = token[0]
	}
	client, err := New(baseURL, bearer, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func lazyRequest(paths []string) Request {
	files := make([]File, len(paths))
	manifestFiles := make([]ManifestFile, len(paths))
	for index, sourcePath := range paths {
		files[index] = File{Path: sourcePath}
		manifestFiles[index] = ManifestFile{Path: sourcePath}
	}
	return Request{
		Project: "project-1", AuthResourcePath: "/programs/org/projects/project", OperationID: "operation-1",
		Manifest: Manifest{Repository: "https://github.com/example/data", Commit: strings.Repeat("b", 40), Files: manifestFiles},
		Files:    files,
	}
}

func localRequest(t *testing.T, paths []string) Request {
	t.Helper()
	request := lazyRequest(paths)
	for index := range request.Files {
		request.Files[index].LocalPath = writeTempFile(t, path.Base(paths[index]), "resource "+resourceType(paths[index]))
	}
	return request
}

func elevenPaths() []string {
	paths := make([]string, len(elevenResourceTypes))
	for index, resource := range elevenResourceTypes {
		paths[index] = "META/" + resource + ".ndjson"
	}
	return paths
}

func uploadEvents(resources []string) []string {
	events := make([]string, len(resources))
	for index, resource := range resources {
		events[index] = "file:" + resource
	}
	return events
}

func receiptForPath(sourcePath string) SourceFileReceipt {
	return SourceFileReceipt{
		Path: sourcePath, ResourceType: resourceType(sourcePath), SHA256: strings.Repeat("c", 64), Rows: 1, State: "READY",
	}
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	localPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(localPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return localPath
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		panic(err)
	}
}

type unreadableValidationTransport struct{ calls int }

func (t *unreadableValidationTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"X-Request-Id": []string{"validation-request"}}, Body: unreadableValidationBody{}}, nil
}

type unreadableValidationBody struct{}

func (unreadableValidationBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (unreadableValidationBody) Close() error             { return nil }
func TestPublishDoesNotRetryUnreadableValidationResponse(t *testing.T) {
	transport := &unreadableValidationTransport{}
	client, err := New("https://loom.example", "token", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.doJSON(context.Background(), http.MethodPut, "https://loom.example/test", "operation", nil)
	var responseErr *Error
	if transport.calls != 1 || !errors.As(err, &responseErr) || responseErr.Retryable || responseErr.StatusCode != http.StatusUnprocessableEntity || responseErr.RequestID != "validation-request" {
		t.Fatalf("calls=%d err=%#v", transport.calls, err)
	}
}
