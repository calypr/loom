package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
)

func TestGetDefaultExplorerUsesRecipeOnlyViewerWhenOwnerIsMissing(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	response, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
	if err != nil {
		t.Fatalf("get missing default Explorer: %v", err)
	}
	if fixture.executions.calls != 1 || fixture.recipes.calls != 1 {
		t.Fatalf("exact execution/recipe reads = %d/%d, want one each", fixture.executions.calls, fixture.recipes.calls)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var state explorer.ExplorerStateV1
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.ExplorerID != "default" || state.Management != explorer.ManagementRepository || state.Runtime == nil || state.Runtime.Status != "ACTIVE" || len(state.Runtime.Outputs) != 1 {
		t.Fatalf("default Viewer state = %#v", state)
	}
	output := state.Runtime.Outputs[0]
	if output.Selector != fixture.selector || len(output.Columns) != 3 {
		t.Fatalf("default Viewer output = %#v", output)
	}
	wantColumns := []string{"patient_id", "dynamic_tag", "patient_codes"}
	for i, want := range wantColumns {
		if output.Columns[i].Column != want {
			t.Fatalf("default Viewer column %d = %q, want %q", i, output.Columns[i].Column, want)
		}
	}
	if output.Columns[0].Label != "Patient ID" || !output.Columns[0].Filterable || !output.Columns[1].Filterable {
		t.Fatalf("default scalar columns = %#v", output.Columns)
	}
	if output.Columns[2].Filterable || output.Columns[2].Sortable {
		t.Fatalf("repeated column has unsafe capabilities: %#v", output.Columns[2])
	}
	for _, hidden := range []string{"__loom_row_id", "auth_resource_path", "project_id", "_key", "patient_profile"} {
		for _, column := range output.Columns {
			if column.Column == hidden {
				t.Fatalf("default Viewer exposed internal or object column %q", hidden)
			}
		}
	}
	for _, columns := range [][]publication.PhysicalColumn{
		state.Generated.Dataset.Outputs[0].Columns,
		state.Generated.Materializations[0].Columns,
		output.Materialization.Columns,
	} {
		if len(columns) != len(output.Columns) {
			t.Fatalf("default Viewer metadata columns = %#v, want public output columns only", columns)
		}
		for _, column := range columns {
			if defaultViewerInternalColumn(column) || column.Name == "patient_profile" {
				t.Fatalf("default Viewer metadata exposed internal/object column %q", column.Name)
			}
		}
	}
}

func TestGetExplorerOnlyUsesDefaultViewerForMissingDefault(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	if _, err := fixture.store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "default", Title: "Authored Default", ManagementMode: explorer.ManagementRepository,
	}); err != nil {
		t.Fatal(err)
	}
	response, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
	if err != nil {
		t.Fatalf("get authored default: %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var authored explorer.ExplorerStateV1
	if err := json.Unmarshal(data, &authored); err != nil {
		t.Fatal(err)
	}
	if authored.Title != "Authored Default" || authored.Runtime != nil {
		t.Fatalf("authored state was replaced by default projection: %#v", authored)
	}
	if fixture.releases.calls != 0 {
		t.Fatalf("active releases read for authored default = %d, want 0", fixture.releases.calls)
	}

	_, err = fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "named")
	status, _ := explorerLegacyResponse(err)
	if err == nil || status != 404 {
		t.Fatalf("named Explorer error/status = %v/%d, want not found", err, status)
	}
	if fixture.releases.calls != 0 {
		t.Fatalf("active releases read for named miss = %d, want 0", fixture.releases.calls)
	}
}

