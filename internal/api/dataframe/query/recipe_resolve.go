package queryapi

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func (s *Service) resolveRecipeBundle(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (recipe.Bundle, error) {
	resolved, err := schema.Resolve(ctx, bundle, schema.Scope{
		Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
		AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...), AuthScopeMode: string(bindings.AuthScopeMode),
	}, reciperuntime.NewFieldDiscovery(s.discoverFields))
	if err != nil {
		return recipe.Bundle{}, queryInvalidErrorOrBackend(err)
	}
	return resolved.Bundle, nil
}
