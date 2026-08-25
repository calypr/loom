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
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func TestExplorerV2RESTLifecycle(t *testing.T) {
	t.Skip("legacy ExplorerConfigV2 authoring routes removed by Builder cutover")
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
	repositoryConfig.Views = []explorer.ConfigView{{
		ID: "document-reference", Title: "Documents", Output: "DocumentReference",
		Table:        explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id", Label: "ID", Visible: true}}},
		Filters:      []explorer.ConfigFilter{{Column: "id", Label: "Identifier"}},
		Charts:       []explorer.ConfigChart{{Column: "id", Type: "bar", Title: "Identifiers"}},
		FixedFilters: map[string][]string{"id": {"dr-1"}},
	}}
	repositoryConfig.SharedFilters = map[string][]explorer.SharedFilter{"identifier": {{Output: "DocumentReference", Column: "id"}}}
	repository, err = json.Marshal(repositoryConfig)
	if err != nil {
		t.Fatal(err)
	}
	repositorySelector := dataset.DataframeSelector{Recipe: "repository", TranslationVersion: "interactive", Output: "DocumentReference"}
	repositoryMaterialization := explorer.Materialization{OutputID: "DocumentReference", Output: "DocumentReference", MaterializationID: "execution-a", Selector: &repositorySelector, Columns: []publication.PhysicalColumn{{Name: "id", LogicalType: "String"}}}
	if _, err := service.SaveRepositoryConfig(context.Background(), explorer.RepositoryConfig{Project: "project-a", Config: repository, SourceGeneration: "generation-a", ExecutionID: "execution-a", Materializations: []explorer.Materialization{repositoryMaterialization}, Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{Name: "DocumentReference", State: "PUBLISHED", Queryable: true, Selector: &repositorySelector, Columns: repositoryMaterialization.Columns}}}, Publication: explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a", ExecutionID: "execution-a"}}); err != nil {
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
	var listed []explorer.ExplorerStateV1
	decodeBody(t, list.Body, &listed)
	if len(listed) != 2 || listed[0].APIVersion != explorer.ExplorerStateV1APIVersion || listed[0].Kind != explorer.ExplorerStateV1Kind || strings.Contains(list.Body, `"draftConfig"`) || strings.Contains(list.Body, `"activeConfig"`) {
		t.Fatalf("list state V1 mismatch: %#v body=%s", listed, list.Body)
	}
	var listedObjects []map[string]json.RawMessage
	decodeBody(t, list.Body, &listedObjects)
	for _, object := range listedObjects {
		for _, key := range []string{"draftBundle", "draftVersion", "materializations", "publicationState", "draftConfig", "activeConfig"} {
			if _, exists := object[key]; exists {
				t.Fatalf("legacy top-level state field %q leaked from list response: %s", key, list.Body)
			}
		}
	}
	defaultResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	if defaultResponse.StatusCode != http.StatusOK {
		t.Fatalf("default status=%d body=%s", defaultResponse.StatusCode, defaultResponse.Body)
	}
	var defaultState explorer.ExplorerStateV1
	decodeBody(t, defaultResponse.Body, &defaultState)
	if defaultState.APIVersion != explorer.ExplorerStateV1APIVersion || defaultState.Kind != explorer.ExplorerStateV1Kind || defaultState.Title != "Default" || strings.Contains(defaultResponse.Body, `"draftConfig"`) || strings.Contains(defaultResponse.Body, `"activeConfig"`) {
		t.Fatalf("default state V1 mismatch: %#v body=%s", defaultState, defaultResponse.Body)
	}
	if defaultState.Runtime == nil || len(defaultState.Runtime.Outputs) != 1 {
		t.Fatalf("default runtime projection missing: %#v body=%s", defaultState.Runtime, defaultResponse.Body)
	}
	runtimeOutput := defaultState.Runtime.Outputs[0]
	if runtimeOutput.OutputID != "DocumentReference" || runtimeOutput.Selector != repositorySelector || len(runtimeOutput.Columns) != 1 || runtimeOutput.Columns[0].Name != "id" || runtimeOutput.Columns[0].EmissionID == "" {
		t.Fatalf("default runtime output mismatch: %#v", runtimeOutput)
	}
	if len(runtimeOutput.Table.Columns) != 1 || len(runtimeOutput.Filters) != 1 || len(runtimeOutput.Charts) != 1 || len(runtimeOutput.FixedFilters) != 1 || len(defaultState.Runtime.SharedFilters["identifier"]) != 1 {
		t.Fatalf("default runtime presentation mismatch: %#v", defaultState.Runtime)
	}
	var defaultObject map[string]json.RawMessage
	decodeBody(t, defaultResponse.Body, &defaultObject)
	for _, key := range []string{"draftBundle", "draftVersion", "materializations", "publicationState", "draftConfig", "activeConfig"} {
		if _, exists := defaultObject[key]; exists {
			t.Fatalf("legacy top-level state field %q leaked from detail response: %s", key, defaultResponse.Body)
		}
	}
	var defaultConfig explorer.ConfigV2
	decodeBody(t, string(repositoryBaselineConfig(repository)), &defaultConfig)
	if len(defaultConfig.Views) != 0 {
		t.Fatalf("default baseline contains views: %#v", defaultConfig.Views)
	}
	var activeDefaultConfig explorer.ConfigV2
	decodeBody(t, string(repository), &activeDefaultConfig)
	if len(activeDefaultConfig.Views) != 1 || activeDefaultConfig.Views[0].Output != "DocumentReference" {
		t.Fatalf("default active config lost presentation: %#v", activeDefaultConfig.Views)
	}
	var defaultDraft map[string]any
	if err := json.Unmarshal(repository, &defaultDraft); err != nil {
		t.Fatal(err)
	}
	defaultDraft["explorer"].(map[string]any)["title"] = "Edited Default"
	defaultDraftBytes, _ := json.Marshal(defaultDraft)
	defaultDraftRequest, _ := json.Marshal(map[string]any{"config": json.RawMessage(defaultDraftBytes), "expectedDraftVersion": defaultState.Draft.Version, "expectedDraftDigest": defaultState.Draft.Digest})
	defaultWrite := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/default/draft", string(defaultDraftRequest))
	if defaultWrite.StatusCode != http.StatusOK {
		t.Fatalf("default draft write status=%d body=%s", defaultWrite.StatusCode, defaultWrite.Body)
	}
	var updatedDefault explorerV2State
	decodeBody(t, defaultWrite.Body, &updatedDefault)
	if updatedDefault.DraftVersion != defaultState.Draft.Version+1 || updatedDefault.DraftDigest == defaultState.Draft.Digest {
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
	if len(releaseActivations) != 0 {
		t.Fatalf("configuration publish unexpectedly activated a dataset release: %#v", releaseActivations)
	}
	defaultAfterPublish := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default", "")
	var activeDefaultState explorer.ExplorerStateV1
	decodeBody(t, defaultAfterPublish.Body, &activeDefaultState)
	defaultOwner, err := service.Get(context.Background(), "project-a", "default")
	if defaultAfterPublish.StatusCode != http.StatusOK || activeDefaultState.Active.RevisionID == "" || string(defaultOwner.ActiveConfig) != string(defaultOwner.DraftConfig) {
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
	if stale.StatusCode != http.StatusOK || !strings.Contains(stale.Body, `"draftVersion":3`) {
		t.Fatalf("last-write-wins status=%d body=%s", stale.StatusCode, stale.Body)
	}

	compileRequest, _ := json.Marshal(map[string]any{"output": "DocumentReference", "config": json.RawMessage(updated.DraftConfig), "expectedDraftVersion": updated.DraftVersion})
	compiled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/authoring/compile", string(compileRequest))
	if compiled.StatusCode != http.StatusOK || !strings.Contains(compiled.Body, `"recipeDigest"`) || !strings.Contains(compiled.Body, `"diagnostics":[]`) {
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
	if len(releaseActivations) != 0 {
		t.Fatalf("custom configuration publish unexpectedly activated a dataset release: %#v", releaseActivations)
	}
	stateResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var active explorer.ExplorerStateV1
	decodeBody(t, stateResponse.Body, &active)
	activeOwner, err := service.Get(context.Background(), "project-a", "custom-explorer")
	if stateResponse.StatusCode != http.StatusOK || active.Active.RevisionID == "" || string(activeOwner.ActiveConfig) != string(activeOwner.DraftConfig) {
		t.Fatalf("active state=%#v", active)
	}
	if len(active.Generated.Materializations) != 1 || active.Generated.Materializations[0].Selector == nil || active.Generated.Materializations[0].Selector.Recipe != "repository" {
		t.Fatalf("configuration publish did not reuse the active dataset selector: %#v", active.Generated.Materializations)
	}
	priorActiveRevision := active.Active.RevisionID
	releaseFails = true
	draftExplorer["title"] = "Failed release draft"
	draftBytes, _ = json.Marshal(draft)
	draftRequest, _ = json.Marshal(map[string]any{"config": json.RawMessage(draftBytes), "expectedDraftVersion": active.Draft.Version, "expectedDraftDigest": active.Draft.Digest})
	failedReleaseDraft := requestJSON(t, app, http.MethodPut, "/api/v1/projects/project-a/explorers/custom-explorer/draft", string(draftRequest))
	if failedReleaseDraft.StatusCode != http.StatusOK {
		t.Fatalf("failed release draft save status=%d body=%s", failedReleaseDraft.StatusCode, failedReleaseDraft.Body)
	}
	var failedReleaseState explorerV2State
	decodeBody(t, failedReleaseDraft.Body, &failedReleaseState)
	failedReleasePublish := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom-explorer/publish", `{"expectedDraftVersion":`+fmt.Sprint(failedReleaseState.DraftVersion)+`,"expectedDraftDigest":"`+failedReleaseState.DraftDigest+`"}`)
	if failedReleasePublish.StatusCode != http.StatusOK {
		t.Fatalf("configuration publish should not depend on release activation status=%d body=%s", failedReleasePublish.StatusCode, failedReleasePublish.Body)
	}
	unchangedAfterReleaseFailure := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var releaseUnchanged explorer.ExplorerStateV1
	decodeBody(t, unchangedAfterReleaseFailure.Body, &releaseUnchanged)
	if releaseUnchanged.Active.RevisionID == priorActiveRevision {
		t.Fatalf("configuration publish did not activate independently of release state: %#v", releaseUnchanged)
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
	if failedPublish.StatusCode != http.StatusOK {
		t.Fatalf("configuration publish should not call materialization status=%d body=%s", failedPublish.StatusCode, failedPublish.Body)
	}
	unchangedResponse := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/custom-explorer", "")
	var unchanged explorer.ExplorerStateV1
	decodeBody(t, unchangedResponse.Body, &unchanged)
	if unchanged.Active.RevisionID == releaseUnchanged.Active.RevisionID {
		t.Fatalf("materializer availability affected configuration activation: %#v", unchanged)
	}
}

func TestExplorerV2StateRepairsPersistedStalePresentation(t *testing.T) {
	ctx := context.Background()
	service, err := explorer.NewService(explorer.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	bundle := testV2Bundle("repair")
	recipeRaw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	cfg := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "custom", Title: "Custom", Management: "interactive"},
		Recipe:     recipeRaw,
		Views: []explorer.ConfigView{{
			ID: "main", Title: "Main", Output: "DocumentReference",
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "id"}, {Column: "category_coding_code"}}},
		}},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.CreateInteractiveV2(ctx, "project-a", "custom", raw, "author")
	if err != nil {
		t.Fatal(err)
	}
	recipeDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	revision, err := service.InsertReadyRevisionV2WithMetadata(ctx, owner, raw, "sha256:config", explorer.Compilation{
		Bundle: bundle, RecipeDigest: recipeDigest,
		EmittedColumns: []explorer.EmittedColumn{{OutputID: "DocumentReference", PublicColumn: "id", SelectionID: "id", LogicalType: "string"}},
	}, "schema", "generation-a", "author", nil, explorer.DatasetMetadata{}, explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ActivateInteractive(ctx, "project-a", "custom", revision.ID); err != nil {
		t.Fatal(err)
	}
	state, err := getExplorerV2State(ctx, service, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	var repaired explorer.ConfigV2
	if err := json.Unmarshal(state.DraftConfig, &repaired); err != nil {
		t.Fatal(err)
	}
	if len(repaired.Views) != 1 || len(repaired.Views[0].Table.Columns) != 1 || repaired.Views[0].Table.Columns[0].Column != "id" {
		t.Fatalf("draft presentation was not repaired: %#v", repaired.Views)
	}
	persisted, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted.DraftConfig), "category_coding_code") {
		t.Fatalf("repaired draft was not persisted: %s", persisted.DraftConfig)
	}
}

func TestActiveExplorerDatasetRequiresCanonicalReleaseMetadata(t *testing.T) {
	bundle := testV2Bundle("explorer-custom")
	candidate := activeExplorerDataset{
		Generation:       "generation-a",
		Dataset:          explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{Name: "DocumentReference", State: "PUBLISHED", Queryable: true}}},
		Materializations: []explorer.Materialization{{Output: "DocumentReference", MaterializationID: "execution-a"}},
	}
	if activeExplorerDatasetSupports(candidate, bundle) {
		t.Fatal("incomplete release metadata was accepted without canonical selectors")
	}
}

