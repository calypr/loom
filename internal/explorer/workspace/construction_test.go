package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
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

func TestConstructionGroupLeavesJSONMatchRecipe(t *testing.T) {
	cases := []struct {
		name      string
		workspace any
		recipe    any
	}{
		{
			name:      "aggregate",
			workspace: ConstructionGroupAggregate{Operation: ConstructionGroupSum, InputColumnID: "amount", OutputColumnID: "total"},
			recipe:    recipe.ConstructionGroupAggregate{Operation: recipe.ConstructionGroupSum, InputColumnID: "amount", OutputColumnID: "total"},
		},
		{
			name:      "current stage source",
			workspace: ConstructionGroupFieldCurrentStage{SourceRole: "ROOT", OccurrenceID: "root", OwnerResourceType: "Patient", ResourceType: "Patient", CanonicalPath: "id"},
			recipe:    recipe.ConstructionGroupCurrentStageFieldSource{SourceRole: "ROOT", OccurrenceID: "root", OwnerResourceType: "Patient", ResourceType: "Patient", CanonicalPath: "id"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			workspaceJSON, err := json.Marshal(test.workspace)
			if err != nil {
				t.Fatalf("marshal workspace value: %v", err)
			}
			recipeJSON, err := json.Marshal(test.recipe)
			if err != nil {
				t.Fatalf("marshal recipe value: %v", err)
			}
			if !bytes.Equal(workspaceJSON, recipeJSON) {
				t.Fatalf("JSON differs: workspace=%s recipe=%s", workspaceJSON, recipeJSON)
			}
		})
	}
}

