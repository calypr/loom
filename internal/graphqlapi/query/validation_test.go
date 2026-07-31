package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/generated/graphqlapi/model"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
)

func TestValidateUsesProductionCompilerWithoutExecutingRows(t *testing.T) {
	executed := false
	discoverFields := func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{}, nil
	}
	discoverReferences := func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		return []catalog.PopulatedReference{}, nil
	}
	inner := runtime.NewService(runtime.ServiceConfig{
		ExecuteRows: func(context.Context, runtime.ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error {
			executed = true
			return nil
		},
	})
	service := NewService(Config{Dataframes: inner, DiscoverFields: discoverFields, DiscoverReferences: discoverReferences})
	result, err := service.Validate(context.Background(), model.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		Limit:            intPtr(25),
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !result.Valid || result.Project != "P1" || result.RootResourceType != "Patient" {
		t.Fatalf("unexpected validation result: %#v", result)
	}
	if result.Limit != 25 || result.RequestFingerprint == "" || len(result.Columns) == 0 {
		t.Fatalf("validation result omitted compiler metadata: %#v", result)
	}
	if executed {
		t.Fatal("Validate executed rows")
	}
	if result.NormalizedInput.Project != "P1" || result.NormalizedInput.RootResourceType != "Patient" {
		t.Fatalf("normalized input was not returned: %#v", result.NormalizedInput)
	}
}

func TestValidateResolvesFieldRefsBeforeCompilation(t *testing.T) {
	discoverFields := func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		return []catalog.PopulatedField{{ResourceType: options.ResourceType, Path: "gender"}}, nil
	}
	discoverReferences := func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		return []catalog.PopulatedReference{}, nil
	}
	inner := runtime.NewService(runtime.ServiceConfig{})
	service := NewService(Config{Dataframes: inner, DiscoverFields: discoverFields, DiscoverReferences: discoverReferences})
	fieldRef := "Patient.gender"
	result, err := service.Validate(context.Background(), model.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields:       []*model.FhirFieldSelectInput{{Name: "gender", FieldRef: &fieldRef}},
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(result.NormalizedInput.RootFields) != 1 || result.NormalizedInput.RootFields[0].Selector == nil {
		t.Fatalf("fieldRef was not resolved: %#v", result.NormalizedInput.RootFields)
	}
	if len(result.Columns) < 2 {
		t.Fatalf("compiled columns = %#v", result.Columns)
	}
}

func intPtr(value int) *int { return &value }