func TestOutputsNeedingMaterializationReusesUnchangedOutputs(t *testing.T) {
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "Patient"}, {Name: "Specimen"}}}
	patientSelector := dataset.DataframeSelector{Recipe: "explorer", TranslationVersion: "v1", Output: "Patient"}
	specimenSelector := dataset.DataframeSelector{Recipe: "explorer", TranslationVersion: "v1", Output: "Specimen"}
	active := activeExplorerDataset{
		Generation: "generation-a",
		Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{
			{Name: "Patient", State: "PUBLISHED", Queryable: true, Fingerprint: "patient-v1", Selector: &patientSelector},
			{Name: "Specimen", State: "PUBLISHED", Queryable: true, Fingerprint: "specimen-v1", Selector: &specimenSelector},
		}},
		Materializations: []explorer.Materialization{
			{Output: "Patient", MaterializationID: "execution-patient", Fingerprint: "patient-v1", Selector: &patientSelector},
			{Output: "Specimen", MaterializationID: "execution-specimen", Fingerprint: "specimen-v1", Selector: &specimenSelector},
		},
	}
	changed := outputsNeedingMaterialization(bundle, active, "generation-a", map[string]string{"Patient": "patient-v1", "Specimen": "specimen-v2"})
	if len(changed) != 1 || changed[0].Name != "Specimen" {
		t.Fatalf("changed outputs = %#v, want only Specimen", changed)
	}
}