func TestConstructionCombineJSONMatchesRecipe(t *testing.T) {
	combine := ConstructionCombine{
		Kind:             ConstructionCombineKeyJoin,
		Keys:             []ConstructionCombineKey{{LeftColumnID: "patient", RightColumnID: "subject"}},
		Projections:      []ConstructionCombineProjection{{OutputColumnID: "patient", InputIndex: 0, InputColumnID: "patient"}},
		JoinType:         ConstructionCombineLeftJoin,
		RightMatchPolicy: ConstructionCombinePreserveAllMatches,
	}
	workspaceJSON, err := json.Marshal(combine)
	if err != nil {
		t.Fatalf("marshal workspace combine: %v", err)
	}
	recipeJSON, err := json.Marshal(recipe.ConstructionCombine(combine))
	if err != nil {
		t.Fatalf("marshal recipe combine: %v", err)
	}
	if !bytes.Equal(workspaceJSON, recipeJSON) {
		t.Fatalf("combine JSON differs: workspace=%s recipe=%s", workspaceJSON, recipeJSON)
	}
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

func TestConstructionCombineAuthoringAllowsNullableSparseAppendMappings(t *testing.T) {
	newAppendDocument := func() Document {
		document := terminalCombineDocument()
		step := document.Construction.Steps[0]
		step.Inputs = append(step.Inputs, ConstructionInputRef{
			Kind: ConstructionInputTableRevision, TableID: "project:1:archive", RevisionID: "archive-r2", OutputID: "archive",
		})
		step.Outputs = []StageColumn{
			{ID: "record_id", Name: "record_id", Label: "Record ID", Type: "string"},
			{ID: "status", Name: "status", Label: "Status", Type: "string", Nullable: true},
			{ID: "patient_gender", Name: "patient_gender", Label: "Patient gender", Type: "string", Nullable: true},
		}
		step.Operation.Combine = &ConstructionCombine{Kind: ConstructionCombineAppend, Projections: []ConstructionCombineProjection{
			{OutputColumnID: "record_id", InputIndex: 0, InputColumnID: "observation-id"},
			{OutputColumnID: "record_id", InputIndex: 1, InputColumnID: "report-id"},
			{OutputColumnID: "record_id", InputIndex: 2, InputColumnID: "patient-id"},
			{OutputColumnID: "status", InputIndex: 0, InputColumnID: "observation-status"},
			{OutputColumnID: "status", InputIndex: 2, InputColumnID: "patient-status"},
			{OutputColumnID: "patient_gender", InputIndex: 2, InputColumnID: "gender"},
		}}
		document.Construction.Steps = []ConstructionStep{step}
		return document
	}

	t.Run("sparse mappings validate", func(t *testing.T) {
		if err := newAppendDocument().Validate(); err != nil {
			t.Fatalf("nullable three-input APPEND with omitted mappings rejected: %v", err)
		}
	})
	t.Run("all-empty output rejected", func(t *testing.T) {
		document := newAppendDocument()
		step := document.Construction.Steps[0]
		step.Operation.Combine.Projections = step.Operation.Combine.Projections[:5]
		document.Construction.Steps[0] = step
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "not mapped from any input") {
			t.Fatalf("all-empty APPEND output error = %v", err)
		}
	})
	t.Run("missing mapping requires nullable output", func(t *testing.T) {
		document := newAppendDocument()
		step := document.Construction.Steps[0]
		step.Outputs[1].Nullable = false
		document.Construction.Steps[0] = step
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "must be nullable") {
			t.Fatalf("missing-input non-nullable APPEND output error = %v", err)
		}
	})
	t.Run("missing mapping requires scalar output", func(t *testing.T) {
		document := newAppendDocument()
		step := document.Construction.Steps[0]
		step.Outputs[1].Type = "array"
		document.Construction.Steps[0] = step
		if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "concrete scalar type") {
			t.Fatalf("missing-input non-scalar APPEND output error = %v", err)
		}
	})
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
	base.Columns = []Column{}
	base.Construction = &Construction{Version: ConstructionVersion}
	candidateConstruction := *terminalCombineDocument().Construction

	added, impact, err := base.AnalyzeConstructionCandidate(candidateConstruction, "join_labs", nil)
	if err != nil {
		t.Fatalf("append terminal Combine: %v", err)
	}
	if len(impact.MissingInputs) != 0 || len(added.Construction.Steps) != 1 {
		t.Fatalf("appended Combine proposal = %#v, impact=%#v", added.Construction, impact)
	}
	if added.Columns == nil {
		t.Fatal("appending a construction step collapsed an explicitly empty source column list to nil")
	}
	legacyBase := workspaceDocument("training")
	legacyBase.Columns = []Column{}
	legacyAdded, _, err := legacyBase.AnalyzeConstructionCandidate(candidateConstruction, "join_labs", nil)
	if err != nil {
		t.Fatalf("append Combine from an empty legacy table: %v", err)
	}
	if legacyAdded.Columns == nil {
		t.Fatal("upgrading and appending to an explicitly empty source column list collapsed it to nil")
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

func TestConstructionGroupKeyRequiresExactlyOneSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "legacy public column", body: `{"inputColumnId":"site","outputColumnId":"site_group"}`, want: true},
		{name: "field choice", body: `{"fieldSource":{"kind":"CHOICE","choiceId":"signed-choice"},"outputColumnId":"status_group"}`, want: true},
		{name: "legacy semantic choice", body: `{"semanticCategory":{"kind":"CHOICE","choiceId":"semantic-choice"},"outputColumnId":"category_group"}`, want: true},
		{name: "missing source", body: `{"outputColumnId":"group"}`},
		{name: "two sources", body: `{"inputColumnId":"site","semanticCategory":{"kind":"CHOICE","choiceId":"semantic-choice"},"outputColumnId":"group"}`},
		{name: "unknown sibling source", body: `{"inputColumnId":"site","fieldSource":{"kind":"CHOICE","choiceId":"signed-choice"},"outputColumnId":"group"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var key ConstructionGroupKey
			err := json.Unmarshal([]byte(test.body), &key)
			if (err == nil) != test.want {
				t.Fatalf("json.Unmarshal() error = %v, want success %t", err, test.want)
			}
		})
	}
}

func TestConstructionGroupFieldSourceBindingVariantsAreStrict(t *testing.T) {
	t.Parallel()

	validRootScalar := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}`
	validAncestorScalar := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]"}`
	validCoding := `{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`
	for _, body := range []string{validRootScalar, validAncestorScalar, validCoding} {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(body), &source); err != nil {
			t.Fatalf("decode valid BINDING %s: %v", body, err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate valid BINDING %s: %v", body, err)
		}
	}

	invalid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"fake-candidate","canonicalPath":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"ROOT","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-b","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]","boundarySelector":"valueCodeableConcept.coding[]"}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-a","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]","arbitraryPath":"Observation.secret"}`,
	}
	for _, body := range invalid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(body), &source); err == nil {
			if err := validateConstructionGroupFieldSource(source); err == nil {
				t.Errorf("accepted invalid BINDING %s", body)
			}
		}
	}
}

