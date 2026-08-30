package server

import (
	"context"
	"fmt"
	"log/slog"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	materializationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

// explorerReceiptMaterializer keeps publication bound to the immutable
// compilation receipt. The receipt is the only artifact crossing the HTTP to
// execution boundary; its resolved recipe is lowered in memory by the execution package.
func explorerReceiptMaterializer(recipeEngine *dataframeexecution.Engine, target publication.Target, registry *materializationarango.Registry, degradation error, logger *slog.Logger, batchRows, batchBytes int) ExplorerV2ReceiptMaterializer {
	return func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if receipt == nil {
			return graphresolver.RecipeExecution{}, fmt.Errorf("compilation receipt is required")
		}
		if target == nil {
			return graphresolver.RecipeExecution{}, fmt.Errorf("Explorer publication is unavailable: %w", degradation)
		}
		bindings.IncludeAuthResourcePath = true
		var identity publication.BundleIdentity
		_, err := recipeEngine.MaterializeResolvedBundle(ctx, receipt.Bundle, bindings, func(run context.Context, full dataframeexecution.Resolved) error {
			if validationErr := validateReceiptResolution(receipt, &full); validationErr != nil {
				return validationErr
			}
			var publishErr error
			identity, publishErr = publishResolvedRecipe(run, recipeEngine, target, receipt.Bundle.Name, bindings, full, batchRows, batchBytes)
			return publishErr
		})
		if err != nil {
			return graphresolver.RecipeExecution{}, err
		}
		published, err := registry.FindExecutionByKey(ctx, identity.Key())
		if err != nil {
			return graphresolver.RecipeExecution{}, fmt.Errorf("load Explorer publication: %w", err)
		}
		outputs := make([]graphresolver.RecipeExecutionOutput, 0, len(published.Outputs))
		for _, output := range published.Outputs {
			count := int(output.RowCount)
			outputs = append(outputs, graphresolver.RecipeExecutionOutput{Name: output.Name, State: string(output.State.Canonical()), RowCount: &count, Columns: append([]publication.PhysicalColumn(nil), output.Columns...)})
		}
		if logger != nil {
			logger.Info("Explorer receipt materialization complete", "project", bindings.Project, "execution", published.ID)
		}
		return graphresolver.RecipeExecution{ID: published.ID, Name: receipt.Bundle.Name, RecipeDigest: published.RecipeDigest, ResolvedSchemaDigest: published.SchemaDigest, SourceGeneration: published.DatasetGeneration, State: string(published.State.Canonical()), Outputs: outputs}, nil
	}
}
