package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublishStreamsMultipartAndAuthorization(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "{\"resourceType\":\"Patient\",\"id\":\"p1\"}\n")
	var received upload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "operation-1" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var err error
		received, err = readUpload(r)
		if err != nil {
			t.Errorf("read multipart upload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"operation_id":"operation-1","source_revision":7,"manifest_hash":"`+strings.Repeat("a", 64)+`","execution_id":"execution-7","state":"PUBLISHED","reused":false}`)
	}))
	defer server.Close()

	client, err := New(server.URL, "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Publish(context.Background(), sourceRequest(localPath))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receipt.OperationID != "operation-1" || receipt.SourceRevision != 7 || receipt.ManifestHash != strings.Repeat("a", 64) || receipt.ExecutionID != "execution-7" || receipt.State != "PUBLISHED" || receipt.Reused {
		t.Fatalf("receipt = %#v", receipt)
	}
	if received.fields["auth_resource_path"] != "program/project" {
		t.Fatalf("auth_resource_path = %q", received.fields["auth_resource_path"])
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(received.fields["manifest"]), &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if manifest.Repository != "https://github.com/example/data" || manifest.Commit != strings.Repeat("b", 40) || len(manifest.Files) != 1 || manifest.Files[0].Path != "META/Patient.ndjson" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if len(received.files) != 1 || received.files[0].name != "Patient.ndjson" || received.files[0].content != "{\"resourceType\":\"Patient\",\"id\":\"p1\"}\n" {
		t.Fatalf("uploaded files = %#v", received.files)
	}
}

func TestPublishRetriesRetryableResponseAndReopensFiles(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "first payload\n")
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)
		uploaded, err := readUpload(r)
		if err != nil {
			t.Errorf("read attempt %d: %v", attempt, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(uploaded.files) != 1 || uploaded.files[0].content != "first payload\n" {
			t.Errorf("attempt %d file = %#v", attempt, uploaded.files)
		}
		if attempt == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"code":"BACKEND_UNAVAILABLE","message":"try again","retryable":true}}`)
			return
		}
		writeReceipt(w, "operation-1")
	}))
	defer server.Close()
	client, err := New(server.URL, "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Publish(context.Background(), sourceRequest(localPath))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receipt.State != "PUBLISHED" || attempts.Load() != 2 {
		t.Fatalf("receipt = %#v, attempts = %d", receipt, attempts.Load())
	}
}

func TestPublishRetriesTransportFailureDuringUpload(t *testing.T) {
	payload := strings.Repeat("z", 2<<20)
	localPath := writeFixture(t, "Patient.ndjson", payload)
	var attempts atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			_, _ = io.CopyN(io.Discard, request.Body, 4096)
			_ = request.Body.Close()
			return nil, errors.New("injected transient transport failure")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if !bytes.Contains(body, []byte(payload)) {
			return nil, errors.New("retried multipart body did not contain the staged file")
		}
		return response(http.StatusOK, receiptJSON("operation-1", 3, strings.Repeat("c", 64), "execution-3", "PUBLISHED")), nil
	})
	client, err := New("https://loom.example", "test-secret", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Publish(context.Background(), sourceRequest(localPath))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if attempts.Load() != 2 || receipt.ExecutionID != "execution-3" {
		t.Fatalf("attempts = %d, receipt = %#v", attempts.Load(), receipt)
	}
}

func TestPublishRetriesTruncatedAcknowledgementAfterCommit(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "resource\n")
	var attempts atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempt := attempts.Add(1)
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			return nil, err
		}
		if attempt == 1 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       &truncatedBody{data: []byte(`{"operation_id":"operation-1"}`)},
				Request:    request,
			}, nil
		}
		body := receiptJSON("operation-1", 4, strings.Repeat("d", 64), "execution-4", "PUBLISHED")
		body = strings.Replace(body, `"reused":false`, `"reused":true`, 1)
		return response(http.StatusOK, body), nil
	})
	client, err := New("https://loom.example", "test-secret", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Publish(context.Background(), sourceRequest(localPath))
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if attempts.Load() != 2 || !receipt.Reused || receipt.ExecutionID != "execution-4" {
		t.Fatalf("attempts = %d, receipt = %#v", attempts.Load(), receipt)
	}
}

func TestPublishRedactsTokenFromStructuredError(t *testing.T) {
	const token = "secret-token-value"
	localPath := writeFixture(t, "Patient.ndjson", "resource\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = readUpload(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `{"error":{"code":"FORBIDDEN","message":"rejected %s","requestId":"request-1","retryable":false}}`, token)
	}))
	defer server.Close()
	client, err := New(server.URL, token, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Publish(context.Background(), sourceRequest(localPath))
	var publishErr *Error
	if !errors.As(err, &publishErr) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if publishErr.Code != "FORBIDDEN" || publishErr.StatusCode != http.StatusForbidden || publishErr.Retryable || publishErr.RequestID != "request-1" {
		t.Fatalf("structured error = %#v", publishErr)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(publishErr.Message, token) {
		t.Fatalf("token leaked in error: %v", err)
	}
}

func TestPublishRejectsInvalidReceiptsWithoutRetry(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "wrong operation", body: receiptJSON("another-operation", 1, strings.Repeat("a", 64), "execution-1", "PUBLISHED")},
		{name: "zero revision", body: receiptJSON("operation-1", 0, strings.Repeat("a", 64), "execution-1", "PUBLISHED")},
		{name: "invalid hash", body: receiptJSON("operation-1", 1, "no-hash", "execution-1", "PUBLISHED")},
		{name: "missing execution", body: receiptJSON("operation-1", 1, strings.Repeat("a", 64), "", "PUBLISHED")},
		{name: "wrong state", body: receiptJSON("operation-1", 1, strings.Repeat("a", 64), "execution-1", "RUNNING")},
		{name: "invalid JSON", body: "{"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			localPath := writeFixture(t, "Patient.ndjson", "resource\n")
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				_, _ = readUpload(r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(server.URL, "test-secret", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Publish(context.Background(), sourceRequest(localPath))
			if err == nil {
				t.Fatal("Publish() error = nil, want invalid receipt error")
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

func TestPublishCancellationInterruptsRetryDelay(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "resource\n")
	firstRequest := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = readUpload(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"BACKEND_UNAVAILABLE","message":"retry","retryable":true}}`)
		firstRequest <- struct{}{}
	}))
	defer server.Close()
	client, err := New(server.URL, "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Publish(ctx, sourceRequest(localPath))
		done <- err
	}()
	<-firstRequest
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

