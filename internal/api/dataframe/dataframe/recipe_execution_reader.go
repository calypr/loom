package dataframe

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/authscope"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func NewRecipeExecutionReader(releases RecipeExecutionStore, scopes *authscope.ScopeResolver, resolveScope RecipeExecutionScopeResolver) RecipeExecutionReader {
	if releases == nil {
		return func(context.Context, string) (publication.BundleExecution, error) {
			return publication.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
		}
	}
	reader := NewAuthorizedRecipeExecutionRecordReader(releases, scopes, resolveScope, true)
	return func(ctx context.Context, id string) (publication.BundleExecution, error) {
		return reader(ctx, strings.TrimSpace(id))
	}
}