func TestConstructionGroupFieldSourcePreservesLegacyCurrentStageWire(t *testing.T) {
	t.Parallel()

	legacy := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-ancestor","sourceRole":"ANCESTOR","occurrenceId":"observation","resourceType":"ObservationComponent","ownerOccurrenceId":"observation","ownerResourceType":"Observation","candidateId":"component-value","canonicalPath":"valueQuantity.value","boundarySelector":"component[]"}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-coding","sourceRole":"CURRENT_ITEM","occurrenceId":"observation","resourceType":"Coding","ownerOccurrenceId":"observation","ownerResourceType":"Observation","canonicalPath":"valueCodeableConcept.coding[]"}`,
	}
	for _, raw := range legacy {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err != nil {
			t.Fatalf("decode legacy BINDING: %v", err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate legacy BINDING: %v", err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal legacy BINDING: %v", err)
		}
		if string(encoded) != raw {
			t.Fatalf("legacy BINDING wire changed:\n got %s\nwant %s", encoded, raw)
		}
	}
}

func TestConstructionGroupFieldSourceUsesExplicitInputSum(t *testing.T) {
	t.Parallel()

	valid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-root","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-related","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status-candidate","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape-related-coding","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Coding","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT","codingSelector":"valueCodeableConcept.coding[]","codingEmptyPolicy":"GROUP","ordinalSemantics":"ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"}}}`,
	}
	for _, raw := range valid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err != nil {
			t.Fatalf("decode explicit input BINDING: %v", err)
		}
		if err := validateConstructionGroupFieldSource(source); err != nil {
			t.Fatalf("validate explicit input BINDING: %v", err)
		}
		encoded, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("marshal explicit input BINDING: %v", err)
		}
		if string(encoded) != raw {
			t.Fatalf("input BINDING wire changed:\n got %s\nwant %s", encoded, raw)
		}
	}

	invalid := []string{
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"},"relatedOccurrence":{"anchorOccurrenceId":"root"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","untrustedPath":"Observation.secret"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","sourceRole":"ROOT","occurrenceId":"root","resourceType":"Observation","ownerOccurrenceId":"root","ownerResourceType":"Observation","candidateId":"status","canonicalPath":"status","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"different-occurrence","toNodeId":"node-other","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status","emptyPolicy":"PRESERVE_PARENT","multiplicity":"ALL_TARGETS_PER_INPUT"}}}`,
		`{"kind":"BINDING","valueKind":"CODING_IDENTITY","sourceStageId":"source_projection","sourceShapeId":"shape","input":{"kind":"RELATED_OCCURRENCE","relatedOccurrence":{"anchorOccurrenceId":"root","anchorNodeId":"node-root","anchorResourceType":"Patient","route":[{"stepIndex":0,"fromOccurrenceId":"root","fromNodeId":"node-root","fromResourceType":"Patient","edgeId":"edge-subject","relationship":"subject","storageDirection":"INBOUND","matchMode":"OPTIONAL","toOccurrenceId":"observation-occurrence","toNodeId":"node-observation","toResourceType":"Observation"}],"targetOccurrenceId":"observation-occurrence","targetNodeId":"node-observation","targetResourceType":"Observation","resourceType":"Coding","emptyPolicy":"PRESERVE_PARENT","multiplicity":"DISTINCT_TARGET_PER_INPUT","codingSelector":"valueCodeableConcept.coding[]","ordinalSemantics":"ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"}}}`,
	}
	for _, raw := range invalid {
		var source ConstructionGroupFieldSource
		if err := json.Unmarshal([]byte(raw), &source); err == nil {
			if err := validateConstructionGroupFieldSource(source); err == nil {
				t.Errorf("accepted invalid input BINDING %s", raw)
			}
		}
	}
}

func TestConstructionGroupFieldSourceChoiceCannotCarryInput(t *testing.T) {
	t.Parallel()

	var source ConstructionGroupFieldSource
	err := json.Unmarshal([]byte(`{"kind":"CHOICE","choiceId":"signed","input":{"kind":"CURRENT_STAGE","currentStage":{}}}`), &source)
	if err == nil || !strings.Contains(err.Error(), "CHOICE requires only choiceId") {
		t.Fatalf("decode CHOICE with binding input error = %v", err)
	}
}

func TestConstructionGroupFieldSourceNestedCurrentStageValidateCloneJSONCycle(t *testing.T) {
	t.Parallel()

	raw := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"stage-a","sourceShapeId":"shape-a","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active"}}}`
	var original ConstructionGroupFieldSource
	if err := json.Unmarshal([]byte(raw), &original); err != nil {
		t.Fatalf("decode nested current-stage BINDING: %v", err)
	}
	if err := original.Validate(); err != nil {
		t.Fatalf("validate nested current-stage BINDING: %v", err)
	}

	construction := &Construction{Version: 1, Steps: []ConstructionStep{{
		ID:     "group-step",
		Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "construction-a",
			Keys:           []ConstructionGroupKey{{OutputColumnID: "active", FieldSource: &original}},
		}},
		Outputs: []StageColumn{{ID: "active", Name: "Active", Type: "boolean"}},
	}}}
	cloned, err := cloneConstruction(construction)
	if err != nil {
		t.Fatalf("clone construction: %v", err)
	}
	encoded, err := json.Marshal(cloned)
	if err != nil {
		t.Fatalf("marshal cloned construction: %v", err)
	}
	var roundTripped Construction
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal cloned construction: %v", err)
	}
	got := roundTripped.Steps[0].Operation.Group.Keys[0].FieldSource
	if got == nil || got.Input == nil || got.Input.Kind != "CURRENT_STAGE" || got.Input.CurrentStage == nil {
		t.Fatalf("nested CURRENT_STAGE provenance lost through clone/JSON cycle: %#v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate round-tripped nested CURRENT_STAGE BINDING: %v", err)
	}
}

