package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestBuildGraphPhysicalPlanRootOnly(t *testing.T) {
	plan, err := BuildGraphPhysicalPlan(semantic.SemanticPlan{Version: 1, Project: "p", Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}}, 100, ir.DefaultPhysicalOptimizationPolicy())
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
	plan, err := BuildGraphPhysicalPlan(semantic.SemanticPlan{Version: 1, Project: "p", Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: semantic.TraversalMatchRequired}}}}, 100, ir.DefaultPhysicalOptimizationPolicy())
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
	base := semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: semantic.TraversalMatchOptional}}}
	optional, err := BuildGraphPhysicalPlan(semantic.SemanticPlan{Version: 1, Project: "p", Root: base}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets[0]; got != "path_root" {
		t.Fatalf("optional path sets = %#v, want root fallback", optional.Operations[len(optional.Operations)-1].GraphReturn.PathSets)
	}
	base.Children[0].MatchMode = semantic.TraversalMatchRequired
	required, err := BuildGraphPhysicalPlan(semantic.SemanticPlan{Version: 1, Project: "p", Root: base}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, set := range required.Operations[len(required.Operations)-1].GraphReturn.PathSets {
		if set == "path_root" {
			t.Fatal("required graph unexpectedly returns root-only path")
		}
	}
}
