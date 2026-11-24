package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestPreviewInputBuildsRecipeBundle(t *testing.T) {
	gender := "gender"
	wantGender := "female"
	input := recipe.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields: []*recipe.FhirFieldSelectInput{{
			Name: "gender", FieldRef: &gender,
			Selector: &recipe.FhirFieldSelectorInput{ValuePath: "gender"},
		}},
		RootFilters: []*recipe.FhirFilterInput{{
			Select: "gender", Operator: recipe.FhirFilterOperatorEquals,
			Values: []*recipe.FhirFilterValueInput{{Kind: recipe.FhirFilterValueKindString, String: &wantGender}},
		}},
		RootAggregates: []*recipe.FhirAggregateInput{{
			Name: "condition_count", Operation: recipe.FhirAggregateOperationCount,
		}},
		Traverse: []*recipe.FhirTraversalStepInput{{
			EdgeLabel: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
			MatchMode: func() *recipe.FhirTraversalMatchMode {
				value := recipe.FhirTraversalMatchModeRequired
				return &value
			}(),
			Filters: []*recipe.FhirFilterInput{{
				Select: "id", Operator: recipe.FhirFilterOperatorExists,
			}},
			Aggregates: []*recipe.FhirAggregateInput{{Name: "count", Operation: recipe.FhirAggregateOperationCount}},
		}},
	}
	bundle, err := input.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Outputs) != 1 || len(bundle.Outputs[0].Fields) != 1 || len(bundle.Outputs[0].Traversals) != 1 {
		t.Fatalf("bundle shape = %#v", bundle)
	}
	if bundle.Outputs[0].RowGrain != "patient" || len(bundle.Outputs[0].Filters) != 1 || bundle.Outputs[0].Aggregates[0].Operation != recipe.AggregateCount {
		t.Fatalf("bundle metadata = %#v", bundle.Outputs[0])
	}
	if bundle.Outputs[0].Traversals[0].MatchMode != recipe.MatchRequired || len(bundle.Outputs[0].Traversals[0].Filters) != 1 {
		t.Fatalf("traversal semantics = %#v", bundle.Outputs[0].Traversals[0])
	}
	if _, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "generation"}); err != nil {
		t.Fatalf("mapped bundle does not build semantically: %v", err)
	}
}

func TestPreviewInputRejectsIncompleteAggregatePredicate(t *testing.T) {
	path := "gender"
	input := recipe.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootAggregates: []*recipe.FhirAggregateInput{{
			Name: "count", Operation: recipe.FhirAggregateOperationCount,
			PredicatePath: &path,
		}},
	}
	if _, err := input.Bundle(); err == nil {
		t.Fatal("expected incomplete aggregate predicate to fail")
	}
}

func TestPreviewInputMapsCatalogResolutionContracts(t *testing.T) {
	family := "observation_code_value"
	path := "code.coding[].code"
	input := recipe.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootCatalogProjections: []*recipe.FhirCatalogProjectionInput{{
			Name: "populated", IncludePaths: []string{"identifier.*"}, Kinds: []string{"scalar"},
			Naming: recipe.FhirColumnNamingPath, ValueMode: recipe.FhirValueModeFirst, MaxColumns: 32,
		}},
		RootPivots: []*recipe.FhirPivotInput{{
			Name: "observations", ColumnSelector: &recipe.FhirFieldSelectorInput{ValuePath: "code"}, ValueSelector: &recipe.FhirFieldSelectorInput{ValuePath: "value"},
			Discovery: &recipe.FhirPivotDiscoveryInput{Family: &family, Path: &path, MaxColumns: 16},
		}},
		Traverse: []*recipe.FhirTraversalStepInput{{
			EdgeLabel: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			CatalogProjections: []*recipe.FhirCatalogProjectionInput{{Name: "observation_fields", IncludePaths: []string{"value*"}, Naming: recipe.FhirColumnNamingPathSuffix, ValueMode: recipe.FhirValueModeFirst, MaxColumns: 8}},
		}},
	}
	bundle, err := input.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	output := bundle.Outputs[0]
	if len(output.CatalogProjections) != 1 || output.CatalogProjections[0].MaxColumns != 32 {
		t.Fatalf("root catalog projection = %#v", output.CatalogProjections)
	}
	if output.Pivots[0].Discovery == nil || output.Pivots[0].Discovery.Family != family || output.Pivots[0].Discovery.Path != path {
		t.Fatalf("pivot discovery = %#v", output.Pivots[0])
	}
	if len(output.Traversals) != 1 || len(output.Traversals[0].CatalogProjections) != 1 {
		t.Fatalf("traversal catalog projection = %#v", output.Traversals)
	}
}

func TestPreviewRecipeResolutionUsesScopedCatalogBeforeSemanticTyping(t *testing.T) {
	input := recipe.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootCatalogProjections: []*recipe.FhirCatalogProjectionInput{{Name: "populated", IncludePaths: []string{"gender"}, Kinds: []string{"scalar"}, Naming: recipe.FhirColumnNamingPath, ValueMode: recipe.FhirValueModeFirst, MaxColumns: 4}},
	}
	bundle, err := input.Bundle()
	if err != nil {
		t.Fatal(err)
	}
	var got fieldcatalog.PopulatedFieldOptions
	service := NewService(Config{DiscoverFields: func(_ context.Context, options fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		got = options
		return []fieldcatalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
	}})
	resolved, err := service.resolveRecipeBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "g", AuthResourcePaths: []string{"/controlled"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "P1" || got.DatasetGeneration != "g" || len(got.AuthResourcePaths) != 1 || len(resolved.Outputs[0].Fields) != 1 || resolved.Outputs[0].Fields[0].Name != "gender" {
		t.Fatalf("scoped preview resolution = options:%#v bundle:%#v", got, resolved)
	}
}
