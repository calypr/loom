package server

import (
	"context"
	"log/slog"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

type ExplorerV2ReceiptCompileRequest = lifecycle.CompileReceiptRequest
type ExplorerV2ReceiptCompiler = lifecycle.ReceiptCompiler
type ExplorerV2ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ExplorerV2ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)
type ExplorerV2ReceiptMaterializer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error)
type ExplorerV2GenerationValidator func(context.Context, string, string) error
type ExplorerV2ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error
type ExplorerV2ReleasePreparer func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error)

type ExplorerV2LifecycleConfig struct {
	CompileReceipt                ExplorerV2ReceiptCompiler
	Capability                    ExplorerCapabilityReader
	CapabilityToken               ExplorerCapabilityTokenReader
	AuthorizedCapabilityCompile   ExplorerAuthorizedCapabilityCompilationReader
	AuthorizedCapabilityExecution ExplorerAuthorizedCapabilityExecutionReader
	PreviewReceipt                ExplorerV2ReceiptPreviewer
	MaterializeReceipt            ExplorerV2ReceiptMaterializer
	ReceiptLookup                 ExplorerV2ReceiptReader
	Logger                        *slog.Logger
	ValidateReleaseGeneration     ExplorerV2GenerationValidator
	ActivateRelease               ExplorerV2ReleaseActivator
	PrepareRelease                ExplorerV2ReleasePreparer
}
