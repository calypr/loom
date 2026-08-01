package load

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/calypr/loom/internal/ingest"
)

type capturingBulkRunner struct {
	req     ImportRequest
	content []byte
	summary ingest.LoadSummary
}

func (r *capturingBulkRunner) Run(_ context.Context, req ImportRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.req = req
	content, err := os.ReadFile(req.StagedFilePath)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	r.content = content
	return r.summary, nil
}

func TestBulkResourceUsesPathIdentityAndMultipartFile(t *testing.T) {
	runner := &capturingBulkRunner{summary: ingest.LoadSummary{Files: 1, VerticesInserted: 2}}
	svc, err := NewService(ServiceConfig{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc})
	if err != nil {
		t.Fatal(err)
	}

	req := newMultipartRequest(t, map[string]string{"auth_resource_path": "/programs/p1/projects/p1"}, "file", "Patient.ndjson", []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n"))
	req.Method = http.MethodPut
	req.URL.Path = "/api/v1/projects/P1/resources/Patient"

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if runner.req.Project != "P1" || runner.req.ResourceType != "Patient" {
		t.Fatalf("path identity not passed to loader: %+v", runner.req)
	}
	if runner.req.AuthResourcePath != "p1-p1" {
		t.Fatalf("auth resource path = %q, want normalized project key", runner.req.AuthResourcePath)
	}
	if runner.req.Truncate {
		t.Fatal("bulk resource route must use overwrite mode, not truncate mode")
	}
	if string(runner.content) != "{\"resourceType\":\"Patient\",\"id\":\"1\"}\n" {
		t.Fatalf("unexpected staged content: %q", runner.content)
	}
	if _, err := os.Stat(runner.req.StagedFilePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged upload still exists after request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["project"] != "P1" || payload["resource_type"] != "Patient" {
		t.Fatalf("unexpected response: %#v", payload)
	}
}

func TestBulkResourceRouteIsIndependentOfLegacyImportDisableFlag(t *testing.T) {
	runner := &capturingBulkRunner{}
	svc, err := NewService(ServiceConfig{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc, DisableSingleResourceImports: true})
	if err != nil {
		t.Fatal(err)
	}

	req := newMultipartRequest(t, nil, "file", "Specimen.ndjson", []byte("{\"resourceType\":\"Specimen\",\"id\":\"1\"}\n"))
	req.Method = http.MethodPut
	req.URL.Path = "/api/v1/projects/P1/resources/Specimen"
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
