package dataframe

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/api/authpolicy"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

type RecipeExecutionStore interface {
	GetExecution(context.Context, string) (materialization.BundleExecution, error)
}

type RecipeExecutionScopeResolver func(context.Context, *authscope.ScopeResolver, *authscope.Principal, string, string, []string) (authscope.ReadScope, error)

type RecipeExecutionReadFailure struct {
	Authorization bool
	Err           error
}

func (e *RecipeExecutionReadFailure) Error() string { return e.Err.Error() }
func (e *RecipeExecutionReadFailure) Unwrap() error { return e.Err }

func NewAuthorizedRecipeExecutionRecordReader(store RecipeExecutionStore, scopeResolver *authscope.ScopeResolver, resolveScope RecipeExecutionScopeResolver, requireID bool) func(context.Context, string) (materialization.BundleExecution, error) {
	return func(ctx context.Context, id string) (materialization.BundleExecution, error) {
		if store == nil {
			return materialization.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		execution, err := store.GetExecution(ctx, id)
		if err != nil {
			return materialization.BundleExecution{}, &RecipeExecutionReadFailure{Err: err}
		}
		if requireID && execution.ID == "" {
			return materialization.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
		}
		principal, _ := authscope.PrincipalFromContext(ctx)
		if err := authscope.AuthorizeProject(principal, execution.Project, false); err != nil {
			return materialization.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
		}
		if scopeResolver != nil {
			resolve := resolveScope
			if resolve == nil {
				resolve = func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, generation string, declared []string) (authscope.ReadScope, error) {
					return scopes.ResolveReadScopeForGeneration(ctx, principal, project, generation, declared)
				}
			}
			scope, scopeErr := resolve(ctx, scopeResolver, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths)
			if scopeErr != nil {
				return materialization.BundleExecution{}, &RecipeExecutionReadFailure{Authorization: true, Err: scopeErr}
			}
			if scope.Mode == authscope.ReadScopeRestricted && !scope.CoversExecutionPaths(execution.AuthResourcePaths) {
				return materialization.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
			}
		}
		return execution, nil
	}
}

func NewRecipeExecutionAuthorizer(store RecipeExecutionStore, scopes *authscope.ScopeResolver) func(context.Context, string) error {
	read := NewAuthorizedRecipeExecutionRecordReader(store, scopes, nil, false)
	return func(ctx context.Context, id string) error {
		execution, err := read(ctx, id)
		if err != nil {
			var failure *RecipeExecutionReadFailure
			if errors.As(err, &failure) {
				err = failure.Err
			}
			return recipeExecutionLookupError(err)
		}
		if err := checkRecipeExecutionState(execution.State); err != nil {
			return err
		}
		for _, output := range execution.Outputs {
			if err := checkRecipeExecutionState(output.State); err != nil {
				return dataframeerrors.Wrap(err, dataframeerrors.CodeInternalError, "")
			}
		}
		return nil
	}
}

func checkRecipeExecutionState(state materialization.BundleState) error {
	switch state {
	case materialization.BundleQueued, materialization.BundleRunning, materialization.BundlePublished,
		materialization.BundlePending, materialization.BundlePreflight, materialization.BundleValidating,
		materialization.BundleLoading, materialization.BundleReady, materialization.BundleFailed:
		return nil
	default:
		return fmt.Errorf("unsupported bundle execution state %q", state)
	}
}

func recipeExecutionLookupError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "")
	}
	if errors.Is(err, materialization.ErrBundleNotFound) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeRecipeExecutionNotFound, "")
	}
	classified := authpolicy.Classify(err, authpolicy.OperationConcealedLookup)
	if _, ok := dataframeerrors.AsUserError(classified); ok {
		return classified
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func RecipeExecutionOutputMessage(message string, state materialization.BundleState) string {
	if state == materialization.BundleReady || state == materialization.BundlePublished {
		return ""
	}
	return message
}