func TestPublishBuildsMissingDatasetOutput(t *testing.T) {
	t.Skip("legacy ExplorerConfigV2 publish route removed by Builder cutover")
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	repository := baselineExplorerConfigV2("project-a", "Default")
	repositorySelector := dataset.DataframeSelector{Recipe: "repository", TranslationVersion: "interactive", Output: "DocumentReference"}
	repositoryMaterialization := explorer.Materialization{OutputID: "DocumentReference", Output: "DocumentReference", MaterializationID: "execution-a", Selector: &repositorySelector, Columns: []publication.PhysicalColumn{{Name: "id", LogicalType: "String"}}}
	if _, err := service.SaveRepositoryConfig(context.Background(), explorer.RepositoryConfig{
		Project: "project-a", Config: repository, SourceGeneration: "generation-a", ExecutionID: "execution-a",
		Materializations: []explorer.Materialization{repositoryMaterialization},
		Dataset:          explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{Name: "DocumentReference", State: "PUBLISHED", Queryable: true, Selector: &repositorySelector, Columns: repositoryMaterialization.Columns}}},
		Publication:      explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-a", ExecutionID: "execution-a"},
	}); err != nil {
		t.Fatal(err)
	}
	missingBundle := testV2Bundle("explorer-custom")
	missingBundle.Outputs[0].Name = "SubstanceDefinition"
	missingBundle.Outputs[0].RootResourceType = "SubstanceDefinition"
	missingBundle.Outputs[0].RowGrain = "substance_definition"
	compile := func(_ context.Context, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, error) {
		digest, _ := missingBundle.Digest()
		return ExplorerV2CompileResult{Config: request.Config, Bundle: missingBundle, RecipeDigest: digest, SourceGeneration: "generation-a", ResolvedSchemaDigest: "schema-a"}, nil
	}
	materializeCalls := 0
	var materializedOutputNames [][]string
	materialize := func(_ context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		materializeCalls++
		materializedOutputNames = append(materializedOutputNames, append([]string(nil), bindings.OutputNames...))
		outputs := make([]graphresolver.RecipeExecutionOutput, 0, len(bundle.Outputs))
		for _, output := range bundle.Outputs {
			outputs = append(outputs, graphresolver.RecipeExecutionOutput{Name: output.Name, State: "PUBLISHED", Columns: []publication.PhysicalColumn{{Name: "id", LogicalType: "String"}}})
		}
		return graphresolver.RecipeExecution{ID: "execution-substance", SourceGeneration: "generation-a", ResolvedSchemaDigest: "schema-substance", State: "PUBLISHED", Outputs: outputs}, nil
	}
	var releaseActivations [][]dataset.DataframeSelector
	activateRelease := func(_ context.Context, project, generation string, selectors []dataset.DataframeSelector) error {
		if project != "project-a" || generation != "generation-a" {
			t.Fatalf("release identity = %s/%s", project, generation)
		}
		releaseActivations = append(releaseActivations, append([]dataset.DataframeSelector(nil), selectors...))
		return nil
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{Compile: compile, Materialize: materialize, ActivateRelease: activateRelease})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Needs Load","blank":true}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.StatusCode, created.Body)
	}
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/needs-load/publish", `{}`)
	if published.StatusCode != http.StatusOK || !strings.Contains(published.Body, `"publicationId"`) {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, published.Body)
	}
	if materializeCalls != 1 || len(releaseActivations) != 1 || len(releaseActivations[0]) != 1 || releaseActivations[0][0].Output != "SubstanceDefinition" {
		t.Fatalf("build lifecycle calls=%d releases=%#v", materializeCalls, releaseActivations)
	}
	if len(materializedOutputNames) != 1 || len(materializedOutputNames[0]) != 1 || materializedOutputNames[0][0] != "SubstanceDefinition" {
		t.Fatalf("materialized output selection = %#v", materializedOutputNames)
	}
	active, err := service.ActiveRevision(context.Background(), "project-a", "needs-load")
	if err != nil {
		t.Fatal(err)
	}
	if len(active.Materializations) != 1 || active.Materializations[0].Output != "SubstanceDefinition" || active.Materializations[0].Selector == nil {
		t.Fatalf("active materializations=%#v", active.Materializations)
	}
	updatedRepository, err := service.RepositoryConfig(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedRepository.Dataset.Outputs) != 2 || len(updatedRepository.Materializations) != 2 {
		t.Fatalf("merged repository dataset=%#v materializations=%#v", updatedRepository.Dataset, updatedRepository.Materializations)
	}
}

