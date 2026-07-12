package dataframebuilder

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/discovery"
	"github.com/calypr/loom/internal/recipe"
	"github.com/calypr/loom/internal/recipecompiler"
)

func TestServicePrepareRecipeUsesFreshScopedCatalogFacts(t *testing.T) {
	facts := recipeServiceFacts()
	genderID := recipeServiceCapabilityID(t, facts, "Patient", "gender")
	requestedPaths := []string{"scope-b"}
	var fieldOptions []catalog.PopulatedFieldOptions
	service := NewService(Config{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldOptions = append(fieldOptions, options)
			return append([]catalog.PopulatedField(nil), facts.Fields...), nil
		},
	})
	ctx := recipeServiceContext()

	plan, err := service.PrepareRecipe(ctx, RecipeRequest{
		Recipe:            recipePreview(genderID),
		AuthResourcePaths: requestedPaths,
	})
	if err != nil {
		t.Fatalf("PrepareRecipe() error = %v", err)
	}
	if len(fieldOptions) != 1 {
		t.Fatalf("discover fields calls = %d, want 1", len(fieldOptions))
	}
	if got := fieldOptions[0]; got.Project != "project-a" || got.ResourceType != "" || got.PivotOnly {
		t.Fatalf("catalog options = %+v, want normalized all-types non-pivot read", got)
	}
	if got, want := fieldOptions[0].AuthResourcePaths, []string{"scope-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog scope = %#v, want %#v", got, want)
	}
	if plan.Recipe.Project != "project-a" || plan.Builder.Project != "project-a" {
		t.Fatalf("normalized project = recipe %q builder %q", plan.Recipe.Project, plan.Builder.Project)
	}
	if plan.Builder.RootResourceType != "Patient" || plan.Builder.RowGrain != dataframe.RowGrainPatient {
		t.Fatalf("root-only recipe builder = %+v", plan.Builder)
	}
	if got, want := plan.Builder.AuthResourcePaths, []string{"scope-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prepared builder scope = %#v, want %#v", got, want)
	}
	if len(plan.Builder.Fields) != 1 || plan.Builder.Fields[0].FieldRef != genderID || plan.Builder.Fields[0].Select != "gender" {
		t.Fatalf("opaque root field did not resolve through fresh facts: %#v", plan.Builder.Fields)
	}

	// Neither the request nor a resolver-owned result can mutate the prepared
	// builder's authorization scope after preparation.
	requestedPaths[0] = "changed-after-prepare"
	if got := plan.Builder.AuthResourcePaths[0]; got != "scope-b" {
		t.Fatalf("prepared builder aliases request auth scope: %q", got)
	}
}

