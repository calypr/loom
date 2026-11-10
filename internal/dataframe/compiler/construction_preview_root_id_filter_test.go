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
	assertConstructionRootIDFilter(t, testAQLText(t, query.Command), "Specimen", id)
	if got := testAQLBinds(t, query.Command)["construction_filter_value"]; got != id {
		t.Fatalf("filter bind = %#v, want %q", got, id)
	}
	if testAQLBinds(t, query.Command)["auth_resource_paths_unrestricted"] != false || testAQLBinds(t, query.Command)["scope_allowed"] != true {
		t.Fatalf("root ID fast path did not preserve the restricted auth-scope binds: %#v", testAQLBinds(t, query.Command))
	}
	if paths, ok := testAQLBinds(t, query.Command)["auth_resource_paths"].([]string); !ok || len(paths) != 1 || paths[0] != "Specimen/"+id {
		t.Fatalf("root ID fast path changed auth paths: %#v", testAQLBinds(t, query.Command)["auth_resource_paths"])
	}

	page, err := CompileRecipeOutputPageWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilter(t, testAQLText(t, page.RootKeys), "Specimen", id)
	if !strings.Contains(testAQLText(t, page.RootKeys), "payload.id") {
		t.Fatalf("root-key query lost the exact payload ID recheck:\n%s", testAQLText(t, page.RootKeys))
	}
	if !strings.Contains(testAQLText(t, page.Rows), "root.id") || !strings.Contains(testAQLText(t, page.Rows), "@construction_filter_value") {
		t.Fatalf("selected-root query lost the root seek or construction predicate:\n%s", testAQLText(t, page.Rows))
	}
	if !strings.Contains(testAQLText(t, page.Rows), "FILTER __loom_construction_input_1.specimen_id == @construction_filter_value") {
		t.Fatalf("selected-root query lost the original source-column filter:\n%s", testAQLText(t, page.Rows))
	}

	fullQuery, err := CompileRecipeOutputWithPolicy(compiled, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilterNotInjected(t, testAQLText(t, fullQuery.Command))

	fullBindings := bindings
	fullBindings.PreviewLimit = 0
	fullPage, err := CompileRecipeOutputPageWithPolicy(compiled, fullBindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertConstructionRootIDFilterNotInjected(t, testAQLText(t, fullPage.RootKeys))
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
	if strings.Contains(testAQLText(t, query.Command), "FILTER root.id == @construction_filter_value") {
		t.Fatalf("non-ID source filter was pushed onto root.id:\n%s", testAQLText(t, query.Command))
	}
	if !strings.Contains(testAQLText(t, query.Command), "@construction_filter_value") {
		t.Fatalf("original non-ID construction filter was lost:\n%s", testAQLText(t, query.Command))
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
