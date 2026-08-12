package queryapi

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

// recipeFieldDiscovery adapts the existing scoped field catalog to the
// backend-neutral recipe schema resolver. The GraphQL one-shot path and the
// stored-recipe server path therefore resolve catalog declarations with the
// same project, generation, and authorization semantics.
type recipeFieldDiscovery struct {
	read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
}

func (d recipeFieldDiscovery) Fields(ctx context.Context, scope schema.Scope, resourceType string) ([]schema.FieldCandidate, error) {
	var unrestricted *bool
	switch authscope.ReadScopeMode(scope.AuthScopeMode) {
	case authscope.ReadScopeUnrestricted:
		value := true
		unrestricted = &value
	case authscope.ReadScopeRestricted:
		value := false
		unrestricted = &value
	}
	read := d.read
	if read == nil {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	fields, err := read(ctx, catalog.PopulatedFieldOptions{
		Project: scope.Project, DatasetGeneration: scope.DatasetGeneration,
		AuthResourcePaths:             append([]string(nil), scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: unrestricted, ResourceType: resourceType,
	})
	if err != nil {
		return nil, queryBackend(err)
	}
	return catalogFieldsToRecipeCandidates(fields), nil
}

func catalogFieldsToRecipeCandidates(fields []catalog.PopulatedField) []schema.FieldCandidate {
	result := make([]schema.FieldCandidate, 0, len(fields))
	for _, field := range fields {
		result = append(result, schema.FieldCandidate{
			ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind,
			Population: field.DocCount, Examples: append([]string(nil), field.DistinctValues...),
			DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated, PivotCandidate: field.PivotCandidate,
			PivotFamily: field.PivotFamily, PivotColumns: append([]string(nil), field.PivotColumns...),
			PivotColumnSelect: field.PivotColumnSelect, PivotValueSelect: field.PivotValueSelect,
			ExtensionValues: func() []schema.ExtensionValueObservation {
				values := make([]schema.ExtensionValueObservation, len(field.ExtensionValues))
				for i, value := range field.ExtensionValues {
					values[i] = schema.ExtensionValueObservation{URL: value.URL, SourcePath: value.SourcePath, ValuePath: value.ValuePath, ValueType: value.ValueType, URLPath: append([]string(nil), value.URLPath...)}
				}
				return values
			}(),
			SemanticObservations: func() []schema.SemanticObservation {
				values := make([]schema.SemanticObservation, len(field.SemanticObservations))
				for i, value := range field.SemanticObservations {
					values[i] = schema.SemanticObservation{SourcePath: value.Source.Path, KeySelector: value.Key.Selector, KeySystem: value.Key.System, KeyCode: value.Key.Code, KeyDisplay: value.Key.Display, ValueSelector: value.Value.Selector, ValueType: value.Value.Type, Cardinality: value.Cardinality, Population: value.Population, Examples: append([]string(nil), value.Examples...), ExamplesTruncated: value.ExamplesTruncated}
				}
				return values
			}(),
		})
	}
	return result
}