func TestServiceRunAndStreamRecipeUsePreparedScopeWithoutDatabase(t *testing.T) {
	facts := recipeServiceFacts()
	genderID := recipeServiceCapabilityID(t, facts, "Patient", "gender")
	var prepareOptions []catalog.PopulatedFieldOptions
	var dataframeOptions []catalog.PopulatedFieldOptions
	var executionScopes [][]string

	dataframes := dataframe.NewService(dataframe.ServiceConfig{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			dataframeOptions = append(dataframeOptions, options)
			if options.PivotOnly {
				return []catalog.PopulatedField{}, nil
			}
			return []catalog.PopulatedField{facts.Fields[0]}, nil // Patient.gender
		},
		ExecuteRows: func(_ context.Context, _ dataframe.ExecuteQueryOptions, _ string, bindVars map[string]any, visit func(map[string]any) error) error {
			paths, ok := bindVars["auth_resource_paths"].([]string)
			if !ok {
				t.Fatalf("auth_resource_paths bind type = %T, want []string", bindVars["auth_resource_paths"])
			}
			executionScopes = append(executionScopes, append([]string(nil), paths...))
			return visit(map[string]any{"_key": "patient-1", "gender": "female"})
		},
	})
	service := NewService(Config{
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			prepareOptions = append(prepareOptions, options)
			return append([]catalog.PopulatedField(nil), facts.Fields...), nil
		},
		Dataframes: dataframes,
	})
	ctx := recipeServiceContext()
	req := RecipeRunRequest{
		Recipe:            recipePreview(genderID),
		AuthResourcePaths: []string{"scope-b"},
		Limit:             7,
	}

	result, err := service.RunRecipe(ctx, req)
	if err != nil {
		t.Fatalf("RunRecipe() error = %v", err)
	}
	if result.RowCount != 1 || len(result.Rows) != 1 || result.Rows[0]["gender"] != "female" {
		t.Fatalf("RunRecipe result = %#v", result)
	}

	streamedRows := 0
	streamed, err := service.StreamRecipe(ctx, req, func(row map[string]any) error {
		streamedRows++
		if row["_key"] != "patient-1" || row["gender"] != "female" {
			t.Fatalf("streamed row = %#v", row)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("StreamRecipe() error = %v", err)
	}
	if streamed.RowCount != 1 || streamedRows != 1 {
		t.Fatalf("StreamRecipe counts = result %d visitor %d", streamed.RowCount, streamedRows)
	}

	if len(prepareOptions) != 2 {
		t.Fatalf("fresh recipe catalog reads = %d, want one per run", len(prepareOptions))
	}
	for _, options := range prepareOptions {
		if options.ResourceType != "" || options.PivotOnly || !reflect.DeepEqual(options.AuthResourcePaths, []string{"scope-b"}) {
			t.Fatalf("recipe catalog options = %+v", options)
		}
	}
	if len(dataframeOptions) != 6 { // validation fields/pivots plus pivot expansion for Run and Stream
		t.Fatalf("dataframe catalog reads = %d, want 6", len(dataframeOptions))
	}
	for _, options := range dataframeOptions {
		if options.ResourceType != "Patient" || !reflect.DeepEqual(options.AuthResourcePaths, []string{"scope-b"}) {
			t.Fatalf("dataframe catalog options = %+v", options)
		}
	}
	if got, want := executionScopes, [][]string{{"scope-b"}, {"scope-b"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("query execution scopes = %#v, want %#v", got, want)
	}
}

func TestServicePrepareRecipeRejectsStaleRelatedAndRawCapabilities(t *testing.T) {
	facts := recipeServiceFacts()
	genderID := recipeServiceCapabilityID(t, facts, "Patient", "gender")
	birthDateID := recipeServiceCapabilityID(t, facts, "Patient", "birthDate")
	observationID := recipeServiceCapabilityID(t, facts, "Observation", "valueInteger")

	tests := []struct {
		name   string
		fields []catalog.PopulatedField
		recipe recipe.Recipe
		want   error
	}{
		{
			name:   "stale opaque capability",
			fields: []catalog.PopulatedField{facts.Fields[1]}, // Patient.birthDate, no longer gender
			recipe: recipePreview(genderID),
			want:   recipecompiler.ErrColumnCapabilityUnavailable,
		},
		{
			name:   "related root capability",
			fields: facts.Fields,
			recipe: recipePreview(genderID, observationID),
			want:   recipecompiler.ErrRelatedResource,
		},
		{
			name:   "raw FHIR path is not a capability",
			fields: facts.Fields,
			recipe: recipePreview("gender"),
			want:   recipecompiler.ErrColumnCapabilityUnavailable,
		},
		{
			name:   "unknown opaque-looking capability",
			fields: facts.Fields,
			recipe: recipePreview("col_0000000000000000000000000000000000000000000000000000000000000000"),
			want:   recipecompiler.ErrColumnCapabilityUnavailable,
		},
		{
			name:   "fresh root capability remains accepted",
			fields: facts.Fields,
			recipe: recipePreview(birthDateID),
			want:   nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(Config{
				DiscoverFields: func(_ context.Context, _ catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
					return append([]catalog.PopulatedField(nil), test.fields...), nil
				},
			})
			plan, err := service.PrepareRecipe(recipeServiceContext(), RecipeRequest{Recipe: test.recipe})
			if test.want == nil {
				if err != nil {
					t.Fatalf("PrepareRecipe() error = %v", err)
				}
				if len(plan.Builder.Fields) != 1 || plan.Builder.Fields[0].Select != "birthDate" {
					t.Fatalf("fresh opaque root plan = %+v", plan.Builder)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("PrepareRecipe() error = %v, want %v", err, test.want)
			}
			if len(plan.Builder.Fields) != 0 || len(plan.Builder.Filters) != 0 {
				t.Fatalf("failed capability resolution returned builder = %+v", plan.Builder)
			}
		})
	}
}

func TestServicePrepareRecipeRejectsUnauthorizedScopeBeforeCatalogRead(t *testing.T) {
	facts := recipeServiceFacts()
	genderID := recipeServiceCapabilityID(t, facts, "Patient", "gender")
	called := false
	service := NewService(Config{
		DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			called = true
			return facts.Fields, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Projects:          []string{"project-a"},
		AuthResourcePaths: []string{"scope-a"},
	})

	_, err := service.PrepareRecipe(ctx, RecipeRequest{
		Recipe:            recipePreview(genderID),
		AuthResourcePaths: []string{"scope-b"},
	})
	if err == nil {
		t.Fatal("expected unauthorized scope error")
	}
	if called {
		t.Fatal("catalog read occurred before scope authorization")
	}
}

func recipeServiceFacts() discovery.CatalogFacts {
	return discovery.CatalogFacts{
		Project: "project-a",
		Fields: []catalog.PopulatedField{
			{Project: "project-a", ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 3, DistinctValues: []string{"female", "male"}},
			{Project: "project-a", ResourceType: "Patient", Path: "birthDate", Kind: "scalar", DocCount: 3, DistinctValues: []string{"1970-01-01", "2000-12-31"}},
			{Project: "project-a", ResourceType: "Observation", Path: "valueInteger", Kind: "scalar", DocCount: 2, DistinctValues: []string{"12", "13"}},
		},
	}
}

func recipeServiceCapabilityID(t *testing.T, facts discovery.CatalogFacts, resourceType string, canonicalPath string) string {
	t.Helper()
	snapshot, err := discovery.BuildSnapshot(facts)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	resolver, err := discovery.NewCapabilityResolver(facts)
	if err != nil {
		t.Fatalf("NewCapabilityResolver() error = %v", err)
	}
	for _, column := range snapshot.Columns {
		resolved, err := resolver.ResolveColumn(column.ID)
		if err != nil {
			t.Fatalf("ResolveColumn(%q) error = %v", column.ID, err)
		}
		if resolved.ResourceType == resourceType && resolved.CanonicalPath == canonicalPath {
			return string(column.ID)
		}
	}
	t.Fatalf("no opaque capability for %s.%s", resourceType, canonicalPath)
	return ""
}

func recipePreview(columnIDs ...string) recipe.Recipe {
	columns := make([]recipe.ColumnSelection, 0, len(columnIDs))
	for index, id := range columnIDs {
		name := "column"
		if index == 0 {
			name = "gender"
		}
		columns = append(columns, recipe.ColumnSelection{ID: id, OutputName: name})
	}
	return recipe.Recipe{
		Version:          recipe.VersionV1,
		Template:         recipe.TemplatePatientCohort,
		TemplateVersion:  1,
		Project:          " project-a ",
		GenerationPolicy: recipe.GenerationLatest,
		Grain:            recipe.GrainPatient,
		Columns:          columns,
		Destination:      recipe.Destination{Type: recipe.DestinationPreview},
	}
}

func recipeServiceContext() context.Context {
	return authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		Subject:           "recipe-user",
		Projects:          []string{"project-a"},
		AuthResourcePaths: []string{"scope-a", "scope-b"},
	})
}
