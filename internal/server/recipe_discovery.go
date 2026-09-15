package server

import (
	"context"

	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func recipeSchemaResolver(read func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error), cache *catalog.Cache) func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
	if cache != nil {
		read = cache.DiscoverFields(read)
	}
	discovery := queryapi.NewRecipeFieldDiscovery(read)
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
