package load

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
)

type sourceLoadStoreFixture struct {
	status       ingest.SourceLoadStatus
	beginCount   int
	uploadCount  int
	publishCount int
	uploadedData string
	uploadErr    error
}

func (f *sourceLoadStoreFixture) Begin(context.Context, string, string, ingest.SourceLoadPlan) (ingest.SourceLoadStatus, error) {
	f.beginCount++
	return f.status, nil
}

func (f *sourceLoadStoreFixture) Status(context.Context, string, string) (ingest.SourceLoadStatus, error) {
	return f.status, nil
}

func (f *sourceLoadStoreFixture) UploadFile(_ context.Context, _, _, _, path string) (ingest.SourceFileReceipt, error) {
	f.uploadCount++
	content, err := os.ReadFile(path)
	if err != nil {
		return ingest.SourceFileReceipt{}, err
	}
	f.uploadedData = string(content)
	return ingest.SourceFileReceipt{Path: "META/Patient.ndjson", ResourceType: "Patient", SHA256: "abc", Rows: 1, State: "READY"}, f.uploadErr
}

func (f *sourceLoadStoreFixture) Publish(context.Context, string, string) (ingest.SourcePublishReceipt, error) {
	f.publishCount++
	return ingest.SourcePublishReceipt{}, nil
}

type sourceLoadAuthorizerFixture struct {
	err   error
	paths []string
}

func (a *sourceLoadAuthorizerFixture) AuthorizeWrite(_ context.Context, _ *authscope.Principal, _, authPath string) error {
	a.paths = append(a.paths, authPath)
	return a.err
}

type sourceLoadCountingReader struct {
	reader io.Reader
	read   int
}

func (r *sourceLoadCountingReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += n
	return n, err
}

func newSourceLoadTestHandler(t *testing.T, store *sourceLoadStoreFixture, authorizer authscope.Authorizer, maxFileBytes int64) *Handler {
	t.Helper()
	handler, err := NewHandler(Config{
		Service:          &Service{},
		Authorizer:       authorizer,
		SourceLoader:     store,
		SourceLoadLimits: SourceLoadLimits{MaxFileBytes: maxFileBytes},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return handler
}

func sourceLoadMultipart(t *testing.T, filenames ...string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, filename := range filenames {
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("CreateFormFile(%q): %v", filename, err)
		}
		if _, err := io.WriteString(part, `{"resourceType":"Patient","id":"one"}`+"\n"); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.Boundary()
}

func TestSourceLoadRejectsMultipleFileParts(t *testing.T) {
	bodyBytes, boundary := sourceLoadMultipart(t, "META/Patient.ndjson", "META/Observation.ndjson")
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: "/project/data"}}
	authorizer := &sourceLoadAuthorizerFixture{}
	handler := newSourceLoadTestHandler(t, store, authorizer, 1<<20)
	_, err := handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(bytes.NewReader(bodyBytes), boundary), nil)
	if err == nil || store.uploadCount != 0 {
		t.Fatalf("multiple files accepted or reached store: err=%v uploadCount=%d", err, store.uploadCount)
	}
	if mapped := httpapi.MapDataframeError(err, "multi-file"); mapped.Status != http.StatusBadRequest {
		t.Fatalf("multiple files status = %d, want 400: %#v", mapped.Status, mapped.Body)
	}
}

func TestSourceLoadRejectsNonCanonicalFilename(t *testing.T) {
	bodyBytes, boundary := sourceLoadMultipart(t, "Patient.ndjson")
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: "project/data"}}
	handler := newSourceLoadTestHandler(t, store, &sourceLoadAuthorizerFixture{}, 1<<20)
	_, err := handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(bytes.NewReader(bodyBytes), boundary), nil)
	if err == nil || store.uploadCount != 0 {
		t.Fatalf("noncanonical filename accepted or reached store: err=%v uploadCount=%d", err, store.uploadCount)
	}
	if mapped := httpapi.MapDataframeError(err, "bad-name"); mapped.Status != http.StatusBadRequest {
		t.Fatalf("noncanonical filename status = %d, want 400: %#v", mapped.Status, mapped.Body)
	}
}

