package recipe

import (
	"encoding/json"
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
	if err := combine.Validate(2, outputs); err == nil || !strings.Contains(err.Error(), "must be nullable") {
		t.Fatalf("incomplete non-nullable append mapping error = %v", err)
	}
}

func TestConstructionCombineAppendAllowsNullableMissingInputsButRejectsEmptyAndNonNullableOutputs(t *testing.T) {
	outputs := []StageColumn{
		{ID: "id", Name: "id", Type: "string"},
		{ID: "value", Name: "value", Type: "integer", Nullable: true},
	}
	combine := ConstructionCombine{
		Kind: ConstructionCombineAppend,
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "id", InputIndex: 0, InputColumnID: "left-id"},
			{OutputColumnID: "id", InputIndex: 1, InputColumnID: "right-id"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "right-value"},
		},
	}
	if err := combine.Validate(2, outputs); err != nil {
		t.Fatalf("valid append with an omitted nullable input mapping: %v", err)
	}

	nonNullable := append([]StageColumn(nil), outputs...)
	nonNullable[1].Nullable = false
	if err := combine.Validate(2, nonNullable); err == nil || !strings.Contains(err.Error(), "must be nullable") {
		t.Fatalf("missing-input non-nullable output error = %v", err)
	}

	allEmpty := combine
	allEmpty.Projections = combine.Projections[:2]
	if err := allEmpty.Validate(2, outputs); err == nil || !strings.Contains(err.Error(), "not mapped from any input") {
		t.Fatalf("all-empty output error = %v", err)
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

func TestConstructionAcceptsOnlyStandaloneExactRevisionCombine(t *testing.T) {
	combine := ConstructionCombine{
		Kind: ConstructionCombineKeyJoin, JoinType: ConstructionCombineLeftJoin,
		RightMatchPolicy: ConstructionCombinePreserveAllMatches,
		Keys:             []ConstructionCombineKey{{LeftColumnID: "left_id", RightColumnID: "right_id"}},
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "left", InputIndex: 0, InputColumnID: "left_id"},
			{OutputColumnID: "right", InputIndex: 1, InputColumnID: "right_label"},
		},
	}
	construction := Construction{
		Version: 1,
		Steps: []ConstructionStep{{
			ID: "join",
			Inputs: []ConstructionInputRef{
				{Kind: ConstructionTableRevisionInput, TableID: "source:1:left", RevisionID: "execution-left", OutputID: "left"},
				{Kind: ConstructionTableRevisionInput, TableID: "source:1:right", RevisionID: "execution-right", OutputID: "right"},
			},
			Operation: ConstructionOperation{Kind: ConstructionCombineOp, Combine: &combine},
			Outputs: []StageColumn{
				{ID: "left", Name: "left_value", Type: "integer"},
				{ID: "right", Name: "right_value", Type: "string", Nullable: true},
			},
		}},
	}
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("valid terminal combine: %v", err)
	}

	badRevision := construction
	badRevision.Steps = append([]ConstructionStep(nil), construction.Steps...)
	badRevision.Steps[0].Inputs = append([]ConstructionInputRef(nil), construction.Steps[0].Inputs...)
	badRevision.Steps[0].Inputs[1].RevisionID = " "
	if err := badRevision.Validate(nil); err == nil || !strings.Contains(err.Error(), "revisionId") {
		t.Fatalf("untrimmed revision error = %v", err)
	}

	badNullability := construction
	badNullability.Steps = append([]ConstructionStep(nil), construction.Steps...)
	badNullability.Steps[0].Outputs = append([]StageColumn(nil), construction.Steps[0].Outputs...)
	badNullability.Steps[0].Outputs[1].Nullable = false
	if err := badNullability.Validate(nil); err == nil || !strings.Contains(err.Error(), "must be nullable") {
		t.Fatalf("left-join nullability error = %v", err)
	}

	withSource := construction
	withSource.Steps = append([]ConstructionStep(nil), construction.Steps...)
	if err := withSource.Validate([]Field{{Name: "ignored"}}); err == nil || !strings.Contains(err.Error(), "source projection") {
		t.Fatalf("source prefix error = %v", err)
	}
}

func TestConstructionCombineRecipeJSONRoundTripsExactRefsAndPayload(t *testing.T) {
	combine := ConstructionCombine{
		Kind: ConstructionCombineAppend,
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "person", InputIndex: 0, InputColumnID: "left-person"},
			{OutputColumnID: "person", InputIndex: 1, InputColumnID: "right-person"},
		},
	}
	bundle := Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion, Name: "combined", TranslationVersion: "1",
		Outputs: []Output{{
			Name: "joined", RootResourceType: "Patient", RowGrain: "patient",
			Construction: &Construction{Version: 1, Steps: []ConstructionStep{{
				ID: "append", Inputs: []ConstructionInputRef{
					{Kind: ConstructionTableRevisionInput, TableID: "left:1:people", RevisionID: "execution-left", OutputID: "people"},
					{Kind: ConstructionTableRevisionInput, TableID: "right:1:people", RevisionID: "execution-right", OutputID: "people"},
				},
				Operation: ConstructionOperation{Kind: ConstructionCombineOp, Combine: &combine},
				Outputs:   []StageColumn{{ID: "person", Name: "person_id", Type: "string"}},
			}}},
		}},
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"kind":"TABLE_REVISION"`, `"tableId":"left:1:people"`, `"revisionId":"execution-left"`, `"outputId":"people"`, `"kind":"COMBINE"`, `"inputColumnId":"right-person"`} {
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("Combine recipe JSON is missing %s: %s", expected, encoded)
		}
	}
	var decoded Bundle
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-tripped Combine bundle is invalid: %v", err)
	}
}
