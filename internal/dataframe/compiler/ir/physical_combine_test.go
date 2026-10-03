package ir

import (
	"strings"
	"testing"
)

func TestPhysicalClickHouseCombineValidatesClosedExactInputPlan(t *testing.T) {
	plan := PhysicalPlan{
		Version: 1, Engine: PhysicalEngineClickHouse,
		ClickHouseCombine: &PhysicalClickHouseCombine{
			Kind: PhysicalCombineKeyJoin,
			Inputs: []PhysicalCombineInputRef{
				{TableID: "4:r1:1v7:patients", RevisionID: "execution-a", OutputID: "patients"},
				{TableID: "4:r2:1v7:scores", RevisionID: "execution-b", OutputID: "scores"},
			},
			Keys:             []PhysicalCombineKey{{LeftColumnID: "patient-id", RightColumnID: "subject-id"}},
			JoinType:         "LEFT",
			RightMatchPolicy: "PRESERVE_ALL",
			Projections: []PhysicalCombineProjection{
				{OutputColumnID: "patient-id", InputIndex: 0, InputColumnID: "patient-id"},
				{OutputColumnID: "score", InputIndex: 1, InputColumnID: "score"},
			},
			Outputs: []PhysicalCombineOutputColumn{
				{ID: "patient-id", Name: "patient_id", LogicalType: "string", ClickHouseType: "String"},
				{ID: "score", Name: "score", LogicalType: "number", ClickHouseType: "Nullable(Float64)", Nullable: true},
			},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid ClickHouse plan: %v", err)
	}
}

func TestPhysicalClickHouseCombineRejectsAQLPayloadAndUnpinnedInputs(t *testing.T) {
	combine := &PhysicalClickHouseCombine{
		Kind: PhysicalCombineMembership,
		Inputs: []PhysicalCombineInputRef{
			{TableID: "left", RevisionID: "revision-1", OutputID: "left"},
			{TableID: "right", RevisionID: "", OutputID: "right"},
		},
		Keys:           []PhysicalCombineKey{{LeftColumnID: "id", RightColumnID: "id"}},
		MembershipMode: "INCLUDE",
		Projections:    []PhysicalCombineProjection{{OutputColumnID: "id", InputIndex: 0, InputColumnID: "id"}},
		Outputs:        []PhysicalCombineOutputColumn{{ID: "id", Name: "id", LogicalType: "string", ClickHouseType: "String"}},
	}
	plan := PhysicalPlan{Version: 1, Engine: PhysicalEngineAQL, ClickHouseCombine: combine}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "AQL physical plan") {
		t.Fatalf("AQL plan combine payload error = %v", err)
	}
	plan.Engine = PhysicalEngineClickHouse
	plan.ClickHouseCombine = nil
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "requires a typed combine payload") {
		t.Fatalf("missing ClickHouse payload error = %v", err)
	}
}
