package aql

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestCodedGroupSourceInliningUsesPhysicalRootScanVariable(t *testing.T) {
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "selected_root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
	}}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source_projection",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group_codes", InputStageID: "source_projection", Kind: ir.PhysicalStageCodedGroupOp,
			CodedGroup: &ir.PhysicalStageCodedGroup{
				RootCollectionBindKey: "root_collection", SourceIdentityColumn: "_key", SourceRowsUnique: true,
			},
		}},
	}
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{}); got != "selected_root" {
		t.Fatalf("inline root variable = %q, want physical RootScan variable %q", got, "selected_root")
	}
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{terminalProjectionColumn: "source_records"}); got != "" {
		t.Fatalf("terminal-projection render selected inline root %q, want fallback", got)
	}
	plan.Operations = append(plan.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp}, plan.Operations[1])
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{}); got != "" {
		t.Fatalf("cardinality-changing source selected inline root %q, want fallback", got)
	}
}
