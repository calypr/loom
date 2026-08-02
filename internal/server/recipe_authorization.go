package server

import (
	"context"
	"errors"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type recipeAuthorization struct {
	resolver *authscope.ScopeResolver
}

var _ graphresolver.RecipeAuthorizer = recipeAuthorization{}

func (a recipeAuthorization) AuthorizeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if bindings.Project == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveReadScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, recipeAuthorizationError(err)
	}
	if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeUnauthorizedProject, "")
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

func (a recipeAuthorization) AuthorizeWrite(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if bindings.Project == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveWriteScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, recipeAuthorizationError(err)
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

func recipeAuthorizationError(err error) error {
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	case errors.Is(err, authscope.ErrForbidden):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthorizedProject, "")
	case errors.Is(err, authscope.ErrAuthorizationBackendUnavailable):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	default:
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
}
