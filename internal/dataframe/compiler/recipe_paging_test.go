package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCompileRecipeOutputPageSelectsRootsBeforeExpansion(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Fields: []semantic.SemanticField{{
			Name: "id", FieldRef: "Patient.id",
			Expr: semantic.SemanticExpression{Expression: expression.Select(expression.SelectorRef{Path: "id"})}, Projection: spec.ProjectionScalar,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := spec.ParseSelector("extension[].url")
	if err != nil {
		t.Fatal(err)
	}
	unnest := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
		InputVariable: "root", OutputVariable: "item", JoinMode: ir.PhysicalUnnestInner,
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Patient", Selector: selector, ExecutionMode: ir.PhysicalSelectorConditionalArray},
		},
	}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[6:], plan.Operations[5:])
	plan.Operations[5] = unnest
	page, err := CompileRecipeOutputPageWithPolicy(lower.CompiledRecipeOutput{
		Name: "patients", RootResourceType: "Patient", Plan: plan,
	}, recipe.RuntimeBindings{Project: "p"}, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if got := page.RootKeysBindVars[RootPageSizeBind]; got != 25 {
		t.Fatalf("root page size bind = %#v, want 25", got)
	}
	if strings.Contains(page.RootKeysQuery, "unnest") || strings.Contains(page.RootKeysQuery, "extension") {
		t.Fatalf("root-key discovery crossed the expansion boundary:\n%s", page.RootKeysQuery)
	}
	keyFilter := strings.Index(page.RowsQuery, "root._key IN @"+RootPageKeysBind)
	unnestIndex := strings.Index(page.RowsQuery, "FOR item IN")
	if keyFilter < 0 || unnestIndex < 0 || keyFilter > unnestIndex {
		t.Fatalf("selected-root filter was not rendered before UNNEST:\n%s", page.RowsQuery)
	}
	if !strings.Contains(page.RootKeysQuery, "root._key > @"+RootPageAfterKeyBind) {
		t.Fatalf("root-key discovery is not a keyset query:\n%s", page.RootKeysQuery)
	}
}

func TestCompileRecipeOutputPageRetainsSinglePopulationComputation(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "population-page-test",
		TranslationVersion:  "population-page-test",
		Outputs: []recipe.Output{{
			Name: "Specimens", RootResourceType: "Specimen", RowGrain: "specimen",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			Population: &recipe.PopulationConstraint{
				SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 2,
				ResourceType: "Specimen",
			},
		}},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", SelectionProject: "project/a", DatasetGeneration: "generation-a", SelectionMembersCollection: "loom_explorer_selection_members"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	page, err := CompileRecipeOutputPageWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for name, query := range map[string]string{"root keys": page.RootKeysQuery, "rows": page.RowsQuery} {
		if got := strings.Count(query, "FOR population_member IN @@population_members_collection"); got != 1 {
			t.Fatalf("%s population member scan count = %d, want 1:\n%s", name, got, query)
		}
		if !strings.Contains(query, "FILTER LENGTH(__loom_population_members_value) > 0") {
			t.Fatalf("%s query lost the population eligibility filter:\n%s", name, query)
		}
	}
	if got := strings.Count(page.RowsQuery, "__loom_population_members_value"); got != 3 {
		t.Fatalf("paged rows matched-member value references = %d, want LET/filter/RETURN:\n%s", got, page.RowsQuery)
	}
	filterIndex := strings.Index(page.RowsQuery, "root._key IN @"+RootPageKeysBind)
	populationIndex := strings.Index(page.RowsQuery, "FILTER LENGTH(__loom_population_members_value) > 0")
	if populationIndex < 0 || filterIndex < 0 || populationIndex > filterIndex {
		t.Fatalf("population eligibility was not evaluated before selected-root paging:\n%s", page.RowsQuery)
	}
}