func TestConstructionGroupFieldSourceRejectsNestedInputWithLegacyFlatFields(t *testing.T) {
	t.Parallel()

	var source ConstructionGroupFieldSource
	err := json.Unmarshal([]byte(`{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"stage-a","sourceShapeId":"shape-a","sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"root","ownerResourceType":"Patient","resourceType":"Patient","candidateId":"status-candidate","canonicalPath":"active"}}}`), &source)
	if err == nil || !strings.Contains(err.Error(), "cannot combine input with legacy flat fields") {
		t.Fatalf("decode mixed nested and flat BINDING error = %v", err)
	}
}

func TestConstructionRowValueHasOneExactLegacyOrScalarFieldSource(t *testing.T) {
	t.Parallel()

	var value ConstructionRowValue
	if err := json.Unmarshal([]byte(`{"fieldSource":{"kind":"CHOICE","choiceId":"source-choice"},"outputColumnId":"tags","policy":"ALL"}`), &value); err != nil {
		t.Fatalf("decode scalar Group row-value CHOICE: %v", err)
	}
	if value.InputColumnID != "" || value.FieldSource == nil || value.FieldSource.Kind != "CHOICE" || value.OutputColumnID != "tags" {
		t.Fatalf("decoded field-source row value = %#v", value)
	}
	step := ConstructionStep{
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{}},
		RowValues: []ConstructionRowValue{value},
		Outputs:   []StageColumn{{ID: "tags", Name: "tags", Type: "array"}},
	}
	if err := validateConstructionRowValues(step, map[string]StageColumn{}); err != nil {
		t.Fatalf("validate scalar Group row-value CHOICE: %v", err)
	}

	for _, raw := range []string{
		`{"outputColumnId":"tags","policy":"ALL"}`,
		`{"inputColumnId":"input","fieldSource":{"kind":"CHOICE","choiceId":"source-choice"},"outputColumnId":"tags","policy":"ALL"}`,
		`{"inputColumnId":"input","outputColumnId":"tags","policy":"ALL","arbitraryPath":"Observation.secret"}`,
	} {
		var invalid ConstructionRowValue
		if err := json.Unmarshal([]byte(raw), &invalid); err == nil {
			t.Errorf("accepted invalid row-value source shape %s", raw)
		}
	}

	step.RowValues[0].FieldSource = &ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: "source_projection", SourceShapeID: "shape",
		SourceRole: "CURRENT_ITEM", OccurrenceID: "observation", ResourceType: "Coding", OwnerOccurrenceID: "observation",
		OwnerResourceType: "Observation", CanonicalPath: "valueCodeableConcept.coding[]",
	}
	if err := validateConstructionRowValues(step, map[string]StageColumn{}); err == nil {
		t.Fatal("accepted CODING_IDENTITY as a row-value scalar")
	}
}

