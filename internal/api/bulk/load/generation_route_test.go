package load

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type fakeGenerationRunner struct {
	summary ingest.LoadSummary
	req     GenerationLoadRequest
}

func (r *fakeGenerationRunner) RunGeneration(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.req = req
	return r.summary, nil
}

func (r *fakeGenerationRunner) Run(context.Context, ImportRequest, ingest.EventSink) (ingest.LoadSummary, error) {
	return ingest.LoadSummary{}, nil
}

func TestCreateGenerationStagesCompleteBundle(t *testing.T) {
	runner := &fakeGenerationRunner{summary: ingest.LoadSummary{Files: 2, VerticesInserted: 4}}
	svc, err := NewService(ServiceConfig{Loader: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	req := newGenerationMultipartRequest(t, "P1", "generation-1", map[string][]byte{
		"Patient.ndjson":  []byte(`{"resourceType":"Patient","id":"1"}` + "\n"),
		"Specimen.ndjson": []byte(`{"resourceType":"Specimen","id":"2"}` + "\n"),
	})
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if runner.req.Project != "P1" || runner.req.Generation != "generation-1" {
		t.Fatalf("request = %#v", runner.req)
	}
}

func TestCreateGenerationPropagatesDeferredActivation(t *testing.T) {
	runner := &fakeGenerationRunner{}
	svc, err := NewService(ServiceConfig{Loader: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	req := newGenerationMultipartRequestWithDefer(t, "P1", "generation-1", true, map[string][]byte{
		"Patient.ndjson": []byte(`{"resourceType":"Patient","id":"1"}` + "\n"),
	})
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !runner.req.DeferActivation {
		t.Fatalf("generation request = %#v, want deferred activation", runner.req)
	}
}

func newGenerationMultipartRequest(t *testing.T, project, generation string, files map[string][]byte) *http.Request {
	return newGenerationMultipartRequestWithDefer(t, project, generation, false, files)
}

func newGenerationMultipartRequestWithDefer(t *testing.T, project, generation string, deferActivation bool, files map[string][]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("project", project)
	_ = writer.WriteField("generation", generation)
	if deferActivation {
		_ = writer.WriteField("defer_activation", "true")
	}
	for name, content := range files {
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/P1/generations/generation-1", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
