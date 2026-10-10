package ir

import (
	"strings"
	"testing"
)

func TestWorkspaceOutputCombineIsTypedButNotExecutableWithoutCapture(t *testing.T) {
	combine := PhysicalClickHouseCombine{
		Kind: PhysicalCombineAppend,
		Inputs: []PhysicalCombineInputRef{
			{WorkspaceOutputID: "grouped"},
			{TableID: "source:1:table", RevisionID: "table-r1", OutputID: "table"},
		},
		Projections: []PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "group_value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "table_value"},
		},
		Outputs: []PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	if err := combine.ValidateForWorkspaceCompilation(); err != nil {
		t.Fatalf("resolver-scoped typed combine rejected: %v", err)
	}
	if err := combine.Validate(); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture") {
		t.Fatalf("ordinary combine validation error = %v", err)
	}
	plan := PhysicalPlan{Version: 1, Engine: PhysicalEngineClickHouse, ClickHouseCombine: &combine}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture") {
		t.Fatalf("ordinary physical-plan validation error = %v", err)
	}
}
