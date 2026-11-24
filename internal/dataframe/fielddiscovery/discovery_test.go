package fielddiscovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldcatalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func TestFieldDiscoveryMatchesStoredRecipeMetadataPolicy(t *testing.T) {
	read := func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		return []fieldcatalog.PopulatedField{
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
		{ResourceType: "Observation", Path: "status", Kind: "scalar", DistinctValues: []string{"final"}, ExtensionValues: []fieldcatalog.ExtensionValueObservation{}},
		{ResourceType: "Observation", Path: "code", Kind: "codeable_concept", ExtensionValues: []fieldcatalog.ExtensionValueObservation{}, PivotCandidate: true, PivotFamily: "code", PivotColumns: []string{"system"}, PivotColumnSelect: "code.coding.system", PivotValueSelect: "code.coding.code", PivotItemSource: "code.coding", PivotItemResourceType: "Coding", PivotValueSelectors: []string{"code.coding.code"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared recipe discovery = %#v, want %#v", got, want)
	}
}

func TestFieldDiscoveryUsesRetryableBackendErrorForBothFailureForms(t *testing.T) {
	for name, read := range map[string]func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error){
		"nil reader": nil,
		"backend reader": func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
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

func TestRecipeFieldDiscoverySkipsLoomMetadata(t *testing.T) {
	discovery := NewFieldDiscovery(func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		return []fieldcatalog.PopulatedField{
			{ResourceType: "DocumentReference", Path: "project_id", Kind: "scalar"},
			{ResourceType: "DocumentReference", Path: "status", Kind: "scalar"},
		}, nil
	})

	fields, err := discovery.Fields(context.Background(), schema.Scope{}, "DocumentReference")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Path != "status" {
		t.Fatalf("discovered fields = %#v", fields)
	}
}

func TestRecipeFieldDiscoveryCarriesExtensionObservations(t *testing.T) {
	discovery := NewFieldDiscovery(func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error) {
		return []fieldcatalog.PopulatedField{{ResourceType: "DocumentReference", Path: "content[].attachment.extension[].url", ExtensionValues: []fieldcatalog.ExtensionValueObservation{{URL: "http://example.org/source_path", SourcePath: "content[].attachment.extension[]", ValuePath: "valueUrl", ValueType: "string"}}}}, nil
	})
	fields, err := discovery.Fields(context.Background(), schema.Scope{}, "DocumentReference")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || len(fields[0].ExtensionValues) != 1 || fields[0].ExtensionValues[0].ValuePath != "valueUrl" {
		t.Fatalf("extension observations = %#v", fields)
	}
}
