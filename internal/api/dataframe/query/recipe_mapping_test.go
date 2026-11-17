package queryapi

import (
	"context"
	"testing"

	dataframemodel "github.com/calypr/loom/internal/api/dataframe"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestRecipeBundleFromInputMapsRichGraphQLShape(t *testing.T) {
	gender := "gender"
	wantGender := "female"
	input := dataframemodel.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields: []*dataframemodel.FhirFieldSelectInput{{
			Name: "gender", FieldRef: &gender,
			Selector: &dataframemodel.FhirFieldSelectorInput{ValuePath: "gender"},
		}},
		RootFilters: []*dataframemodel.FhirFilterInput{{
			Select: "gender", Operator: dataframemodel.FhirFilterOperatorEquals,
			Values: []*dataframemodel.FhirFilterValueInput{{Kind: dataframemodel.FhirFilterValueKindString, String: &wantGender}},
		}},
		RootAggregates: []*dataframemodel.FhirAggregateInput{{
			Name: "condition_count", Operation: dataframemodel.FhirAggregateOperationCount,
		}},
		Traverse: []*dataframemodel.FhirTraversalStepInput{{
			EdgeLabel: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
			MatchMode: func() *dataframemodel.FhirTraversalMatchMode {
				value := dataframemodel.FhirTraversalMatchModeRequired
				return &value
			}(),
			Filters: []*dataframemodel.FhirFilterInput{{
				Select: "id", Operator: dataframemodel.FhirFilterOperatorExists,
			}},
			Aggregates: []*dataframemodel.FhirAggregateInput{{Name: "count", Operation: dataframemodel.FhirAggregateOperationCount}},
		}},
	}
	bundle, err := RecipeBundleFromInput(input)
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
	if _, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "generation"}); err != nil {
		t.Fatalf("mapped bundle does not build semantically: %v", err)
	}
}

func TestRecipeBundleFromInputRejectsIncompleteAggregatePredicate(t *testing.T) {
	path := "gender"
	input := dataframemodel.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootAggregates: []*dataframemodel.FhirAggregateInput{{
			Name: "count", Operation: dataframemodel.FhirAggregateOperationCount,
			PredicatePath: &path,
		}},
	}
	if _, err := RecipeBundleFromInput(input); err == nil {
		t.Fatal("expected incomplete aggregate predicate to fail")
	}
}

func TestRecipeBundleFromInputMapsCatalogResolutionContracts(t *testing.T) {
	family := "observation_code_value"
	path := "code.coding[].code"
	input := dataframemodel.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootCatalogProjections: []*dataframemodel.FhirCatalogProjectionInput{{
			Name: "populated", IncludePaths: []string{"identifier.*"}, Kinds: []string{"scalar"},
			Naming: dataframemodel.FhirColumnNamingPath, ValueMode: dataframemodel.FhirValueModeFirst, MaxColumns: 32,
		}},
		RootPivots: []*dataframemodel.FhirPivotInput{{
			Name: "observations", ColumnSelector: &dataframemodel.FhirFieldSelectorInput{ValuePath: "code"}, ValueSelector: &dataframemodel.FhirFieldSelectorInput{ValuePath: "value"},
			Discovery: &dataframemodel.FhirPivotDiscoveryInput{Family: &family, Path: &path, MaxColumns: 16},
		}},
		Traverse: []*dataframemodel.FhirTraversalStepInput{{
			EdgeLabel: "subject_Patient", ToResourceType: "Observation", Alias: "observation",
			CatalogProjections: []*dataframemodel.FhirCatalogProjectionInput{{Name: "observation_fields", IncludePaths: []string{"value*"}, Naming: dataframemodel.FhirColumnNamingPathSuffix, ValueMode: dataframemodel.FhirValueModeFirst, MaxColumns: 8}},
		}},
	}
	bundle, err := RecipeBundleFromInput(input)
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

func TestGraphQLRecipeResolutionUsesScopedCatalogBeforeSemanticTyping(t *testing.T) {
	input := dataframemodel.FhirDataframeInput{
		Project: "P1", RootResourceType: "Patient",
		RootCatalogProjections: []*dataframemodel.FhirCatalogProjectionInput{{Name: "populated", IncludePaths: []string{"gender"}, Kinds: []string{"scalar"}, Naming: dataframemodel.FhirColumnNamingPath, ValueMode: dataframemodel.FhirValueModeFirst, MaxColumns: 4}},
	}
	bundle, err := RecipeBundleFromInput(input)
	if err != nil {
		t.Fatal(err)
	}
	var got catalog.PopulatedFieldOptions
	service := NewService(Config{DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		got = options
		return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar"}}, nil
	}})
	resolved, err := service.resolveRecipeBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "g", AuthResourcePaths: []string{"/controlled"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "P1" || got.DatasetGeneration != "g" || len(got.AuthResourcePaths) != 1 || len(resolved.Outputs[0].Fields) != 1 || resolved.Outputs[0].Fields[0].Name != "gender" {
		t.Fatalf("scoped GraphQL resolution = options:%#v bundle:%#v", got, resolved)
	}
}
