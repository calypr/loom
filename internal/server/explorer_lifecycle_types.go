package server

import (
	"context"
	"log/slog"

	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

type ExplorerV2Previewer func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (map[string][]map[string]any, error)
type ExplorerV2Materializer = graphresolver.ExplorerBundleMaterializer

type ExplorerV2ReceiptCompileRequest struct {
	Project       string
	ExplorerID    string
	Workspace     authoringv2.Workspace
	SnapshotToken string
	RequestID     string
	Authorized    AuthorizedCapability
}

type ExplorerV2ReceiptCompiler func(context.Context, ExplorerV2ReceiptCompileRequest) (*explorer.CompilationReceipt, error)
type ExplorerV2ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ExplorerV2ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (engine.PreviewSummary, error)
type ExplorerV2ReceiptMaterializer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (graphresolver.RecipeExecution, error)
type ExplorerV2GenerationValidator func(context.Context, string, string) error
type ExplorerV2ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error

type ExplorerV2LifecycleConfig struct {
	CompileReceipt                ExplorerV2ReceiptCompiler
	Capability                    ExplorerCapabilityReader
	CapabilityToken               ExplorerCapabilityTokenReader
	AuthorizedCapabilityCompile   ExplorerAuthorizedCapabilityCompilationReader
	AuthorizedCapabilityExecution ExplorerAuthorizedCapabilityExecutionReader
	Preview                       ExplorerV2Previewer
	PreviewReceipt                ExplorerV2ReceiptPreviewer
	Materialize                   ExplorerV2Materializer
	MaterializeReceipt            ExplorerV2ReceiptMaterializer
	ReceiptLookup                 ExplorerV2ReceiptReader
	Logger                        *slog.Logger
	ValidateReleaseGeneration     ExplorerV2GenerationValidator
	ActivateRelease               ExplorerV2ReleaseActivator
}
