package dataframe

import (
	"context"
	"github.com/calypr/loom/internal/api/dataframe/recipeexec"
	"strings"

	"github.com/calypr/loom/internal/authscope"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func NewRecipeExecutionReader(releases recipeexec.RecipeExecutionStore, scopes *authscope.ScopeResolver, resolveScope recipeexec.RecipeExecutionScopeResolver) RecipeExecutionReader {
	if releases == nil {
		return func(context.Context, string) (publication.BundleExecution, error) {
			return publication.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
		}
	}
	reader := recipeexec.NewAuthorizedRecipeExecutionRecordReader(releases, scopes, resolveScope, true)
	return func(ctx context.Context, id string) (publication.BundleExecution, error) {
		return reader(ctx, strings.TrimSpace(id))
	}
}
