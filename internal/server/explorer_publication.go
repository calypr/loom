package server

import (
	"context"
	"fmt"
	"log/slog"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/dataframe/publication"
	materializationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
)

func explorerBundleMaterializer(recipeEngine *engine.Engine, target publication.Target, registry *materializationarango.Registry, degradation error, logger *slog.Logger, batchRows, batchBytes int) graphresolver.ExplorerBundleMaterializer {
	return func(ctx context.Context, bundle recipe.Bundle, bindings recipe.RuntimeBindings) (graphresolver.RecipeExecution, error) {
		if target == nil {
			return graphresolver.RecipeExecution{}, fmt.Errorf("Explorer publication is unavailable: %w", degradation)
		}
		bindings.IncludeAuthResourcePath = true
		var identity publication.BundleIdentity
		_, err := recipeEngine.MaterializeBundle(ctx, bundle, bindings, func(run context.Context, full engine.Resolved) error {
			var publishErr error
			identity, publishErr = publishResolvedRecipe(run, recipeEngine, target, bundle.Name, bindings, full, batchRows, batchBytes)
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
		logger.Info("Explorer materialization complete", "project", bindings.Project, "execution", published.ID)
		return graphresolver.RecipeExecution{ID: published.ID, Name: bundle.Name, RecipeDigest: published.RecipeDigest, ResolvedSchemaDigest: published.SchemaDigest, SourceGeneration: published.DatasetGeneration, State: string(published.State.Canonical()), Outputs: outputs}, nil
	}
}
