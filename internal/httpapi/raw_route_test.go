package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type fakeRawExporter struct {
	request RawDumpRequest
}

func (f *fakeRawExporter) ResolveGeneration(context.Context, string, string) (string, error) {
	return "active-1", nil
}

func (f *fakeRawExporter) ExportRaw(context.Context, string, string, authscope.ReadScope, io.Writer) error {
	return nil
}

func (f *fakeRawExporter) ExportRawFiltered(_ context.Context, req RawDumpRequest, _ authscope.ReadScope, out io.Writer) error {
	f.request = req
	_, err := io.WriteString(out, `{"resourceType":"Patient","id":"p1"}`+"\n")
	return err
}

type capturingBundleRunner struct {
	request GenerationLoadRequest
	files   map[string]string
}

func (r *capturingBundleRunner) RunBundle(_ context.Context, req GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.request = req
	r.files = map[string]string{}
	entries, err := os.ReadDir(req.StagedDir)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(req.StagedDir, entry.Name()))
		if err != nil {
			return ingest.LoadSummary{}, err
		}
		r.files[entry.Name()] = string(content)
	}
	return ingest.LoadSummary{Files: len(entries), VerticesInserted: 2}, nil
}

func TestDumpRawAcceptsResourceTypeAndLimit(t *testing.T) {
	service, err := NewService(ServiceConfig{Runner: fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	exporter := &fakeRawExporter{}
	server, err := NewHTTPServer(HTTPConfig{Service: service, RawExporter: exporter, DisableSingleResourceImports: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/api/v1/raw?project=P1&resourceType=Patient&limit=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if exporter.request.Project != "P1" || exporter.request.ResourceType != "Patient" || exporter.request.Limit != 2 || exporter.request.Generation != "active-1" {
		t.Fatalf("request = %#v", exporter.request)
	}
}

func TestLoadRawPartitionsMixedFHIRNDJSON(t *testing.T) {
	runner := &capturingBundleRunner{}
	service, err := NewService(ServiceConfig{Runner: fakeRunner{}, BundleRunner: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: service})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"resourceType":"Patient","id":"p1"}` + "\n" + `{"resourceType":"Specimen","id":"s1"}` + "\n"
	req := httptest.NewRequest(http.MethodPut, "/api/v1/raw?project=P1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s", resp.StatusCode, payload)
	}
	if runner.request.Project != "P1" || len(runner.files) != 2 {
		t.Fatalf("request = %#v files = %#v", runner.request, runner.files)
	}
}

func TestLoadRawUsesWriteAuthorization(t *testing.T) {
	service, err := NewService(ServiceConfig{Runner: fakeRunner{}, BundleRunner: &capturingBundleRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: service, Authorizer: denyAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/raw?project=P1", strings.NewReader(`{"resourceType":"Patient","id":"p1"}`+"\n"))
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}
