package compiler

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestStreamingMissingCategoryPivotPreviewKeepsFullScopedPopulation(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "loom_dev_cda_fhir", DatasetGeneration: "cda-fhir-v1"}
	output := streamingMissingCategoryPivotTestOutput()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{output},
	}
	semanticPlan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(semanticPlan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}

	physical, err := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[0].Plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 25)
	if err != nil {
		t.Fatal(err)
	}
	physical = withoutUnusedTerminalPivotPresenceCompanions(physical)
	if !canRenderStreamingMissingCategoryPivotPreview(compiled.Outputs[0], bindings, physical) {
		t.Fatal("full scoped terminal MISSING-category Pivot did not pass the streaming eligibility proof")
	}

	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Query, "COLLECT ") || !strings.Contains(query.Query, " AGGREGATE ") {
		t.Fatalf("query does not stream grouped values through COLLECT AGGREGATE:\n%s", query.Query)
	}
	if strings.Contains(query.Query, "INTO __loom_reshape_pivot_group_rows_") {
		t.Fatalf("query materializes a full group-row array:\n%s", query.Query)
	}
	collectAt := strings.Index(query.Query, "COLLECT ")
	assertAt := strings.Index(query.Query, "FILTER ASSERT(")
	sortAt := strings.Index(query.Query, "SORT ")
	limitAt := strings.Index(query.Query, "LIMIT @limit")
	if collectAt < 0 || assertAt < collectAt || sortAt < assertAt || limitAt < sortAt {
		t.Fatalf("full-population Pivot assertions/window are out of order: collect=%d assert=%d sort=%d limit=%d\n%s", collectAt, assertAt, sortAt, limitAt, query.Query)
	}
	for _, required := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER root_scope_allowed == @scope_allowed",
		"TABLE_PIVOT_UNLISTED_CATEGORY",
	} {
		if !strings.Contains(query.Query, required) {
			t.Fatalf("query lost required full-scope or typed-category clause %q:\n%s", required, query.Query)
		}
	}
	pivot := compiled.Outputs[0].Plan.StageSequence.Stages[0].GroupedPivot
	categoryColumnBind := streamingTestBindKey(query.BindVars, "reshape_category_column", pivot.CategoryColumn)
	presenceColumnBind := streamingTestBindKey(query.BindVars, "reshape_category_presence_column", pivot.CategoryPresenceColumn)
	if categoryColumnBind == "" || presenceColumnBind == "" {
		t.Fatalf("category/presence bind keys missing: category=%q presence=%q", categoryColumnBind, presenceColumnBind)
	}
	missingMatch := fmt.Sprintf("NOT (%s[@%s] == true)", pivot.InputRowVariable, presenceColumnBind)
	nullMatch := fmt.Sprintf("(%s[@%s] == true AND %s[@%s] == null)", pivot.InputRowVariable, presenceColumnBind, pivot.InputRowVariable, categoryColumnBind)
	if !strings.Contains(query.Query, missingMatch) || !strings.Contains(query.Query, nullMatch) {
		t.Fatalf("MISSING and explicit NULL categories are not distinguished by the presence sidecar: missing=%q null=%q\n%s", missingMatch, nullMatch, query.Query)
	}

	// The renderer must not rewrite the validated canonical stage while it
	// rebinds the clone to direct root projections for this preview query.
	canonicalPivot := compiled.Outputs[0].Plan.StageSequence.Stages[0].GroupedPivot
	if canonicalPivot == nil || len(canonicalPivot.InputProjections) == 0 ||
		canonicalPivot.InputProjections[0].Value.Variable != compiled.Outputs[0].Plan.StageSequence.Stages[0].InputRowVariable {
		t.Fatal("streaming render mutated the canonical Pivot input projections")
	}

	// Construction previews always request source identity. A Pivot has
	// composite source metadata and must not gain a single root ID, so this
	// production-shaped ERROR case remains eligible for streaming.
	identityOutput := streamingMissingCategoryPivotTestOutput()
	id := recipe.StageColumn{ID: "id_id", Name: "id", Type: "string", Nullable: true}
	identityOutput.Fields = append([]recipe.Field{{Name: "id", ColumnID: id.ID, Expr: recipe.Expression{Select: "root.id"}}}, identityOutput.Fields...)
	identityOutput.Construction.SourceColumns = append([]recipe.StageColumn{id}, identityOutput.Construction.SourceColumns...)
	identityPivot := identityOutput.Construction.Steps[0].Operation.Pivot
	identityPivot.Categories = []recipe.ConstructionPivotCategory{identityPivot.Categories[0], identityPivot.Categories[2]}
	identityPivot.DuplicatePolicy = recipe.PivotDuplicateError
	identityPivot.MissingCellPolicy = recipe.PivotMissingCellNull
	identityStep := &identityOutput.Construction.Steps[0]
	identityOutputs := make([]recipe.StageColumn, 0, len(identityStep.Outputs)-1)
	for _, column := range identityStep.Outputs {
		if column.ID != "null_value_id" {
			identityOutputs = append(identityOutputs, column)
		}
	}
	identityStep.Outputs = identityOutputs
	identityBundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                identityOutput.Name, TranslationVersion: "test", Outputs: []recipe.Output{identityOutput},
	}
	identityPlan, err := semantic.BuildRecipePlan(identityBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	identityResolved, err := semantic.ResolveRecipePlan(identityPlan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	identityCompiled, err := lower.CompileResolvedRecipePlan(identityResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !outputHasCompositeSource(identityCompiled.Outputs[0]) {
		t.Fatal("production-shaped Pivot output lost its composite-source classification")
	}
	identityBindings := bindings
	identityBindings.IncludeSourceIdentity = true
	identityQuery, err := CompileRecipeOutputWithPolicy(identityCompiled.Outputs[0], identityBindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(identityQuery.Query, " AGGREGATE ") || strings.Contains(identityQuery.Query, "INTO __loom_reshape_pivot_group_rows_") {
		t.Fatalf("composite-source ERROR Pivot did not use full-population streaming aggregation:\n%s", identityQuery.Query)
	}
	for _, required := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER root_scope_allowed == @scope_allowed",
	} {
		if !strings.Contains(identityQuery.Query, required) {
			t.Fatalf("identity-enabled streaming Pivot lost scoped source predicate %q:\n%s", required, identityQuery.Query)
		}
	}
	if strings.Contains(identityQuery.Query, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("composite-source Pivot acquired a single-root source ID:\n%s", identityQuery.Query)
	}
	for _, column := range identityQuery.OutputSchema {
		if column.Name == ir.PreviewSourceResourceIDColumn {
			t.Fatalf("composite-source Pivot added internal root-ID schema column: %#v", column)
		}
	}
	errorLimitAt := strings.Index(identityQuery.Query, "LIMIT @limit")
	for _, code := range []string{"TABLE_PIVOT_CELL_CARDINALITY", "TABLE_PIVOT_UNLISTED_CATEGORY"} {
		assertAt := strings.Index(identityQuery.Query, code)
		if assertAt < 0 || errorLimitAt < 0 || assertAt >= errorLimitAt {
			t.Fatalf("streaming Pivot lost full-population %s check before the preview window: assertion=%d limit=%d\n%s", code, assertAt, errorLimitAt, identityQuery.Query)
		}
	}
	ordinaryIdentityQuery, err := CompileRecipeOutputWithPolicy(identityCompiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if identityQuery.Query != ordinaryIdentityQuery.Query || !reflect.DeepEqual(identityQuery.BindVars, ordinaryIdentityQuery.BindVars) || !reflect.DeepEqual(identityQuery.OutputSchema, ordinaryIdentityQuery.OutputSchema) || !reflect.DeepEqual(identityQuery.RowIdentity, ordinaryIdentityQuery.RowIdentity) {
		t.Fatal("requesting composite source metadata changed the Pivot query, schema, binds, or stable group identity")
	}

	assertionOutput := streamingMissingCategoryPivotTestOutput()
	assertionPivot := assertionOutput.Construction.Steps[0].Operation.Pivot
	assertionPivot.DuplicatePolicy = recipe.PivotDuplicateError
	assertionPivot.MissingCellPolicy = recipe.PivotMissingCellError
	assertionBundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                assertionOutput.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{assertionOutput},
	}
	assertionPlan, err := semantic.BuildRecipePlan(assertionBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	assertionResolved, err := semantic.ResolveRecipePlan(assertionPlan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	assertionOutputs, err := lower.CompileResolvedRecipePlan(assertionResolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertionQuery, err := CompileRecipeOutputWithPolicy(assertionOutputs.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertionLimit := strings.Index(assertionQuery.Query, "LIMIT @limit")
	for _, code := range []string{"TABLE_PIVOT_CELL_MISSING", "TABLE_PIVOT_CELL_CARDINALITY", "TABLE_PIVOT_UNLISTED_CATEGORY"} {
		assertion := strings.Index(assertionQuery.Query, code)
		if assertion < 0 || assertionLimit < 0 || assertion >= assertionLimit {
			t.Fatalf("full-population %s assertion does not precede preview LIMIT: assertion=%d limit=%d\n%s", code, assertion, assertionLimit, assertionQuery.Query)
		}
	}
}

func streamingMissingCategoryPivotTestOutput() recipe.Output {
	d := "d"
	return recipe.Output{
		Name: "streaming_missing_category", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "code", ColumnID: "code_id", Expr: recipe.Expression{Select: "root.valueQuantity.code"}},
			{Name: "value", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "status_id", Name: "status", Type: "string"},
				{ID: "code_id", Name: "code", Type: "string", Nullable: true},
				{ID: "value_id", Name: "value", Type: "decimal", Nullable: true},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "pivot", GroupKeyIDs: []string{"status_id"}, CategoryColumnID: "code_id", ValueColumnID: "value_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_value_id"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_value_id"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &d}, OutputColumnID: "d_value_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "status_id", Name: "status", Type: "string"},
					{ID: "missing_value_id", Name: "missing_value", Type: "decimal", Nullable: true},
					{ID: "null_value_id", Name: "null_value", Type: "decimal", Nullable: true},
					{ID: "d_value_id", Name: "d", Type: "decimal", Nullable: true},
				},
			}},
		},
	}
}

