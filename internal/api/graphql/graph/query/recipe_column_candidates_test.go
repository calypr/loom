package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipeColumnCandidatesAreAuthorizedGenerationScopedAndPaginated(t *testing.T) {
	var got catalog.PopulatedFieldOptions
	service := NewService(Config{ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")}, DiscoverFields: func(_ context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		got = opts
		return []catalog.PopulatedField{{Project: "P1", DatasetGeneration: opts.DatasetGeneration, ResourceType: "Patient", Path: "identifier[].system", Kind: "scalar", DocCount: 4, DistinctValues: []string{"urn:a", "urn:b"}}}, nil
	}})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "reader", Projects: []string{"P1"}, AuthResourcePaths: []string{"scope-a"}})
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "columns", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "ids", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 8}}}}}
	first, err := service.RecipeColumnCandidates(ctx, RecipeColumnCandidatesRequest{Project: "P1", Recipe: bundle, Output: "patients", First: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.DatasetGeneration != "generation-1" || len(got.AuthResourcePaths) != 1 || got.AuthResourcePaths[0] != "scope-a" {
		t.Fatalf("catalog scope=%+v", got)
	}
	if len(first.Candidates) != 1 || !first.HasNext || first.EndCursor == "" || first.TotalCount != 2 || !first.Complete {
		t.Fatalf("first page=%+v", first)
	}
	second, err := service.RecipeColumnCandidates(ctx, RecipeColumnCandidatesRequest{Project: "P1", Recipe: bundle, Output: "patients", First: 1, After: first.EndCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Candidates) != 1 || second.HasNext || second.Candidates[0].ID == first.Candidates[0].ID {
		t.Fatalf("second page=%+v", second)
	}
}

func TestRecipeColumnCandidatesReportsBlockingMaxColumnsCompleteness(t *testing.T) {
	service := NewService(Config{ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "g")}, DiscoverFields: func(_ context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{{ResourceType: "Patient", Path: "identifier[].system", DocCount: 2, DistinctValues: []string{"a", "b"}}}, nil
	}})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "reader", Projects: []string{"P1"}})
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "columns", TranslationVersion: "1", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "ids", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.system"}, Value: &recipe.Expression{Select: "item.value"}, MaxColumns: 1}}}}}
	resp, err := service.RecipeColumnCandidates(ctx, RecipeColumnCandidatesRequest{Project: "P1", Recipe: bundle, Output: "patients"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Complete || len(resp.Diagnostics) != 1 {
		t.Fatalf("completeness=%+v", resp)
	}
}
