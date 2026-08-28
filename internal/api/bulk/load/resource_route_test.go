package load

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type capturingResourceRunner struct {
	req     ImportRequest
	content []byte
	summary ingest.LoadSummary
}

func (r *capturingResourceRunner) Run(_ context.Context, req ImportRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.req = req
	content, err := os.ReadFile(req.StagedFilePath)
	if err != nil {
		return ingest.LoadSummary{}, err
	}
	r.content = content
	return r.summary, nil
}

func TestResourceRouteUsesPathIdentityAndMultipartFile(t *testing.T) {
	runner := &capturingResourceRunner{summary: ingest.LoadSummary{Files: 1, VerticesInserted: 2}}
	service, err := NewService(ServiceConfig{Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authscope.StaticAuthenticator{}, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{Service: service, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	server.App().Put("/api/v1/projects/:project/resources/:resourceType", handler.HandleResource)

	req := resourceMultipartRequest(t, map[string]string{"auth_resource_path": "/programs/p1/projects/p1"}, "file", "Patient.ndjson", []byte("{\"resourceType\":\"Patient\",\"id\":\"1\"}\n"))
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
		t.Fatal("resource route must use overwrite mode, not truncate mode")
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

func resourceMultipartRequest(t *testing.T, fields map[string]string, fileField, fileName string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
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
	req := httptest.NewRequest(http.MethodPut, "/", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
