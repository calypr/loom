package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type denyAuthorizer struct{}

func (denyAuthorizer) AuthorizeWrite(ctx context.Context, principal *authscope.Principal, project, authResourcePath string) error {
	return errors.New("nope")
}

func TestCreateImportAccepted(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1, VerticesInserted: 2, EdgesInserted: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc})
	if err != nil {
		t.Fatal(err)
	}

	req := newMultipartRequest(t, map[string]string{
		"project":       "P1",
		"resource_type": "Patient",
	}, "file", "Patient.ndjson", []byte(`{"resourceType":"Patient","id":"1"}`+"\n"))

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["resource_type"] != "Patient" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	summary, ok := payload["summary"].(map[string]any)
	if !ok {
		t.Fatalf("missing summary: %#v", payload)
	}
	if summary["vertices_inserted"] != float64(2) {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestHealthzDoesNotRequireAuthentication(t *testing.T) {
	svc, err := NewService(ServiceConfig{Runner: fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc, Authenticator: authscope.BasicAuthenticator{Username: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func TestNamedGraphQLBackendRoutes(t *testing.T) {
	svc, err := NewService(ServiceConfig{Runner: fakeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server, err := NewHTTPServer(HTTPConfig{
		Service:                  svc,
		GraphQLHandler:           handler,
		ClickHouseGraphQLHandler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/graphql/graph", "/graphql/flat"} {
		resp, err := server.App().Test(httptest.NewRequest(http.MethodPost, path, nil))
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusNoContent {
			resp.Body.Close()
			t.Fatalf("request %s status = %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodPost, "/graphql", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Fatalf("legacy /graphql unexpectedly served the GraphQL handler")
	}
}

func TestCreateImportRejectsMissingProject(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc})
	if err != nil {
		t.Fatal(err)
	}

	req := newMultipartRequest(t, map[string]string{
		"resource_type": "Patient",
	}, "file", "Patient.ndjson", []byte(`{"resourceType":"Patient","id":"1"}`+"\n"))

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestCreateImportRejectsUnauthorized(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{
		Service:    svc,
		Authorizer: denyAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := newMultipartRequest(t, map[string]string{
		"project":       "P1",
		"resource_type": "Patient",
	}, "file", "Patient.ndjson", []byte(`{"resourceType":"Patient","id":"1"}`+"\n"))

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}
}

func TestCreateImportRejectsUnsupportedMediaType(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports", strings.NewReader(`{"resourceType":"Patient"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestCreateImportIsDisabledForGenerationAwareDeployment(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{Service: svc, DisableSingleResourceImports: true})
	if err != nil {
		t.Fatal(err)
	}
	req := newMultipartRequest(t, map[string]string{
		"project":       "P1",
		"resource_type": "Patient",
	}, "file", "Patient.ndjson", []byte(`{"resourceType":"Patient","id":"1"}`+"\n"))
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d; body = %s", resp.StatusCode, http.StatusConflict, string(body))
	}
	var payload errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != "legacy_import_disabled" {
		t.Fatalf("error payload = %#v, want legacy_import_disabled", payload)
	}
}

func TestApolloSandboxRouteServed(t *testing.T) {
	svc, err := NewService(ServiceConfig{
		Runner: fakeRunner{summary: ingest.LoadSummary{Files: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewHTTPServer(HTTPConfig{
		Service:              svc,
		ApolloSandboxHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("apollo")) }),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/apollo", nil)
	resp, err := server.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "apollo" {
		t.Fatalf("unexpected body %q", string(body))
	}
}

func newMultipartRequest(t *testing.T, fields map[string]string, fileField, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/imports", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
