package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

type recordingWriteAuthorizer struct {
	paths []string
}

func (a *recordingWriteAuthorizer) AuthorizeWrite(_ context.Context, _ *authscope.Principal, _, authResourcePath string) error {
	a.paths = append(a.paths, authResourcePath)
	return errors.New("stop after authorization")
}

func generatedMutationTestApp(authorizer authscope.Authorizer) *fiber.App {
	app := fiber.New()
	routes := &HTTPRoutes{explorer: &explorerHTTPHandlers{
		authorizer:    authorizer,
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return nil },
		application:   &lifecycle.Service{},
	}}
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	return app
}

func TestGeneratedExplorerMutationRoutesForwardAuthResourcePath(t *testing.T) {
	authorizer := &recordingWriteAuthorizer{}
	app := generatedMutationTestApp(authorizer)
	const scope = "/programs/HTAN_INT/projects/BForePC"
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "create", path: "/api/v1/projects/HTAN_INT%252FBForePC/explorers", body: `{"name":"test"}`},
		{name: "commands", path: "/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/commands", body: `{}`},
		{name: "reconcile", path: "/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/reconcile", body: `{}`},
		{name: "publish", path: "/api/v1/projects/HTAN_INT%252FBForePC/explorers/test/authoring/v2/publish", body: `{}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path+"?auth_resource_path=%2Fprograms%2FHTAN_INT%2Fprojects%2FBForePC", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusForbidden)
			}
			if got := authorizer.paths[len(authorizer.paths)-1]; got != scope {
				t.Fatalf("auth resource path=%q, want %q", got, scope)
			}
		})
	}
}

func TestGeneratedExplorerMutationRoutePreservesNoAuthOmission(t *testing.T) {
	authorizer := &recordingWriteAuthorizer{}
	app := generatedMutationTestApp(authorizer)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/NCPI_ACCEPTANCE/explorers", strings.NewReader(`{"name":"test"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if len(authorizer.paths) != 1 || authorizer.paths[0] != "" {
		t.Fatalf("auth resource paths=%q, want one empty path", authorizer.paths)
	}
}
