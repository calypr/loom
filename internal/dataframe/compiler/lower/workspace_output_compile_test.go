package lower

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestWorkspaceOutputResolverUsesFinalGroupAndPivotSchemas(t *testing.T) {
	grouped := recipe.Output{
		Name: "grouped", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_patients",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender_id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient_count_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "group_gender_id", Name: "gender", Type: "string"}, {ID: "patient_count_id", Name: "patient_count", Type: "integer"}},
			}},
		},
	}
	pivoted := constructionTestOutput()
	pivoted.Name = "pivoted"
	pivoted.Construction.Steps = append([]recipe.ConstructionStep(nil), pivoted.Construction.Steps[:1]...)
	pivoted.Construction.Steps[0].Outputs = []recipe.StageColumn{
		{ID: "group_id", Name: "group", Type: "string"},
		{ID: "amount_a_id", Name: "amount_a", Type: "integer", Nullable: true},
		{ID: "amount_b_id", Name: "amount_b", Type: "integer", Nullable: true},
	}
	combine := recipe.ConstructionCombine{
		Kind: recipe.ConstructionCombineKeyJoin, JoinType: recipe.ConstructionCombineLeftJoin,
		RightMatchPolicy: recipe.ConstructionCombinePreserveAllMatches,
		Keys:             []recipe.ConstructionCombineKey{{LeftColumnID: "group_gender_id", RightColumnID: "group_id"}},
		Projections: []recipe.ConstructionCombineProjection{
			{OutputColumnID: "gender", InputIndex: 0, InputColumnID: "group_gender_id"},
			{OutputColumnID: "amount", InputIndex: 1, InputColumnID: "amount_a_id"},
		},
	}
	combined := combineRecipeOutput(combine, []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "grouped"},
		{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: "pivoted"},
	}, []recipe.StageColumn{
		{ID: "gender", Name: "gender", Type: "string", Nullable: true},
		{ID: "amount", Name: "amount", Type: "integer", Nullable: true},
	})
	combined.Name = "combined"
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "workspace", TranslationVersion: "1",
		Outputs: []recipe.Output{combined, grouped, pivoted},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan: %v", err)
	}
	if len(compiled.Outputs) != 3 || compiled.Outputs[0].Name != "combined" || compiled.Outputs[1].Name != "grouped" || compiled.Outputs[2].Name != "pivoted" {
		t.Fatalf("compiled outputs lost authored order: %#v", compiled.Outputs)
	}
	combinedOutput := compiled.Outputs[0]
	if len(combinedOutput.WorkspaceOutputSources) != 2 {
		t.Fatalf("workspace sources = %#v, want both sibling inputs", combinedOutput.WorkspaceOutputSources)
	}
	for _, source := range combinedOutput.WorkspaceOutputSources {
		var expected []CompiledOutputColumn
		switch source.OutputID {
		case "grouped":
			expected = compiled.Outputs[1].OutputSchema
		case "pivoted":
			expected = compiled.Outputs[2].OutputSchema
		default:
			t.Fatalf("unexpected source output ID %q", source.OutputID)
		}
		if !equalCompiledOutputSchemas(source.Schema, expected) {
			t.Fatalf("source %q schema differs from finalized compiler output", source.OutputID)
		}
	}
	if compiled.Outputs[0].Plan.ClickHouseCombine.Inputs[0].WorkspaceOutputID != "grouped" || compiled.Outputs[0].Plan.ClickHouseCombine.Inputs[1].WorkspaceOutputID != "pivoted" {
		t.Fatalf("physical workspace refs = %#v", compiled.Outputs[0].Plan.ClickHouseCombine.Inputs)
	}
	selectedResolved := resolved
	selectedResolved.SemanticPlan.Bindings.OutputNames = []string{"combined"}
	selected, err := CompileResolvedRecipePlan(selectedResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile selected combined output with its dependency closure: %v", err)
	}
	if len(selected.Outputs) != 1 || len(selected.Outputs[0].WorkspaceOutputSources) != 2 {
		t.Fatalf("selected output did not retain both resolved dependency schemas: %#v", selected.Outputs)
	}

	mismatched := bundle
	mismatched.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
	construction := *bundle.Outputs[0].Construction
	construction.Steps = append([]recipe.ConstructionStep(nil), bundle.Outputs[0].Construction.Steps...)
	mismatched.Outputs[0].Construction = &construction
	mismatched.Outputs[0].Construction.Steps[0].Outputs = append([]recipe.StageColumn(nil), bundle.Outputs[0].Construction.Steps[0].Outputs...)
	mismatched.Outputs[0].Construction.Steps[0].Outputs[1].Type = "string"
	mismatchPlan, err := semantic.BuildRecipePlan(mismatched, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatalf("BuildRecipePlan(mismatched schema): %v", err)
	}
	mismatchResolved, err := semantic.ResolveRecipePlan(mismatchPlan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("ResolveRecipePlan(mismatched schema): %v", err)
	}
	if _, err := CompileResolvedRecipePlan(mismatchResolved, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "compiler-owned type") {
		t.Fatalf("client-declared output type mismatch error = %v", err)
	}
}

func equalCompiledOutputSchemas(left, right []CompiledOutputColumn) bool {
	return reflect.DeepEqual(left, right)
}
