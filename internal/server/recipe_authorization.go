package server

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/api/authpolicy"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

// recipeAuthorization is the selected-backend recipe authorizer for both
// lanes (arch-backend-08): the scoping law is backend-neutral, and the one
// backend-specific decision is the SelectionMembersCollection stamp policy.
// The Arango lane stamps the AQL group-rows collection its renderer consumes
// onto every binding (construction passes arangofactory.SelectionMembersCollection);
// the PostgreSQL lane leaves it empty (construction passes the zero value)
// because that binding is consumed exclusively by the AQL group-rows
// renderer -- the PostgreSQL renderer never reads it, and a PostgreSQL-tagged
// command never reaches an AQL renderer (closed dispatch rejects it at the
// compiler boundary). Stamping an Arango collection name onto every
// PostgreSQL binding is what the sibling type this core replaces avoided.
type recipeAuthorization struct {
	resolver                   *authscope.ScopeResolver
	selectionMembersCollection string
}

var _ graphresolver.RecipeAuthorizer = recipeAuthorization{}

func (a recipeAuthorization) AuthorizeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	bindings.Project = strings.TrimSpace(bindings.Project)
	bindings.SelectionProject = projectid.Canonical(bindings.Project)
	bindings.SelectionMembersCollection = a.selectionMembersCollection
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
	if strings.TrimSpace(bindings.Project) == "" {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeProjectRequired, "")
	}
	bindings.Project = strings.TrimSpace(bindings.Project)
	bindings.SelectionProject = projectid.Canonical(bindings.Project)
	bindings.SelectionMembersCollection = a.selectionMembersCollection
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
	classified := authpolicy.Classify(err, authpolicy.OperationRecipeAuthorization)
	if _, ok := dataframeerrors.AsUserError(classified); ok {
		return classified
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
