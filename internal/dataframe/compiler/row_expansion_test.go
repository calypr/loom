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

func TestExpandedWindowSortsStableIdentityBeforeUnrelatedSets(t *testing.T) {
	output := compileWindowExpansionOutput(t)
	window, err := withGenericPhysicalExecutionWindow(output.Plan, 25)
	if err != nil {
		t.Fatal(err)
	}
	unnestIndex, sortIndex, siblingSetIndex := -1, -1, -1
	var unnest *ir.PhysicalUnnest
	for index, operation := range window.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			unnestIndex, unnest = index, operation.Unnest
		}
		if operation.Kind == ir.PhysicalSortOp {
			sortIndex = index
		}
		if operation.Kind == ir.PhysicalSetOp && operation.Source.SemanticNode == "focus" {
			siblingSetIndex = index
		}
	}
	if unnest == nil || unnestIndex >= sortIndex || sortIndex >= siblingSetIndex {
		t.Fatalf("expanded window positions: unnest=%d sort=%d sibling-set=%d", unnestIndex, sortIndex, siblingSetIndex)
	}
	sort := window.Operations[sortIndex].Sort
	wantKeys := ir.PhysicalUnnestSortKeys(*unnest)
	if len(sort.Keys) != len(wantKeys) {
		t.Fatalf("sort key count = %d, want %d: %#v", len(sort.Keys), len(wantKeys), sort.Keys)
	}
	for index := range wantKeys {
		if !reflect.DeepEqual(sort.Keys[index], wantKeys[index]) {
			t.Fatalf("sort key %d = %#v, want %#v", index, sort.Keys[index], wantKeys[index])
		}
	}
	rendered, err := aql.RenderPhysicalPlan(window)
	if err != nil {
		t.Fatal(err)
	}
	query := rendered.Query
	rootRoute := strings.Index(query, "FOR __loom_expansion_edge_1 IN @@expansion_route_1_edge_collection")
	terminalRoute := strings.Index(query, "FOR __loom_expansion_edge_2 IN @@expansion_route_2_edge_collection")
	windowSort := strings.Index(query, "SORT root._key ASC, __loom_expansion_edge_1._key ASC, __loom_expansion_edge_2._key ASC, __loom_expansion_node_2._key ASC, __loom_has_expanded_item ASC, position ASC")
	siblingSet := strings.Index(query, "LET child_set_1 =")
	if rootRoute < 0 || terminalRoute <= rootRoute || windowSort <= terminalRoute || siblingSet <= windowSort {
		t.Fatalf("query did not keep owner route, stable expanded window, and unrelated set ordered:\n%s", query)
	}
	if strings.Contains(query[:windowSort], "LET child_set_1 =") || strings.Count(query, "FILTER ASSERT(LENGTH(__loom_physical_unnest_source_0) > 0") != 1 {
		t.Fatalf("selected route was rematerialized or expansion boundary was duplicated:\n%s", query)
	}
}

func compileWindowExpansionOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "expanded-window", TranslationVersion: "test",
		Outputs: []recipe.Output{{
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
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}
