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

func TestPhysicalClickHouseCombineValidatesWorkspaceArtifactsOnlyAtCaptureBoundary(t *testing.T) {
	combine := PhysicalClickHouseCombine{
		Kind: PhysicalCombineKeyJoin,
		Inputs: []PhysicalCombineInputRef{
			{WorkspaceOutputID: "grouped"},
			{TableID: "scores", RevisionID: "scores-r1", OutputID: "scores"},
		},
		Keys:             []PhysicalCombineKey{{LeftColumnID: "patient", RightColumnID: "patient"}},
		JoinType:         "LEFT",
		RightMatchPolicy: "PRESERVE_ALL",
		Projections: []PhysicalCombineProjection{
			{OutputColumnID: "patient", InputIndex: 0, InputColumnID: "patient"},
			{OutputColumnID: "score", InputIndex: 1, InputColumnID: "score"},
		},
		Outputs: []PhysicalCombineOutputColumn{
			{ID: "patient", Name: "patient", LogicalType: "string", ClickHouseType: "String"},
			{ID: "score", Name: "score", LogicalType: "integer", ClickHouseType: "Int64"},
		},
	}
	if err := combine.Validate(); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture") {
		t.Fatalf("ordinary validation accepted unresolved workspace ref: %v", err)
	}
	if err := combine.ValidateForWorkspaceCompilation(); err != nil {
		t.Fatalf("workspace compiler validation rejected resolved source shape: %v", err)
	}
	if err := combine.ValidateWithWorkspaceArtifacts(); err != nil {
		t.Fatalf("capture-boundary validation rejected workspace ref: %v", err)
	}

	malformed := combine
	malformed.Inputs = append([]PhysicalCombineInputRef(nil), combine.Inputs...)
	malformed.Inputs[0].TableID = "client-table"
	malformed.Inputs[0].RevisionID = "client-revision"
	if err := malformed.ValidateWithWorkspaceArtifacts(); err == nil || !strings.Contains(err.Error(), "only one exact workspace output ID") {
		t.Fatalf("capture-boundary validation accepted a mixed workspace/published ref: %v", err)
	}

	duplicate := combine
	duplicate.Inputs = append([]PhysicalCombineInputRef(nil), combine.Inputs...)
	duplicate.Inputs[1] = PhysicalCombineInputRef{WorkspaceOutputID: "grouped"}
	if err := duplicate.ValidateWithWorkspaceArtifacts(); err == nil || !strings.Contains(err.Error(), "duplicates a workspace output") {
		t.Fatalf("capture-boundary validation accepted duplicate workspace refs: %v", err)
	}

	private := combine
	private.Inputs = append([]PhysicalCombineInputRef(nil), combine.Inputs...)
	private.Inputs[0] = PhysicalCombineInputRef{PrivateStageID: "stage-a"}
	if err := private.ValidateWithWorkspaceArtifacts(); err == nil || !strings.Contains(err.Error(), "cannot reference a private stage") {
		t.Fatalf("workspace artifact validation accepted an unowned private stage: %v", err)
	}
}

func TestPhysicalClickHouseAppendAllowsOnlyNullableScalarMissingInputs(t *testing.T) {
	combine := PhysicalClickHouseCombine{
		Kind: PhysicalCombineAppend,
		Inputs: []PhysicalCombineInputRef{
			{TableID: "left", RevisionID: "left-revision", OutputID: "left"},
			{TableID: "right", RevisionID: "right-revision", OutputID: "right"},
		},
		Projections: []PhysicalCombineProjection{{OutputColumnID: "value", InputIndex: 0, InputColumnID: "left-value"}},
		Outputs: []PhysicalCombineOutputColumn{{
			ID: "value", Name: "value", LogicalType: "integer", ClickHouseType: "Nullable(Int64)", Nullable: true,
		}},
	}
	if err := combine.Validate(); err != nil {
		t.Fatalf("valid append with one missing nullable input: %v", err)
	}

	nonNullable := combine
	nonNullable.Outputs = append([]PhysicalCombineOutputColumn(nil), combine.Outputs...)
	nonNullable.Outputs[0].Nullable = false
	nonNullable.Outputs[0].ClickHouseType = "Int64"
	if err := nonNullable.Validate(); err == nil || !strings.Contains(err.Error(), "nullable scalar") {
		t.Fatalf("non-nullable missing-input error = %v", err)
	}

	allEmpty := combine
	allEmpty.Outputs = append([]PhysicalCombineOutputColumn(nil), combine.Outputs...)
	allEmpty.Outputs = append(allEmpty.Outputs, PhysicalCombineOutputColumn{ID: "empty", Name: "empty", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true})
	if err := allEmpty.Validate(); err == nil || !strings.Contains(err.Error(), "not projected") {
		t.Fatalf("all-empty output error = %v", err)
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