func TestPublishUsesCompiledGenerationInsteadOfStaleRepositoryGeneration(t *testing.T) {
	t.Skip("legacy ExplorerConfigV2 publish route removed by Builder cutover")
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	repository := baselineExplorerConfigV2("project-a", "Default")
	oldSelector := dataset.DataframeSelector{Recipe: "repository", TranslationVersion: "interactive", Output: "DocumentReference"}
	oldMaterialization := explorer.Materialization{OutputID: "DocumentReference", Output: "DocumentReference", MaterializationID: "execution-old", Selector: &oldSelector}
	if _, err := service.SaveRepositoryConfig(context.Background(), explorer.RepositoryConfig{
		Project: "project-a", Config: repository, SourceGeneration: "generation-old", ExecutionID: "execution-old",
		Materializations: []explorer.Materialization{oldMaterialization},
		Dataset:          explorer.DatasetMetadata{Generation: "generation-old", Outputs: []explorer.DatasetOutput{{Name: "DocumentReference", State: "PUBLISHED", Queryable: true, Selector: &oldSelector}}},
		Publication:      explorer.PublicationMetadata{State: "ACTIVE", Generation: "generation-old", ExecutionID: "execution-old"},
	}); err != nil {
		t.Fatal(err)
	}
	bundle := testV2Bundle("explorer-custom")
	compile := func(_ context.Context, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, error) {
		digest, _ := bundle.Digest()
		return ExplorerV2CompileResult{Config: request.Config, Bundle: bundle, RecipeDigest: digest, SourceGeneration: "generation-new", ResolvedSchemaDigest: "schema-new", OutputFingerprints: map[string]string{"DocumentReference": "fingerprint-new"}}, nil
	}
	validatedGeneration := ""
	validateGeneration := func(_ context.Context, project, generation string) error {
		if project != "project-a" {
			t.Fatalf("validated project = %q", project)
		}
		validatedGeneration = generation
		return nil
	}
	materializedGeneration := ""
	materialize := func(_ context.Context, _ recipe.Bundle, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		materializedGeneration = bindings.DatasetGeneration
		return graphresolver.RecipeExecution{ID: "execution-new", SourceGeneration: bindings.DatasetGeneration, ResolvedSchemaDigest: "schema-new", State: "PUBLISHED", Outputs: []graphresolver.RecipeExecutionOutput{{Name: "DocumentReference", State: "PUBLISHED"}}}, nil
	}
	activatedGeneration := ""
	activateRelease := func(_ context.Context, project, generation string, _ []dataset.DataframeSelector) error {
		if project != "project-a" {
			t.Fatalf("activated project = %q", project)
		}
		activatedGeneration = generation
		return nil
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{
		Compile: compile, Materialize: materialize, ValidateReleaseGeneration: validateGeneration, ActivateRelease: activateRelease,
	})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Current Generation","blank":true}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.StatusCode, created.Body)
	}
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/current-generation/publish", `{}`)
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, published.Body)
	}
	if validatedGeneration != "generation-new" || materializedGeneration != "generation-new" || activatedGeneration != "generation-new" {
		t.Fatalf("publish generations validate=%q materialize=%q activate=%q", validatedGeneration, materializedGeneration, activatedGeneration)
	}
	updated, err := service.RepositoryConfig(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if updated.SourceGeneration != "generation-new" || updated.Dataset.Generation != "generation-new" || updated.Publication.Generation != "generation-new" {
		t.Fatalf("updated repository generation = %#v", updated)
	}
}

