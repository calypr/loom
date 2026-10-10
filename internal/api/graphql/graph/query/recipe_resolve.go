package queryapi

import (
	"context"

	recipeapi "github.com/calypr/loom/internal/api/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func (s *Service) resolveRecipeBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
	resolved, err := schema.Resolve(ctx, bundle, schema.Scope{
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...), AuthScopeMode: string(bindings.AuthScopeMode),
	}, recipeapi.NewFieldDiscovery(s.discoverFields))
	if err != nil {
		return recipe.Bundle{}, queryInvalidErrorOrBackend(err)
	}
	return resolved.Bundle, nil
}
