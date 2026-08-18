package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func TestExplorerV2RESTLifecycle(t *testing.T) {
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	repository := baselineExplorerConfigV2("project-a", "Default")
	var repositoryConfig explorer.ConfigV2
	if err := json.Unmarshal(repository, &repositoryConfig); err != nil {
		t.Fatal(err)
	}
	repositoryConfig.Explorer.Management = "repository"
	repositoryConfig.Views = []explorer.ConfigView{{ID: "document-reference", Title: "Documents", Output: "DocumentReference", Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Label: "ID", Visible: true}}}}}
	repository, err = json.Marshal(repositoryConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveRepositoryConfig(context.Background(), explorer.RepositoryConfig{Project: "project-a", Config: repository, SourceGeneration: "generation-a"}); err != nil {
		t.Fatal(err)
	}
	repositoryBundle := testV2Bundle("repository")
	bundle := testV2Bundle("explorer-custom")
	compile := func(_ context.Context, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, error) {
		compiledBundle := bundle
		if request.ExplorerID == "default" {
			compiledBundle = repositoryBundle
		}
		digest, _ := compiledBundle.Digest()
		return ExplorerV2CompileResult{Config: request.Config, Bundle: compiledBundle, RecipeDigest: digest, SourceGeneration: "generation-a", EmittedColumns: []explorer.EmittedColumn{{OutputID: "DocumentReference", PublicColumn: "id", SelectionID: "id", LogicalType: "string"}}}, nil
	}
	preview := func(_ context.Context, _ recipe.Bundle, _ recipe.RuntimeBindings) (map[string][]map[string]any, error) {
		return map[string][]map[string]any{"DocumentReference": {{"id": "dr-1"}}}, nil
	}
	materializeFails := false
	materialize := func(_ context.Context, _ recipe.Bundle, _ recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if materializeFails {
			return graphresolver.RecipeExecution{}, errors.New("backend unavailable")
		}
		return graphresolver.RecipeExecution{ID: "execution-a", SourceGeneration: "generation-a", State: "PUBLISHED", Outputs: []graphresolver.RecipeExecutionOutput{{Name: "DocumentReference", State: "PUBLISHED"}}}, nil
	}
	var releaseActivations [][]dataset.DataframeSelector
	releaseFails := false
	activateRelease := func(_ context.Context, project, generation string, selectors []dataset.DataframeSelector) error {
		if project != "project-a" || generation != "generation-a" {
			t.Fatalf("release identity = %s/%s", project, generation)
		}
		if releaseFails {
			return errors.New("release conflict")
		}
		releaseActivations = append(releaseActivations, append([]dataset.DataframeSelector(nil), selectors...))
		return nil
	}
	catalog := func(_ context.Context, project, _ string, _ string) (explorer.CatalogSnapshot, error) {
		return explorer.NewCatalogSnapshot(project, "generation-a", "scope-a", explorer.Catalog{
			Nodes: []explorer.CatalogNode{{ID: "node-document-reference", ResourceType: "DocumentReference"}},
			Selections: map[string]explorer.CatalogSelection{
				"selection-id": {ID: "selection-id", NodeID: "node-document-reference", FieldRef: "DocumentReference.id", Select: "id", LogicalType: "string", Filterable: true},
			},
		}, true, false, nil)
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{Compile: compile, Catalog: catalog, Preview: preview, Materialize: materialize, ActivateRelease: activateRelease})

	catalogResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer/authoring/catalog", "")
	if catalogResponse.StatusCode != http.StatusOK || !strings.Contains(catalogResponse.Body, `"snapshotToken":"sha256:`) || !strings.Contains(catalogResponse.Body, `"selectionId":"selection-id"`) {
		t.Fatalf("catalog status=%d body=%s", catalogResponse.StatusCode, catalogResponse.Body)
	}

	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Custom Explorer"}`)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.StatusCode, response.Body)
	}
	var created explorerV2State
	decodeBody(t, response.Body, &created)
	if created.ExplorerID != "custom-explorer" || created.Management != explorer.ManagementInteractive || created.DraftVersion != 1 {
		t.Fatalf("created state=%#v", created)
	}

	list := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers", "")
	if list.StatusCode != http.StatusOK || !strings.Contains(list.Body, `"explorerId":"default"`) || !strings.Contains(list.Body, `"explorerId":"custom-explorer"`) {
		t.Fatalf("list status=%d body=%s", list.StatusCode, list.Body)
	}
	defaultResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	if defaultResponse.StatusCode != http.StatusOK {
		t.Fatalf("default status=%d body=%s", defaultResponse.StatusCode, defaultResponse.Body)
	}
	var defaultState explorerV2State
	decodeBody(t, defaultResponse.Body, &defaultState)
	if len(defaultState.BaselineConfig) == 0 || len(defaultState.DraftConfig) == 0 || len(defaultState.ActiveConfig) == 0 {
		t.Fatalf("default state lifecycle config mismatch: %#v", defaultState)
	}
	var defaultConfig explorer.ConfigV2
	decodeBody(t, string(defaultState.BaselineConfig), &defaultConfig)
	if len(defaultConfig.Views) != 0 {
		t.Fatalf("default baseline contains views: %#v", defaultConfig.Views)
	}
	var activeDefaultConfig explorer.ConfigV2
	decodeBody(t, string(defaultState.ActiveConfig), &activeDefaultConfig)
	if len(activeDefaultConfig.Views) != 1 || activeDefaultConfig.Views[0].Output != "DocumentReference" {
		t.Fatalf("default active config lost presentation: %#v", activeDefaultConfig.Views)
	}
	var defaultDraft map[string]any
	if err := json.Unmarshal(defaultState.DraftConfig, &defaultDraft); err != nil {
		t.Fatal(err)
	}
	defaultDraft["explorer"].(map[string]any)["title"] = "Edited Default"
	defaultDraftBytes, _ := json.Marshal(defaultDraft)
	defaultDraftRequest, _ := json.Marshal(map[string]any{"config": json.RawMessage(defaultDraftBytes), "expectedDraftVersion": defaultState.DraftVersion, "expectedDraftDigest": defaultState.DraftDigest})
	defaultWrite := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/default/draft", string(defaultDraftRequest))
	if defaultWrite.StatusCode != http.StatusOK {
		t.Fatalf("default draft write status=%d body=%s", defaultWrite.StatusCode, defaultWrite.Body)
	}
	var updatedDefault explorerV2State
	decodeBody(t, defaultWrite.Body, &updatedDefault)
	if updatedDefault.DraftVersion != defaultState.DraftVersion+1 || updatedDefault.DraftDigest == defaultState.DraftDigest {
		t.Fatalf("default draft state=%#v", updatedDefault)
	}
	defaultCompileRequest, _ := json.Marshal(map[string]any{"output": "DocumentReference", "config": json.RawMessage(updatedDefault.DraftConfig), "expectedDraftVersion": updatedDefault.DraftVersion})
	defaultCompiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/default/authoring/compile", string(defaultCompileRequest))
	if defaultCompiled.StatusCode != http.StatusOK {
		t.Fatalf("default compile status=%d body=%s", defaultCompiled.StatusCode, defaultCompiled.Body)
	}
	defaultPublishRequest := `{"expectedDraftVersion":` + fmt.Sprint(updatedDefault.DraftVersion) + `,"expectedDraftDigest":"` + updatedDefault.DraftDigest + `"}`
	defaultPublished := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/default/publish", defaultPublishRequest)
	if defaultPublished.StatusCode != http.StatusOK || !strings.Contains(defaultPublished.Body, `"publicationId"`) {
		t.Fatalf("default publish status=%d body=%s", defaultPublished.StatusCode, defaultPublished.Body)
	}
	if len(releaseActivations) != 1 || len(releaseActivations[0]) != 1 || releaseActivations[0][0].Output != "DocumentReference" {
		t.Fatalf("default release activations = %#v", releaseActivations)
	}
	defaultAfterPublish := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	var activeDefaultState explorerV2State
	decodeBody(t, defaultAfterPublish.Body, &activeDefaultState)
	if defaultAfterPublish.StatusCode != http.StatusOK || activeDefaultState.ActiveRevisionID == "" || string(activeDefaultState.ActiveConfig) != string(activeDefaultState.DraftConfig) {
		t.Fatalf("default active state=%#v", activeDefaultState)
	}

	var draft map[string]any
	if err := json.Unmarshal(created.DraftConfig, &draft); err != nil {
		t.Fatal(err)
	}
	if len(draft["views"].([]any)) == 0 {
		t.Fatalf("custom fork did not receive presentation views: %s", created.DraftConfig)
	}
	draftExplorer := draft["explorer"].(map[string]any)
	draftExplorer["title"] = "Edited"
	draftBytes, _ := json.Marshal(draft)
	draftRequest, _ := json.Marshal(map[string]any{"config": json.RawMessage(draftBytes), "expectedDraftVersion": created.DraftVersion, "expectedDraftDigest": created.DraftDigest})
	saved := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom-explorer/draft", string(draftRequest))
	if saved.StatusCode != http.StatusOK {
		t.Fatalf("draft status=%d body=%s", saved.StatusCode, saved.Body)
	}
	var updated explorerV2State
	decodeBody(t, saved.Body, &updated)
	if updated.DraftVersion != 2 || updated.DraftDigest == created.DraftDigest {
		t.Fatalf("saved state=%#v", updated)
	}
	stale := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom-explorer/draft", string(draftRequest))
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, `"code":"DRAFT_CONFLICT"`) || !strings.Contains(stale.Body, `"currentVersion":2`) {
		t.Fatalf("stale status=%d body=%s", stale.StatusCode, stale.Body)
	}

	compileRequest, _ := json.Marshal(map[string]any{"output": "DocumentReference", "config": json.RawMessage(updated.DraftConfig), "expectedDraftVersion": updated.DraftVersion})
	compiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/authoring/compile", string(compileRequest))
	if compiled.StatusCode != http.StatusOK || !strings.Contains(compiled.Body, `"recipeDigest"`) {
		t.Fatalf("compile status=%d body=%s", compiled.StatusCode, compiled.Body)
	}

	previewRequest, _ := json.Marshal(map[string]any{"config": json.RawMessage(updated.DraftConfig), "output": "DocumentReference", "limit": 25, "draftDigest": updated.DraftDigest})
	previewResponse := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/preview", string(previewRequest))
	if previewResponse.StatusCode != http.StatusOK || !strings.Contains(previewResponse.Body, `"rowCount":1`) {
		t.Fatalf("preview status=%d body=%s", previewResponse.StatusCode, previewResponse.Body)
	}
	beforePublish, err := service.ActiveRevision(context.Background(), "project-a", "custom-explorer")
	if err != explorer.ErrNotFound {
		t.Fatalf("active revision before publish=%v, want ErrNotFound", beforePublish)
	}

	publishRequest := `{"expectedDraftVersion":2,"expectedDraftDigest":"` + updated.DraftDigest + `"}`
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/publish", publishRequest)
	if published.StatusCode != http.StatusOK || !strings.Contains(published.Body, `"publicationId"`) || !strings.Contains(published.Body, `"activeUrl"`) {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, published.Body)
	}
	if len(releaseActivations) != 2 || len(releaseActivations[1]) != 1 || releaseActivations[1][0].Recipe != bundle.Name {
		t.Fatalf("custom release activations = %#v", releaseActivations)
	}
	stateResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var active explorerV2State
	decodeBody(t, stateResponse.Body, &active)
	if stateResponse.StatusCode != http.StatusOK || active.ActiveRevisionID == "" || string(active.ActiveConfig) != string(active.DraftConfig) {
		t.Fatalf("active state=%#v", active)
	}
	priorActiveRevision := active.ActiveRevisionID
	releaseFails = true
	draftExplorer["title"] = "Failed release draft"
	draftBytes, _ = json.Marshal(draft)
	draftRequest, _ = json.Marshal(map[string]any{"config": json.RawMessage(draftBytes), "expectedDraftVersion": active.DraftVersion, "expectedDraftDigest": active.DraftDigest})
	failedReleaseDraft := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom-explorer/draft", string(draftRequest))
	if failedReleaseDraft.StatusCode != http.StatusOK {
		t.Fatalf("failed release draft save status=%d body=%s", failedReleaseDraft.StatusCode, failedReleaseDraft.Body)
	}
	var failedReleaseState explorerV2State
	decodeBody(t, failedReleaseDraft.Body, &failedReleaseState)
	failedReleasePublish := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/publish", `{"expectedDraftVersion":`+fmt.Sprint(failedReleaseState.DraftVersion)+`,"expectedDraftDigest":"`+failedReleaseState.DraftDigest+`"}`)
	if failedReleasePublish.StatusCode != http.StatusConflict || !strings.Contains(failedReleasePublish.Body, `"code":"RELEASE_ACTIVATION_FAILED"`) {
		t.Fatalf("failed release publish status=%d body=%s", failedReleasePublish.StatusCode, failedReleasePublish.Body)
	}
	unchangedAfterReleaseFailure := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var releaseUnchanged explorerV2State
	decodeBody(t, unchangedAfterReleaseFailure.Body, &releaseUnchanged)
	if releaseUnchanged.ActiveRevisionID != priorActiveRevision || string(releaseUnchanged.ActiveConfig) == string(releaseUnchanged.DraftConfig) {
		t.Fatalf("failed release activation changed active state: %#v", releaseUnchanged)
	}

	releaseFails = false
	materializeFails = true
	draftExplorer["title"] = "Failed publication draft"
	draftBytes, _ = json.Marshal(draft)
	draftRequest, _ = json.Marshal(map[string]any{"config": json.RawMessage(draftBytes), "expectedDraftVersion": failedReleaseState.DraftVersion, "expectedDraftDigest": failedReleaseState.DraftDigest})
	failedDraft := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom-explorer/draft", string(draftRequest))
	if failedDraft.StatusCode != http.StatusOK {
		t.Fatalf("failed publication draft save status=%d body=%s", failedDraft.StatusCode, failedDraft.Body)
	}
	var failedDraftState explorerV2State
	decodeBody(t, failedDraft.Body, &failedDraftState)
	failedPublish := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/publish", `{"expectedDraftVersion":`+fmt.Sprint(failedDraftState.DraftVersion)+`,"expectedDraftDigest":"`+failedDraftState.DraftDigest+`"}`)
	if failedPublish.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("failed publish status=%d body=%s", failedPublish.StatusCode, failedPublish.Body)
	}
	unchangedResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var unchanged explorerV2State
	decodeBody(t, unchangedResponse.Body, &unchanged)
	if unchanged.ActiveRevisionID != priorActiveRevision || string(unchanged.ActiveConfig) == string(unchanged.DraftConfig) {
		t.Fatalf("failed publication changed active state: %#v", unchanged)
	}
}

