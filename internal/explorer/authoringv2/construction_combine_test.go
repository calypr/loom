package authoringv2

import (
	"reflect"
	"strings"
	"testing"
)

func terminalCombineDocument() Document {
	document := workspaceDocument("training")
	document.Columns = nil
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID: "join_labs",
		Inputs: []ConstructionInputRef{
			{Kind: ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
			{Kind: ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
		},
		Operation: ConstructionOperation{Kind: ConstructionOperationCombine, Combine: &ConstructionCombine{
			Kind: ConstructionCombineKeyJoin,
			Keys: []ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
			Projections: []ConstructionCombineProjection{
				{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
				{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
			},
			JoinType: ConstructionCombineLeftJoin, RightMatchPolicy: ConstructionCombinePreserveAllMatches,
		}},
		Outputs: []StageColumn{
			{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
			{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
		},
	}}}
	return document
}

func TestConstructionCombineValidatesAndRoundTripsExactPins(t *testing.T) {
	document := terminalCombineDocument()
	if err := document.Validate(); err != nil {
		t.Fatalf("valid terminal combine rejected: %v", err)
	}

	workspace := constructionWorkspace(document)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical workspace: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	got := decoded.Documents[0].Construction
	if !reflect.DeepEqual(got, document.Construction) {
		t.Fatalf("combine changed during save/reload:\n got: %#v\nwant: %#v", got, document.Construction)
	}
	if got.Steps[0].Inputs[0].RevisionID != "patients-r4" || got.Steps[0].Inputs[1].RevisionID != "labs-r9" {
		t.Fatalf("table input revisions changed or reordered: %#v", got.Steps[0].Inputs)
	}
}

func TestConstructionCombineValidatesAppendAndMembershipModes(t *testing.T) {
	document := terminalCombineDocument()
	step := document.Construction.Steps[0]
	step.Outputs = []StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"}}
	step.Inputs = append(step.Inputs, ConstructionInputRef{
		Kind: ConstructionInputTableRevision, TableID: "project:1:archive", RevisionID: "archive-r2", OutputID: "archive",
	})
	step.Operation.Combine = &ConstructionCombine{Kind: ConstructionCombineAppend, Projections: []ConstructionCombineProjection{
		{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "person_id"},
		{OutputColumnID: "patient_id", InputIndex: 1, InputColumnID: "patient_id"},
		{OutputColumnID: "patient_id", InputIndex: 2, InputColumnID: "subject_id"},
	}}
	document.Construction.Steps = []ConstructionStep{step}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid three-input append rejected: %v", err)
	}

	step.Inputs = step.Inputs[:2]
	step.Operation.Combine = &ConstructionCombine{Kind: ConstructionCombineMembership,
		Keys:           []ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
		Projections:    []ConstructionCombineProjection{{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "person_id"}},
		MembershipMode: ConstructionCombineIncludeMatches,
	}
	document.Construction.Steps = []ConstructionStep{step}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid membership operation rejected: %v", err)
	}
}

func TestConstructionCombineRejectsSourceStagesInvalidPinsAndNullability(t *testing.T) {
	t.Run("source columns", func(t *testing.T) {
		document := terminalCombineDocument()
		document.Columns = []Column{constructionSourceColumn("source_id", "source_id", "Source", "string")}
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "source projection columns") {
			t.Fatalf("source columns error = %v", err)
		}
	})
	t.Run("additional operation", func(t *testing.T) {
		document := terminalCombineDocument()
		document.Construction.Steps = append(document.Construction.Steps, ConstructionStep{ID: "filter_after_join"})
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "only construction step") {
			t.Fatalf("additional step error = %v", err)
		}
	})
	t.Run("floating input", func(t *testing.T) {
		document := terminalCombineDocument()
		document.Construction.Steps[0].Inputs[1] = ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: "other"}
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "TABLE_REVISION") {
			t.Fatalf("non-pinned input error = %v", err)
		}
	})
	t.Run("left output nullability", func(t *testing.T) {
		document := terminalCombineDocument()
		document.Construction.Steps[0].Outputs[1].Nullable = false
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "must be nullable") {
			t.Fatalf("left join nullability error = %v", err)
		}
	})
}

