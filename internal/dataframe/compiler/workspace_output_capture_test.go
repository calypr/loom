package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestWorkspaceOutputExecutionRemainsExplicitlyUnsupportedUntilCapture(t *testing.T) {
	output := lower.CompiledRecipeOutput{
		Name: "combined",
		Plan: ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &ir.PhysicalClickHouseCombine{
			Kind:   ir.PhysicalCombineAppend,
			Inputs: []ir.PhysicalCombineInputRef{{WorkspaceOutputID: "base"}, {TableID: "table", RevisionID: "r1", OutputID: "table"}},
		}},
		WorkspaceOutputSources: []lower.WorkspaceOutputSource{{InputIndex: 0, OutputID: "base", Schema: []lower.CompiledOutputColumn{{ID: "value", Kind: "string"}}}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a"}
	if _, err := CompileRecipeOutputWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("query compile error = %v", err)
	}
	if _, err := CompileRecipeOutputPageWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("page compile error = %v", err)
	}
}
