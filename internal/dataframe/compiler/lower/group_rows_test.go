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
