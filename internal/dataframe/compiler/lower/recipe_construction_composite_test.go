package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestComposeClickHouseCombineRendersExactUnboundedAQLPrefix(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project", DatasetGeneration: "generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"},
		IncludeAuthResourcePath: true,
	}
	prefix := compileCompositePrefixTestOutput(t, constructionTestOutput(), bindings)
	combine := compositeAppendTestPlan(prefix.Plan.StageSequence.FinalStageID)

	composite, err := ComposeClickHouseCombine(prefix.Plan, combine, bindings)
	if err != nil {
		t.Fatalf("ComposeClickHouseCombine() error = %v", err)
	}
	rendered, err := aql.RenderClickHousePrefix(composite)
	if err != nil {
		t.Fatalf("RenderClickHousePrefix() error = %v", err)
	}
	for _, expected := range []string{"TABLE_UNPIVOT", "@construction_private_auth_resource_path"} {
		if !strings.Contains(rendered.Query, expected) {
			t.Errorf("AQL prefix is missing %q: %s", expected, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "LIMIT @") {
		t.Fatalf("private prefix unexpectedly has a preview limit: %s", rendered.Query)
	}
	if rendered.BindVars["construction_private_auth_resource_path"] != "/programs/p1" {
		t.Fatalf("private prefix auth path bind = %#v", rendered.BindVars["construction_private_auth_resource_path"])
	}

	mismatchedScope := ir.ClonePhysicalPlan(composite)
	mismatchedScope.ClickHousePrefix.AuthResourcePaths = []string{"/programs/p2"}
	if _, err := aql.RenderClickHousePrefix(mismatchedScope); err == nil || !strings.Contains(err.Error(), "scope differs from the AQL authorization bindings") {
		t.Fatalf("mismatched composite scope error = %v", err)
	}
	mismatchedStage := ir.ClonePhysicalPlan(composite)
	mismatchedStage.ClickHouseCombine.Inputs[0].PrivateStageID = "different_stage"
	if _, err := aql.RenderClickHousePrefix(mismatchedStage); err == nil || !strings.Contains(err.Error(), "does not match the exact AQL prefix stage") {
		t.Fatalf("mismatched composite stage error = %v", err)
	}
}

func TestComposeClickHouseCombineRejectsRestrictedMultiPathPrefix(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project", DatasetGeneration: "generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/a", "/programs/b"},
		IncludeAuthResourcePath: true,
	}
	prefix := compileCompositePrefixTestOutput(t, constructionTestOutput(), bindings)
	combine := compositeAppendTestPlan(prefix.Plan.StageSequence.FinalStageID)

	if _, err := ComposeClickHouseCombine(prefix.Plan, combine, bindings); err == nil || !strings.Contains(err.Error(), "exactly one immutable authorization path") {
		t.Fatalf("multi-path restricted prefix error = %v", err)
	}
}

func TestComposeClickHouseCombineRejectsGroupPrefix(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project", DatasetGeneration: "generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"},
		IncludeAuthResourcePath: true,
	}
	output := constructionTestOutput()
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID:     "grouped",
		Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_rows",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "group_id", OutputColumnID: "grouped_id"}},
			Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "grouped_id", Name: "grouped", Type: "string"},
			{ID: "row_count", Name: "row_count", Type: "integer"},
		},
	}}
	prefix := compileCompositePrefixTestOutput(t, output, bindings)
	combine := compositeAppendTestPlan(prefix.Plan.StageSequence.FinalStageID)

	if _, err := ComposeClickHouseCombine(prefix.Plan, combine, bindings); err == nil || !strings.Contains(err.Error(), "GROUP prefixes cannot feed") {
		t.Fatalf("GROUP prefix error = %v", err)
	}
}

func compileCompositePrefixTestOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "composite-test",
		TranslationVersion: "test", Outputs: []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan() error = %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-digest", bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan() error = %v", err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan() error = %v", err)
	}
	return compiled.Outputs[0]
}

func compositeAppendTestPlan(stageID string) ir.PhysicalClickHouseCombine {
	return ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{PrivateStageID: stageID},
			{TableID: "table-right", RevisionID: "revision-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "amount", InputIndex: 0, InputColumnID: "amount_id"},
			{OutputColumnID: "amount", InputIndex: 1, InputColumnID: "right_amount"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "amount", Name: "amount", LogicalType: "integer", ClickHouseType: "Int64"}},
	}
}