func TestRelatedExpandContributorQuantifierMatchesSourceCardinality(t *testing.T) {
	repeatedSource := ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-code",
		NodeID: "observation", ResourceType: "Observation", Path: "category[].coding[].code",
		Cardinality: "many", LogicalType: "code",
		RepeatedBoundaries: []capability.RepeatedBoundary{
			{Path: "category[]", MaxItems: 2},
			{Path: "category[].coding[]", MaxItems: 4},
		},
	}
	code := &ContributorValue{Kind: ContributorValueCode, Code: &ContributorCode{Code: "d"}}
	makeStep := func(source ConstructionRelatedFieldSource, predicate *ContributorPredicate) ConstructionStep {
		input := []StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
		outputs := append(append([]StageColumn(nil), input...), StageColumn{
			ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true,
		})
		return ConstructionStep{
			ID: "expand_observations", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
				AnchorColumnID: "_key", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
				Route: []capability.ConstructionRouteStep{{
					EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
					FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
					StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				}},
				ContributorRule:   ConstructionRelatedContributorRule{Policy: ConstructionRelatedAllMatches, Predicate: predicate},
				ContributorSource: &source, ContributorChoiceID: "field-choice",
				EmptyPolicy: ConstructionExpandEmptyPreserveParent, RelatedRecordColumnID: "observation-id",
			}},
			Outputs: outputs,
		}
	}
	inputColumns := []StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}}
	input := map[string]StageColumn{"patient-id": inputColumns[0]}

	for _, test := range []struct {
		name      string
		predicate *ContributorPredicate
		wantError string
	}{
		{
			name: "repeated EQUALS requires ANY and code literal",
			predicate: &ContributorPredicate{
				CandidateID: "observation-code", Operator: ContributorEquals, Quantifier: ContributorAny, Value: code,
			},
		},
		{
			name:      "repeated EXISTS requires ANY",
			predicate: &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny},
		},
		{
			name:      "repeated predicate cannot omit quantifier",
			predicate: &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists},
			wantError: "requires the ANY quantifier",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			step := makeStep(repeatedSource, test.predicate)
			err := validateConstructionRelatedExpand(step, input, inputColumns)
			if test.wantError == "" && err != nil {
				t.Fatalf("valid repeated contributor rejected: %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
		})
	}
	eligibilityStep := ConstructionStep{
		ID: "keep_patients", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationRelatedEligibility, RelatedEligibility: &ConstructionRelatedEligibility{
			AnchorColumnID: "patient-id", ChoiceID: "route-choice", TargetNodeID: "observation", TargetResourceType: "Observation",
			Route: []capability.ConstructionRouteStep{{
				EdgeID: "subject-patient", FromNodeID: "patient", ToNodeID: "observation",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorRule: ConstructionRelatedContributorRule{Policy: ConstructionRelatedAllMatches, Predicate: &ContributorPredicate{
				CandidateID: "observation-code", Operator: ContributorEquals, Quantifier: ContributorAny, Value: code,
			}},
			ContributorSource: &repeatedSource, ContributorChoiceID: "field-choice",
			Match: ConstructionRelatedEligibilityMatch{Kind: ConstructionRelatedEligibilityExists},
		}},
		Outputs: inputColumns,
	}
	if err := validateConstructionRelatedEligibility(eligibilityStep, inputColumns); err == nil || !strings.Contains(err.Error(), "one exact scalar field") {
		t.Fatalf("related eligibility accepted repeated contributor source: %v", err)
	}
	scalarEligibility := *eligibilityStep.Operation.RelatedEligibility
	eligibilitySource := repeatedSource
	eligibilitySource.Path, eligibilitySource.Cardinality, eligibilitySource.LogicalType = "status", "optional_one", "code"
	eligibilitySource.RepeatedBoundaries = nil
	scalarEligibility.ContributorSource = &eligibilitySource
	scalarEligibility.ContributorRule.Predicate = &ContributorPredicate{CandidateID: "observation-code", Operator: ContributorExists}
	eligibilityStep.Operation.RelatedEligibility = &scalarEligibility
	if err := validateConstructionRelatedEligibility(eligibilityStep, inputColumns); err != nil {
		t.Fatalf("existing scalar eligibility contributor rejected: %v", err)
	}

	scalarSource := repeatedSource
	scalarSource.Path, scalarSource.Cardinality, scalarSource.LogicalType = "status", "optional_one", "code"
	scalarSource.RepeatedBoundaries = nil
	if err := validateConstructionRelatedExpand(makeStep(scalarSource, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists,
	}), input, inputColumns); err != nil {
		t.Fatalf("scalar contributor without quantifier rejected: %v", err)
	}
	if err := validateConstructionRelatedExpand(makeStep(scalarSource, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny,
	}), input, inputColumns); err == nil || !strings.Contains(err.Error(), "scalar contributor predicate must not specify a quantifier") {
		t.Fatalf("scalar contributor ANY error = %v", err)
	}

	badBoundaries := repeatedSource
	badBoundaries.RepeatedBoundaries = append([]capability.RepeatedBoundary(nil), repeatedSource.RepeatedBoundaries...)
	badBoundaries.RepeatedBoundaries[1].Path = "category[].code[]"
	if err := validateConstructionRelatedExpand(makeStep(badBoundaries, &ContributorPredicate{
		CandidateID: "observation-code", Operator: ContributorExists, Quantifier: ContributorAny,
	}), input, inputColumns); err == nil || !strings.Contains(err.Error(), "boundaries must match") {
		t.Fatalf("mismatched repeated boundary error = %v", err)
	}
}

func codedGroupDocument() Document {
	document := workspaceDocument("observations")
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns = []Column{
		constructionSourceColumn("observation_id", "observation_id", "Observation ID", "string"),
		constructionSourceColumn("site_id", "site", "Site", "string"),
	}
	family := capability.SemanticFrameFamily{
		BindingID: "observation-type-binding", ResourceType: "Observation", SourcePath: "type.coding",
		OwningScope: "Observation", KeyPath: "type.coding", ValuePath: "type", LogicalType: "string",
		RuleVersion: "1", SchemaVersion: 1,
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID: "coded_input", OwnerStepID: "group_by_type",
			Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationCodedPivot, CodedPivot: &ConstructionCodedPivot{
				ConstructionID:  "coded_input",
				Source:          &ConstructionCodedPivotSource{Family: family, CandidateID: "observation-candidate", NodeID: "observation-node", FieldPath: "type.coding"},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "specimen_type", OutputColumnID: "specimen_source"}},
				DuplicatePolicy: ConstructionPivotDuplicateError, MissingCellPolicy: ConstructionPivotMissingNull,
			}},
			RowValues: []ConstructionRowValue{{InputColumnID: "site_id", OutputColumnID: "site_input", Policy: ConstructionRowValueOne}},
			Outputs: []StageColumn{
				{ID: "specimen_source", Name: "specimen_type", Label: "Specimen type", Type: "string"},
				{ID: "site_input", Name: "site", Label: "Site", Type: "string"},
			},
		},
		{
			ID: "group_by_type", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "coded_input"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
				ConstructionID: "group_by_type", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
				Keys: []ConstructionGroupKey{
					{InputColumnID: "specimen_source", OutputColumnID: "specimen_group"},
					{InputColumnID: "site_input", OutputColumnID: "site_group"},
				},
				Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "records"}},
			}},
			Outputs: []StageColumn{
				{ID: "specimen_group", Name: "specimen_type", Label: "Specimen type", Type: "string"},
				{ID: "site_group", Name: "site", Label: "Site", Type: "string"},
				{ID: "records", Name: "records", Label: "Records", Type: "integer"},
			},
		},
	}}
	return document
}