func TestPublishPreflightsGenerationBeforeMaterialization(t *testing.T) {
	t.Skip("legacy ExplorerConfigV2 publish route removed by Builder cutover")
	store := explorer.NewMemoryStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testV2Bundle("explorer-custom")
	compile := func(_ context.Context, request ExplorerV2CompileRequest) (ExplorerV2CompileResult, error) {
		digest, _ := bundle.Digest()
		return ExplorerV2CompileResult{Config: request.Config, Bundle: bundle, RecipeDigest: digest, SourceGeneration: "generation-missing"}, nil
	}
	materializeCalls := 0
	materialize := func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		materializeCalls++
		return graphresolver.RecipeExecution{}, nil
	}
	app := fiber.New()
	RegisterExplorerLifecycleRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, ExplorerV2LifecycleConfig{
		Compile: compile, Materialize: materialize,
		ValidateReleaseGeneration: func(context.Context, string, string) error { return dataset.ErrSnapshotNotFound },
		ActivateRelease:           func(context.Context, string, string, []dataset.DataframeSelector) error { return nil },
	})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Missing Generation","blank":true}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.StatusCode, created.Body)
	}
	published := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/missing-generation/publish", `{}`)
	if published.StatusCode != http.StatusServiceUnavailable || !strings.Contains(published.Body, `"code":"DATASET_GENERATION_UNAVAILABLE"`) || !strings.Contains(published.Body, `"retryable":true`) {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, published.Body)
	}
	if materializeCalls != 0 {
		t.Fatalf("materializer called %d times after failed generation preflight", materializeCalls)
	}
}

