package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileExplicitGroupRowsUsesPinnedRevisionTerminal(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows", TranslationVersion: "test",
		Outputs: []recipe.Output{{Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups", GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"}}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if len(output.Plan.Operations) != 1 || output.Plan.Operations[0].Kind != ir.PhysicalGroupRowsOp {
		t.Fatalf("grouped physical operations = %#v", output.Plan.Operations)
	}
	if output.RowIdentity == nil || output.RowIdentity.Grain != "groups" || strings.Join(output.RowIdentity.Fields, ",") != "group_revision_id,group_id" {
		t.Fatalf("group row identity = %#v", output.RowIdentity)
	}
	if output.Plan.BindVars["group_rows_revision_id"] != "grouprev_1" || output.Plan.BindVars["group_rows_resource_collection"] != "Patient" {
		t.Fatalf("group row binds = %#v", output.Plan.BindVars)
	}
	if output.Plan.BindVars["project"] != "project/1" {
		t.Fatalf("explicit group project bind = %#v, want canonical selection project", output.Plan.BindVars["project"])
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ASSERT(revision != null AND revision.state == \"COMPLETE\"",
		"SORT definition.ordinal ASC, definition.groupId ASC",
		"source_identity: {project: member.ref.project",
		"__loom_row_id: {group_revision_id: revision._key, group_id: definition.groupId}",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("group query missing %q:\n%s", want, rendered.Query)
		}
	}
	if strings.Contains(rendered.Query, "FOR root IN") || strings.Contains(rendered.Query, "UNNEST") {
		t.Fatalf("group query scans roots or unnests unrelated repeated fields:\n%s", rendered.Query)
	}
}

func TestExplicitGroupRowsMemberValuesRetainTypedStageCapabilities(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows with member values", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
			Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueAll}},
			},
		}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if output.Plan.StageSequence != nil {
		t.Fatal("expected the non-composed explicit-group path without authored construction")
	}
	if len(output.Stages) != 2 || output.Stages[1].Operation != "GROUP_ROWS" {
		t.Fatalf("explicit-group stages = %#v", output.Stages)
	}
	stage := output.Stages[1]
	wantColumns := map[string]struct{ kind, cardinality string }{
		"group_label":   {kind: "string", cardinality: "required_one"},
		"group_ordinal": {kind: "integer", cardinality: "required_one"},
		"specimen_id":   {kind: "string", cardinality: "many"},
	}
	for _, column := range stage.Columns {
		if want, ok := wantColumns[column.ID]; ok {
			if column.Kind != want.kind || column.Cardinality != want.cardinality {
				t.Errorf("column %q has type %s/%s, want %s/%s", column.ID, column.Kind, column.Cardinality, want.kind, want.cardinality)
			}
			delete(wantColumns, column.ID)
		}
	}
	if len(wantColumns) != 0 {
		t.Errorf("group stage omitted required typed columns: %#v", wantColumns)
	}
	capabilities := make(map[recipe.ConstructionOperationKind]StageOperationCapability, len(stage.Capabilities))
	for _, capability := range stage.Capabilities {
		capabilities[capability.Operation] = capability
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionFilterOp, recipe.ConstructionOperationKind("ROW_VALUES")} {
		if capability, ok := capabilities[operation]; !ok || !capability.Supported {
			t.Errorf("GROUP_ROWS capability %q = %#v, supported = %t; want supported", operation, capability, ok && capability.Supported)
		}
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionCodedGroupOp, recipe.ConstructionCodedPivotOp} {
		if capability, ok := capabilities[operation]; !ok || capability.Supported {
			t.Errorf("GROUP_ROWS capability %q = %#v, want the schema-aware typed capability to remain unsupported", operation, capability)
		}
	}
}

func TestCompileExplicitGroupRowsWithSourceOnlyConstructionMetadata(t *testing.T) {
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "group rows with source metadata", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "GroupedPatients", RootResourceType: "Patient", RowGrain: "groups",
			Fields:       []recipe.Field{{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
			Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "gender", Name: "gender", Type: "string"}}, Steps: []recipe.ConstructionStep{}},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "gender", Policy: recipe.ConstructionRowValueOne}},
			},
		}},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project/1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatalf("source-only projection metadata should not block explicit groups: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile explicit groups with source-only metadata: %v", err)
	}
	output := compiled.Outputs[0]
	if len(output.Plan.Operations) != 1 || output.Plan.Operations[0].Kind != ir.PhysicalGroupRowsOp {
		t.Fatalf("grouped physical operations = %#v, want the explicit-group source", output.Plan.Operations)
	}
	if got := compiledSchemaNames(output.OutputSchema); !equalStringSlices(got, []string{"group_revision_id", "group_id", "group_label", "group_ordinal", "members", "__loom_row_id", "gender"}) {
		t.Fatalf("source-only group output schema = %#v", got)
	}
	if len(output.Stages) != 2 || output.Stages[1].Operation != "GROUP_ROWS" {
		t.Fatalf("source-only explicit-group stage descriptors = %#v", output.Stages)
	}
	for _, column := range output.Stages[1].Columns {
		if column.ID == "" || column.Name == "" || column.Label == "" {
			t.Fatalf("group-row stage column is missing compiler-owned identity or presentation metadata: %#v", column)
		}
	}
}

