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
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/gofiber/fiber/v3"
)

func TestRepositoryDeploymentPersistsExecutableDataframeSelectors(t *testing.T) {
	store := newTestExplorerStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testAuthoringV2CapabilitySnapshot()
	materialize := func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if bundle.Name != "native-test" {
			t.Fatalf("materialized identity = %s/%s", bundle.Name, bundle.TranslationVersion)
		}
		return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: "generation-a", State: "PUBLISHED", Outputs: []graphresolver.RecipeExecutionOutput{{Name: "patients", State: "PUBLISHED", Columns: []publication.PhysicalColumn{{Name: "c_patient", LogicalType: "string", ClickHouse: "String"}}}}}, nil
	}
	activationCount := 0
	activateRelease := func(_ context.Context, project, generation string, selectors []dataset.DataframeSelector) error {
		activationCount++
		if project != "project-a" || generation != "generation-a" || len(selectors) != 1 || selectors[0].Output != "patients" {
			t.Fatalf("release activation = %s/%s %#v", project, generation, selectors)
		}
		return nil
	}
	app := fiber.New()
	config := ExplorerV2LifecycleConfig{
		Capability:      func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		CapabilityToken: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		AuthorizedCapabilityCompile: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		},
		CompileReceipt: func(ctx context.Context, request ExplorerV2ReceiptCompileRequest) (*explorer.CompilationReceipt, error) {
			return persistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
		MaterializeReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
			return materialize(ctx, receipt.Bundle, bindings)
		},
		ActivateRelease: activateRelease,
	}
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)

	request := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(baselineExplorerWorkspaceV2()), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if request.StatusCode != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", request.StatusCode, request.Body)
	}
	repeated := requestJSONWithHeaders(t, app, http.MethodPost, "/api/v1/projects/project-a/generations/generation-a/explorer-config", string(baselineExplorerWorkspaceV2()), map[string]string{"X-Loom-Source-Commit": "commit-a"})
	if repeated.StatusCode != http.StatusOK {
		t.Fatalf("repeated deploy status=%d body=%s", repeated.StatusCode, repeated.Body)
	}
	if activationCount != 2 {
		t.Fatalf("release activation count = %d, want 2", activationCount)
	}
	state, err := service.Get(context.Background(), "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Dataset.Outputs) != 1 || state.Dataset.Outputs[0].Selector == nil {
		t.Fatalf("dataset selector missing: %#v", state.Dataset.Outputs)
	}
	selector := state.Dataset.Outputs[0].Selector
	if selector.Recipe != "native-test" || selector.Output != "patients" {
		t.Fatalf("dataset selector = %#v", selector)
	}
	if len(state.Materializations) != 1 || state.Materializations[0].Selector == nil || *state.Materializations[0].Selector != *selector {
		t.Fatalf("materialization selector = %#v", state.Materializations)
	}
	active, err := service.ActiveRevision(context.Background(), "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.AuthoringBundle) == 0 || active.CompilationReceiptID == "" || len(active.PublicOutputContract) == 0 {
		t.Fatalf("active repository revision lost V2 artifacts: %#v", active)
	}
	builder := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if builder.StatusCode != http.StatusOK || !strings.Contains(builder.Body, `"workspace"`) {
		t.Fatalf("builder reload status=%d body=%s", builder.StatusCode, builder.Body)
	}
	viewer := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	if viewer.StatusCode != http.StatusOK || !strings.Contains(viewer.Body, `"label":"Patient ID"`) {
		t.Fatalf("viewer state status=%d body=%s", viewer.StatusCode, viewer.Body)
	}
	store.mu.Lock()
	broken := store.revisions[active.ID]
	broken.PublicOutputContract = nil
	store.revisions[active.ID] = broken
	store.mu.Unlock()
	withoutContract := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if withoutContract.StatusCode != http.StatusOK || !strings.Contains(withoutContract.Body, `"workspace"`) {
		t.Fatalf("builder should hydrate valid intent without runtime contract: status=%d body=%s", withoutContract.StatusCode, withoutContract.Body)
	}
	store.mu.Lock()
	broken = store.revisions[active.ID]
	broken.AuthoringBundle = nil
	store.revisions[active.ID] = broken
	store.mu.Unlock()
	missing := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if missing.StatusCode != http.StatusConflict || !strings.Contains(missing.Body, `"code":"AUTHORING_STATE_MISSING"`) {
		t.Fatalf("missing active authoring state status=%d body=%s", missing.StatusCode, missing.Body)
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
