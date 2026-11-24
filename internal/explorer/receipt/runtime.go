package receipt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/calypr/loom/internal/explorer/importedsource"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/authscope"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/explorer"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
)

type RuntimeConfig struct {
	Engine                  *dataframeexecution.Engine
	Explorers               *explorer.Service
	CapabilityResolver      *capabilityresolver.Resolver
	Logger                  *slog.Logger
	CombineInputSchemas     CombineInputSchemaResolver
	ImportedSources         *importedsource.Sources
	ConstructionSourceStage ports.ConstructionSourceStageCompiler
	Materializer            ports.ReceiptMaterializer
}

type Runtime struct {
	engine                  *dataframeexecution.Engine
	explorers               *explorer.Service
	capabilities            *capabilityresolver.Resolver
	logger                  *slog.Logger
	combineInputSchemas     CombineInputSchemaResolver
	importedSources         *importedsource.Sources
	constructionSourceStage ports.ConstructionSourceStageCompiler
	materializer            ports.ReceiptMaterializer
}

func NewRuntime(config RuntimeConfig) Runtime {
	return Runtime{
		engine: config.Engine, explorers: config.Explorers,
		capabilities: config.CapabilityResolver, logger: config.Logger,
		combineInputSchemas: config.CombineInputSchemas,
		importedSources:     config.ImportedSources, materializer: config.Materializer,
		constructionSourceStage: config.ConstructionSourceStage,
	}
}

func (r Runtime) CompileReceipt(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
	return Compile(ctx, request, r.capabilities, r.engine, r.explorers, r.logger, r.combineInputSchemas, r.importedSources)
}

func (r Runtime) ConstructionSourceStage(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
	if r.constructionSourceStage == nil {
		return CompileConstructionSourceStage(ctx, request, r.engine)
	}
	return CompileConstructionSourceStage(ctx, request, r.engine, r.constructionSourceStage)
}

func (r Runtime) ReceiptLookup(ctx context.Context, project, explorerID, receiptID string) (*receiptmodel.CompilationReceipt, error) {
	return r.explorers.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
}

func (r Runtime) PreviewReceipt(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
	if receipt == nil {
		return dataframeexecution.PreviewSummary{}, fmt.Errorf("compilation receipt is required")
	}
	resolved, err := CompileValidatedResolution(ctx, r.engine, receipt, bindings)
	if err != nil {
		r.logger.Error("Explorer receipt preview resolution failed", "receipt_id", receipt.ID, "error", err)
		return dataframeexecution.PreviewSummary{}, ClassifyPreviewResolutionError(receipt.ID, err)
	}
	output := ""
	if len(bindings.OutputNames) > 0 {
		output = bindings.OutputNames[0]
	}
	summary, previewErr := r.engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: output, Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
	if previewErr != nil {
		LogPreviewContractFailure(r.logger, httpctx.RequestIDFromContext(ctx), receipt.ID, output, previewErr)
	}
	return summary, previewErr
}

func (r Runtime) ValidateReceiptStream(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) error {
	err := ValidateFullStream(ctx, r.engine, receipt, bindings)
	if err != nil {
		r.logger.Error("Explorer row-value policy full-stream validation failed", "receipt_id", receipt.ID, "error", err)
	}
	return err
}

func (r Runtime) ScanCategories(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, executionarango.PivotCategoryScanTimeout)
	defer cancel()
	scanCtx = executionarango.WithPivotPreviewQueryBudget(scanCtx)
	started := time.Now()
	attrs := []any{"output_id", request.Output, "stage_id", request.StageID, "category_column_id", request.ColumnID, "value_column_id", request.ValueColumnID}
	if receipt == nil {
		err := fmt.Errorf("compilation receipt is missing")
		logServerDiagnostic(r.logger, slog.LevelError, "Explorer table-shape category scan resolution", httpctx.RequestIDFromContext(ctx), "category_scan_resolution", time.Since(started), err, attrs...)
		return dataframeexecution.CategoryScanResult{}, err
	}
	resolved, err := CompileValidatedResolution(scanCtx, r.engine, receipt, bindings)
	if err != nil {
		resultErr := CategoryScanResolutionError(receipt.ID, err)
		logServerDiagnostic(r.logger, slog.LevelError, "Explorer table-shape category scan resolution", httpctx.RequestIDFromContext(ctx), "category_scan_resolution", time.Since(started), resultErr, attrs...)
		return dataframeexecution.CategoryScanResult{}, resultErr
	}
	logServerDiagnostic(r.logger, slog.LevelInfo, "Explorer table-shape category scan resolution", httpctx.RequestIDFromContext(ctx), "category_scan_resolution", time.Since(started), nil, attrs...)
	scanStarted := time.Now()
	result, err := r.engine.ScanCategories(scanCtx, resolved, request)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
	}
	logServerDiagnostic(r.logger, level, "Explorer table-shape category scan", httpctx.RequestIDFromContext(ctx), "category_scan", time.Since(scanStarted), err, attrs...)
	return result, err
}