func TestExplorerPublishErrorHidesBackendCause(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.Locals("request_id", "request-a")
		return explorerV2ErrorWithDetails(c, http.StatusServiceUnavailable, "DATASET_BUILD_FAILED", "the dataset could not be built", publicExplorerPublishDetails(fiber.Map{
			"generation": "generation-a",
			"output":     "Patient",
			"cause":      "secret backend query and credentials",
		}))
	})
	response := requestJSON(t, app, http.MethodGet, "/", "")
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, `"retryable":true`) || !strings.Contains(response.Body, `"requestId":"request-a"`) {
		t.Fatalf("publish error status=%d body=%s", response.StatusCode, response.Body)
	}
	if strings.Contains(response.Body, "secret backend") || !strings.Contains(response.Body, `"output":"Patient"`) {
		t.Fatalf("publish error leaked backend cause or lost public context: %s", response.Body)
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

func TestRuntimeProjectionOmitsUnpublishedEmissions(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "recipe", TranslationVersion: "v1", Output: "Patient"}
	config, err := json.Marshal(explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "default", Title: "Patients", Management: "repository"},
		Views: []explorer.ConfigView{{
			ID: "patients", Title: "Patients", Output: "Patient",
			Table:        explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "c_id", Visible: true}, {Column: "c_missing", Visible: true}}},
			Filters:      []explorer.ConfigFilter{{Column: "c_missing"}},
			FixedFilters: map[string][]string{"c_missing": {"x"}},
		}},
		SharedFilters: map[string][]explorer.SharedFilter{"missing": {{Output: "Patient", Column: "c_missing"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := &explorerV2State{
		Project:      "project-a",
		ExplorerID:   "default",
		ActiveConfig: config,
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "Patient", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "c_id", LogicalType: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{
			{EmissionID: "em_id", OutputID: "Patient", PublicColumn: "c_id", LogicalType: "string"},
			{EmissionID: "em_missing", OutputID: "Patient", PublicColumn: "c_missing", LogicalType: "string"},
		},
	}
	runtime := runtimeV1FromExplorerV2State(state)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime=%#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 1 || output.Columns[0].EmissionID != "em_id" {
		t.Fatalf("unpublished column leaked into runtime columns: %#v", output.Columns)
	}
	if len(output.Table.Columns) != 1 || output.Table.Columns[0].EmissionID != "em_id" || len(output.Filters) != 0 || len(output.FixedFilters) != 0 {
		t.Fatalf("unpublished presentation binding leaked into runtime: %#v", output)
	}
	if len(runtime.SharedFilters) != 0 {
		t.Fatalf("unpublished shared filter leaked into runtime: %#v", runtime.SharedFilters)
	}
}

func TestRuntimeProjectionDefaultsPublishedColumnsWhenPresentationIsEmpty(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "recipe", TranslationVersion: "authoring-v1", Output: "Patient"}
	config := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "default", Title: "Patients", Management: "repository"},
		Views: []explorer.ConfigView{{
			ID: "patients", Title: "Patients", Output: "Patient",
			// An empty table is what a stale legacy presentation can leave
			// behind after the published physical names change.
			Table: explorer.ConfigTable{},
		}},
	}
	state := &explorerV2State{
		ActiveConfig: mustJSON(config),
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "Patient", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_c_id", LogicalType: "String"}, {Name: "patient_c_name", LogicalType: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{
			{EmissionID: "em_id", OutputID: "Patient", PublicColumn: "c_id", LogicalType: "string"},
			{EmissionID: "em_name", OutputID: "Patient", PublicColumn: "c_name", LogicalType: "string"},
		},
	}
	runtime := runtimeV1FromExplorerV2State(state)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime=%#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Table.Columns) != 2 || !output.Table.Columns[0].Visible || !output.Table.Columns[1].Visible {
		t.Fatalf("default table bindings=%#v", output.Table)
	}
	if len(output.Columns) != 2 || !output.Columns[0].Visible || !output.Columns[1].Visible {
		t.Fatalf("default runtime columns=%#v", output.Columns)
	}
}

