package server

import (
	"context"
	"fmt"

	"github.com/calypr/loom/graphqlapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type recipeAuthorization struct {
	resolver *authscope.ScopeResolver
}

var _ graphqlapi.RecipeAuthorizer = recipeAuthorization{}

func (a recipeAuthorization) AuthorizeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if bindings.Project == "" {
		return recipe.RuntimeBindings{}, fmt.Errorf("recipe project is required")
	}
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveReadScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, err
	}
	if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
		return recipe.RuntimeBindings{}, fmt.Errorf("caller has no read access to project %q", bindings.Project)
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

func (a recipeAuthorization) AuthorizeWrite(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if bindings.Project == "" {
		return recipe.RuntimeBindings{}, fmt.Errorf("recipe project is required")
	}
	if a.resolver == nil {
		bindings.AuthScopeMode = authscope.ReadScopeUnrestricted
		return bindings, nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	scope, err := a.resolver.ResolveWriteScopeForGeneration(ctx, principal, bindings.Project, bindings.DatasetGeneration, bindings.AuthResourcePaths)
	if err != nil {
		return recipe.RuntimeBindings{}, err
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}
