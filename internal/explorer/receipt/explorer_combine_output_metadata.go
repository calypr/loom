package receipt

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"

	"github.com/calypr/loom/internal/authscope"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type ResolvedCombineInputSchema map[int]facade.ResolvedClickHouseTable

type CombineInputSchemaResolver func(context.Context, facade.CompiledRecipeOutput, recipe.RuntimeBindings) (ResolvedCombineInputSchema, error)

func ExactPublishedCombineInputSchemas(
	ctx context.Context,
	output facade.CompiledRecipeOutput,
	bindings recipe.RuntimeBindings,
	reader reciperuntime.ExactMaterializationReader,
	withPins dataframeexecution.WithExecutionReadPins,
	scopes *authscope.ScopeResolver,
) (ResolvedCombineInputSchema, error) {
	result := ResolvedCombineInputSchema{}
	combine := output.Plan.ClickHouseCombine
	if combine == nil {
		return result, nil
	}
	indices := make([]int, 0, len(combine.Inputs))
	revisions := make([]string, 0, len(combine.Inputs))
	for index, input := range combine.Inputs {
		if input.WorkspaceOutputID != "" {
			continue
		}
		if input.TableID == "" || input.RevisionID == "" || input.OutputID == "" {
			return nil, fmt.Errorf("compiled Combine input %d is missing its exact published identity", index)
		}
		indices = append(indices, index)
		revisions = append(revisions, input.RevisionID)
	}
	if len(indices) == 0 {
		return result, nil
	}
	if reader == nil || withPins == nil {
		return nil, fmt.Errorf("exact published Combine input metadata resolver and read pins are required")
	}
	requestedScope, err := reciperuntime.ScopeFromBindings(bindings)
	if err != nil {
		return nil, fmt.Errorf("resolve Combine metadata scope: %w", err)
	}
	resolver := reciperuntime.NewCombineInputResolver(reader, scopes)
	err = withPins(ctx, revisions, func(pinnedCtx context.Context) error {
		for _, index := range indices {
			reference := combine.Inputs[index]
			materialization, err := reader.ExactExecutionMaterialization(pinnedCtx, reference.RevisionID, reference.OutputID)
			if err != nil {
				return fmt.Errorf("resolve exact Combine metadata input %d: %w", index, err)
			}
			resolved, err := resolver.ResolveMaterialization(pinnedCtx, index, reference, materialization, bindings, requestedScope)
			if err != nil {
				return err
			}
			result[index] = resolved
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("resolve exact published Combine input schemas under read pins: %w", err)
	}
	return result, nil
}
