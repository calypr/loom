package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedPivotRepeatedCodeableConceptDisplayHonorsErrorCardinality(t *testing.T) {
	compiled := compileDerivedTestOutput(t, codedPivotDisplayOutput(true))
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStagePivotOp {
		t.Fatalf("stage sequence = %#v, want one typed Pivot stage", sequence)
	}
	pivot := sequence.Stages[0].GroupedPivot
	if pivot == nil || pivot.CodedCorrelation == nil {
		t.Fatalf("coded Pivot IR = %#v, want a typed coded correlation", pivot)
	}
	if pivot.DuplicatePolicy != "ERROR" {
		t.Fatalf("coded Pivot duplicate policy = %q, want ERROR", pivot.DuplicatePolicy)
	}
	if got := pivot.CodedCorrelation.ValueSelector.CanonicalPath(); got != "valueCodeableConcept.text" {
		t.Fatalf("preferred correlated value selector = %q", got)
	}
	if len(pivot.CodedCorrelation.ValueFallbacks) != 1 || pivot.CodedCorrelation.ValueFallbacks[0].CanonicalPath() != "valueCodeableConcept.coding[].display" ||
		!pivot.CodedCorrelation.FallbackOnBlankString {
		t.Fatalf("physical correlated fallback = %#v, want repeated displays and blank-string fallback enabled", pivot.CodedCorrelation)
	}
}

func TestConstructionCodedPivotGenericStringKeepsDefaultEmptyPrimaryBehavior(t *testing.T) {
	compiled := compileDerivedTestOutput(t, codedPivotDisplayOutput(false))
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || pivot.CodedCorrelation == nil || pivot.CodedCorrelation.FallbackOnBlankString {
		t.Fatalf("generic string correlation = %#v, want blank-string fallback disabled by default", pivot)
	}
}

func codedPivotDisplayOutput(fallbackOnBlankString bool) recipe.Output {
	output := codedPivotOutput()
	source := recipe.ConstructionCodedPivotSource{
		BindingID:       "observation-value-codeable-concept",
		ResourceType:    "Observation",
		SourcePath:      "Observation.valueCodeableConcept.text",
		SourceCanonical: "http://loinc.org",
		KeyPath:         "code.coding[]",
		ValuePath:       "valueCodeableConcept.text",
		ValueFallbacks:  []string{"valueCodeableConcept.coding[].display"},
		ChoiceArms:      []string{"valueCodeableConcept"},
		LogicalType:     "string",
		RuleVersion:     "semantic-rule-v1",
		SchemaVersion:   1,
		CandidateID:     "observation-value-codeable-concept",
		NodeID:          "observation-root",
		FieldPath:       "Observation.valueCodeableConcept.text",
		Route:           []recipe.ConstructionRelatedRouteStep{},
	}
	if fallbackOnBlankString {
		source.FallbackOnBlankString = true
	}
	output.Construction.Steps[0].Operation.CodedPivot.Source = source
	output.Construction.Steps[0].Operation.CodedPivot.Categories = []recipe.ConstructionCodedPivotCategory{{
		System: "urn:study", Code: "stage", OutputColumnID: "stage_display",
	}}
	output.Construction.Steps[0].Operation.CodedPivot.DuplicatePolicy = recipe.PivotDuplicateError
	output.Construction.Steps[0].Outputs = []recipe.StageColumn{{
		ID: "stage_display", Name: "stage_display", Type: "string", Nullable: true,
	}}
	return output
}