func TestDefaultViewerRequiresFullExecutionScopeCoverage(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	fixture.execution.AuthResourcePaths = []string{"allowed", "denied"}
	fixture.executions.execution.AuthResourcePaths = []string{"allowed", "denied"}
	fixture.handlers.defaultViewer = newRepositoryDefaultViewer(
		fixture.releases, fixture.executions, fixture.recipes,
		defaultViewerTestScope{scope: authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed"}}},
		fixture.recipe.Name,
	)
	_, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
	status, _ := explorerLegacyResponse(err)
	if err == nil || status != 404 {
		t.Fatalf("scope-denied default Viewer error/status = %v/%d, want concealed not found", err, status)
	}
	if fixture.recipes.calls != 0 {
		t.Fatalf("recipe metadata read before complete scope coverage = %d, want 0", fixture.recipes.calls)
	}
}

func TestDefaultViewerTreatsTypedNilScopeResolverAsUnconfigured(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	var scope *authscope.ScopeResolver
	fixture.handlers.defaultViewer = newRepositoryDefaultViewer(fixture.releases, fixture.executions, fixture.recipes, scope, fixture.recipe.Name)
	response, err := fixture.handlers.getExplorer(context.Background(), "project-a", "default")
	if err != nil {
		t.Fatalf("get default Viewer with authentication disabled: %v", err)
	}
	if len(response["runtime"].(map[string]any)["outputs"].([]any)) != 1 {
		t.Fatalf("no-auth default Viewer response omitted its runtime: %#v", response)
	}
}

func TestDefaultViewerRejectsStaleBindingsAndFailedExecutions(t *testing.T) {
	t.Run("stale generation binding", func(t *testing.T) {
		fixture := newDefaultViewerTestFixture(t)
		fixture.releases.active.Release.Publications[0].Stale = true
		_, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
		status, _ := explorerLegacyResponse(err)
		if err == nil || status != 404 || fixture.executions.calls != 0 {
			t.Fatalf("stale binding result = error %v status %d execution reads %d", err, status, fixture.executions.calls)
		}
	})

	t.Run("failed execution", func(t *testing.T) {
		fixture := newDefaultViewerTestFixture(t)
		fixture.execution.State = publication.BundleFailed
		fixture.executions.execution.State = publication.BundleFailed
		_, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
		status, _ := explorerLegacyResponse(err)
		if err == nil || status != 404 || fixture.recipes.calls != 0 {
			t.Fatalf("failed execution result = error %v status %d recipe reads %d", err, status, fixture.recipes.calls)
		}
	})
}

func TestDefaultViewerRequiresPinnedRecipeDigest(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	fixture.recipes.entry.Digest = "wrong-digest"
	_, err := fixture.handlers.getExplorer(defaultViewerTestContext(), "project-a", "default")
	status, _ := explorerLegacyResponse(err)
	if err == nil || status != 500 {
		t.Fatalf("recipe digest mismatch error/status = %v/%d, want integrity failure", err, status)
	}
}

func TestDefaultViewerReadsLegacyProjectReleaseKey(t *testing.T) {
	fixture := newDefaultViewerTestFixture(t)
	canonicalProject := "HTAN_INT/BForePC"
	legacyProject := projectid.Legacy(canonicalProject)
	fixture.releases.wantProject = legacyProject
	fixture.releases.active.Release.Project = legacyProject
	fixture.execution.Project = legacyProject
	fixture.executions.execution.Project = legacyProject
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{canonicalProject}})

	response, err := fixture.handlers.getExplorer(ctx, canonicalProject, "default")
	if err != nil {
		t.Fatalf("get default Viewer for canonical project %q: %v", canonicalProject, err)
	}
	if response["project"] != canonicalProject || fixture.releases.requestedProject != legacyProject {
		t.Fatalf("viewer project=%v, release lookup project=%q; want canonical=%q and storage key=%q", response["project"], fixture.releases.requestedProject, canonicalProject, legacyProject)
	}
}

type defaultViewerTestFixture struct {
	handlers   *explorerHTTPHandlers
	store      *testExplorerStore
	releases   *defaultViewerTestReleases
	executions *defaultViewerTestExecutions
	recipes    *defaultViewerTestRecipes
	execution  publication.BundleExecution
	recipe     recipe.Bundle
	selector   dataset.DataframeSelector
}

