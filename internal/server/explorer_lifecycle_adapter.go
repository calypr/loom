package server

import (
	"context"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

// newExplorerLifecycleApplication is the only server-to-application wiring
// seam. GraphQL execution values are converted here so the lifecycle package
// never depends on resolver transport types.
func newExplorerLifecycleApplication(store *explorer.Service, config ExplorerV2LifecycleConfig) *lifecycle.Service {
	if store == nil {
		return nil
	}
	toExecution := func(value graphresolver.RecipeExecution) lifecycle.Execution {
		result := lifecycle.Execution{ID: value.ID, Name: value.Name, RecipeDigest: value.RecipeDigest, ResolvedSchemaDigest: value.ResolvedSchemaDigest, SourceGeneration: value.SourceGeneration, State: value.State}
		result.Outputs = make([]lifecycle.ExecutionOutput, 0, len(value.Outputs))
		for _, output := range value.Outputs {
			result.Outputs = append(result.Outputs, lifecycle.ExecutionOutput{Name: output.Name, State: output.State, RowCount: output.RowCount, Columns: output.Columns})
		}
		return result
	}
	appConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: config.Capability,
			Token:   config.CapabilityToken,
			Catalog: func(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
				return authoringV2Catalog(snapshot, explorerID)
			},
		},
	}
	if config.CompileReceipt != nil {
		appConfig.CompileReceipt = func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return config.CompileReceipt(ctx, request)
		}
	}
	if config.PreviewReceipt != nil {
		appConfig.PreviewReceipt = func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			return config.PreviewReceipt(ctx, receipt, bindings, visit)
		}
	}
	if config.ValidateReleaseGeneration != nil {
		appConfig.ValidateReleaseGeneration = func(ctx context.Context, project, generation string) error {
			return config.ValidateReleaseGeneration(ctx, project, generation)
		}
	}
	if config.ActivateRelease != nil {
		appConfig.ActivateRelease = func(ctx context.Context, project, generation string, selectors []dataset.DataframeSelector) error {
			return config.ActivateRelease(ctx, project, generation, selectors)
		}
	}
	if config.PrepareRelease != nil {
		appConfig.PrepareRelease = func(ctx context.Context, project, generation string, selectors []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
			return config.PrepareRelease(ctx, project, generation, selectors)
		}
	}
	if config.AuthorizedCapabilityCompile != nil {
		appConfig.Capability.ForCompilation = config.AuthorizedCapabilityCompile
	}
	if config.AuthorizedCapabilityExecution != nil {
		appConfig.Capability.ForExecution = config.AuthorizedCapabilityExecution
	}
	if config.MaterializeReceipt != nil {
		appConfig.MaterializeReceipt = func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (lifecycle.Execution, error) {
			value, err := config.MaterializeReceipt(ctx, receipt, bindings)
			return toExecution(value), err
		}
	}
	if config.ReceiptLookup != nil {
		appConfig.ReceiptLookup = func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return config.ReceiptLookup(ctx, project, explorerID, receiptID)
		}
	}
	if config.Logger != nil {
		// Logging remains a transport/deployment concern. Preview metrics are
		// emitted by the existing server adapter around the sink callback.
	}
	application, err := lifecycle.New(store, appConfig)
	if err != nil {
		return nil
	}
	return application
}