func TestCompileCohortAtPersistedConstructionBoundary(t *testing.T) {
	selectedID := "a"
	selectedGroup := "pair"
	output := recipe.Output{
		Name: "CohortComposition", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "keep_a", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedID}},
					}},
					Outputs: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
				},
				{
					ID: "keep_pair", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "group_id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &selectedGroup}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group_id", Type: "string"},
						{ID: "group_label", Name: "group_label", Type: "string"},
						{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
						{ID: "members", Name: "members", Type: "array"},
						{ID: "id", Name: "id", Type: "array"},
					},
				},
			},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "keep_a",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort composition", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got := compiled.Outputs[0]
	if got.Plan.StageSequence == nil {
		t.Fatal("composed cohort output has no typed stage sequence")
	}
	want := []string{"keep_a", recipe.ConstructionCohortGroupStageID, "keep_pair"}
	if len(got.Plan.StageSequence.Stages) != len(want) {
		t.Fatalf("effective stage sequence = %#v", got.Plan.StageSequence.Stages)
	}
	for index, id := range want {
		if got.Plan.StageSequence.Stages[index].ID != id {
			t.Fatalf("stage %d ID = %q, want %q", index, got.Plan.StageSequence.Stages[index].ID, id)
		}
	}
	if got.Plan.StageSequence.Stages[1].Kind != ir.PhysicalStageCohortGroupOp {
		t.Fatalf("cohort stage kind = %q", got.Plan.StageSequence.Stages[1].Kind)
	}
	if got.Stages[2].Columns == nil {
		t.Fatal("cohort stage omitted the compiler-owned output capability schema")
	}
	capabilities := make(map[recipe.ConstructionOperationKind]StageOperationCapability, len(got.Stages[1].Capabilities))
	for _, capability := range got.Stages[1].Capabilities {
		capabilities[capability.Operation] = capability
	}
	for _, operation := range []recipe.ConstructionOperationKind{recipe.ConstructionFilterOp} {
		if capability, ok := capabilities[operation]; !ok || !capability.Supported {
			t.Errorf("composed cohort capability %q = %#v, supported = %t; want supported", operation, capability, ok && capability.Supported)
		}
	}
	rendered, err := aql.RenderPhysicalPlan(got.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, wantQuery := range []string{
		"LET __loom_construction_stage_1 = (",
		"FILTER", "resource._key IN", "EXPLICIT_GROUP_SOURCE_STALE",
		"LET __loom_construction_stage_3 = (", "group_id",
	} {
		if !strings.Contains(rendered.Query, wantQuery) {
			t.Fatalf("composed AQL is missing %q:\n%s", wantQuery, rendered.Query)
		}
	}
}

func TestCohortAfterGroupUsesRetainedRootContributorSet(t *testing.T) {
	output := recipe.Output{
		Name: "GroupedCohort", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{
			{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "status", ColumnID: "status", Expr: recipe.Expression{Select: "root.active"}},
		},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}, {ID: "status", Name: "status", Type: "boolean"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status", Keys: []recipe.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_key"}},
					Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "records"}},
					MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				}},
				Outputs: []recipe.StageColumn{{ID: "status_key", Name: "status_key", Type: "boolean"}, {ID: "records", Name: "records", Type: "integer"}},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE", AfterStepID: "group_status",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "grouped cohort", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project-1", SelectionProject: "project-1", DatasetGeneration: "generation-1"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "project-1", "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	physical, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	compiled := physical.Outputs[0]
	if compiled.Plan.StageSequence == nil || len(compiled.Plan.StageSequence.Stages) != 2 {
		t.Fatalf("group→cohort plan has no two-stage sequence: %#v", compiled.Plan.StageSequence)
	}
	group, cohort := compiled.Plan.StageSequence.Stages[0], compiled.Plan.StageSequence.Stages[1]
	if group.Group == nil || group.Group.RootContributorOutputColumn != rootContributorSetColumn {
		t.Fatalf("preceding GROUP did not retain root contributor identity: %#v", group)
	}
	if cohort.CohortGroup == nil || cohort.CohortGroup.ContributorInputColumn != rootContributorSetColumn {
		t.Fatalf("cohort did not consume the compiler-proven contributor set: %#v", cohort)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "resource._key IN") || !strings.Contains(rendered.Query, "UNIQUE(FLATTEN") {
		t.Fatalf("cohort did not intersect pinned members with retained grouped contributors:\n%s", rendered.Query)
	}
}
