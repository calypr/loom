package server

import (
	"context"
	"strings"

	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	"github.com/calypr/loom/internal/authscope"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

var recipeExecutionReadScope = func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, datasetGeneration string, declared []string) (authscope.ReadScope, error) {
	return scopes.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, declared)
}

func newRecipeExecutionReader(releases publication.BundleCatalog, scopes *authscope.ScopeResolver) dataframeapi.RecipeExecutionReader {
	if releases == nil {
		return func(context.Context, string) (publication.BundleExecution, error) {
			return publication.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
		}
	}
	resolveScope := dataframeapi.RecipeExecutionScopeResolver(func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, generation string, declared []string) (authscope.ReadScope, error) {
		return recipeExecutionReadScope(ctx, scopes, principal, project, generation, declared)
	})
	reader := dataframeapi.NewAuthorizedRecipeExecutionRecordReader(releases, scopes, resolveScope, true)
	return func(ctx context.Context, id string) (publication.BundleExecution, error) {
		return reader(ctx, strings.TrimSpace(id))
	}
}