func TestRepositoryStateExposesPublishedPacketAndPresentationFreeBaseline(t *testing.T) {
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	raw := baselineExplorerConfigV2("project-a", "Default")
	var cfg explorer.ConfigV2
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Views = []explorer.ConfigView{{ID: "document-reference", Title: "Documents", Output: "DocumentReference", Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Visible: true}}}}}
	raw, err = json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testV2Bundle("repository")
	owner, revision, err := service.UpsertRepositoryV2(context.Background(), raw, "commit-a", "generation-a", "etl", explorer.Compilation{Bundle: bundle, RecipeDigest: "sha256:recipe"}, "sha256:schema", nil, explorer.DatasetMetadata{}, explorer.PublicationMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	if owner.ActiveRevisionID != "" {
		t.Fatalf("owner was active before activation: %#v", owner)
	}
	if err := service.ActivateRepository(context.Background(), "project-a", revision.ID); err != nil {
		t.Fatal(err)
	}
	state, err := getExplorerV2State(context.Background(), service, "project-a", "default")
	if err != nil {
		t.Fatal(err)
	}
	var baseline, active explorer.ConfigV2
	decodeBody(t, string(state.BaselineConfig), &baseline)
	decodeBody(t, string(state.ActiveConfig), &active)
	if len(baseline.Views) != 0 || len(active.Views) != 1 || active.Views[0].Output != "DocumentReference" || len(state.DraftConfig) == 0 {
		t.Fatalf("repository state did not separate lifecycle configs: baseline=%#v active=%#v state=%#v", baseline.Views, active.Views, state)
	}
}

func testV2Bundle(name string) recipe.Bundle {
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: name, TranslationVersion: "interactive", Outputs: []recipe.Output{{Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "document_reference", Fields: []recipe.Field{{Name: "id", FieldRef: "DocumentReference.id", Expr: recipe.Expression{Select: "root.id"}}}}}}
}

func baselineExplorerConfigV2(project, title string) []byte {
	bundle := testV2Bundle("repository")
	recipeRaw, _ := json.Marshal(bundle)
	cfg := explorer.ConfigV2{APIVersion: explorer.ConfigV2APIVersion, Kind: "ExplorerConfig", Project: project, Explorer: explorer.ConfigExplorer{ID: "default", Title: title, Management: "repository"}, Recipe: recipeRaw}
	raw, _ := json.Marshal(cfg)
	return raw
}

type testHTTPResponse struct {
	StatusCode int
	Body       string
}

func requestJSON(t *testing.T, app *fiber.App, method, path, body string) testHTTPResponse {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
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

func decodeBody(t *testing.T, body string, value any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), value); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}
