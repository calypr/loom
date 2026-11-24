package recipetest

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type Registry struct{}

func (Registry) LoadRecipe(context.Context, string) (recipe.Entry, error) {
	return recipe.Entry{}, nil
}

func (registry Registry) LoadRecipeVersion(ctx context.Context, name, _ string) (recipe.Entry, error) {
	return registry.LoadRecipe(ctx, name)
}
