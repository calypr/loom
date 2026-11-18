package recipetest

import (
	"context"

	"github.com/calypr/loom/internal/dataframe/recipe/exec"
)

type Registry struct{}

func (Registry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, nil
}

func (registry Registry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return registry.LoadRecipe(ctx, name)
}
