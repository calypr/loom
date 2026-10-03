package lower

import (
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileTerminalKeyJoinToTypedClickHousePlan(t *testing.T) {
	output := combineRecipeOutput(recipe.ConstructionCombine{
		Kind: recipe.ConstructionCombineKeyJoin,
		Keys: []recipe.ConstructionCombineKey{{LeftColumnID: "left_patient", RightColumnID: "right_patient"}},
		Projections: []recipe.ConstructionCombineProjection{
			{OutputColumnID: "patient", InputIndex: 0, InputColumnID: "left_patient"},
			{OutputColumnID: "status", InputIndex: 1, InputColumnID: "right_status"},
		},
		JoinType: recipe.ConstructionCombineLeftJoin, RightMatchPolicy: recipe.ConstructionCombinePreserveAllMatches,
	}, []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "registry:1:patients", RevisionID: "exec-left", OutputID: "patients"},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "clinical:1:conditions", RevisionID: "exec-right", OutputID: "conditions"},
	}, []recipe.StageColumn{
		{ID: "patient", Name: "patient_id", Type: "string"},
		{ID: "status", Name: "condition_status", Type: "code", Nullable: true},
	})
	compiled := compileTerminalCombineOutput(t, output)
	plan := compiled.Plan
	if plan.Engine != ir.PhysicalEngineClickHouse || plan.ClickHouseCombine == nil || plan.StageSequence != nil || len(plan.Operations) != 0 {
		t.Fatalf("terminal combine plan = %#v", plan)
	}
	combine := plan.ClickHouseCombine
	if combine.Kind != ir.PhysicalCombineKeyJoin || combine.JoinType != "LEFT" || combine.RightMatchPolicy != "PRESERVE_ALL" {
		t.Fatalf("key join policy = %#v", combine)
	}
	if len(combine.Inputs) != 2 || combine.Inputs[0] != (ir.PhysicalCombineInputRef{TableID: "registry:1:patients", RevisionID: "exec-left", OutputID: "patients"}) || combine.Inputs[1] != (ir.PhysicalCombineInputRef{TableID: "clinical:1:conditions", RevisionID: "exec-right", OutputID: "conditions"}) {
		t.Fatalf("exact combine refs = %#v", combine.Inputs)
	}
	if len(combine.Outputs) != 2 || combine.Outputs[0].ID != "patient" || combine.Outputs[0].ClickHouseType != "String" || combine.Outputs[1].LogicalType != "code" || combine.Outputs[1].ClickHouseType != "Nullable(String)" || !combine.Outputs[1].Nullable {
		t.Fatalf("typed combine schema = %#v", combine.Outputs)
	}
	if got, want := compiledSchemaNames(compiled.OutputSchema), []string{"__loom_row_id", "patient_id", "condition_status"}; !equalStringSlices(got, want) {
		t.Fatalf("final combine schema = %#v, want %#v", got, want)
	}
	if len(compiled.Stages) != 1 || compiled.Stages[0].Operation != "KEY_JOIN" || compiled.Stages[0].RowIdentityColumn != "__loom_row_id" {
		t.Fatalf("combine stage descriptor = %#v", compiled.Stages)
	}
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("optimize terminal combine: %v", err)
	}
	if optimized.Engine != ir.PhysicalEngineClickHouse || optimized.ClickHouseCombine == nil || len(optimized.ClickHouseCombine.Inputs) != 2 {
		t.Fatalf("optimized terminal combine lost its typed inputs: %#v", optimized)
	}
}

