package fielddiscovery

import (
	"context"
	"github.com/calypr/loom/internal/catalog/fieldcatalog"

	"github.com/calypr/loom/internal/catalog/discoverycache"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func RecipeSchemaResolver(read func(context.Context, fieldcatalog.PopulatedFieldOptions) ([]fieldcatalog.PopulatedField, error), cache *discoverycache.Cache) func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
	if cache != nil {
		read = cache.DiscoverFields(read)
	}
	discovery := NewFieldDiscovery(read)
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
