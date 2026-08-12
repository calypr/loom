package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type recipeCatalogDiscovery struct {
	read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
}

func (d recipeCatalogDiscovery) Fields(ctx context.Context, scope schema.Scope, resourceType string) ([]schema.FieldCandidate, error) {
	var unrestricted *bool
	switch authscope.ReadScopeMode(scope.AuthScopeMode) {
	case authscope.ReadScopeUnrestricted:
		value := true
		unrestricted = &value
	case authscope.ReadScopeRestricted:
		value := false
		unrestricted = &value
	}
	reader := d.read
	if reader == nil {
		return nil, fmt.Errorf("catalog field discovery is unavailable")
	}
	fields, err := reader(ctx, catalog.PopulatedFieldOptions{
		Project:                       scope.Project,
		DatasetGeneration:             scope.DatasetGeneration,
		AuthResourcePaths:             append([]string(nil), scope.AuthResourcePaths...),
		AuthResourcePathsUnrestricted: unrestricted,
		ResourceType:                  resourceType,
	})
	if err != nil {
		return nil, err
	}
	result := make([]schema.FieldCandidate, 0, len(fields))
	for _, field := range fields {
		if isLoomMetadataField(field.Path) {
			continue
		}
		result = append(result, schema.FieldCandidate{
			ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind,
			DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated,
			PivotCandidate: field.PivotCandidate, PivotFamily: field.PivotFamily,
			PivotColumns:      append([]string(nil), field.PivotColumns...),
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

func recipeSchemaResolver(read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error), cache *catalog.Cache) func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
	if cache != nil {
		read = cache.DiscoverFields(read)
	}
	discovery := recipeCatalogDiscovery{read: read}
	return func(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
		resolved, err := schema.Resolve(ctx, bundle, schema.Scope{
			Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
			AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
			AuthScopeMode:     string(bindings.AuthScopeMode),
		}, discovery)
		if err != nil {
			return recipe.Bundle{}, err
		}
		if !bundleHasConceptSelections(resolved.Bundle) {
			return resolved.Bundle, nil
		}
		fields := make([]catalog.PopulatedField, 0)
		for _, resourceType := range conceptResourceTypes(resolved.Bundle) {
			part, discoverErr := read(ctx, catalog.PopulatedFieldOptions{
				Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, ResourceType: resourceType,
				AuthResourcePaths:             append([]string(nil), bindings.AuthResourcePaths...),
				AuthResourcePathsUnrestricted: unrestrictedAuthScope(bindings),
			})
			if discoverErr != nil {
				return recipe.Bundle{}, discoverErr
			}
			fields = append(fields, part...)
		}
		concepts := semantic.DiscoverCatalog(fields, semantic.CatalogOptions{Project: bindings.Project, SourceGeneration: bindings.DatasetGeneration})
		lowered, err := semantic.LowerBundleConceptSelections(resolved.Bundle, concepts.ResultsByResource())
		if err != nil {
			return recipe.Bundle{}, err
		}
		// Keep authored selections in the durable draft/revision; this transient
		// execution copy contains only concrete recipe constructs for the legacy
		// planner and still carries concept identity on generated columns.
		for index := range lowered.Outputs {
			lowered.Outputs[index].ConceptSelections = nil
		}
		return lowered, nil
	}
}

func bundleHasConceptSelections(bundle recipe.Bundle) bool {
	for _, output := range bundle.Outputs {
		if len(output.ConceptSelections) != 0 {
			return true
		}
	}
	return false
}

func conceptResourceTypes(bundle recipe.Bundle) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, output := range bundle.Outputs {
		if len(output.ConceptSelections) == 0 {
			continue
		}
		resourceType := strings.TrimSpace(output.RootResourceType)
		if resourceType == "" {
			continue
		}
		if _, ok := seen[resourceType]; ok {
			continue
		}
		seen[resourceType] = struct{}{}
		result = append(result, resourceType)
	}
	return result
}

func unrestrictedAuthScope(bindings recipe.RuntimeBindings) *bool {
	value := bindings.AuthScopeMode == authscope.ReadScopeUnrestricted || (bindings.AuthScopeMode == "" && len(bindings.AuthResourcePaths) == 0)
	return &value
}
