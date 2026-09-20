package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileRecipeRootExpansionBuildsOccurrenceIdentity(t *testing.T) {
	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedPatients", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		Fields:   []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Expand:   &recipe.Expansion{OwnerOccurrenceID: "patient-root", From: recipe.Expression{Select: "root.identifier[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	})
	if len(output.RowIdentity.Fields) != 3 || output.RowIdentity.Fields[2] != "__loom_expansion_identity" {
		t.Fatalf("row identity fields = %#v", output.RowIdentity.Fields)
	}
	unnest := findRecipeUnnest(t, output.Plan)
	if unnest.Owner.OccurrenceID != "patient-root" || len(unnest.Owner.Route) != 0 || unnest.Owner.OwnerVariable != "root" {
		t.Fatalf("root expansion owner = %#v", unnest.Owner)
	}
	if got := ir.PhysicalUnnestSortKeys(*unnest); len(got) != 3 || got[0].Variable != "root" || got[1].Variable != "__loom_has_expanded_item" || got[2].Variable != "position" {
		t.Fatalf("root expansion sort keys = %#v", got)
	}
	identity := findExpansionIdentityProjection(t, output.Plan)
	fields := make(map[string]ir.PhysicalExpression)
	for _, field := range identity.Object.Fields {
		fields[field.Name] = field.Expression
	}
	if fields["item_present"].Value == nil || fields["item_present"].Value.Variable != "__loom_has_expanded_item" {
		t.Fatalf("item/empty discriminator = %#v", fields["item_present"])
	}
	if fields["ordinal"].Value == nil || fields["ordinal"].Value.Variable != "position" {
		t.Fatalf("ordinal identity = %#v", fields["ordinal"])
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LENGTH(__loom_physical_unnest_source_0) == 0 ? [null]",
		"LET __loom_has_expanded_item = __loom_physical_unnest_index_0 != null",
		"LET position = __loom_physical_unnest_index_0",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("root expansion query missing %q:\n%s", want, rendered.Query)
		}
	}
	if !containsExpansionIdentityName(rendered.BindVars) {
		t.Fatalf("rendered bind vars do not carry the hidden identity projection name: %#v", rendered.BindVars)
	}
}

func TestCompileRecipeRelatedExpansionReusesDeepOwnerRoute(t *testing.T) {
	output := compileExpansionRecipeOutput(t, recipe.Output{
		Name: "ExpandedDocuments", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingAlias,
		Fields:                []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Expand:                &recipe.Expansion{OwnerOccurrenceID: "document-owner", From: recipe.Expression{Select: "document.identifier[]"}, As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionError},
		Identity:              &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{
			{
				Name: "subject_Patient", OccurrenceID: "specimen-owner", Alias: "specimen", ToResourceType: "Specimen",
				Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "specimen.id"}}},
				Traversals: []recipe.Traversal{{
					Name: "subject_Specimen", OccurrenceID: "document-owner", Alias: "document", ToResourceType: "DocumentReference",
					Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "document.id"}}},
				}},
			},
			{
				Name: "focus_Patient", OccurrenceID: "focus-owner", Alias: "focus", ToResourceType: "Observation",
				Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "focus.id"}}},
			},
		},
	})
	var expansion *ir.PhysicalUnnest
	sets := map[string]bool{}
	for _, operation := range output.Plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			expansion = operation.Unnest
		}
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			sets[operation.Source.SemanticNode] = true
		}
	}
	if expansion == nil || len(expansion.Owner.Route) != 2 || expansion.Owner.OccurrenceID != "document-owner" || expansion.Owner.OwnerVariable != "__loom_expansion_node_2" {
		t.Fatalf("compiled related expansion = %#v", expansion)
	}
	if expansion.Owner.Route[0].OccurrenceID != "specimen-owner" || expansion.Owner.Route[1].OccurrenceID != "document-owner" {
		t.Fatalf("route occurrence order = %#v", expansion.Owner.Route)
	}
	if sets["specimen"] || sets["document"] || !sets["focus"] {
		t.Fatalf("selected-path reuse / sibling row-set layout = %#v", sets)
	}
	identity := findExpansionIdentityProjection(t, output.Plan)
	var edgeWitnesses []string
	for _, field := range identity.Object.Fields {
		if strings.HasPrefix(field.Name, "edge_") {
			edgeWitnesses = append(edgeWitnesses, field.Name)
		}
	}
	if len(edgeWitnesses) != 2 || edgeWitnesses[0] != "edge_0_id" || edgeWitnesses[1] != "edge_1_id" {
		t.Fatalf("ordered route witnesses = %#v", edgeWitnesses)
	}
	rendered, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FOR __loom_expansion_edge_1 IN @@expansion_route_1_edge_collection",
		"FOR __loom_expansion_edge_2 IN @@expansion_route_2_edge_collection",
		"FILTER ASSERT(LENGTH(__loom_physical_unnest_source_0) > 0",
		"__loom_expansion_edge_1._id",
		"__loom_expansion_edge_2._id",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("related expansion query missing %q:\n%s", want, rendered.Query)
		}
	}
}

func compileExpansionRecipeOutput(t *testing.T, output recipe.Output) CompiledRecipeOutput {
	t.Helper()
	plan, err := semantic.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "row-expansion", TranslationVersion: "test", Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled output count = %d", len(compiled.Outputs))
	}
	return compiled.Outputs[0]
}

func findRecipeUnnest(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalUnnest {
	t.Helper()
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalUnnestOp && plan.Operations[index].Unnest != nil {
			return plan.Operations[index].Unnest
		}
	}
	t.Fatal("compiled plan has no row expansion")
	return nil
}

func findExpansionIdentityProjection(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalExpression {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "__loom_expansion_identity" {
				if !projection.Hidden || projection.Expression == nil || projection.Expression.Object == nil {
					t.Fatalf("expansion identity projection = %#v", projection)
				}
				return projection.Expression
			}
		}
	}
	t.Fatal("compiled plan has no hidden expansion identity object")
	return nil
}

func containsExpansionIdentityName(bindVars map[string]any) bool {
	for _, value := range bindVars {
		if value == "__loom_expansion_identity" {
			return true
		}
	}
	return false
}
