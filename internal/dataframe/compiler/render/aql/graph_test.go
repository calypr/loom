package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRenderGraphPhysicalPlanUsesGlobalLookaheadWindow(t *testing.T) {
	plan := ir.PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root": "fhir_patient", "edges": "fhir_edge", "label": "has", "type": "Specimen", "limit": 3,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root"}},
			{Kind: ir.PhysicalPathSeedOp, PathSeed: &ir.PhysicalPathSeed{Variable: "seed", Node: ir.PhysicalPathNode{Alias: "patient", ResourceType: "Patient", Value: ir.PhysicalValue{Variable: "root"}}}},
			{Kind: ir.PhysicalPathExtendOp, PathExtend: &ir.PhysicalPathExtend{
				Variable: "hop", SourceVariable: "seed",
				Traversal:    ir.PhysicalTraversal{TargetVariable: "target", EdgeVariable: "edge", Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edges", EdgeLabelBindKey: "label", TargetTypeBindKey: "type", EdgeTargetTypeField: "to_type"},
				Node:         ir.PhysicalPathNode{Alias: "specimen", ResourceType: "Specimen", Value: ir.PhysicalValue{Variable: "target"}},
				Relationship: ir.PhysicalPathRelationship{Alias: "specimenEdge", LabelBindKey: "label", FromResourceType: "Patient", ToResourceType: "Specimen"},
			}},
			{Kind: ir.PhysicalGraphReturnOp, GraphReturn: &ir.PhysicalGraphReturn{PathSets: []string{"seed", "hop"}, LimitBindKey: "limit"}},
		},
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(rendered.Query, "LIMIT @limit"); got != 1 {
		t.Fatalf("expected one global limit, got %d:\n%s", got, rendered.Query)
	}
	if !strings.Contains(rendered.Query, "LET __loom_physical_graph_rows = (") {
		t.Fatalf("expected path construction subquery:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "COLLECT __loom_path_identity") {
		t.Fatalf("expected semantic path dedupe:\n%s", rendered.Query)
	}
}