func TestConstructionCombineStepEditKeepsExactInputPinsWithoutStageDependencies(t *testing.T) {
	accepted := terminalCombineDocument()
	replacement := accepted.Construction.Steps[0]
	replacement.Operation.Combine = &ConstructionCombine{
		Kind: ConstructionCombineKeyJoin,
		Keys: []ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
			{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
		},
		JoinType: ConstructionCombineInnerJoin, RightMatchPolicy: ConstructionCombinePreserveAllMatches,
	}
	candidate, impact, err := accepted.AnalyzeStepEdit(replacement)
	if err != nil {
		t.Fatalf("edit combine step: %v", err)
	}
	if len(impact.AffectedStepIDs) != 0 || impact.HasMissingInputs() {
		t.Fatalf("external pinned inputs were reported as stage dependencies: %#v", impact)
	}
	if !reflect.DeepEqual(candidate.Construction.Steps[0].Inputs, accepted.Construction.Steps[0].Inputs) {
		t.Fatalf("editing operation changed pinned inputs: %#v", candidate.Construction.Steps[0].Inputs)
	}
	if candidate.Construction.Steps[0].Operation.Combine.JoinType != ConstructionCombineInnerJoin {
		t.Fatal("candidate did not retain the selected Combine edit")
	}
	if accepted.Construction.Steps[0].Operation.Combine.JoinType != ConstructionCombineLeftJoin {
		t.Fatal("editing the candidate mutated the accepted construction")
	}
}

func TestConstructionCombineCandidateCanAppendEditAndRemoveStandaloneStage(t *testing.T) {
	base := workspaceDocument("training")
	base.Columns = nil
	base.Construction = &Construction{Version: ConstructionVersion}
	candidateConstruction := *terminalCombineDocument().Construction

	added, impact, err := base.AnalyzeConstructionCandidate(candidateConstruction, "join_labs", nil)
	if err != nil {
		t.Fatalf("append terminal Combine: %v", err)
	}
	if len(impact.MissingInputs) != 0 || len(added.Construction.Steps) != 1 {
		t.Fatalf("appended Combine proposal = %#v, impact=%#v", added.Construction, impact)
	}

	edited := *added.Construction
	edited.Steps = append([]ConstructionStep(nil), edited.Steps...)
	edited.Steps[0].Operation.Combine = &ConstructionCombine{
		Kind: ConstructionCombineKeyJoin,
		Keys: []ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
		Projections: []ConstructionCombineProjection{
			{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
			{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
		},
		JoinType: ConstructionCombineInnerJoin, RightMatchPolicy: ConstructionCombinePreserveAllMatches,
	}
	updated, _, err := added.AnalyzeConstructionCandidate(edited, "join_labs", nil)
	if err != nil {
		t.Fatalf("edit terminal Combine candidate: %v", err)
	}
	if updated.Construction.Steps[0].Operation.Combine.JoinType != ConstructionCombineInnerJoin {
		t.Fatalf("edited candidate join type = %q", updated.Construction.Steps[0].Operation.Combine.JoinType)
	}
	if added.Construction.Steps[0].Operation.Combine.JoinType != ConstructionCombineLeftJoin {
		t.Fatal("candidate edit mutated the accepted Combine")
	}

	removed, impact, err := updated.AnalyzeConstructionCandidate(Construction{Version: ConstructionVersion}, "", []string{"join_labs"})
	if err != nil {
		t.Fatalf("remove terminal Combine: %v", err)
	}
	if len(removed.Construction.Steps) != 0 || !reflect.DeepEqual(impact.RemovedStepIDs, []string{"join_labs"}) {
		t.Fatalf("removed construction=%#v impact=%#v", removed.Construction, impact)
	}
}
