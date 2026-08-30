package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
)

type invalidRecipeRegistry struct{}

func (invalidRecipeRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{Bundle: recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "default",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "missing", Expr: recipe.Expression{Select: "root.missing"}}},
		}},
	}}, nil
}

func (r invalidRecipeRegistry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}

func TestMaterializeMarksResolutionFailures(t *testing.T) {
	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = engine.Materialize(context.Background(), "default", recipe.RuntimeBindings{Project: "P1"}, nil)
	var resolution *ResolutionError
	if !errors.As(err, &resolution) {
		t.Fatalf("error = %v, want ResolutionError", err)
	}
}

func testResolvedBundle(dynamicColumns []string) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "resolved",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			DynamicColumns: []recipe.DynamicColumn{{
				Name: "identifier", Source: recipe.Expression{Select: "root.identifier[].value"},
				Columns: dynamicColumns,
			}},
		}},
	}
}

func testEngine(queryRows QueryRows) *Engine {
	engine, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows:   queryRows,
	})
	if err != nil {
		panic(err)
	}
	return engine
}

func TestPreviewOutputFiltersInternalColumnsAndReturnsSafePlanSummary(t *testing.T) {
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			"id":                          "p1",
			"_key":                        "internal-key",
			"__loom_row_id":               "internal-row-id",
			"auth_resource_path":          "/private/path",
			"__loom_dynamic_runtime_keys": map[string]any{"family": []string{"x"}},
		})
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Output != "Patient" || summary.RowCount != 1 || summary.PlanMode != "physical" || summary.PlanProfile != "generic_fhir_graph_recipe" || summary.PlanFingerprint == "" {
		t.Fatalf("unsafe or incomplete preview summary: %#v", summary)
	}
	if len(rows) != 1 || rows[0]["id"] != "p1" || len(rows[0]) != 1 {
		t.Fatalf("preview row was not public-only: %#v", rows)
	}
}

func TestPreviewOutputExecutesOnlyRequestedOutputAndRejectsUnknownBeforeQuery(t *testing.T) {
	queries := 0
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		queries++
		return visit(map[string]any{"id": "p1"})
	})
	bundle := testResolvedBundle([]string{})
	bundle.Outputs = append(bundle.Outputs, recipe.Output{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Observation", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("query count = %d, want 1", queries)
	}
	_, err = e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Missing", Limit: 1}, func(map[string]any) error { return nil })
	if err == nil || queries != 1 {
		t.Fatalf("unknown output err=%v query count=%d, want error before query", err, queries)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("unknown output error = %v, want INVALID_REQUEST", err)
	}
}

func TestPreviewOutputEnforcesLimitAndCancellation(t *testing.T) {
	rowsSeen := 0
	e := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for index := 0; index < 10; index++ {
			rowsSeen++
			if err := visit(map[string]any{"id": fmt.Sprintf("p%d", index)}); err != nil {
				return err
			}
		}
		return nil
	})
	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	summary, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(map[string]any) error {
		count++
		return nil
	})
	if err != nil || count != 2 || summary.RowCount != 2 || rowsSeen != 3 {
		t.Fatalf("limit count=%d summary=%#v rowsSeen=%d err=%v", count, summary, rowsSeen, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.PreviewOutput(ctx, resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil })
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("canceled preview error = %v, want CLIENT_CANCELED", err)
	}

	cancelEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		if err := visit(map[string]any{"id": "p1"}); err != nil {
			return err
		}
		return visit(map[string]any{"id": "p2"})
	})
	resolved, err = cancelEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	_, err = cancelEngine.PreviewOutput(ctx, resolved, PreviewRequest{Output: "Patient", Limit: 2}, func(map[string]any) error {
		cancel()
		return nil
	})
	userErr, ok = dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("mid-query canceled preview error = %v, want CLIENT_CANCELED", err)
	}
}

func TestPreviewOutputNormalizesVisitorAndDynamicSchemaErrors(t *testing.T) {
	driftEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			"id":                          "p1",
			"value":                       "v",
			"__loom_dynamic_runtime_keys": map[string]any{"identifier": []string{"unexpected"}},
		})
	})
	resolved, err := driftEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{"value"}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = driftEngine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil })
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDynamicSchemaDrift) {
		t.Fatalf("dynamic drift error = %v, want DYNAMIC_SCHEMA_DRIFT", err)
	}

	visitorErr := errors.New("visitor failed")
	visitorEngine := testEngine(func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"id": "p1"})
	})
	resolved, err = visitorEngine.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = visitorEngine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return visitorErr })
	userErr, ok = dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInternalError) || !errors.Is(err, visitorErr) {
		t.Fatalf("visitor error = %v, want typed internal error preserving cause", err)
	}
}

