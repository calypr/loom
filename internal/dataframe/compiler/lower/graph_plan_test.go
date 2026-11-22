package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestBuildGraphPhysicalPlanRootOnly(t *testing.T) {
	plan, err := BuildGraphPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient"}}, recipeplan.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	if rendered.Query == "" {
		t.Fatal("expected graph query")
	}
}

func TestBuildGraphPhysicalPlanOneHopRequired(t *testing.T) {
	plan, err := BuildGraphPhysicalPlan(recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchRequired}}}}, recipeplan.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("BuildGraphPhysicalPlan() error = %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	if !strings.Contains(rendered.Query, "GRAPH_RETURN") && !strings.Contains(rendered.Query, "COLLECT") {
		t.Fatalf("expected path union query, got %s", rendered.Query)
	}
}

func TestBuildGraphPhysicalPlanRootFallbackOnlyForOptionalBranches(t *testing.T) {
	base := recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []recipeplan.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchOptional}}}
	optional, err := BuildGraphPhysicalPlan(recipeplan.OutputPlan{Root: base}, recipeplan.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets[0]; got != "path_root" {
		t.Fatalf("optional path sets = %#v, want root fallback", optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets)
	}
	base.Children[0].MatchMode = spec.TraversalMatchRequired
	required, err := BuildGraphPhysicalPlan(recipeplan.OutputPlan{Root: base}, recipeplan.ExecutionContext{Project: "p"}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range required.Operations[len(required.Operations)-1].GraphReturn.PathSets {
		if set == "path_root" {
			t.Fatal("required graph unexpectedly returns root-only path")
		}
	}
}