func TestStreamingMissingCategoryPivotIndexFallsBackWithoutScalarKeyProof(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "loom_dev_cda_fhir", DatasetGeneration: "cda-fhir-v1"}
	output := streamingMissingCategoryPivotTestOutput()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{output},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[0].Plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 25)
	if err != nil {
		t.Fatal(err)
	}
	physical = withoutUnusedTerminalPivotPresenceCompanions(physical)
	root, sourceReturn, ok := previewCoveringIndexSource(physical)
	if !ok {
		t.Fatal("fixture did not produce a direct-root source return")
	}
	var status *ir.PhysicalProjection
	for index := range sourceReturn.Projections {
		if sourceReturn.Projections[index].Name == "status" {
			status = &sourceReturn.Projections[index]
			break
		}
	}
	if status == nil || status.Expression == nil || status.Expression.Extract == nil ||
		status.Expression.Extract.ExecutionMode != ir.PhysicalSelectorDirectScalar {
		t.Fatal("fixture status selector has no direct-scalar proof")
	}
	status.Expression = nil
	status.Value = ir.PhysicalValue{Variable: root.Variable, Path: []string{"payload", "status"}}
	if index := streamingMissingCategoryPivotIndexSpec(physical); index != nil {
		t.Fatalf("untyped source path was promoted to a scalar index key: %+v", index)
	}
}

func streamingTestBindKey(bindVars map[string]any, nameFragment string, expected any) string {
	for key, value := range bindVars {
		if strings.Contains(key, nameFragment) && value == expected {
			return key
		}
	}
	return ""
}
