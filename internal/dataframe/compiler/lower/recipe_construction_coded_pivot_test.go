package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedPivotStreamsRootAndCorrelatesCategoryWithOwnedValue(t *testing.T) {
	compiled := compileDerivedTestOutput(t, codedPivotOutput())
	sequence := compiled.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 1 || sequence.Stages[0].Kind != ir.PhysicalStagePivotOp {
		t.Fatalf("stage sequence = %#v, want one typed Pivot stage", sequence)
	}
	if !sequence.PreviewSourceWindowByRootID {
		pivot := sequence.Stages[0].GroupedPivot
		t.Fatalf("direct-root CODED_PIVOT must support a bounded preview window before coded value extraction: sourceEligible=%t root=%#v source=%#v schema=%#v pivot=%#v", constructionRootIDPivotSourceEligible(&compiled.Plan, "Observation"), compiled.Plan.Operations[0].RootScan, compiled.Plan.Source, compiled.Stages[0].Columns, pivot)
	}
	pivot := sequence.Stages[0].GroupedPivot
	if pivot == nil || pivot.CodedCorrelation == nil || pivot.CodedSourceVariable == "" ||
		!pivot.OneInputRowPerGroup || len(pivot.CodedCategories) != 2 {
		t.Fatalf("coded Pivot IR = %#v, want direct root source, one-row groups, and frozen categories", pivot)
	}
	correlation := pivot.CodedCorrelation
	if got, want := correlation.OwnerSelector.CanonicalPath(), "component[]"; got != want {
		t.Fatalf("coded Pivot owner selector = %q, want %q", got, want)
	}
	if got, want := correlation.KeySelector.CanonicalPath(), "code.coding[]"; got != want {
		t.Fatalf("coded Pivot key selector = %q, want %q", got, want)
	}
	if got, want := correlation.ValueSelector.CanonicalPath(), "valueQuantity.value"; got != want {
		t.Fatalf("coded Pivot value selector = %q, want %q", got, want)
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "__loom_construction_source_projection") || strings.Contains(rendered.Query, "COLLECT ") {
		t.Fatalf("coded Pivot must stream each direct root without materializing payload groups:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "FOR "+pivot.CodedSourceVariable+" IN @@root_collection") ||
		!strings.Contains(rendered.Query, "FLATTEN(") || !strings.Contains(rendered.Query, "SUM(") {
		t.Fatalf("coded Pivot AQL is missing root streaming or the existing Pivot reducer:\n%s", rendered.Query)
	}
	for _, category := range pivot.CodedCategories {
		if !strings.Contains(rendered.Query, "@"+category.SystemBindKey) || !strings.Contains(rendered.Query, "@"+category.CodeBindKey) {
			t.Fatalf("coded category %q lost its bound system/code pair:\n%s", category.Output, rendered.Query)
		}
	}
	if !strings.Contains(rendered.Query, "FILTER __coded_system == @") || !strings.Contains(rendered.Query, "FILTER __coded_code == @") {
		t.Fatalf("system and code must be matched on the same Coding item:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "RETURN __coded_values") || strings.Contains(rendered.Query, "RETURN UNIQUE(__coded_values)") {
		t.Fatalf("coded Pivot must preserve repeated values for the configured duplicate policy:\n%s", rendered.Query)
	}
	ownerMarker := "coded_pivot_owner"
	markerIndex := strings.Index(rendered.Query, ownerMarker)
	if markerIndex < 0 {
		t.Fatalf("coded Pivot AQL has no owner loop:\n%s", rendered.Query)
	}
	loopStart := strings.LastIndex(rendered.Query[:markerIndex], "FOR ")
	loopLineEnd := strings.Index(rendered.Query[markerIndex:], " IN ")
	if loopStart < 0 || loopLineEnd < 0 {
		t.Fatalf("could not find coded Pivot owner variable in AQL:\n%s", rendered.Query)
	}
	ownerVariable := strings.TrimSpace(rendered.Query[loopStart+len("FOR ") : markerIndex+loopLineEnd])
	ownedSelector := "FOR __root IN [" + ownerVariable + "]"
	if got := strings.Count(rendered.Query, ownedSelector); got < len(pivot.CodedCategories)*2 {
		t.Fatalf("each category must evaluate its coding keys and values under the same owner %q; found %d owned selectors, want at least %d:\n%s", ownerVariable, got, len(pivot.CodedCategories)*2, rendered.Query)
	}
}

func codedPivotOutput() recipe.Output {
	output := codedGroupOutput("Observation", "component[].code.coding[]", recipe.ConstructionGroupMissingKeyGroup)
	output.Name = "construction_coded_pivot"
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "pivot_coded_components", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{
			Kind: recipe.ConstructionCodedPivotOp,
			CodedPivot: &recipe.ConstructionCodedPivot{
				ConstructionID: "pivot_coded_components",
				Source: recipe.ConstructionCodedPivotSource{
					BindingID: "observation-component-value", ResourceType: "Observation",
					SourcePath: "Observation.component[].valueQuantity.value", SourceCanonical: "http://loinc.org",
					OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: "valueQuantity.value",
					ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal", RuleVersion: "semantic-rule-v1", SchemaVersion: 1,
					CandidateID: "observation-component-value", NodeID: "observation-component", FieldPath: "Observation.component[].valueQuantity.value",
					Route: []recipe.ConstructionRelatedRouteStep{},
				},
				Categories: []recipe.ConstructionCodedPivotCategory{
					{System: "http://loinc.org", Code: "8480-6", OutputColumnID: "systolic_id"},
					{System: "http://loinc.org", Code: "8462-4", OutputColumnID: "diastolic_id"},
				},
				DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			},
		},
		Outputs: []recipe.StageColumn{
			{ID: "systolic_id", Name: "systolic", Type: "decimal", Nullable: true},
			{ID: "diastolic_id", Name: "diastolic", Type: "decimal", Nullable: true},
		},
	}}
	return output
}