func TestSourceLoadAuthorizesBeforeReadingMultipartBody(t *testing.T) {
	reader := &sourceLoadCountingReader{reader: strings.NewReader("malformed multipart body")}
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: " /project/data/ "}}
	authorizer := &sourceLoadAuthorizerFixture{err: authscope.ErrForbidden}
	handler := newSourceLoadTestHandler(t, store, authorizer, 1<<20)
	_, err := handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(reader, "boundary"), nil)
	if !errors.Is(err, authscope.ErrForbidden) || reader.read != 0 || store.uploadCount != 0 {
		t.Fatalf("unauthorized upload consumed body or reached store: err=%v bytesRead=%d uploadCount=%d", err, reader.read, store.uploadCount)
	}
	if len(authorizer.paths) != 1 || authorizer.paths[0] != "project/data" {
		t.Fatalf("authorization paths = %v, want normalized persisted path", authorizer.paths)
	}
}

func TestSourceLoadBeginAndStatusAuthorizeUsingNormalizedScope(t *testing.T) {
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: " /project/data/ "}}
	authorizer := &sourceLoadAuthorizerFixture{}
	handler := newSourceLoadTestHandler(t, store, authorizer, 1<<20)
	plan := ingest.SourceLoadPlan{
		Repository:       "https://github.com/example/source",
		Commit:           "0123456789012345678901234567890123456789",
		Files:            []ingest.SourceManifestFile{{Path: "META/Patient.ndjson"}},
		AuthResourcePath: " /project/data/ ",
	}
	if _, err := handler.BeginSourceLoad(context.Background(), "project-a", "task-1", plan, nil); err != nil {
		t.Fatalf("BeginSourceLoad: %v", err)
	}
	if _, err := handler.GetSourceLoadStatus(context.Background(), "project-a", "task-1", nil); err != nil {
		t.Fatalf("GetSourceLoadStatus: %v", err)
	}
	if store.beginCount != 1 || len(authorizer.paths) != 2 || authorizer.paths[0] != "project/data" || authorizer.paths[1] != "project/data" {
		t.Fatalf("begin calls=%d authorization paths=%v", store.beginCount, authorizer.paths)
	}
}

func TestSourceLoadPublishAuthorizesBeforePublishing(t *testing.T) {
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: "/project/data"}}
	authorizer := &sourceLoadAuthorizerFixture{err: authscope.ErrForbidden}
	handler := newSourceLoadTestHandler(t, store, authorizer, 1<<20)
	_, err := handler.PublishSourceLoad(context.Background(), "project-a", "task-1", nil)
	if !errors.Is(err, authscope.ErrForbidden) || store.publishCount != 0 {
		t.Fatalf("publish authorization not enforced: err=%v publishCount=%d", err, store.publishCount)
	}
}

func TestSourceLoadLifecycleErrorsHaveStableHTTPStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		statusCode int
		code       string
		retryable  bool
	}{
		{name: "not found", err: ingest.ErrSourceLoadNotFound, statusCode: http.StatusNotFound, code: "DATASET_NOT_FOUND"},
		{name: "in progress", err: ingest.ErrSourceLoadInProgress, statusCode: http.StatusConflict, code: "PUBLICATION_IN_PROGRESS", retryable: true},
		{name: "incomplete", err: ingest.ErrSourceLoadIncomplete, statusCode: http.StatusConflict, code: "PUBLICATION_CONFLICT", retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mapped := httpapi.MapDataframeError(normalizeError(test.err), "source-state")
			if mapped.Status != test.statusCode || mapped.Body.Error.Code != test.code || mapped.Body.Error.Retryable != test.retryable {
				t.Fatalf("mapped lifecycle error = %#v", mapped)
			}
		})
	}
}

