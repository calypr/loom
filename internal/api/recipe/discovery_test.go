package recipeapi

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func TestFieldDiscoveryMatchesStoredRecipeMetadataPolicy(t *testing.T) {
	read := func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{
			{ResourceType: "Observation", Path: "project_id", Kind: "scalar"},
			{ResourceType: "Observation", Path: "status", Kind: "scalar", DistinctValues: []string{"final"}},
			{ResourceType: "Observation", Path: "code", Kind: "codeable_concept", PivotCandidate: true, PivotFamily: "code", PivotColumns: []string{"system"}, PivotColumnSelect: "code.coding.system", PivotValueSelect: "code.coding.code", PivotItemSource: "code.coding", PivotItemResourceType: "Coding", PivotValueSelectors: []string{"code.coding.code"}},
		}, nil
	}
	got, err := NewFieldDiscovery(read).Fields(context.Background(), schema.Scope{Project: "project", DatasetGeneration: "generation"}, "Observation")
	if err != nil {
		t.Fatal(err)
	}
	want := []schema.FieldCandidate{
		{ResourceType: "Observation", Path: "status", Kind: "scalar", DistinctValues: []string{"final"}, ExtensionValues: []schema.ExtensionValueObservation{}},
		{ResourceType: "Observation", Path: "code", Kind: "codeable_concept", ExtensionValues: []schema.ExtensionValueObservation{}, PivotCandidate: true, PivotFamily: "code", PivotColumns: []string{"system"}, PivotColumnSelect: "code.coding.system", PivotValueSelect: "code.coding.code", PivotItemSource: "code.coding", PivotItemResourceType: "Coding", PivotValueSelectors: []string{"code.coding.code"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared recipe discovery = %#v, want %#v", got, want)
	}
}

func TestFieldDiscoveryUsesRetryableBackendErrorForBothFailureForms(t *testing.T) {
	for name, read := range map[string]func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error){
		"nil reader": nil,
		"backend reader": func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			return nil, errors.New("catalog unavailable")
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewFieldDiscovery(read).Fields(context.Background(), schema.Scope{}, "Observation")
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok || userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
				t.Fatalf("error = %#v, want retryable BACKEND_UNAVAILABLE", err)
			}
		})
	}
}