func TestRuntimeProjectionResolvesQualifiedPublishedColumns(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "explorer_project_default", TranslationVersion: "authoring-v1", Output: "Patient"}
	config := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "default", Title: "Patients", Management: "repository"},
		Views: []explorer.ConfigView{{
			ID: "patients", Title: "Patients", Output: "Patient",
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "c_id", Label: "Patient ID", Visible: true}}},
		}},
		SharedFilters: map[string][]explorer.SharedFilter{"patient": {{Output: "Patient", Column: "c_id"}}},
	}
	state := &explorerV2State{
		ActiveConfig: mustJSON(config),
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "Patient", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_c_id", LogicalType: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{
			EmissionID: "em_id", OutputID: "Patient", PublicColumn: "c_id", LogicalType: "string",
		}},
	}
	runtime := runtimeV1FromExplorerV2State(state)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime=%#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 1 || output.Columns[0].Name != "patient_c_id" {
		t.Fatalf("runtime columns=%#v", output.Columns)
	}
	if len(output.Table.Columns) != 1 || output.Table.Columns[0].EmissionID != "em_id" {
		t.Fatalf("runtime table=%#v", output.Table)
	}
	if got := runtime.SharedFilters["patient"]; len(got) != 1 || got[0].EmissionID != "em_id" {
		t.Fatalf("runtime shared filters=%#v", runtime.SharedFilters)
	}
}