func TestSourceLoadEnforcesPerFileLimit(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "META/Patient.ndjson")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, strings.Repeat("x", 1<<20)); err != nil {
		t.Fatalf("write large multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	reader := &sourceLoadCountingReader{reader: bytes.NewReader(body.Bytes())}
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: "project/data"}}
	handler := newSourceLoadTestHandler(t, store, &sourceLoadAuthorizerFixture{}, 4)
	_, err = handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(reader, writer.Boundary()), nil)
	if err == nil || store.uploadCount != 0 {
		t.Fatalf("oversized file accepted or reached store: err=%v uploadCount=%d", err, store.uploadCount)
	}
	if reader.read >= body.Len() {
		t.Fatalf("oversized multipart body was drained: read %d of %d bytes", reader.read, body.Len())
	}
	mapped := httpapi.MapDataframeError(err, "large-file")
	if mapped.Status != http.StatusRequestEntityTooLarge || mapped.Body.Error.Details["maximum"] != int64(4) {
		t.Fatalf("oversized file response = %#v", mapped)
	}
}

func TestSourceLoadTruncatedMultipartIsInvalidRequest(t *testing.T) {
	const truncated = "--b\r\nContent-Disposition: form-data; name=\"file\"; filename=\"META/Patient.ndjson\"\r\n\r\n{}"
	store := &sourceLoadStoreFixture{status: ingest.SourceLoadStatus{AuthResourcePath: "project/data"}}
	handler := newSourceLoadTestHandler(t, store, &sourceLoadAuthorizerFixture{}, 1<<20)
	_, err := handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(strings.NewReader(truncated), "b"), nil)
	if err == nil || store.uploadCount != 0 {
		t.Fatalf("truncated multipart was accepted or reached store: err=%v uploadCount=%d", err, store.uploadCount)
	}
	if mapped := httpapi.MapDataframeError(err, "truncated"); mapped.Status != http.StatusBadRequest {
		t.Fatalf("truncated multipart status = %d, want 400: %#v", mapped.Status, mapped.Body)
	}
}

func TestSourceLoadValidationErrorUsesBoundedSafeDetails(t *testing.T) {
	issues := make([]ingest.RowErrorSample, 101)
	issues[0] = ingest.RowErrorSample{
		File: "META/Patient.ndjson", Line: 7, ResourceType: "Patient", Category: "validation",
		Message: `META/Patient.ndjson row 7: required field "identifier" is missing from Patient.`,
	}
	for index := 1; index < len(issues); index++ {
		issues[index] = ingest.RowErrorSample{
			File: "META/Patient.ndjson", Line: index + 7, ResourceType: "Patient", Category: "validation",
			Message: "patient-payload secret=private",
		}
	}
	store := &sourceLoadStoreFixture{
		status: ingest.SourceLoadStatus{AuthResourcePath: "project/data"},
		uploadErr: &ingest.SourceFileValidationError{
			Issues:      issues,
			TotalErrors: 104,
			Truncated:   true,
		},
	}
	handler := newSourceLoadTestHandler(t, store, &sourceLoadAuthorizerFixture{}, 1<<20)
	bodyBytes, boundary := sourceLoadMultipart(t, "META/Patient.ndjson")
	_, err := handler.UploadSourceLoadFile(context.Background(), "project-a", "task-1", "Patient", multipart.NewReader(bytes.NewReader(bodyBytes), boundary), nil)
	if err == nil {
		t.Fatal("invalid source rows were accepted")
	}
	mapped := httpapi.MapDataframeError(normalizeError(err), "bad-rows")
	if mapped.Status != http.StatusUnprocessableEntity || mapped.Body.Error.Code != string(dataframeerrors.CodeInvalidData) {
		t.Fatalf("validation response = %#v", mapped)
	}
	if strings.Contains(mapped.Body.Error.Message, "private") || strings.Contains(mapped.Body.Error.Message, "patient-payload") {
		t.Fatalf("public message leaked row content: %q", mapped.Body.Error.Message)
	}
	details := mapped.Body.Error.Details
	emittedIssues, ok := details["issues"].([]any)
	if !ok || len(emittedIssues) != 100 || details["totalErrors"] != int64(104) || details["truncated"] != true {
		t.Fatalf("validation details = %#v", details)
	}
	first := emittedIssues[0].(map[string]any)
	if first["filename"] != "Patient.ndjson" || first["row"] != 7 || first["resourceType"] != "Patient" ||
		first["message"] != `Required field "identifier" is missing.` {
		t.Fatalf("safe issue = %#v", first)
	}
}
