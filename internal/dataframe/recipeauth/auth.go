package recipeauth

import (
	"context"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/errors/authpolicy"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

type Authorization struct {
	resolver                   *authscope.ScopeResolver
	selectionMembersCollection string
}

func (a Authorization) AuthorizeRead(ctx context.Context, bindings recipe.RuntimeBindings) (recipe.RuntimeBindings, error) {
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
		return recipe.RuntimeBindings{}, AuthorizationError(err)
	}
	if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) == 0 {
		return recipe.RuntimeBindings{}, dataframeerrors.NewError(dataframeerrors.CodeUnauthorizedProject, "")
	}
	bindings.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	bindings.AuthScopeMode = scope.Mode
	return bindings, nil
}

func AuthorizationError(err error) error {
	classified := authpolicy.Classify(err, authpolicy.OperationRecipeAuthorization)
	if _, ok := dataframeerrors.AsUserError(classified); ok {
		return classified
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func NewAuthorization(resolver *authscope.ScopeResolver, selectionMembersCollection string) Authorization {
	return Authorization{resolver: resolver, selectionMembersCollection: selectionMembersCollection}
}
