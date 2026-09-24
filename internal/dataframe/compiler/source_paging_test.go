package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestSourcePageKeepsOnlyRequestedSourceAndDependencies(t *testing.T) {
	plan := projectionDependencyTestPlan(t)
	output := lower.CompiledRecipeOutput{
		Name: "source-page", RootResourceType: "Patient",
		OptimizedPlan: &plan, OutputSchema: []lower.CompiledOutputColumn{{Name: "id"}},
	}
	page, err := CompileRecipeOutputSourcePageWithPolicy(output,
		recipe.RuntimeBindings{Project: "project"}, 25,
		ir.DefaultPhysicalOptimizationPolicy(), []string{"child_set"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Sources) != 2 || page.Sources[0].Variable != "root" || page.Sources[1].Variable != "child_set" {
		t.Fatalf("source page variables = %#v", page.Sources)
	}
	if strings.Contains(page.Sources[0].Query, "parent_set") || strings.Contains(page.Sources[0].Query, "child_set") || strings.Contains(page.Sources[0].Query, "unrelated_set") {
		t.Fatalf("root source retained an unrelated child set")
	}
	if !strings.Contains(page.Sources[1].Query, "parent_set") || !strings.Contains(page.Sources[1].Query, "child_set") || strings.Contains(page.Sources[1].Query, "unrelated_set") {
		t.Fatalf("child source did not retain exactly its dependency chain")
	}
	if !strings.Contains(page.Sources[1].Query, "@loom_root_page_keys") {
		t.Fatalf("child source lost the selected-root page bound")
	}
	if _, err := CompileRecipeOutputSourcePageWithPolicy(output,
		recipe.RuntimeBindings{Project: "project"}, 25,
		ir.DefaultPhysicalOptimizationPolicy(), []string{"missing_set"}); err == nil {
		t.Fatal("unknown source set was accepted")
	}
}
