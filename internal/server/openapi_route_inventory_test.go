package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/gofiber/fiber/v3"
)

func TestGeneratedRoutesExactlyMatchOpenAPISpec(t *testing.T) {
	app := fiber.New()
	handler := loomapi.NewStrictHandler(&HTTPRoutes{}, nil)
	loomapi.RegisterHandlersWithOptions(app, handler, loomapi.FiberServerOptions{})

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

	got := map[string]bool{}
	for _, route := range app.GetRoutes(true) {
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
	if len(got) != 22 || len(want) != 22 {
		t.Errorf("route count got=%d spec=%d, want 22", len(got), len(want))
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
	assertStatuses("POST", "/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish", "400", "401", "403", "404", "409", "422", "500", "503")
}
