package graphqlapi

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/materialization"
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
			return nil, fmt.Errorf("recipe execution store is not configured")
		}
		execution, err := store.GetExecution(ctx, id)
		if err != nil {
			return nil, err
		}
		if scopeResolver != nil {
			principal, _ := authscope.PrincipalFromContext(ctx)
			scope, scopeErr := scopeResolver.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths)
			if scopeErr != nil {
				return nil, scopeErr
			}
			if scope.Mode == authscope.ReadScopeRestricted && len(scope.AuthResourcePaths) != len(execution.AuthResourcePaths) {
				return nil, fmt.Errorf("recipe execution %q is outside caller scope", id)
			}
		}
		state, err := graphQLRecipeExecutionState(execution.State)
		if err != nil {
			return nil, err
		}
		result := &RecipeExecution{
			ID:                   execution.ID,
			Name:                 execution.Name,
			RecipeDigest:         execution.RecipeDigest,
			ResolvedSchemaDigest: execution.SchemaDigest,
			SourceGeneration:     execution.DatasetGeneration,
			State:                state,
			Error:                execution.Error,
			Outputs:              make([]RecipeExecutionOutput, 0, len(execution.Outputs)),
		}
		for _, output := range execution.Outputs {
			outputState, err := graphQLRecipeExecutionState(output.State)
			if err != nil {
				return nil, fmt.Errorf("output %q: %w", output.Name, err)
			}
			rowCount := int(output.RowCount)
			result.Outputs = append(result.Outputs, RecipeExecutionOutput{
				Name: output.Name, State: outputState, RowCount: &rowCount,
			})
		}
		return result, nil
	}
}

func graphQLRecipeExecutionState(state materialization.BundleState) (string, error) {
	switch state {
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