func (r Runtime) ScanColumnPopulation(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error) {
	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	attrs := []any{"output_id", request.Output, "stage_id", request.StageID, "column_ids", request.ColumnIDs}
	if receipt == nil {
		err := fmt.Errorf("compilation receipt is missing")
		logServerDiagnostic(r.logger, slog.LevelError, "Explorer construction column population resolution", httpctx.RequestIDFromContext(ctx), "column_population_resolution", time.Since(started), err, attrs...)
		return dataframeexecution.ColumnPopulationResult{}, err
	}
	resolved, err := CompileValidatedResolution(scanCtx, r.engine, receipt, bindings)
	if err != nil {
		resultErr := CategoryScanResolutionError(receipt.ID, err)
		logServerDiagnostic(r.logger, slog.LevelError, "Explorer construction column population resolution", httpctx.RequestIDFromContext(ctx), "column_population_resolution", time.Since(started), resultErr, attrs...)
		return dataframeexecution.ColumnPopulationResult{}, resultErr
	}
	logServerDiagnostic(r.logger, slog.LevelInfo, "Explorer construction column population resolution", httpctx.RequestIDFromContext(ctx), "column_population_resolution", time.Since(started), nil, attrs...)
	scanStarted := time.Now()
	result, err := r.engine.ScanColumnPopulation(scanCtx, resolved, request)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
	}
	logServerDiagnostic(r.logger, level, "Explorer construction column population", httpctx.RequestIDFromContext(ctx), "column_population", time.Since(scanStarted), err, attrs...)
	return result, err
}

func (r Runtime) PopulationMapping(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
	if receipt == nil {
		return dataframeexecution.PopulationMappingResult{}, fmt.Errorf("compilation receipt is required")
	}
	resolved, err := CompileValidatedResolution(ctx, r.engine, receipt, bindings)
	if err != nil {
		r.logger.Error("Explorer receipt population mapping resolution failed", "receipt_id", receipt.ID, "error", err)
		return dataframeexecution.PopulationMappingResult{}, ClassifyPreviewResolutionError(receipt.ID, err)
	}
	reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		for _, id := range memberIDs {
			if err := visit(id); err != nil {
				return err
			}
		}
		return nil
	})
	return r.engine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
}

func (r Runtime) CellTrace(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
	if receipt == nil {
		return dataframeexecution.CellTraceResult{}, fmt.Errorf("compilation receipt is required")
	}
	resolved, err := CompileValidatedResolution(ctx, r.engine, receipt, bindings)
	if err != nil {
		r.logger.Error("Explorer receipt cell trace resolution failed", "receipt_id", receipt.ID, "error", err)
		return dataframeexecution.CellTraceResult{}, ClassifyPreviewResolutionError(receipt.ID, err)
	}
	return r.engine.CellTrace(ctx, resolved, request)
}

func (r Runtime) RowLineage(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
	if receipt == nil {
		return dataframeexecution.RowLineageResult{}, fmt.Errorf("compilation receipt is required")
	}
	resolved, err := CompileValidatedResolution(ctx, r.engine, receipt, bindings)
	if err != nil {
		r.logger.Error("Explorer receipt row lineage resolution failed", "receipt_id", receipt.ID, "error", err)
		return dataframeexecution.RowLineageResult{}, ClassifyPreviewResolutionError(receipt.ID, err)
	}
	return r.engine.RowLineage(ctx, resolved, request)
}

func (r Runtime) MaterializeReceipt(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) (ports.Execution, error) {
	return r.materializer(ctx, receipt, bindings)
}

func NewCombineInputSchemaResolver(reader reciperuntime.ExactMaterializationReader, withPins dataframeexecution.WithExecutionReadPins, scopes *authscope.ScopeResolver) CombineInputSchemaResolver {
	return func(ctx context.Context, output facade.CompiledRecipeOutput, bindings recipe.RuntimeBindings) (ResolvedCombineInputSchema, error) {
		return ExactPublishedCombineInputSchemas(ctx, output, bindings, reader, withPins, scopes)
	}
}