func TestGroupOwnedCodedPivotAcceptsExactOneInputsAndRecalculatesOwnedEdit(t *testing.T) {
	accepted := codedGroupDocument()
	if err := accepted.Validate(); err != nil {
		t.Fatalf("valid owned CODED_PIVOT to GROUP chain rejected: %v", err)
	}
	candidate, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps[0].Operation.CodedPivot.Categories[0].Code = "specimen_type_v2"
	replaced, impact, err := accepted.AnalyzeConstructionCandidate(*candidate, "group_by_type", nil)
	if err != nil {
		t.Fatalf("edit owned coded selection: %v", err)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{"coded_input", "group_by_type"}) {
		t.Fatalf("owned helper edit did not recalculate from its root: %#v", impact)
	}
	if err := replaced.Validate(); err != nil {
		t.Fatalf("edited compound construction invalid: %v", err)
	}
	if replaced.Construction.Steps[0].ID != "coded_input" || replaced.Construction.Steps[0].RowValues[0].OutputColumnID != "site_input" {
		t.Fatalf("edit changed stable owned helper identity: %#v", replaced.Construction.Steps[0])
	}
}

func TestGroupOwnedCodedPivotRejectsAmbiguousPassthroughPolicy(t *testing.T) {
	document := codedGroupDocument()
	document.Construction.Steps[0].RowValues[0].Policy = ConstructionRowValueAll
	if err := document.Validate(); err == nil {
		t.Fatal("GROUP-owned coded helper accepted a multi-value passthrough")
	}
}

func TestGroupEditRemovingCodedKeyPrunesOwnedHelper(t *testing.T) {
	accepted := codedGroupDocument()
	candidate, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps = []ConstructionStep{{
		ID: "group_by_type", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "group_by_type", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys:       []ConstructionGroupKey{{InputColumnID: "site_id", OutputColumnID: "site_group"}},
			Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "records"}},
		}},
		Outputs: []StageColumn{
			{ID: "site_group", Name: "site", Label: "Site", Type: "string"},
			{ID: "records", Name: "records", Label: "Records", Type: "integer"},
		},
	}}
	removed, impact, err := accepted.AnalyzeConstructionCandidate(*candidate, "group_by_type", nil)
	if err != nil {
		t.Fatalf("remove the final coded key: %v", err)
	}
	if len(removed.Construction.Steps) != 1 || removed.Construction.Steps[0].ID != "group_by_type" || !reflect.DeepEqual(impact.RemovedStepIDs, []string{"coded_input"}) {
		t.Fatalf("removing the last coded key did not prune its owned helper: construction=%#v impact=%#v", removed.Construction, impact)
	}
}

type constructionChainCase struct {
	ID               string `json:"id"`
	FirstKind        string `json:"firstKind"`
	SecondInput      string `json:"secondInput"`
	SecondColumn     string `json:"secondColumn"`
	Rename           *bool  `json:"rename"`
	Reorder          *bool  `json:"reorder"`
	ExpectedAccepted bool   `json:"expectedAccepted"`
}

func constructionForChainCase(value constructionChainCase) (Construction, []Column, error) {
	if value.ID == "" || value.Rename == nil || value.Reorder == nil {
		return Construction{}, nil, fmt.Errorf("missing case fields")
	}
	if value.FirstKind != "FILTER" && value.FirstKind != "DERIVE" {
		return Construction{}, nil, fmt.Errorf("unknown first operation")
	}
	if value.SecondColumn != "x" && value.SecondColumn != "d" && value.SecondColumn != "foreign" {
		return Construction{}, nil, fmt.Errorf("unknown column")
	}
	names := map[string]string{"x": "x", "d": "d", "foreign": "foreign"}
	firstID, secondID := "first_stage", "second_stage"
	if *value.Rename {
		names = map[string]string{"x": "root_value", "d": "computed_value", "foreign": "missing_value"}
		firstID, secondID = "stage_alpha", "stage_beta"
	}
	root := StageColumn{ID: names["x"], Name: names["x"], Label: "Root value", Type: "integer"}
	outputs := []StageColumn{root}
	first := ConstructionStep{ID: firstID, Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}}
	if value.FirstKind == "DERIVE" {
		one := int64(1)
		outputs = append(outputs, StageColumn{ID: names["d"], Name: names["d"], Label: "Derived value", Type: "integer"})
		first.Operation = ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{ConstructionID: firstID, OutputColumnID: names["d"], Operation: ConstructionDerivedAdd, Left: ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: names["x"]}, Right: ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &ConstructionLiteral{Kind: ConstructionNumericInteger, Integer: &one}}, MissingInputPolicy: ConstructionMissingInputPropagateNull}}
	} else {
		first.Operation = ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: names["x"], Operator: ConstructionFilterExists}}
	}
	first.Outputs = append([]StageColumn(nil), outputs...)
	input := ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: firstID}
	switch value.SecondInput {
	case "PREVIOUS":
	case "SOURCE":
		input = ConstructionInputRef{Kind: ConstructionInputSourceProjection}
	case "SELF":
		input.StepID = secondID
	case "FOREIGN":
		input.StepID = "another_stage"
	case "REVISION":
		input = ConstructionInputRef{Kind: ConstructionInputTableRevision, TableID: "another_table", RevisionID: "revision_1", OutputID: "another_output"}
	default:
		return Construction{}, nil, fmt.Errorf("unknown second input")
	}
	if *value.Reorder {
		for left, right := 0, len(outputs)-1; left < right; left, right = left+1, right-1 {
			outputs[left], outputs[right] = outputs[right], outputs[left]
		}
	}
	second := ConstructionStep{ID: secondID, Inputs: []ConstructionInputRef{input}, Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: names[value.SecondColumn], Operator: ConstructionFilterExists}}, Outputs: outputs}
	columns := []Column{{ColumnID: names["x"], Column: names["x"], Label: "Root value", LogicalType: "integer", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceProjectID}}}
	return Construction{Version: ConstructionVersion, Steps: []ConstructionStep{first, second}}, columns, nil
}