func TestValidatePreviewPlanRequiresCanonicalPhysicalClass(t *testing.T) {
	valid := compiler.CompiledQuery{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}}
	if err := validatePreviewPlan(valid, 2); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []compiler.CompiledQuery{
		{PlanMode: "logical", PlanProfile: previewPlanProfile, Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
		{PlanMode: previewPlanMode, PlanProfile: "other", Limit: 2, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
		{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 2},
		{PlanMode: previewPlanMode, PlanProfile: previewPlanProfile, Limit: 3, PlanDiagnostics: ir.CompilerPlanDiagnostics{Fingerprint: "fingerprint"}},
	} {
		if err := validatePreviewPlan(invalid, 2); err == nil {
			t.Fatalf("invalid plan %#v was admitted", invalid)
		} else if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodePlanTooExpensive) {
			t.Fatalf("invalid plan error = %v, want PLAN_TOO_EXPENSIVE", err)
		}
	}
}

func TestPreviewLimitsHaveStableDefaultAndMaximum(t *testing.T) {
	if got, err := normalizePreviewLimit(0); err != nil || got != DefaultPreviewLimit {
		t.Fatalf("default preview limit = %d, err=%v; want %d", got, err, DefaultPreviewLimit)
	}
	if got, err := normalizePreviewLimit(MaxPreviewLimit); err != nil || got != MaxPreviewLimit {
		t.Fatalf("maximum preview limit = %d, err=%v; want %d", got, err, MaxPreviewLimit)
	}
	for _, limit := range []int{-1, MaxPreviewLimit + 1} {
		if _, err := normalizePreviewLimit(limit); err == nil {
			t.Fatalf("limit %d was accepted", limit)
		}
	}
}

func TestCompileResolvedBundleSkipsCatalogResolverAndRetainsBundle(t *testing.T) {
	resolverCalls := 0
	e, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ResolveBundle: func(context.Context, recipe.Bundle, recipe.RuntimeBindings) (recipe.Bundle, error) {
			resolverCalls++
			return recipe.Bundle{}, errors.New("catalog resolver must not be called")
		},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	bundle := testResolvedBundle([]string{"value"})
	resolved, err := e.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("catalog resolver calls = %d, want 0", resolverCalls)
	}
	if resolved.Bundle.Name != bundle.Name || len(resolved.Bundle.Outputs) != 1 {
		t.Fatalf("resolved bundle was not retained: %#v", resolved.Bundle)
	}
	if len(resolved.Compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(resolved.Compiled.Outputs))
	}
	if _, err := e.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 0 {
		t.Fatalf("catalog resolver calls during PreviewOutput = %d, want 0", resolverCalls)
	}
}

func TestResolveBundleUsesResolverButResolvedPreviewAndMaterializeDoNot(t *testing.T) {
	resolverCalls := 0
	e, err := New(Config{
		Registry: invalidRecipeRegistry{},
		ResolveBundle: func(_ context.Context, bundle recipe.Bundle, _ recipe.RuntimeBindings) (recipe.Bundle, error) {
			resolverCalls++
			return bundle, nil
		},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"id": "p1"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := testResolvedBundle([]string{})
	bindings := recipe.RuntimeBindings{Project: "P1", PreviewLimit: 1}
	normal, err := e.ResolveBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 {
		t.Fatalf("catalog resolver calls after ResolveBundle = %d, want 1", resolverCalls)
	}
	direct, err := e.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	normalStreams, err := e.Streams(context.Background(), normal)
	if err != nil {
		t.Fatal(err)
	}
	directStreams, err := e.Streams(context.Background(), direct)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalStreams) != len(directStreams) || normalStreams[0].query != directStreams[0].query {
		t.Fatalf("resolved output differs from catalog-resolved output")
	}
	if _, err := e.PreviewResolvedBundle(context.Background(), bundle, bindings); err != nil {
		t.Fatal(err)
	}
	if _, err := e.MaterializeResolvedBundle(context.Background(), bundle, bindings, nil); err != nil {
		t.Fatal(err)
	}
	if resolverCalls != 1 {
		t.Fatalf("catalog resolver calls after resolved operations = %d, want 1", resolverCalls)
	}
}

func TestCompileResolvedBundleRejectsUnresolvedDynamicDeclarations(t *testing.T) {
	e, err := New(Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "test-scope" },
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.CompileResolvedBundle(context.Background(), testResolvedBundle(nil), recipe.RuntimeBindings{Project: "P1"})
	if err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("error = %v, want unresolved dynamic declaration", err)
	}

	resolved, err := e.CompileResolvedBundle(context.Background(), testResolvedBundle([]string{}), recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatalf("explicit empty dynamic family rejected: %v", err)
	}
	if len(resolved.Compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(resolved.Compiled.Outputs))
	}
}
