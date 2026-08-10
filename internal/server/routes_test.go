package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/ingest"
)

type routeRunner struct{}

func (routeRunner) Run(context.Context, loadapi.ImportRequest, ingest.EventSink) (ingest.LoadSummary, error) {
	return ingest.LoadSummary{}, nil
}

func TestRegisterRoutesExposesGenerationReleaseWorkflow(t *testing.T) {
	server, err := api.NewHTTPServer(api.HTTPConfig{
		Authenticator: authscope.StaticAuthenticator{},
		Authorizer:    authscope.AllowAllAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceService, err := loadapi.NewService(loadapi.ServiceConfig{Runner: routeRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRoutes(server, resourceService, nil, nil, authscope.AllowAllAuthorizer{}, &resolver.Resolver{}); err != nil {
		t.Fatal(err)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/datasets/project/generations/generation/export"},
		{http.MethodPost, "/loom/api/v1/dataframe/export"},
	} {
		resp, err := server.App().Test(httptest.NewRequest(request.method, request.path, nil))
		if err != nil {
			t.Fatalf("request %s %s: %v", request.method, request.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("route %s %s status = %d, want %d", request.method, request.path, resp.StatusCode, http.StatusNotFound)
		}
	}
	for _, request := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/v1/raw", http.StatusMethodNotAllowed},
		{http.MethodPut, "/api/v1/raw", http.StatusUnsupportedMediaType},
		{http.MethodPost, "/api/v1/datasets/project/generations/generation", http.StatusUnsupportedMediaType},
		{http.MethodPost, "/api/v1/datasets/project/generations/generation/activate", http.StatusBadRequest},
	} {
		resp, err := server.App().Test(httptest.NewRequest(request.method, request.path, nil))
		if err != nil {
			t.Fatalf("request %s %s: %v", request.method, request.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != request.want {
			t.Fatalf("route %s %s status = %d, want %d", request.method, request.path, resp.StatusCode, request.want)
		}
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodPut, "/api/v1/projects/project/resources/Patient", nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("resource route status = %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}
