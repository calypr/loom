package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestWorkspaceOutputExecutionRemainsExplicitlyUnsupportedUntilCapture(t *testing.T) {
	output := lower.CompiledRecipeOutput{
		Name: "combined",
		Plan: ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &ir.PhysicalClickHouseCombine{
			Kind:   ir.PhysicalCombineAppend,
			Inputs: []ir.PhysicalCombineInputRef{{WorkspaceOutputID: "base"}, {TableID: "table", RevisionID: "r1", OutputID: "table"}},
		}},
		WorkspaceOutputSources: []lower.WorkspaceOutputSource{{InputIndex: 0, OutputID: "base", Schema: []lower.CompiledOutputColumn{{ID: "value", Kind: "string"}}}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a"}
	if _, err := CompileRecipeOutputWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("query compile error = %v", err)
	}
	if _, err := CompileRecipeOutputPageWithPolicy(output, bindings, 10, ir.DefaultPhysicalOptimizationPolicy()); err == nil || !strings.Contains(err.Error(), "server-owned workspace capture is not available") {
		t.Fatalf("page compile error = %v", err)
	}
}

func TestCompiledQueryRetainsFinalizedPlanThatRendersExecutedQuery(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "physical-plan-project", DatasetGeneration: "physical-plan-generation"}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "retained_physical_plan",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlanWithPolicy: %v", err)
	}
	if len(queries) != 1 || queries[0].PhysicalPlan.Engine != ir.PhysicalEngineAQL {
		t.Fatalf("compiled query did not retain one finalized AQL plan: %#v", queries)
	}
	rendered, err := aql.RenderPhysicalPlan(queries[0].PhysicalPlan)
	if err != nil {
		t.Fatalf("render retained plan: %v", err)
	}
	if rendered.Query != queries[0].Query || !reflect.DeepEqual(rendered.BindVars, queries[0].BindVars) {
		t.Fatalf("retained plan differs from executed query/binds: queryMatch=%t bindMatch=%t", rendered.Query == queries[0].Query, reflect.DeepEqual(rendered.BindVars, queries[0].BindVars))
	}
	if got := queries[0].BindVars["limit"]; got != 7 {
		t.Fatalf("retained execution plan lost its final preview window, limit bind=%#v", got)
	}
}
