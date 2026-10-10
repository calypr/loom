package server

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type resolvedCombineInputSchema map[int]ir.ResolvedClickHouseTable

type combineInputSchemaResolver func(context.Context, lower.CompiledRecipeOutput, recipe.RuntimeBindings) (resolvedCombineInputSchema, error)

func exactPublishedCombineInputSchemas(
	ctx context.Context,
	output lower.CompiledRecipeOutput,
	bindings recipe.RuntimeBindings,
	reader exactMaterializationReader,
	withPins dataframeexecution.WithExecutionReadPins,
	scopes *authscope.ScopeResolver,
) (resolvedCombineInputSchema, error) {
	result := resolvedCombineInputSchema{}
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
	requestedScope, err := scopeFromRecipeBindings(bindings)
	if err != nil {
		return nil, fmt.Errorf("resolve Combine metadata scope: %w", err)
	}
	resolver := clickHouseCombineInputResolver{reader: reader, scopes: scopes}
	err = withPins(ctx, revisions, func(pinnedCtx context.Context) error {
		for _, index := range indices {
			reference := combine.Inputs[index]
			materialization, err := reader.ExactExecutionMaterialization(pinnedCtx, reference.RevisionID, reference.OutputID)
			if err != nil {
				return fmt.Errorf("resolve exact Combine metadata input %d: %w", index, err)
			}
			resolved, err := resolver.resolveMaterialization(pinnedCtx, index, reference, materialization, bindings, requestedScope)
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
