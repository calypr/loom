package resolver

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/api/authpolicy"
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

// RecipeExecutionScopeResolver is the per-request scope check used by the
// authorized durable-record reader. A transport can provide the same resolver
// through a narrow test seam without changing the shared authorization flow.
type RecipeExecutionScopeResolver func(context.Context, *authscope.ScopeResolver, *authscope.Principal, string, string, []string) (authscope.ReadScope, error)

// RecipeExecutionReadFailure retains the boundary that failed so transports
// can preserve their distinct lookup and authorization error contracts.
type RecipeExecutionReadFailure struct {
	Authorization bool
	Err           error
}

func (e *RecipeExecutionReadFailure) Error() string { return e.Err.Error() }
func (e *RecipeExecutionReadFailure) Unwrap() error { return e.Err }

// NewAuthorizedRecipeExecutionRecordReader loads the durable, transport-neutral
// execution record after applying the project and generation-scope checks.
// REST and GraphQL project the authorized record into their own wire contracts.
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

// NewAuthorizedRecipeExecutionReader performs the project/scope check before
// exposing durable publication status. Execution IDs are opaque and are not
// themselves authorization credentials.
func NewAuthorizedRecipeExecutionReader(store RecipeExecutionStore, scopeResolver *authscope.ScopeResolver) RecipeExecutionReader {
	return func(ctx context.Context, id string) (*RecipeExecution, error) {
		execution, err := NewAuthorizedRecipeExecutionRecordReader(store, scopeResolver, nil, false)(ctx, id)
		if err != nil {
			var failure *RecipeExecutionReadFailure
			if errors.As(err, &failure) {
				err = failure.Err
			}
			return nil, recipeExecutionLookupError(err)
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
			// REPAIR-20c (phantom trio): the output error MIRROR of the single
			// execution-level message applies only while the output itself is
			// not healthy. The lease-lost reconciliation (bundle_store.go
			// :1207-1214) fails the execution while READY outputs stay
			// untouched, and the per-output sanitizer would stamp a healthy
			// fully-published page with its own INTERNAL_ERROR trio whose
			// retryability contradicts the execution truth (top-level state
			// FAILED, errorCode PUBLICATION_LEASE_LOST, retryable=true).
			// Healthy = the durable success states READY/PUBLISHED. Genuine
			// per-output failures (failPhase bundle_store.go:989-995) are
			// FAILED or persist a own FailureCode, and still project
			// unconditionally through ErrorCode/ErrorRetryable/Phase below.
			mirroredError := RecipeExecutionOutputMessage(execution.Error, output.State)
			result.Outputs = append(result.Outputs, RecipeExecutionOutput{
				Name: output.Name, State: outputState, RowCount: &rowCount, Columns: append([]materialization.PhysicalColumn(nil), output.Columns...), Error: mirroredError,
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
	classified := authpolicy.Classify(err, authpolicy.OperationConcealedLookup)
	if _, ok := dataframeerrors.AsUserError(classified); ok {
		return classified
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

// RecipeExecutionOutputMessage mirrors execution failure text only onto outputs
// that have not already published successfully.
func RecipeExecutionOutputMessage(message string, state materialization.BundleState) string {
	if state == materialization.BundleReady || state == materialization.BundlePublished {
		return ""
	}
	return message
}
