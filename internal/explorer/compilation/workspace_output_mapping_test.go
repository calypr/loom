package compilation

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipeConstructionStepPreservesWorkspaceOutputInputIdentity(t *testing.T) {
	step := ws.ConstructionStep{
		ID: "combine",
		Inputs: []ws.ConstructionInputRef{
			{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "grouped_output"},
			{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "pivot_output"},
		},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
			Kind: ws.ConstructionCombineAppend,
			Projections: []ws.ConstructionCombineProjection{
				{OutputColumnID: "value", InputIndex: 0, InputColumnID: "group_value"},
				{OutputColumnID: "value", InputIndex: 1, InputColumnID: "pivot_value"},
			},
		}},
		Outputs: []ws.StageColumn{{ID: "value", Name: "value", Type: "string"}},
	}
	mapped, err := recipeConstructionStep(step)
	if err != nil {
		t.Fatalf("map construction step: %v", err)
	}
	if len(mapped.Inputs) != 2 || mapped.Inputs[0] != (recipe.ConstructionInputRef{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "grouped_output"}) || mapped.Inputs[1] != (recipe.ConstructionInputRef{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "pivot_output"}) {
		t.Fatalf("mapped workspace output refs = %#v", mapped.Inputs)
	}
}