func newDefaultViewerTestFixture(t *testing.T) defaultViewerTestFixture {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "patients-query",
		TranslationVersion:  "v1",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
			Fields: []recipe.Field{{Name: "patient_id", Label: "Patient ID", ColumnID: "column-patient-id", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	selector := dataset.DataframeSelector{Recipe: bundle.Name, TranslationVersion: bundle.TranslationVersion, Output: "patients"}
	now := time.Now().UTC()
	output := publication.BundleOutputRecord{
		Name: "patients", PhysicalTable: "published_patients", Selector: selector, State: publication.BundlePublished, VerifiedAt: &now,
		Columns: []publication.PhysicalColumn{
			{ID: "column-patient-id", Name: "patient_id", SemanticPath: "Patient.id", ClickHouse: "String", LogicalType: "string", Provenance: publication.ColumnExplicit},
			{Name: "dynamic_tag", SemanticPath: "recipe:patients/dynamic_tag", ClickHouse: "String", LogicalType: "string", Provenance: publication.ColumnDiscovered},
			{Name: "patient_codes", ClickHouse: "Array(String)", LogicalType: "string", Repeated: true, Provenance: publication.ColumnDiscovered},
			{Name: "patient_profile", ClickHouse: "String", LogicalType: "object", Provenance: publication.ColumnExplicit},
			{Name: "__loom_row_id", ClickHouse: "String", LoomOwned: true},
			{Name: "auth_resource_path", ClickHouse: "Nullable(String)", LoomOwned: true},
			{Name: "project_id", ClickHouse: "String", LoomOwned: true},
			{Name: "_key", ClickHouse: "String"},
		},
	}
	execution := publication.BundleExecution{
		ID: "execution-a", BundleIdentity: publication.BundleIdentity{
			Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Project: "project-a",
			DatasetGeneration: "generation-a", RecipeDigest: digest, SchemaDigest: "schema-a",
		},
		State: publication.BundlePublished, Outputs: []publication.BundleOutputRecord{output},
		CreatedAt: now, UpdatedAt: now, PublishedAt: &now,
	}
	releases := &defaultViewerTestReleases{active: dataset.ActiveRelease{Release: dataset.ProjectRelease{
		ID: "release-a", Project: "project-a", Generation: "generation-a",
		Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: execution.ID, Generation: execution.DatasetGeneration}},
	}}}
	executions := &defaultViewerTestExecutions{execution: execution}
	recipes := &defaultViewerTestRecipes{entry: recipeexec.Entry{Bundle: bundle, Digest: digest}}
	store := newTestExplorerStore()
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	application, err := lifecycle.New(domain, lifecycle.Config{})
	if err != nil {
		t.Fatal(err)
	}
	handlers := &explorerHTTPHandlers{
		application:   application,
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return nil },
		defaultViewer: newRepositoryDefaultViewer(releases, executions, recipes, nil, bundle.Name),
	}
	return defaultViewerTestFixture{handlers: handlers, store: store, releases: releases, executions: executions, recipes: recipes, execution: execution, recipe: bundle, selector: selector}
}

func defaultViewerTestContext() context.Context {
	return authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{"project-a"}})
}

type defaultViewerTestReleases struct {
	active           dataset.ActiveRelease
	err              error
	calls            int
	wantProject      string
	requestedProject string
}

func (r *defaultViewerTestReleases) ReadActiveRelease(_ context.Context, project string) (dataset.ActiveRelease, error) {
	r.calls++
	r.requestedProject = project
	if r.wantProject != "" && project != r.wantProject {
		return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
	}
	return r.active, r.err
}

type defaultViewerTestExecutions struct {
	execution publication.BundleExecution
	err       error
	calls     int
	ids       []string
}

func (r *defaultViewerTestExecutions) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	r.calls++
	r.ids = append(r.ids, id)
	return r.execution, r.err
}

type defaultViewerTestRecipes struct {
	entry   recipeexec.Entry
	err     error
	calls   int
	name    string
	version string
}

func (r *defaultViewerTestRecipes) LoadRecipeVersion(_ context.Context, name, version string) (recipeexec.Entry, error) {
	r.calls++
	r.name, r.version = name, version
	return r.entry, r.err
}

type defaultViewerTestScope struct {
	scope authscope.ReadScope
	err   error
}

func (s defaultViewerTestScope) ResolveReadScopeForGeneration(context.Context, *authscope.Principal, string, string, []string) (authscope.ReadScope, error) {
	return s.scope, s.err
}
