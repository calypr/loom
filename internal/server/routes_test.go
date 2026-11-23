package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	ingestevent "github.com/calypr/loom/internal/ingest/event"
	"github.com/calypr/loom/internal/ingest/generation"
)

func TestGeneratedRoutesExactlyMatchOpenAPISpec(t *testing.T) {
	want := map[string]bool{}
	spec, err := loomapi.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		t.Fatalf("embedded OpenAPI document is invalid: %v", err)
	}
	for path, item := range spec.Paths.Map() {
		for method := range item.Operations() {
			want[strings.ToUpper(method)+" "+path] = true
		}
	}

	for _, clickHouseEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("clickhouse=%t", clickHouseEnabled), func(t *testing.T) {
			authorizer := authscope.AllowAllAuthorizer{}
			server, err := api.NewHTTPServer(api.HTTPConfig{Authorizer: authorizer, ClickHouseEnabled: clickHouseEnabled})
			if err != nil {
				t.Fatal(err)
			}
			load, err := loadapi.NewService(loadapi.ServiceConfig{LoadGeneration: routeRunner{}.RunGeneration})
			if err != nil {
				t.Fatal(err)
			}
			if err := registerRoutes(server, load, authorizer, &explorerHandlers{}, routeDependencies{}); err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, route := range server.App().GetRoutes(true) {
				path := route.Path
				for _, parameter := range route.Params {
					path = strings.ReplaceAll(path, ":"+parameter, "{"+parameter+"}")
				}
				got[route.Method+" "+path] = true
			}
			for route := range want {
				if !got[route] {
					t.Errorf("OpenAPI operation is not registered: %s", route)
				}
			}
			for route := range got {
				if !want[route] {
					t.Errorf("registered route is absent from OpenAPI: %s", route)
				}
			}
			if len(got) != len(want) {
				t.Errorf("route count got=%d spec=%d", len(got), len(want))
			}
		})
	}
}

func TestOpenAPIOperationsDeclareExplicitResponses(t *testing.T) {
	spec, err := loomapi.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			name := fmt.Sprintf("%s %s", strings.ToUpper(method), path)
			responses := operation.Responses.Map()
			if _, ok := responses["default"]; ok {
				t.Errorf("%s uses a default response instead of an explicit status", name)
			}
			hasSuccess := false
			for status, responseRef := range responses {
				if strings.HasPrefix(status, "2") {
					hasSuccess = true
				}
				if responseRef == nil || responseRef.Value == nil {
					t.Errorf("%s response %s is unresolved", name, status)
					continue
				}
				if responseRef.Value.Description == nil || strings.TrimSpace(*responseRef.Value.Description) == "" {
					t.Errorf("%s response %s has no human-readable description", name, status)
				}
				if len(responseRef.Value.Content) == 0 {
					t.Errorf("%s response %s does not declare a response media type and schema", name, status)
				}
			}
			if !hasSuccess {
				t.Errorf("%s has no explicit successful response", name)
			}
			if path != "/health" && path != "/livez" && path != "/readyz" {
				if _, ok := responses["401"]; !ok {
					t.Errorf("%s omits the authentication failure produced by shared middleware", name)
				}
			}
		}
	}
}

func TestOpenAPIDocumentsOperationSpecificFailureStatuses(t *testing.T) {
	spec, err := loomapi.GetSwagger()
	if err != nil {
		t.Fatal(err)
	}
	assertStatuses := func(method, path string, statuses ...string) {
		t.Helper()
		item := spec.Paths.Value(path)
		if item == nil {
			t.Errorf("path %s not found", path)
			return
		}
		operation := item.GetOperation(method)
		if operation == nil {
			t.Errorf("%s %s not found", method, path)
			return
		}
		responses := operation.Responses.Map()
		for _, status := range statuses {
			if _, ok := responses[status]; !ok {
				t.Errorf("%s does not document response %s", operation.OperationID, status)
			}
		}
	}

	assertStatuses("POST", "/api/v1/datasets/{project}/generations/{generation}", "400", "401", "403", "409", "415", "422", "500", "503")
	assertStatuses("GET", "/api/v1/datasets/{project}/generations/{generation}", "400", "401", "403", "404", "500", "503")
	assertStatuses("POST", "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/preview", "400", "401", "403", "404", "409", "413", "422", "429", "499", "500", "503", "504")
	assertStatuses("POST", "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/cell-trace", "400", "401", "403", "404", "409", "422", "500", "503")
	assertStatuses("POST", "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-proposals", "400", "401", "403", "404", "409", "422", "500", "503")
	assertStatuses("POST", "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish", "400", "401", "403", "404", "409", "422", "500", "503")
	for _, suffix := range []string{"table-shape-capabilities", "table-shape-category-discoveries", "table-shape-resolutions", "table-shape-proposals"} {
		path := "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/" + suffix
		if spec.Paths.Value(path) != nil {
			t.Errorf("retired TableShape route remains documented: %s", path)
		}
	}

}

type routeRunner struct{}

func (routeRunner) RunGeneration(context.Context, loadapi.GenerationLoadRequest, ingestevent.EventSink) (generation.LoadSummary, error) {
	return generation.LoadSummary{}, nil
}

func TestRegisterRoutesExposesGenerationReleaseWorkflow(t *testing.T) {
	server, err := api.NewHTTPServer(api.HTTPConfig{
		Authenticator: authscope.StaticAuthenticator{},
		Authorizer:    authscope.AllowAllAuthorizer{},
	})
	if err != nil {
		t.Fatal(err)
	}
	resourceService, err := loadapi.NewService(loadapi.ServiceConfig{LoadGeneration: routeRunner{}.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRoutes(server, resourceService, authscope.AllowAllAuthorizer{}, &explorerHandlers{}, routeDependencies{}); err != nil {
		t.Fatal(err)
	}

	health, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/livez", nil))
	if err != nil {
		t.Fatal(err)
	}
	healthBody, err := io.ReadAll(health.Body)
	_ = health.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if health.StatusCode != http.StatusOK || string(healthBody) != `{"status":"live"}` {
		t.Fatalf("generated liveness response status=%d body=%s", health.StatusCode, healthBody)
	}

	for _, request := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/datasets/project/generations/generation/export"},
		{http.MethodPost, "/loom/api/v1/dataframe/export"},
		{http.MethodPut, "/api/v1/raw"},
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
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("removed resource route status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestRegisterRoutesRequiresExplorerHandlers(t *testing.T) {
	if err := registerRoutes(nil, nil, nil, nil, routeDependencies{}); err == nil || err.Error() != "create Explorer HTTP routes: Explorer handlers are required" {
		t.Fatalf("registerRoutes() error = %v, want missing Explorer handlers", err)
	}
}
