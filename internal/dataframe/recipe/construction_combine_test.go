package recipe

import (
	"strings"
	"testing"
)

func TestConstructionCombineKeyJoinRequiresExplicitMultiplicityAndProjections(t *testing.T) {
	outputs := []StageColumn{{ID: "patient_id", Name: "patient_id"}, {ID: "score", Name: "score"}}
	combine := ConstructionCombine{
		Kind: ConstructionCombineKeyJoin, JoinType: ConstructionCombineLeftJoin,
		RightMatchPolicy: ConstructionCombinePreserveAllMatches,
		Keys:             []ConstructionCombineKey{{LeftColumnID: "id", RightColumnID: "patient"}},
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "id"},
			{OutputColumnID: "score", InputIndex: 1, InputColumnID: "score"},
		},
	}
	if err := combine.Validate(2, outputs); err != nil {
		t.Fatalf("valid left key join: %v", err)
	}
	combine.RightMatchPolicy = ""
	if err := combine.Validate(2, outputs); err == nil || !strings.Contains(err.Error(), "rightMatchPolicy") {
		t.Fatalf("key join without explicit duplicate behavior error = %v", err)
	}
}

func TestConstructionCombineAppendMapsEveryOutputFromEveryInput(t *testing.T) {
	outputs := []StageColumn{{ID: "id", Name: "id"}, {ID: "value", Name: "value"}}
	combine := ConstructionCombine{
		Kind: ConstructionCombineAppend,
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "id", InputIndex: 0, InputColumnID: "a-id"},
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "a-value"},
			{OutputColumnID: "id", InputIndex: 1, InputColumnID: "b-id"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "b-value"},
		},
	}
	if err := combine.Validate(2, outputs); err != nil {
		t.Fatalf("valid append with independent input IDs: %v", err)
	}
	combine.Projections = combine.Projections[:3]
	if err := combine.Validate(2, outputs); err == nil || !strings.Contains(err.Error(), "not mapped from input 1") {
		t.Fatalf("incomplete append mapping error = %v", err)
	}
}

func TestConstructionCombineMembershipPreservesLeftRows(t *testing.T) {
	outputs := []StageColumn{{ID: "id", Name: "id"}}
	combine := ConstructionCombine{
		Kind: ConstructionCombineMembership, MembershipMode: ConstructionCombineExcludeMatches,
		Keys:        []ConstructionCombineKey{{LeftColumnID: "subject", RightColumnID: "member"}},
		Projections: []ConstructionCombineProjection{{OutputColumnID: "id", InputIndex: 0, InputColumnID: "subject"}},
	}
	if err := combine.Validate(2, outputs); err != nil {
		t.Fatalf("valid membership: %v", err)
	}
	combine.Projections[0].InputIndex = 1
	if err := combine.Validate(2, outputs); err == nil || !strings.Contains(err.Error(), "preserve the left input") {
		t.Fatalf("membership projection error = %v", err)
	}
}