func TestConstructionModelReplay(t *testing.T) {
	const scheduleFile = "testdata/construction_chain_cases.json"
	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4<<20 {
		t.Fatalf("schedule file %s exceeds 4 MiB", scheduleFile)
	}
	var input struct {
		SchemaVersion int                     `json:"schemaVersion"`
		Cases         []constructionChainCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		t.Fatal("trailing input")
	}
	if input.SchemaVersion != 1 || len(input.Cases) == 0 || len(input.Cases) > 200 {
		t.Fatal("invalid frozen schedule set")
	}
	seen := map[string]bool{}
	for _, row := range input.Cases {
		if seen[row.ID] {
			t.Fatal("duplicate schedule case")
		}
		seen[row.ID] = true
		construction, columns, err := constructionForChainCase(row)
		if err != nil {
			t.Fatal(err)
		}
		validationErr := construction.Validate(columns)
		if got := validationErr == nil; got != row.ExpectedAccepted {
			t.Errorf("schedule %s: accepted=%t, want %t (validation error: %v)", row.ID, got, row.ExpectedAccepted, validationErr)
		}
	}
}

func expandConstruction() *Construction {
	return &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID:     "expand_names",
		Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationExpand, Expand: &ConstructionExpand{
			ConstructionID: "expand_names", InputColumnID: "patient_id", OutputColumnID: "name_item",
			EmptyPolicy: ConstructionExpandEmptyExclude,
		}},
		Outputs: []StageColumn{
			{ID: "name_item", Name: "name_item", Label: "Name item", Type: "string"},
		},
	}}}
}

func expandDocument() Document {
	document := workspaceDocument("expand")
	columns := append([]Column(nil), document.Columns...)
	columns[0].ColumnID = "patient_id"
	document.Columns = columns
	return document
}

func TestConstructionExpandWithoutExpandedFieldValidates(t *testing.T) {
	document := expandDocument()
	document.Construction = expandConstruction()
	if err := document.Validate(); err != nil {
		t.Fatalf("valid expand rejected: %v", err)
	}
}

func TestConstructionExpandedFieldValidPayloadRoundTripsPinned(t *testing.T) {
	document := expandDocument()
	construction := expandConstruction()
	construction.Steps[0].Operation.Expand.ExpandedField = &ConstructionExpandedFieldSource{
		Path: "name", Cardinality: ConstructionExpandedFieldCardinalityMany,
		FHIRType: "HumanName", LogicalType: "string",
	}
	document.Construction = construction
	if err := document.Validate(); err != nil {
		t.Fatalf("valid expandedField rejected: %v", err)
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
	if !reflect.DeepEqual(got, construction) {
		t.Fatalf("expandedField changed during save/reload:\n got: %#v\nwant: %#v", got.Steps[0].Operation.Expand.ExpandedField, construction.Steps[0].Operation.Expand.ExpandedField)
	}
	if !strings.Contains(string(encoded), `"expandedField"`) {
		t.Fatalf("canonical JSON dropped expandedField: %s", encoded)
	}
}

func TestConstructionExpandedFieldShapeRefusals(t *testing.T) {
	cases := []struct {
		name    string
		field   ConstructionExpandedFieldSource
		wantErr string
	}{
		{"blank path", ConstructionExpandedFieldSource{Path: " ", Cardinality: ConstructionExpandedFieldCardinalityMany, LogicalType: "string"}, "expandedField.path"},
		{"uppercase cardinality", ConstructionExpandedFieldSource{Path: "name", Cardinality: "MANY", LogicalType: "string"}, "expandedField.cardinality"},
		{"empty cardinality", ConstructionExpandedFieldSource{Path: "name", LogicalType: "string"}, "expandedField.cardinality"},
		{"blank logical type", ConstructionExpandedFieldSource{Path: "name", Cardinality: ConstructionExpandedFieldCardinalityMany, LogicalType: " "}, "expandedField.logicalType"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			document := expandDocument()
			construction := expandConstruction()
			field := testCase.field
			construction.Steps[0].Operation.Expand.ExpandedField = &field
			document.Construction = construction
			err := document.Validate()
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, testCase.wantErr)
			}
		})
	}
}

