package recipe

import (
	"strings"
	"testing"
)

func workspaceOutputCombineRecipe(name, dependency string) Output {
	inputs := []ConstructionInputRef{
		{Kind: ConstructionTableRevisionInput, TableID: "source:1:left", RevisionID: "left-r1", OutputID: "left"},
		{Kind: ConstructionTableRevisionInput, TableID: "source:1:right", RevisionID: "right-r1", OutputID: "right"},
	}
	if dependency != "" {
		inputs[0] = ConstructionInputRef{Kind: ConstructionWorkspaceOutputInput, OutputID: dependency}
	}
	combine := ConstructionCombine{Kind: ConstructionCombineAppend, Projections: []ConstructionCombineProjection{
		{OutputColumnID: "value", InputIndex: 0, InputColumnID: "value"},
		{OutputColumnID: "value", InputIndex: 1, InputColumnID: "value"},
	}}
	return Output{
		Name: name, RootResourceType: "Patient", RowGrain: "patient",
		Construction: &Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "append", Inputs: inputs, Operation: ConstructionOperation{Kind: ConstructionCombineOp, Combine: &combine},
			Outputs: []StageColumn{{ID: "value", Name: "value", Type: "string"}},
		}}},
	}
}

func workspaceOutputBundle(outputs ...Output) Bundle {
	return Bundle{RecipeSchemaVersion: CurrentSchemaVersion, Name: "workspace", TranslationVersion: "1", Outputs: outputs}
}

func TestRecipeBundleWorkspaceOutputDependenciesValidateAndOrderForwardReferences(t *testing.T) {
	bundle := workspaceOutputBundle(workspaceOutputCombineRecipe("combined", "base"), workspaceOutputCombineRecipe("base", ""))
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle with same-bundle output ref rejected: %v", err)
	}
	order, err := bundle.OutputDependencyOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("dependency order = %v, want [1 0]", order)
	}
}

func TestRecipeBundleWorkspaceOutputDependenciesRejectUnknownSelfAndCycles(t *testing.T) {
	for _, test := range []struct {
		name    string
		outputs []Output
		want    string
	}{
		{name: "unknown", outputs: []Output{workspaceOutputCombineRecipe("combined", "missing"), workspaceOutputCombineRecipe("base", "")}, want: "workspace_output_not_found"},
		{name: "self", outputs: []Output{workspaceOutputCombineRecipe("combined", "combined"), workspaceOutputCombineRecipe("base", "")}, want: "workspace_output_self_reference"},
		{name: "cycle", outputs: []Output{workspaceOutputCombineRecipe("first", "second"), workspaceOutputCombineRecipe("second", "first")}, want: "workspace_output_cycle"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := workspaceOutputBundle(test.outputs...).Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestSingleOutputCompilerFragmentRequiresExplicitWorkspaceContext(t *testing.T) {
	fragment := workspaceOutputBundle(workspaceOutputCombineRecipe("combined", "base"))
	if err := fragment.Validate(); err == nil || !strings.Contains(err.Error(), "workspace_output_not_found") {
		t.Fatalf("closed bundle validation error = %v", err)
	}
	if err := fragment.ValidateWithWorkspaceOutputs([]string{"base"}); err != nil {
		t.Fatalf("workspace-context validation rejected sibling output: %v", err)
	}
}
