package resolver

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

// RecipeExecutionStore is the durable subset needed by the GraphQL execution
// status query. Keeping this interface narrower than BundleCatalog makes the
// transport adapter usable with both the production Arango registry and small
// test fakes.
type RecipeExecutionStore interface {
	GetExecution(context.Context, string) (materialization.BundleExecution, error)
}

// NewAuthorizedRecipeExecutionReader performs the project/scope check before
// exposing durable publication status. Execution IDs are opaque and are not
// themselves authorization credentials.
func NewAuthorizedRecipeExecutionReader(store RecipeExecutionStore, scopeResolver *authscope.ScopeResolver) RecipeExecutionReader {
	return func(ctx context.Context, id string) (*RecipeExecution, error) {
		if store == nil {
			return nil, dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
		}
		execution, err := store.GetExecution(ctx, id)
		if err != nil {
			return nil, recipeExecutionLookupError(err)
		}
		if scopeResolver != nil {
			principal, _ := authscope.PrincipalFromContext(ctx)
			scope, scopeErr := scopeResolver.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths)
			if scopeErr != nil {
				return nil, recipeExecutionLookupError(scopeErr)
			}
			if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) != len(execution.AuthResourcePaths) {
				return nil, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
			}
		}
		state, err := graphQLRecipeExecutionState(execution.State)
		if err != nil {
			return nil, err
		}
		result := &RecipeExecution{
			ID:                   execution.ID,
			Name:                 execution.Name,
			TranslationVersion:   execution.TranslationVersion,
			RecipeDigest:         execution.RecipeDigest,
			ResolvedSchemaDigest: execution.SchemaDigest,
			SourceGeneration:     execution.DatasetGeneration,
			State:                state,
			Error:                execution.Error,
			ErrorCode:            execution.FailureCode,
			ErrorRetryable:       execution.FailureRetryable,
			Phase:                execution.FailurePhase,
			Outputs:              make([]RecipeExecutionOutput, 0, len(execution.Outputs)),
		}
		for _, output := range execution.Outputs {
			outputState, err := graphQLRecipeExecutionState(output.State)
			if err != nil {
				return nil, dataframeerrors.Wrap(err, dataframeerrors.CodeInternalError, "")
			}
			rowCount := int(output.RowCount)
			result.Outputs = append(result.Outputs, RecipeExecutionOutput{
				Name: output.Name, State: outputState, RowCount: &rowCount, Error: execution.Error,
				ErrorCode: output.FailureCode, ErrorRetryable: output.FailureRetryable,
				Selector: output.Selector, Phase: output.FailurePhase,
			})
		}
		return result, nil
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
	if errors.Is(err, authscope.ErrUnauthenticated) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnauthenticated, "")
	}
	if errors.Is(err, authscope.ErrForbidden) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeRecipeExecutionNotFound, "")
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func graphQLRecipeExecutionState(state materialization.BundleState) (string, error) {
	switch state {
	case materialization.BundleQueued:
		return "QUEUED", nil
	case materialization.BundleRunning:
		return "RUNNING", nil
	case materialization.BundlePublished:
		return "PUBLISHED", nil
	case materialization.BundlePending:
		return "ACCEPTED", nil
	case materialization.BundlePreflight, materialization.BundleValidating:
		return "VALIDATING", nil
	case materialization.BundleLoading:
		return "RUNNING", nil
	case materialization.BundleReady:
		return "READY", nil
	case materialization.BundleFailed:
		return "FAILED", nil
	default:
		return "", fmt.Errorf("unsupported bundle execution state %q", state)
	}
}
