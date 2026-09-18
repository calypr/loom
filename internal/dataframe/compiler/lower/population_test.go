package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestPopulationRootSourceStartsFromMembersWithoutProvenance(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, want := range []string{
		"FOR population_member IN @@population_members_collection",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"FOR population_source IN @@population_source_collection",
		"population_source.id == population_member.id",
		"COLLECT __loom_physical_population_root_key = population_source._key",
		"LET root = DOCUMENT(@@root_collection, __loom_physical_population_root_key)",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("ordinary population query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@population_members_collection"]; got != "loom_explorer_selection_members" {
		t.Fatalf("population member collection bind = %#v", got)
	}
	if got := rendered.BindVars["population_project"]; got != "project/a" {
		t.Fatalf("population project bind = %#v", got)
	}
	if strings.Contains(rendered.Query, "FOR root IN @@root_collection") || strings.Contains(rendered.Query, "SORTED_UNIQUE") || strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary population query materialized provenance:\n%s", rendered.Query)
	}
}

func TestPopulationSemijoinDirectRootHasNoHiddenProvenanceColumn(t *testing.T) {
	rendered, output := compilePopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, column := range output.OutputSchema {
		if column.Name == "__loom_population_members" || column.Name == "__loom_population_members_value" {
			t.Fatalf("ordinary output schema contains population provenance: %#v", output.OutputSchema)
		}
	}
	if strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary population query contains hidden provenance:\n%s", rendered.Query)
	}
}

func TestPopulationRootSourceReversesRouteAndRemainsScoped(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	for _, want := range []string{
		"FOR population_source IN @@population_source_collection",
		"FOR population_root_edge_0 IN @@population_root_route_0_edge_collection",
		"population_root_edge_0._from == population_source._id",
		"population_root_edge_0.label == @population_root_route_0_label",
		"population_root_node_0.resourceType == @population_root_route_0_target_type",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"population_source.id == population_member.id",
		"COLLECT __loom_physical_population_root_key = population_root_node_0._key",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("ordinary reversed population query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := strings.Count(rendered.Query, "FOR population_member IN @@population_members_collection"); got != 1 {
		t.Fatalf("membership-driven scan count = %d, want 1:\n%s", got, rendered.Query)
	}
	if strings.Contains(rendered.Query, "FOR root IN @@root_collection") || strings.Contains(rendered.Query, "SORTED_UNIQUE") || strings.Contains(rendered.Query, "__loom_population_members") {
		t.Fatalf("ordinary reversed population query materialized provenance:\n%s", rendered.Query)
	}
}

func renderPopulationRecipe(t *testing.T, output recipe.Output) aql.RenderedPhysicalPlan {
	t.Helper()
	rendered, _ := compilePopulationRecipe(t, output)
	return rendered
}

func compilePopulationRecipe(t *testing.T, output recipe.Output) (aql.RenderedPhysicalPlan, CompiledRecipeOutput) {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-test",
		TranslationVersion:  "population-test",
		Outputs:             []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d", len(compiled.Outputs))
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	return rendered, compiled.Outputs[0]
}