func TestConstructionCanReferenceIndexedSourceChildrenAcrossReload(t *testing.T) {
	childID, _, err := lineage.StableSourceChildID(lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"given_slot"}, OccurrenceID: RootOccurrenceID,
		SourcePath: "name[].given[]", Coordinates: []lineage.Coordinate{
			{BoundaryPath: "name[]", Index: 0}, {BoundaryPath: "name[].given[]", Index: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	columns := []Column{{
		ColumnID: "given_slot", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: RootOccurrenceID,
		Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}},
	}}
	document := Document{
		Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: RecordsRowDefinition(),
		Columns: columns, Construction: &Construction{
			Version: ConstructionVersion,
			Steps: []ConstructionStep{{
				ID: "filter_indexed", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
				Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: childID, Operator: ConstructionFilterExists}},
				Outputs: []StageColumn{
					{ID: childID, Name: "given_at_0_1", Label: "Given [0] [1]", Type: "string", Nullable: true},
				},
			}},
		},
	}
	if err := document.Construction.Validate(document.Columns); err != nil {
		t.Fatalf("valid indexed child reference: %v", err)
	}

	normalizeConstructionOutputOrder(&document)
	if got := document.Construction.Steps[0].Outputs[0].Name; got != "given_at_0_1" {
		t.Fatalf("normalized child name = %q, want persisted public name", got)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded Document
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Construction.Validate(reloaded.Columns); err != nil {
		t.Fatalf("reloaded indexed child reference: %v", err)
	}
	if got := reloaded.Construction.Steps[0].Operation.Filter.ColumnID; got != childID {
		t.Fatalf("reloaded filter column ID = %q, want %q", got, childID)
	}
	replacement := reloaded.Construction.Steps[0]
	replacement.Operation.Filter.Operator = ConstructionFilterMissing
	edited, _, err := reloaded.AnalyzeStepEdit(replacement)
	if err != nil {
		t.Fatalf("edit persisted child-consuming step: %v", err)
	}
	if got := edited.Construction.Steps[0].Outputs[0].ID; got != childID {
		t.Fatalf("edited step child ID = %q, want %q", got, childID)
	}

	stale := reloaded
	stale.Columns = append([]Column(nil), reloaded.Columns...)
	stale.Columns[0].Source.Field = &FieldSource{Path: "telecom[].value", ProjectionMode: "INDEXED"}
	if err := stale.Construction.Validate(stale.Columns); err == nil {
		t.Fatal("source edit retained a child reference from the old repeated path")
	}
	stale = reloaded
	stale.Columns = append([]Column(nil), reloaded.Columns...)
	stale.Columns[0].Source.Field = &FieldSource{Path: "name[].given[]", ProjectionMode: "VALUE"}
	if err := stale.Construction.Validate(stale.Columns); err == nil {
		t.Fatal("non-indexed source retained an indexed child reference")
	}
}

func TestIndependentRelatedExpansionsRejectTwoByThreeProduct(t *testing.T) {
	makeStep := func(id, anchor, edge, target string) ConstructionStep {
		return ConstructionStep{ID: id, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
			AnchorColumnID: anchor, Route: []capability.ConstructionRouteStep{{EdgeID: edge, FromNodeID: "patient", ToNodeID: target, StorageDirection: "INBOUND"}},
		}}}
	}
	observations := makeStep("two-observations", "_key", "observations", "observation")
	conditions := makeStep("three-conditions", "_key", "conditions", "condition")

	err := (&Construction{Version: ConstructionVersion, Steps: []ConstructionStep{observations, conditions}}).Validate(nil)
	if err == nil || !strings.Contains(err.Error(), "INDEPENDENT_RELATED_EXPANSIONS_UNSUPPORTED") {
		t.Fatalf("independent branches accepted: %v", err)
	}
	relatedAnchor := makeStep("onward", "observation-document-anchor", "specimens", "specimen")
	if err := validateIndependentRelatedExpansions([]ConstructionStep{observations, relatedAnchor}); err != nil {
		t.Fatalf("onward related-record route rejected: %v", err)
	}
	sameRoute := observations
	sameRoute.ID = "same-route"
	if err := validateIndependentRelatedExpansions([]ConstructionStep{observations, sameRoute}); err != nil {
		t.Fatalf("existing exact-route expansion rejected: %v", err)
	}
}