func TestPublishRejectsUnsafeManifestFilenameBeforeNetwork(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "resource\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := New(server.URL, "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	request := sourceRequest(localPath)
	request.Manifest.Files[0].Path = "META/Patient\r\nX.ndjson"
	request.Files[0].Path = request.Manifest.Files[0].Path
	if _, err := client.Publish(context.Background(), request); err == nil {
		t.Fatal("Publish() error = nil for a control character in the multipart filename")
	}
	if requests.Load() != 0 {
		t.Fatalf("server requests = %d, want no request", requests.Load())
	}
}

func TestPublishRejectsOperationIDWhitespaceBeforeNetwork(t *testing.T) {
	localPath := writeFixture(t, "Patient.ndjson", "resource\n")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := New(server.URL, "test-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, operationID := range []string{" operation-1", "operation-1 ", "\toperation-1", "operation-1\t"} {
		request := sourceRequest(localPath)
		request.OperationID = operationID
		_, err := client.Publish(context.Background(), request)
		var publishErr *Error
		if !errors.As(err, &publishErr) || publishErr.Code != "INVALID_REQUEST" {
			t.Fatalf("Publish(%q) error = %v, want INVALID_REQUEST", operationID, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("server requests = %d, want no request", requests.Load())
	}
}

type uploadedFile struct {
	name    string
	content string
}

type upload struct {
	fields map[string]string
	files  []uploadedFile
}

func readUpload(request *http.Request) (upload, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return upload{}, err
	}
	result := upload{fields: make(map[string]string)}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return result, nil
		}
		if err != nil {
			return upload{}, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return upload{}, err
		}
		if part.FileName() == "" {
			result.fields[part.FormName()] = string(data)
		} else {
			result.files = append(result.files, uploadedFile{name: part.FileName(), content: string(data)})
		}
	}
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	localPath := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(localPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return localPath
}

func sourceRequest(localPath string) Request {
	return Request{
		Project: "project-1", AuthResourcePath: "program/project", OperationID: "operation-1",
		Manifest: Manifest{
			Repository: "https://github.com/example/data", Commit: strings.Repeat("b", 40),
			Files: []ManifestFile{{Path: "META/Patient.ndjson"}},
		},
		Files: []File{{Path: "META/Patient.ndjson", LocalPath: localPath}},
	}
}

func writeReceipt(w http.ResponseWriter, operationID string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, receiptJSON(operationID, 7, strings.Repeat("a", 64), "execution-7", "PUBLISHED"))
}

func receiptJSON(operationID string, revision int64, manifestHash, executionID, state string) string {
	return fmt.Sprintf(`{"operation_id":%q,"source_revision":%d,"manifest_hash":%q,"execution_id":%q,"state":%q,"reused":false}`, operationID, revision, manifestHash, executionID, state)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type truncatedBody struct {
	data []byte
	done bool
}

func (b *truncatedBody) Read(data []byte) (int, error) {
	if b.done {
		return 0, io.ErrUnexpectedEOF
	}
	b.done = true
	return copy(data, b.data), nil
}

func (b *truncatedBody) Close() error { return nil }

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}
