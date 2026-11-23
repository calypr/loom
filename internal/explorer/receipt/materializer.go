package receipt

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	materializationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/compilation"
)

func NewMaterializer(recipeEngine *dataframeexecution.Engine, target publication.Target, registry *materializationarango.Registry, degradation error, logger *slog.Logger, batchRows, batchBytes int, qualityMaxRows, qualityMaxDistinctKeys int64) ports.ReceiptMaterializer {
	return func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) (ports.Execution, error) {
		if receipt == nil {
			return ports.Execution{}, fmt.Errorf("compilation receipt is required")
		}
		if target == nil {
			return ports.Execution{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "materialize", Code: "MATERIALIZATION_UNAVAILABLE", Message: "Explorer publication is not configured", Cause: degradation}
		}
		workspace, err := receipt.CurrentWorkspace()
		if err != nil {
			return ports.Execution{}, fmt.Errorf("publication workspace: %w", err)
		}
		contracts, err := receiptmodel.DecodePublicOutputContracts(receipt.PublicOutputContract)
		if err != nil {
			return ports.Execution{}, fmt.Errorf("publication contracts: %w", err)
		}
		sourceRows := make(map[string]*publication.SourceRowMetadata)
		for _, contract := range contracts.Outputs {
			descriptor, proofErr := compilation.ResolveAddressableSourceRow(workspace, contract, receipt.EmittedColumns)
			if proofErr == nil {
				sourceRows[contract.OutputID] = &publication.SourceRowMetadata{ResourceType: descriptor.ResourceType, IDColumn: descriptor.PhysicalColumn}
			}
		}
		bindings.IncludeAuthResourcePath = true
		var identity publication.BundleIdentity
		_, err = recipeEngine.MaterializeResolvedBundle(ctx, receipt.Bundle, bindings, func(run context.Context, full dataframeexecution.Resolved) error {
			if validationErr := ValidateResolution(receipt, &full); validationErr != nil {
				return validationErr
			}
			var publishErr error
			identity, publishErr = reciperuntime.PublishResolvedRecipe(run, recipeEngine, target, receipt.Bundle.Name, bindings, full, receipt.ID, sourceRows, batchRows, batchBytes, qualityMaxRows, qualityMaxDistinctKeys)
			return publishErr
		})
		if err != nil {
			return ports.Execution{}, err
		}
		published, err := registry.FindExecutionByKey(ctx, identity.Key())
		if err != nil {
			return ports.Execution{}, fmt.Errorf("load Explorer publication: %w", err)
		}
		outputs := make([]ports.ExecutionOutput, 0, len(published.Outputs))
		for _, output := range published.Outputs {
			count := int(output.RowCount)
			outputs = append(outputs, ports.ExecutionOutput{Name: output.Name, State: string(output.State.Canonical()), RowCount: &count, Columns: append([]publication.PhysicalColumn(nil), output.Columns...)})
		}
		if logger != nil {
			logger.Info("Explorer receipt materialization complete", "project", bindings.Project, "execution", published.ID)
		}
		return ports.Execution{
			ID: published.ID, Name: receipt.Bundle.Name, Project: published.Project,
			RecipeDigest: published.RecipeDigest, ResolvedSchemaDigest: published.SchemaDigest,
			SourceGeneration: published.DatasetGeneration, ScopeDigest: published.ScopeDigest,
			State: string(published.State.Canonical()), Outputs: outputs,
			QualityReports: publication.CloneQualityReports(published.QualityReports),
		}, nil
	}
}
