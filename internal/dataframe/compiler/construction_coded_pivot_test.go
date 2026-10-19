package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestConstructionCodedPivotPreviewLimitsRootsBeforeCodedExtraction(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "coded-pivot-project", DatasetGeneration: "coded-pivot-generation"}
	compiled := lowerConstructionOutput(t, constructionCodedPivotPreviewOutput(), bindings)
	if !compiled.Plan.StageSequence.PreviewSourceWindowByRootID {
		t.Fatal("direct-root CODED_PIVOT should allow an indexed source-root preview window")
	}

	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	sortIndex := strings.Index(query.Query, "SORT root.id ASC")
	limitIndex := strings.Index(query.Query, "LIMIT @limit")
	ownerIndex := strings.Index(query.Query, "coded_pivot_owner")
	if sortIndex < 0 || limitIndex < 0 || ownerIndex < 0 || !(sortIndex < limitIndex && limitIndex < ownerIndex) {
		t.Fatalf("preview must bound roots before running correlated category extraction (sort=%d limit=%d owner=%d):\n%s", sortIndex, limitIndex, ownerIndex, query.Query)
	}
	if strings.Contains(query.Query, "__loom_construction_source_projection") || strings.Contains(query.Query, "COLLECT ") {
		t.Fatalf("bounded coded preview must stream selected roots through the typed Pivot stage:\n%s", query.Query)
	}
}

func constructionCodedPivotPreviewOutput() recipe.Output {
	return recipe.Output{
		Name: "coded_pivot_preview", RootResourceType: "Observation", RowGrain: "resource",
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{}, Steps: []recipe.ConstructionStep{{
			ID: "coded_pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{
				Kind: recipe.ConstructionCodedPivotOp,
				CodedPivot: &recipe.ConstructionCodedPivot{
					ConstructionID: "coded_pivot",
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
		}}},
	}
}
