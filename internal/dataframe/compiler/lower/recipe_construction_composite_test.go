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

func TestComposeClickHouseCombineRejectsCohortGroupPrefixAndIRBypass(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"},
		IncludeAuthResourcePath: true, SelectionMembersCollection: "loom_explorer_selection_members",
	}
	value := "patient-1"
	output := recipe.Output{
		Name: "cohort_prefix", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "source_filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "patient_id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}},
				}},
				Outputs: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_cohort", UnassignedMemberPolicy: "EXCLUDE", AfterStepID: "source_filter",
		},
	}
	prefix := compileCompositePrefixTestOutput(t, output, bindings)
	rendered, err := aql.RenderPhysicalPlan(prefix.Plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() for FILTER→COHORT_GROUP: %v", err)
	}
	for _, key := range []string{
		"cohort_group_rows_revision_collection", "cohort_group_rows_selection_collection",
		"cohort_group_rows_selection_members_collection", "cohort_group_rows_membership_collection",
	} {
		if _, ok := rendered.BindVars["@"+key]; !ok {
			t.Errorf("detached cohort source collection bind @%s is missing: %#v", key, rendered.BindVars)
		}
		if _, ok := rendered.BindVars[key]; ok {
			t.Errorf("detached cohort source retained scalar duplicate bind %q", key)
		}
	}
	combine := compositeAppendTestPlan(prefix.Plan.StageSequence.FinalStageID)
	if _, err := ComposeClickHouseCombine(prefix.Plan, combine, bindings); err == nil || !strings.Contains(err.Error(), "COHORT_GROUP prefixes cannot feed") {
		t.Fatalf("COHORT_GROUP prefix error = %v", err)
	}

	// Construct a malformed-at-the-public-seam composite directly to ensure
	// PhysicalPlan validation independently closes the same authorization gap.
	forged := ir.ClonePhysicalPlan(prefix.Plan)
	forged.Engine = ir.PhysicalEngineClickHouse
	forged.ClickHouseCombine = &combine
	const authPathKey = "construction_private_auth_resource_path"
	forged.BindVars[authPathKey] = bindings.AuthResourcePaths[0]
	forged.StageSequence.OutputAuthResourcePathBindKey = authPathKey
	forged.ClickHousePrefix = &ir.PhysicalClickHousePrefix{
		StageID: prefix.Plan.StageSequence.FinalStageID, AuthScopeMode: string(bindings.AuthScopeMode),
		AuthResourcePaths:       append([]string(nil), bindings.AuthResourcePaths...),
		IncludeAuthResourcePath: true, AuthResourcePathBindKey: authPathKey,
	}
	if err := forged.ClickHousePrefix.Validate(forged.StageSequence, forged.BindVars); err == nil || !strings.Contains(err.Error(), "COHORT_GROUP prefixes cannot feed") {
		t.Fatalf("forged ClickHouse prefix validation error = %v", err)
	}
	if err := forged.Validate(); err == nil || !strings.Contains(err.Error(), "COHORT_GROUP prefixes cannot feed") {
		t.Fatalf("forged ClickHouse physical plan validation error = %v", err)
	}
}

func TestCohortRootCandidateScanKeepsFullInputAfterAggregatePrefix(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "project", SelectionProject: "project", DatasetGeneration: "generation",
		AuthScopeMode: authscope.ReadScopeUnrestricted, SelectionMembersCollection: "loom_explorer_selection_members",
	}
	output := recipe.Output{
		Name: "aggregate_before_cohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "aggregate", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "aggregate_patients",
						Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "patient_id", OutputColumnID: "group_patient_id"}},
						Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_patient_id", Name: "patient_id", Type: "string"},
						{ID: "row_count", Name: "row_count", Type: "integer"},
					},
				},
				{
					ID: "filter_aggregate", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "aggregate"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "row_count", Operator: recipe.FilterExists,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_patient_id", Name: "patient_id", Type: "string"},
						{ID: "row_count", Name: "row_count", Type: "integer"},
					},
				},
			},
		},
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_after_aggregate", AfterStepID: "filter_aggregate", UnassignedMemberPolicy: "EXCLUDE"},
	}
	prefix := compileCompositePrefixTestOutput(t, output, bindings)
	if prefix.Plan.Operations[0].RootScan.CohortSource != nil {
		t.Fatal("candidate root scan narrowed input before GROUP→FILTER cohort prefix")
	}
	var sawGroup, sawFilter, sawCohort bool
	for _, stage := range prefix.Plan.StageSequence.Stages {
		switch stage.Kind {
		case ir.PhysicalStageGroupOp:
			sawGroup = true
		case ir.PhysicalStageFilterOp:
			sawFilter = true
		case ir.PhysicalStageCohortGroupOp:
			sawCohort = true
		}
	}
	if !sawGroup || !sawFilter || !sawCohort {
		t.Fatalf("expected typed GROUP→FILTER→COHORT_GROUP stages, saw group=%t filter=%t cohort=%t", sawGroup, sawFilter, sawCohort)
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