func TestCompileTerminalAppendMapsIndependentInputColumnIDs(t *testing.T) {
	output := combineRecipeOutput(recipe.ConstructionCombine{
		Kind: recipe.ConstructionCombineAppend,
		Projections: []recipe.ConstructionCombineProjection{
			{OutputColumnID: "name", InputIndex: 0, InputColumnID: "left_name"},
			{OutputColumnID: "name", InputIndex: 1, InputColumnID: "right_label"},
		},
	}, []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "a:1:people", RevisionID: "exec-a", OutputID: "people"},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "b:1:people", RevisionID: "exec-b", OutputID: "people"},
	}, []recipe.StageColumn{{ID: "name", Name: "display_name", Type: "string"}})
	compiled := compileTerminalCombineOutput(t, output)
	combine := compiled.Plan.ClickHouseCombine
	if combine == nil || combine.Kind != ir.PhysicalCombineAppend || len(combine.Projections) != 2 || combine.Projections[0].InputColumnID == combine.Projections[1].InputColumnID {
		t.Fatalf("append mapping did not preserve independent stable IDs: %#v", combine)
	}
}

func TestCompileTerminalMembershipPreservesLeftProjection(t *testing.T) {
	output := combineRecipeOutput(recipe.ConstructionCombine{
		Kind: recipe.ConstructionCombineMembership, MembershipMode: recipe.ConstructionCombineExcludeMatches,
		Keys:        []recipe.ConstructionCombineKey{{LeftColumnID: "left_id", RightColumnID: "right_id"}},
		Projections: []recipe.ConstructionCombineProjection{{OutputColumnID: "left_id", InputIndex: 0, InputColumnID: "left_id"}},
	}, []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "a:1:people", RevisionID: "exec-a", OutputID: "people"},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "b:1:blocked", RevisionID: "exec-b", OutputID: "blocked"},
	}, []recipe.StageColumn{{ID: "left_id", Name: "person_id", Type: "string"}})
	compiled := compileTerminalCombineOutput(t, output)
	combine := compiled.Plan.ClickHouseCombine
	if combine == nil || combine.Kind != ir.PhysicalCombineMembership || combine.MembershipMode != "EXCLUDE" || len(combine.Projections) != 1 || combine.Projections[0].InputIndex != 0 {
		t.Fatalf("membership mapping = %#v", combine)
	}
}

func TestCompileTerminalCombineRejectsUnsupportedPhysicalTypes(t *testing.T) {
	output := combineRecipeOutput(recipe.ConstructionCombine{
		Kind: recipe.ConstructionCombineAppend,
		Projections: []recipe.ConstructionCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "left_value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "right_value"},
		},
	}, []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "a:1:table", RevisionID: "exec-a", OutputID: "table"},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: "b:1:table", RevisionID: "exec-b", OutputID: "table"},
	}, []recipe.StageColumn{{ID: "value", Name: "value", Type: "number"}})
	if _, err := compileConstructionForCombineTest(output); err == nil {
		t.Fatal("unsupported logical number type was accepted by the physical compiler")
	}
}

func combineRecipeOutput(combine recipe.ConstructionCombine, inputs []recipe.ConstructionInputRef, outputs []recipe.StageColumn) recipe.Output {
	return recipe.Output{
		Name: "combined", RootResourceType: "Patient", RowGrain: "patient",
		Construction: &recipe.Construction{
			Version: 1,
			Steps: []recipe.ConstructionStep{{
				ID: "combine", Inputs: inputs,
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCombineOp, Combine: &combine},
				Outputs:   outputs,
			}},
		},
	}
}

func compileTerminalCombineOutput(t *testing.T, output recipe.Output) CompiledRecipeOutput {
	t.Helper()
	compiled, err := compileConstructionForCombineTest(output)
	if err != nil {
		t.Fatalf("compile terminal combine: %v", err)
	}
	if err := compiled.Plan.Validate(); err != nil {
		t.Fatalf("compiled combine plan is invalid: %v", err)
	}
	return compiled
}

func compileConstructionForCombineTest(output recipe.Output) (CompiledRecipeOutput, error) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "combine-test", TranslationVersion: "1",
		Outputs: []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	if len(compiled.Outputs) != 1 {
		return CompiledRecipeOutput{}, fmt.Errorf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0], nil
}
