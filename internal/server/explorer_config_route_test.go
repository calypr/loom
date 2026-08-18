package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func TestRepositoryDeploymentPersistsExecutableDataframeSelectors(t *testing.T) {
	service, err := explorer.NewService(explorer.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	materialize := func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if bundle.Name != "explorer_project-a_default" || bundle.TranslationVersion != "repository-commit-a" {
			t.Fatalf("materialized identity = %s/%s", bundle.Name, bundle.TranslationVersion)
		}
		return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: "generation-a", State: "PUBLISHED", Outputs: []graphresolver.RecipeExecutionOutput{{Name: "DocumentReference", State: "PUBLISHED"}}}, nil
	}
	app := fiber.New()
	RegisterExplorerConfigV2Route(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, materialize)

	request := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(baselineExplorerConfigV2("project-a", "Default")), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if request.StatusCode != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", request.StatusCode, request.Body)
	}
	repeated := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(baselineExplorerConfigV2("project-a", "Default")), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if repeated.StatusCode != http.StatusOK {
		t.Fatalf("repeated deploy status=%d body=%s", repeated.StatusCode, repeated.Body)
	}
	state, err := getExplorerV2State(context.Background(), service, "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Dataset.Outputs) != 1 || state.Dataset.Outputs[0].Selector == nil {
		t.Fatalf("dataset selector missing: %#v", state.Dataset.Outputs)
	}
	selector := state.Dataset.Outputs[0].Selector
	if selector.Recipe != "explorer_project-a_default" || selector.TranslationVersion != "repository-commit-a" || selector.Output != "DocumentReference" {
		t.Fatalf("dataset selector = %#v", selector)
	}
	if len(state.Materializations) != 1 || state.Materializations[0].Selector == nil || *state.Materializations[0].Selector != *selector {
		t.Fatalf("materialization selector = %#v", state.Materializations)
	}
}

func requestJSONWithHeaders(t *testing.T, app *fiber.App, method, path, body string, headers map[string]string) testHTTPResponse {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return testHTTPResponse{StatusCode: response.StatusCode, Body: string(raw)}
}
