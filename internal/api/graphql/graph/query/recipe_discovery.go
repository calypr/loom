package queryapi

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

// RecipeFieldDiscovery adapts the existing scoped field catalog to the
// backend-neutral recipe schema resolver. The GraphQL one-shot path and the
// stored-recipe server path therefore resolve catalog declarations with the
// same metadata and authorization semantics.
type RecipeFieldDiscovery struct {
	Read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
}

// NewRecipeFieldDiscovery returns the shared catalog adapter used by every
// recipe-resolution entrypoint.
func NewRecipeFieldDiscovery(read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)) schema.Discovery {
	return RecipeFieldDiscovery{Read: read}
}

func (d RecipeFieldDiscovery) Fields(ctx context.Context, scope schema.Scope, resourceType string) ([]schema.FieldCandidate, error) {
	var unrestricted *bool
	switch authscope.ReadScopeMode(scope.AuthScopeMode) {
	case authscope.ReadScopeUnrestricted:
		value := true
		unrestricted = &value
	case authscope.ReadScopeRestricted:
		value := false
		unrestricted = &value
	}
	read := d.Read
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
	result := make([]schema.FieldCandidate, 0, len(fields))
	for _, field := range fields {
		if isLoomMetadataField(field.Path) {
			continue
		}
		result = append(result, schema.FieldCandidate{
			ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind,
			DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated, PivotCandidate: field.PivotCandidate,
			PivotFamily: field.PivotFamily, PivotColumns: append([]string(nil), field.PivotColumns...),
			PivotColumnSelect: field.PivotColumnSelect, PivotValueSelect: field.PivotValueSelect,
			PivotItemSource: field.PivotItemSource, PivotItemResourceType: field.PivotItemResourceType,
			PivotValueSelectors: append([]string(nil), field.PivotValueSelectors...),
			ExtensionValues: func() []schema.ExtensionValueObservation {
				values := make([]schema.ExtensionValueObservation, len(field.ExtensionValues))
				for i, value := range field.ExtensionValues {
					values[i] = schema.ExtensionValueObservation{URL: value.URL, SourcePath: value.SourcePath, ValuePath: value.ValuePath, ValueType: value.ValueType, URLPath: append([]string(nil), value.URLPath...)}
				}
				return values
			}(),
		})
	}
	return result, nil
}

func isLoomMetadataField(path string) bool {
	switch path {
	case "project_id", "auth_resource_path", "dataset_generation":
		return true
	default:
		return false
	}
}
