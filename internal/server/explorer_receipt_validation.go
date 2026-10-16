package server

import (
	"context"
	"fmt"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func validateReceiptFullStream(ctx context.Context, recipeEngine *dataframeexecution.Engine, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) error {
	if receipt == nil {
		return fmt.Errorf("compilation receipt is required")
	}
	if recipeEngine == nil {
		return fmt.Errorf("recipe engine is required")
	}
	if len(bindings.OutputNames) != 1 || strings.TrimSpace(bindings.OutputNames[0]) == "" {
		return fmt.Errorf("exactly one output is required for full-stream validation")
	}
	output := bindings.OutputNames[0]
	resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
	if err != nil {
		return classifyReceiptPreviewResolutionError(receipt.ID, err)
	}
	// Receipt validation compiles every output. Restore the exact execution
	// selector afterward so unrelated receipt outputs are never streamed.
	resolved.Semantic.SemanticPlan.Bindings.OutputNames = []string{output}
	streams, err := recipeEngine.Streams(ctx, resolved)
	if err != nil {
		return classifyReceiptPreviewResolutionError(receipt.ID, err)
	}
	if len(streams) != 1 || streams[0].Name != output {
		return fmt.Errorf("full-stream validation resolved %d streams for output %q", len(streams), output)
	}
	_, err = streams[0].Stream(ctx, func(map[string]any) error { return nil })
	if err != nil {
		return classifyReceiptPreviewResolutionError(receipt.ID, err)
	}
	return nil
}
