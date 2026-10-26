package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestConstructionPreviewPushesLeadingRootIDFilterAndRetainsStagePredicate(t *testing.T) {
	const id = "b7cad184-db67-5542-a975-10fffa3e89e7"
	output := constructionEqualsPageOutput(id)
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"Specimen/" + id},
		PreviewLimit: 25,
	}
	compiled := compileConstructionRootIDTestOutput(t, output, bindings)

	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilter(t, query.Query, "Specimen", id)
	if got := query.BindVars["construction_filter_value"]; got != id {
		t.Fatalf("filter bind = %#v, want %q", got, id)
	}
	if query.BindVars["auth_resource_paths_unrestricted"] != false || query.BindVars["scope_allowed"] != true {
		t.Fatalf("root ID fast path did not preserve the restricted auth-scope binds: %#v", query.BindVars)
	}
	if paths, ok := query.BindVars["auth_resource_paths"].([]string); !ok || len(paths) != 1 || paths[0] != "Specimen/"+id {
		t.Fatalf("root ID fast path changed auth paths: %#v", query.BindVars["auth_resource_paths"])
	}

	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilter(t, page.RootKeysQuery, "Specimen", id)
	if !strings.Contains(page.RootKeysQuery, "payload.id") {
		t.Fatalf("root-key query lost the exact payload ID recheck:\n%s", page.RootKeysQuery)
	}
	if !strings.Contains(page.RowsQuery, "root.id") || !strings.Contains(page.RowsQuery, "@construction_filter_value") {
		t.Fatalf("selected-root query lost the root seek or construction predicate:\n%s", page.RowsQuery)
	}
	if !strings.Contains(page.RowsQuery, "FILTER __loom_construction_input_1.specimen_id == @construction_filter_value") {
		t.Fatalf("selected-root query lost the original source-column filter:\n%s", page.RowsQuery)
	}

	fullQuery, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilterNotInjected(t, fullQuery.Query)

	fullBindings := bindings
	fullBindings.PreviewLimit = 0
	fullPage, err := CompileRecipeOutputPageWithPolicy(compiled, fullBindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilterNotInjected(t, fullPage.RootKeysQuery)
}

func TestConstructionPreviewRootIDFilterRequiresExactPayloadIDProjection(t *testing.T) {
	output := constructionEqualsPageOutput("specimen-a")
	output.Fields[0].Expr.Select = "root.status"
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled := compileConstructionRootIDTestOutput(t, output, bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Query, "FILTER root.id == @construction_filter_value") {
		t.Fatalf("non-ID source filter was pushed onto root.id:\n%s", query.Query)
	}
	if !strings.Contains(query.Query, "@construction_filter_value") {
		t.Fatalf("original non-ID construction filter was lost:\n%s", query.Query)
	}
}

func compileConstructionRootIDTestOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-root-id-preview",
		TranslationVersion:  "construction-root-id-preview",
		Outputs:             []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled output count = %d, want 1", len(compiled.Outputs))
	}
	return compiled.Outputs[0]
}

func assertConstructionRootIDFilter(t *testing.T, query, resourceType, id string) {
	t.Helper()
	project := strings.Index(query, "root.project == @project")
	generation := strings.Index(query, "root.dataset_generation == @dataset_generation")
	auth := strings.Index(query, "root_scope_allowed")
	rootID := strings.Index(query, "root.id == @construction_filter_value")
	projection := strings.Index(query, "RETURN ")
	if project < 0 || generation < 0 || auth < 0 || rootID < 0 || projection < 0 || project > generation || generation > auth || auth > rootID || rootID > projection {
		t.Fatalf("root ID predicate must follow existing project/generation/auth scope and precede source projection (%s, %q):\n%s", resourceType, id, query)
	}
	if !strings.Contains(query, "root.payload.id") || strings.Count(query, "@construction_filter_value") < 2 {
		t.Fatalf("root seek must retain the source payload equality predicate (%s, %q):\n%s", resourceType, id, query)
	}
}

func assertConstructionRootIDFilterNotInjected(t *testing.T, query string) {
	t.Helper()
	if strings.Contains(query, "FILTER root.id == @construction_filter_value") {
		t.Fatalf("unbounded execution unexpectedly used the preview root-ID fast path:\n%s", query)
	}
}
