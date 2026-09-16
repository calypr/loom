package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestPopulationSemijoinDirectRootRendersIndexedMemberScan(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, want := range []string{
		"FOR root IN @@root_collection",
		"FOR population_member IN @@population_members_collection",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.resourceType == @population_resource_type",
		"population_member.id == root.id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered population query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@population_members_collection"]; got != "loom_explorer_selection_members" {
		t.Fatalf("population member collection bind = %#v", got)
	}
	if got := rendered.BindVars["population_project"]; got != "project/a" {
		t.Fatalf("population project bind = %#v", got)
	}
}

func TestPopulationSemijoinDirectRootRetainsHiddenMatchedMembers(t *testing.T) {
	rendered, output := compilePopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	for _, want := range []string{
		"SORTED_UNIQUE((",
		"SORT population_member.id",
		"population_member.id == root.id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("direct population provenance query is missing %q:\n%s", want, rendered.Query)
		}
	}
	if len(output.OutputSchema) == 0 || output.OutputSchema[len(output.OutputSchema)-1].Name != "__loom_population_members" || !output.OutputSchema[len(output.OutputSchema)-1].Internal {
		t.Fatalf("population member projection is not hidden in output schema: %#v", output.OutputSchema)
	}
}

func TestPopulationSemijoinSpecimenDocumentReferenceSubjectRouteRendersInboundTraversal(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 1,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	for _, want := range []string{
		"FOR population_node_0, population_edge_0 IN 1..1 INBOUND root @@population_route_0_edge_collection",
		"population_edge_0.label == @population_route_0_label",
		"population_node_0.resourceType == @population_route_0_target_type",
		"population_member.id == population_node_0.id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered routed population query is missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestPopulationSemijoinReversedRouteRetainsMatchedMembersAtTerminalNode(t *testing.T) {
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
		"FOR population_node_0, population_edge_0 IN 1..1 INBOUND root @@population_route_0_edge_collection",
		"population_member.id == population_node_0.id",
		"SORTED_UNIQUE((",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("reversed population provenance query is missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestPopulationSemijoinProvenanceDeduplicatesSharedTargets(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-documents", MembershipDigest: "sha256:members", MemberCount: 3,
			ResourceType: "DocumentReference",
			Route:        []recipe.PopulationRouteStep{{ResourceType: "DocumentReference", Relationship: "subject_Specimen"}},
		},
	})
	if !strings.Contains(rendered.Query, "SORTED_UNIQUE((") {
		t.Fatalf("shared-target provenance is not sorted and unique:\n%s", rendered.Query)
	}
}

func TestPopulationSemijoinProvenanceExcludesNonmatchingMembers(t *testing.T) {
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
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"population_member.id == population_node_0.id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("nonmatching member filter is missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestPopulationSemijoinProvenanceOrdersMembersDeterministically(t *testing.T) {
	rendered := renderPopulationRecipe(t, recipe.Output{
		Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
			ResourceType: "Specimen",
		},
	})
	sortIndex := strings.Index(rendered.Query, "SORT population_member.id")
	returnIndex := strings.Index(rendered.Query, "RETURN population_member.id")
	if sortIndex < 0 || returnIndex < 0 || sortIndex > returnIndex {
		t.Fatalf("population provenance member order is not deterministic:\n%s", rendered.Query)
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
