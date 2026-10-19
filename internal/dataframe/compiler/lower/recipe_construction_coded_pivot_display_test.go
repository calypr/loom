package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
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
	if got := pivot.CodedCorrelation.ValueSelector.CanonicalPath(); got != "valueCodeableConcept.text" {
		t.Fatalf("preferred correlated value selector = %q", got)
	}
	if len(pivot.CodedCorrelation.ValueFallbacks) != 1 || pivot.CodedCorrelation.ValueFallbacks[0].CanonicalPath() != "valueCodeableConcept.coding[].display" ||
		!pivot.CodedCorrelation.FallbackOnBlankString {
		t.Fatalf("physical correlated fallback = %#v, want repeated displays and blank-string fallback enabled", pivot.CodedCorrelation)
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	if !strings.Contains(query, "LET __value = __s0.text") ||
		!strings.Contains(query, "FOR __s1 IN (__s0.coding ? __s0.coding : [])") ||
		!strings.Contains(query, "LET __value = __s1.display") || !strings.Contains(query, "RETURN __value") ||
		!strings.Contains(query, "TRIM(") {
		t.Fatalf("coded Pivot did not prefer text and retain every repeated display fallback value:\n%s", query)
	}
	cardinality := strings.Index(query, "ASSERT(LENGTH(__loom_physical_construction_reshape_coded_values_0) <= 1, \"TABLE_PIVOT_CELL_CARDINALITY\")")
	first := strings.Index(query, "? FIRST(__loom_physical_construction_reshape_coded_values_0)")
	displayIteration := strings.Index(query, "FOR __s1 IN (__s0.coding ? __s0.coding : [])")
	if displayIteration < 0 || cardinality < displayIteration || first < cardinality {
		t.Fatalf("duplicatePolicy ERROR must count every fallback display before selecting its sole value (display=%d cardinality=%d first=%d):\n%s", displayIteration, cardinality, first, query)
	}
	if strings.Contains(query, "SORTED_UNIQUE(") || strings.Contains(query, "UNIQUE(") {
		t.Fatalf("repeated displays must retain multiplicity for duplicatePolicy ERROR, not be deduplicated:\n%s", query)
	}
	if strings.Contains(query, "FIRST(__coded_values)") || strings.Contains(query, "LIMIT 1\n        RETURN __value") {
		t.Fatalf("repeated display fallback values were reduced before the cell cardinality check:\n%s", query)
	}
}

func TestConstructionCodedPivotGenericStringKeepsDefaultEmptyPrimaryBehavior(t *testing.T) {
	compiled := compileDerivedTestOutput(t, codedPivotDisplayOutput(false))
	pivot := compiled.Plan.StageSequence.Stages[0].GroupedPivot
	if pivot == nil || pivot.CodedCorrelation == nil || pivot.CodedCorrelation.FallbackOnBlankString {
		t.Fatalf("generic string correlation = %#v, want blank-string fallback disabled by default", pivot)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	if !strings.Contains(query, "FILTER LENGTH(__correlation_candidate) > 0") ||
		!strings.Contains(query, "LET __value = __s0.text") || !strings.Contains(query, "FILTER __value != null") {
		t.Fatalf("generic string fallback must keep the existing array-presence check, where empty primary text remains present:\n%s", query)
	}
	if strings.Contains(query, "TRIM(") || strings.Contains(query, "__value != \"\"") {
		t.Fatalf("generic string binding without the flag must not treat an empty string as missing:\n%s", query)
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
