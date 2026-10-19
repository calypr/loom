package compilation

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestRecipeConstructionStepPreservesWorkspaceOutputInputIdentity(t *testing.T) {
	step := authoringv2.ConstructionStep{
		ID: "combine",
		Inputs: []authoringv2.ConstructionInputRef{
			{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "grouped_output"},
			{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "pivot_output"},
		},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
			Kind: authoringv2.ConstructionCombineAppend,
			Projections: []authoringv2.ConstructionCombineProjection{
				{OutputColumnID: "value", InputIndex: 0, InputColumnID: "group_value"},
				{OutputColumnID: "value", InputIndex: 1, InputColumnID: "pivot_value"},
			},
		}},
		Outputs: []authoringv2.StageColumn{{ID: "value", Name: "value", Type: "string"}},
	}
	mapped, err := recipeConstructionStep(step)
	if err != nil {
		t.Fatalf("map construction step: %v", err)
	}
	if len(mapped.Inputs) != 2 || mapped.Inputs[0] != (recipe.ConstructionInputRef{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "grouped_output"}) || mapped.Inputs[1] != (recipe.ConstructionInputRef{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "pivot_output"}) {
		t.Fatalf("mapped workspace output refs = %#v", mapped.Inputs)
	}
}