func TestRuntimeProjectionUsesRecipeFieldRefWhenLabelIsGeneratedColumn(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "explorer_project_default", TranslationVersion: "authoring-v1", Output: "documentreference"}
	logicalName := generatedFieldName("s_title", "base")
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "explorer_project_default", TranslationVersion: "authoring-v1", Outputs: []recipe.Output{{
		Name: "documentreference", RootResourceType: "DocumentReference", RowGrain: "file",
		Fields: []recipe.Field{{Name: logicalName, FieldRef: "DocumentReference.content.attachment.title", Expr: recipe.Expression{Select: "root.content.attachment.title"}}},
	}}}
	config := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "default", Title: "Documents", Management: "repository"},
		Recipe:     mustJSON(bundle),
		Views: []explorer.ConfigView{{
			ID: "documents", Title: "Documents", Output: "documentreference",
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: logicalName, Label: logicalName, Visible: true}}},
		}},
	}
	state := &explorerV2State{
		ActiveConfig: mustJSON(config),
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "documentreference", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "documentreference_" + logicalName, LogicalType: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{EmissionID: "em_title", OutputID: "documentreference", PublicColumn: logicalName, LogicalType: "string"}},
	}
	runtime := runtimeV1FromExplorerV2State(state)
	if runtime == nil || len(runtime.Outputs) != 1 || len(runtime.Outputs[0].Columns) != 1 {
		t.Fatalf("runtime=%#v", runtime)
	}
	if got := runtime.Outputs[0].Columns[0].Label; got != "Content Attachment Title" {
		t.Fatalf("runtime label=%q", got)
	}
}

func TestRuntimeProjectionRepairsLegacyAuthoringColumnIdentity(t *testing.T) {
	selector := dataset.DataframeSelector{Recipe: "explorer_project_default", TranslationVersion: "authoring-v1", Output: "patient"}
	logicalName := generatedFieldName("s_patient_id", "base")
	config := explorer.ConfigV2{
		APIVersion: explorer.ConfigV2APIVersion,
		Kind:       "ExplorerConfig",
		Project:    "project-a",
		Explorer:   explorer.ConfigExplorer{ID: "default", Title: "Patients", Management: "repository"},
		Views: []explorer.ConfigView{{
			ID: "patients", Title: "Patients", Output: "patient",
			Table: explorer.ConfigTable{Columns: []explorer.ConfigColumn{{Column: "c_old_emission_hash", Label: "Patient ID", Visible: true}}},
		}},
	}
	state := &explorerV2State{
		ActiveConfig: mustJSON(config),
		Dataset: explorer.DatasetMetadata{Outputs: []explorer.DatasetOutput{{
			Name: "patient", Selector: &selector,
			Columns: []publication.PhysicalColumn{{Name: "patient_" + logicalName, LogicalType: "String"}},
		}}},
		EmittedColumns: []explorer.EmittedColumn{{
			EmissionID: "em_patient_id", OutputID: "patient", PublicColumn: "c_old_emission_hash",
			CandidateID: "s_patient_id", OccurrenceID: "base", LogicalType: "string",
		}},
	}
	runtime := runtimeV1FromExplorerV2State(state)
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime=%#v", runtime)
	}
	output := runtime.Outputs[0]
	if len(output.Columns) != 1 || output.Columns[0].Name != "patient_"+logicalName || output.Columns[0].EmissionID != "em_patient_id" {
		t.Fatalf("runtime columns=%#v", output.Columns)
	}
	if len(output.Table.Columns) != 1 || output.Table.Columns[0].EmissionID != "em_patient_id" || !output.Table.Columns[0].Visible {
		t.Fatalf("runtime table=%#v", output.Table)
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
