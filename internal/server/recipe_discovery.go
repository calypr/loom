package server

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type recipeCatalogDiscovery struct {
	connectionOptions arangostore.ConnectionOptions
	read              func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
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
		reader = catalog.DiscoverPopulatedFields
	}
	fields, err := reader(ctx, catalog.PopulatedFieldOptions{
		ConnectionOptions:             d.connectionOptions,
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
		result = append(result, schema.FieldCandidate{
			ResourceType: field.ResourceType, Path: field.Path, Kind: field.Kind,
			DistinctValues: append([]string(nil), field.DistinctValues...), DistinctTruncated: field.DistinctTruncated,
			PivotCandidate: field.PivotCandidate, PivotFamily: field.PivotFamily,
			PivotColumns:      append([]string(nil), field.PivotColumns...),
			PivotColumnSelect: field.PivotColumnSelect, PivotValueSelect: field.PivotValueSelect,
			PivotItemSource: field.PivotItemSource, PivotItemResourceType: field.PivotItemResourceType,
			PivotValueSelectors: append([]string(nil), field.PivotValueSelectors...),
		})
	}
	return result, nil
}

func recipeSchemaResolver(connectionOptions arangostore.ConnectionOptions, cache *catalog.Cache) func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
	read := catalog.DiscoverPopulatedFields
	if cache != nil {
		read = cache.DiscoverFields(read)
	}
	discovery := recipeCatalogDiscovery{connectionOptions: connectionOptions, read: read}
	return func(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
		resolved, err := schema.Resolve(ctx, bundle, schema.Scope{
			Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
			AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...),
			AuthScopeMode:     string(bindings.AuthScopeMode),
		}, discovery)
		if err != nil {
			return recipe.Bundle{}, err
		}
		return resolved.Bundle, nil
	}
}
