package compiler

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileCategoryScanWrapsCompleteFinalOutputWithBoundedDistinct(t *testing.T) {
	two := int64(2)
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Observations", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "base", Expr: recipe.Expression{Select: "root.valueInteger"}},
		},
		DerivedColumns: []recipe.DerivedColumn{{
			ConstructionID: "double-base", Name: "doubled", Label: "Doubled", Operation: recipe.DerivedMultiply,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "base"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
			MissingInputPolicy: recipe.MissingInputError,
		}},
	})
	compiled, err := CompileCategoryScanOutputWithPolicy(output, "doubled", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"LET __loom_category_rows = (", "__loom_category_row[\"__loom_category_scan_presence\"]",
		"__loom_category_row[@__loom_category_column]", "COLLECT __loom_category_group_present",
		"SORT __loom_category_group_present ASC", "LIMIT @__loom_category_limit",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Fatalf("category query missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "[\"doubled\"]") {
		t.Fatalf("category column was interpolated into AQL:\n%s", compiled.Query)
	}
	if got := compiled.BindVars[categoryColumnBind]; got != "doubled" {
		t.Fatalf("column bind = %#v", got)
	}
	if got := compiled.BindVars[categoryLimitBind]; got != 257 {
		t.Fatalf("limit bind = %#v", got)
	}
	collectAt := strings.LastIndex(compiled.Query, "COLLECT __loom_category_group_present")
	limitAt := strings.LastIndex(compiled.Query, "LIMIT @__loom_category_limit")
	if collectAt < 0 || limitAt < collectAt {
		t.Fatalf("distinct must precede category limit:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query[:collectAt], "LIMIT @limit") {
		t.Fatalf("category scan retained a preview/root limit before distinct:\n%s", compiled.Query)
	}
	if compiled.Proof.OutputSchemaDigest == "" || compiled.Proof.PlanFingerprint == "" || compiled.Proof.QueryFingerprint == "" || compiled.Proof.Fingerprint == "" {
		t.Fatalf("incomplete proof: %#v", compiled.Proof)
	}
	if compiled.Proof.Output != "Observations" || compiled.Proof.Column != "doubled" || compiled.Proof.MaxValues != 256 {
		t.Fatalf("wrong proof binding: %#v", compiled.Proof)
	}
}

func TestCompileCategoryScanRefusesNonPublicOrNonScalarColumns(t *testing.T) {
	base := lower.CompiledRecipeOutput{OutputSchema: []lower.CompiledOutputColumn{
		{Name: "internal", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Internal: true},
		{Name: "identity", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Identity: true},
		{Name: "repeated", Kind: string(expression.KindString), Cardinality: string(expression.Many)},
		{Name: "object", Kind: string(expression.KindObject), Cardinality: string(expression.OptionalOne)},
		{Name: "unknown_kind", Kind: "mystery", Cardinality: string(expression.OptionalOne)},
	}}
	tests := []struct {
		name string
		code CategoryScanRefusalCode
	}{
		{"missing", CategoryScanColumnUnknown},
		{"internal", CategoryScanColumnInternal},
		{"identity", CategoryScanColumnIdentity},
		{"repeated", CategoryScanColumnRepeated},
		{"object", CategoryScanColumnUnsupported},
		{"unknown_kind", CategoryScanColumnUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := CompileCategoryScanOutputWithPolicy(base, test.name, 256, ir.DefaultPhysicalOptimizationPolicy())
			var refusal *CategoryScanRefusal
			if !errors.As(err, &refusal) || refusal.Code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
			if code, ok := CategoryScanRefusalCodeOf(err); !ok || code != test.code {
				t.Fatalf("typed code = %q/%t", code, ok)
			}
		})
	}
}

func TestCompileCategoryScanProofChangesWithSchemaAndSelectedColumn(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Patients", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}, {Name: "gender", Expr: recipe.Expression{Select: "root.gender"}}},
	})
	left, err := CompileCategoryScanOutputWithPolicy(output, "gender", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rightOutput := output
	rightOutput.OutputSchema = lower.CloneCompiledOutputSchema(output.OutputSchema)
	rightOutput.OutputSchema[0].SemanticPath += ":changed"
	right, err := CompileCategoryScanOutputWithPolicy(rightOutput, "gender", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if left.Proof.OutputSchemaDigest == right.Proof.OutputSchemaDigest || left.Proof.Fingerprint == right.Proof.Fingerprint {
		t.Fatalf("proof was not bound to exact schema: %#v %#v", left.Proof, right.Proof)
	}
}

func TestCompileCategoryScanUsesExactConstructionStagePrefixAndBindsPivotPair(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	scanned, err := CompileCategoryScanStageWithPolicy(output, "derive_total", "status_id", "total_id", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(scanned.Query, "LET __loom_construction_stage_") != 1 || strings.Contains(scanned.Query, "__loom_construction_stage_2") {
		t.Fatalf("category scan did not stop at derive_total:\n%s", scanned.Query)
	}
	proof := scanned.Proof
	if proof.Version != 2 || proof.Output != output.Name || proof.StageID != "derive_total" || proof.ColumnID != "status_id" || proof.ValueColumnID != "total_id" || proof.Column != "status" || proof.MaxValues != 256 {
		t.Fatalf("proof is not bound to the requested stage and pair: %#v", proof)
	}
	if proof.OutputSchemaDigest == "" || proof.PlanFingerprint == "" || proof.QueryFingerprint == "" || proof.Fingerprint == "" {
		t.Fatalf("incomplete stage scan proof: %#v", proof)
	}
	otherPair, err := CompileCategoryScanStageWithPolicy(output, "derive_total", "status_id", "amount_id", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if otherPair.Proof.Fingerprint == proof.Fingerprint {
		t.Fatal("proof fingerprint did not change with the selected value column")
	}

	for _, test := range []struct {
		stage, category, value string
		want                   CategoryScanRefusalCode
	}{
		{stage: "stale_stage", category: "status_id", value: "total_id", want: CategoryScanStageUnknown},
		{stage: "derive_total", category: "stale_category", value: "total_id", want: CategoryScanColumnUnknown},
		{stage: "derive_total", category: "status_id", value: "stale_value", want: CategoryScanColumnUnknown},
	} {
		_, err := CompileCategoryScanStageWithPolicy(output, test.stage, test.category, test.value, 256, ir.DefaultPhysicalOptimizationPolicy())
		var refusal *CategoryScanRefusal
		if !errors.As(err, &refusal) || refusal.Code != test.want {
			t.Errorf("stage scan (%q, %q, %q) error = %v, want refusal %s", test.stage, test.category, test.value, err, test.want)
		}
	}
}

func TestCompileCategoryScanReturnsOnlyTerminalCategoryAfterFilter(t *testing.T) {
	output := compilePopulationMappingOutput(t, constructionCellTraceRecipeOutput())
	scanned, err := CompileCategoryScanStageWithPolicy(output, "keep_positive", "status_id", "total_id", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	finalAt := strings.Index(scanned.Query, "FOR __loom_construction_final_row IN")
	categoryAt := strings.Index(scanned.Query, "FOR __loom_category_row IN __loom_category_rows")
	if finalAt < 0 || categoryAt <= finalAt {
		t.Fatalf("category scan is missing its terminal output boundary:\n%s", scanned.Query)
	}
	terminal := scanned.Query[finalAt:categoryAt]
	if !strings.Contains(scanned.Query[:finalAt], "total") || !strings.Contains(scanned.Query, "__loom_construction_stage_1") || !strings.Contains(scanned.Query, "__loom_construction_stage_2") {
		t.Fatalf("category scan dropped the prior derived filter or exact stage prefix:\n%s", scanned.Query)
	}
	if !strings.Contains(terminal, "status") || strings.Contains(terminal, "amount") || strings.Contains(terminal, "total") {
		t.Fatalf("terminal category projection was not narrowed safely:\n%s", terminal)
	}
	if strings.Contains(terminal, "SORT __loom_construction_final_row.") {
		t.Fatalf("category scan retained an unneeded terminal row sort:\n%s", terminal)
	}
	if strings.Contains(scanned.Query[:finalAt], "MERGE(") {
		t.Fatalf("category scan merged projected objects before its stage filter:\n%s", scanned.Query[:finalAt])
	}
	if !strings.Contains(terminal, `"__loom_category_scan_presence": __loom_construction_final_row["__loom_category_scan_presence"]`) {
		t.Fatalf("terminal category scan did not carry the presence marker through its stage prefix:\n%s", terminal)
	}
	categoryOutput := scanned.Query[categoryAt:]
	for _, required := range []string{
		"COLLECT __loom_category_group_present = __loom_category_present, __loom_category_group_value = __loom_category_value",
		"SORT __loom_category_group_present ASC, TYPENAME(__loom_category_group_value) ASC, __loom_category_group_value ASC",
		"LIMIT @__loom_category_limit",
	} {
		if !strings.Contains(categoryOutput, required) {
			t.Errorf("category scan lost its ordered complete-category contract %q:\n%s", required, categoryOutput)
		}
	}
	if !strings.Contains(scanned.Query, "root_scope_allowed") || !strings.Contains(scanned.Query, "auth_resource_paths") {
		t.Fatalf("narrowed category return lost the source authorization scope:\n%s", scanned.Query)
	}
	stage, found := compiledStageByID(output.Stages, "keep_positive")
	if !found {
		t.Fatal("compiled output lost the scanned stage schema")
	}
	wantSchemaDigest, err := categoryHash(stage.Columns)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.Proof.OutputSchemaDigest != wantSchemaDigest {
		t.Fatalf("category proof was narrowed with the query return: got %q, want full stage schema %q", scanned.Proof.OutputSchemaDigest, wantSchemaDigest)
	}
	wantQueryDigest, err := categoryQueryFingerprint(scanned.Query, scanned.BindVars)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.Proof.QueryFingerprint != wantQueryDigest {
		t.Fatalf("category proof does not bind the unordered query: got %q, want %q", scanned.Proof.QueryFingerprint, wantQueryDigest)
	}
}

func TestCompileCategoryScanPushesDirectScalarEqualityToSourceAndKeepsFallbacks(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanFilterTestOutput(recipe.FilterEquals))
	scanned, err := CompileCategoryScanStageWithPolicy(output, "keep_active", "birth_date_id", "patient_id", 32, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rootFilter := "FILTER root.payload.active == @construction_filter_value"
	stageFilter := "FILTER __loom_construction_input_1.active == @construction_filter_value"
	if strings.Count(scanned.Query, rootFilter) != 1 || !strings.Contains(scanned.Query, stageFilter) {
		t.Fatalf("category scan must duplicate an eligible source equality and retain the stage predicate:\n%s", scanned.Query)
	}
	rootFilterAt := strings.Index(scanned.Query, rootFilter)
	stageReturnAt := strings.Index(scanned.Query, "RETURN { [@__loom_physical_projection_")
	if rootFilterAt < 0 || stageReturnAt < 0 || rootFilterAt > stageReturnAt ||
		strings.Index(scanned.Query, "root.project == @project") > rootFilterAt ||
		strings.Index(scanned.Query, "root.dataset_generation == @dataset_generation") > rootFilterAt ||
		strings.Index(scanned.Query, "root_scope_allowed") > rootFilterAt {
		t.Fatalf("source equality must follow project, generation, and authorization scope before source projection:\n%s", scanned.Query)
	}

	ineligibleOutput := compilePopulationMappingOutput(t, categoryScanFilterTestOutput(recipe.FilterExists))
	ineligible, err := CompileCategoryScanStageWithPolicy(ineligibleOutput, "keep_active", "birth_date_id", "patient_id", 32, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ineligible.Query, "FILTER root.payload.active") || !strings.Contains(ineligible.Query, "FILTER __loom_construction_input_1.active != null") {
		t.Fatalf("ineligible non-equality filter should remain on the staged path:\n%s", ineligible.Query)
	}
}

func categoryScanFilterTestOutput(operator recipe.FilterOperator) recipe.Output {
	trueValue := true
	filter := &recipe.ConstructionFilter{ColumnID: "active_id", Operator: operator}
	if operator == recipe.FilterEquals {
		filter.Values = []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &trueValue}}
	}
	columns := []recipe.StageColumn{
		{ID: "patient_id", Name: "patient_id"},
		{ID: "active_id", Name: "active"},
		{ID: "birth_date_id", Name: "birth_date"},
	}
	return recipe.Output{
		Name: "category_filter_test", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "active", ColumnID: "active_id", Expr: recipe.Expression{Select: "root.active"}},
			{Name: "birth_date", ColumnID: "birth_date_id", Expr: recipe.Expression{Select: "root.birthDate"}},
		},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: columns,
			Steps: []recipe.ConstructionStep{{
				ID: "keep_active", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: filter},
				Outputs:   columns,
			}},
		},
	}
}

func TestCompileSourceProjectionCategoryScanOffersCoveringIndexForExplicitPivotPair(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "pre_pivot_category_discovery", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "resource_id", Name: "resource_id"},
				{ID: "category_id", Name: "category"},
				{ID: "amount_id", Name: "amount"},
			},
		},
	})
	if output.Plan.StageSequence != nil {
		t.Fatal("source-only output unexpectedly has a construction stage sequence")
	}
	scanned, err := CompileCategoryScanStageWithPolicy(output, recipe.ConstructionSourceProjectionID, "category_id", "amount_id", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	index := scanned.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("direct source category discovery did not emit an explicit-pair covering-index spec:\n%s", scanned.Query)
	}
	wantFields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.gender", "payload.id", "payload.multipleBirthInteger"}
	if index.Collection != "Patient" || !strings.HasPrefix(index.Name, previewCoveringIndexNamePrefix) || !reflect.DeepEqual(index.Fields, wantFields) {
		t.Fatalf("category scan covering-index metadata = %+v, want collection Patient fields %#v", index, wantFields)
	}
	projectionCount := 0
	for key, value := range scanned.BindVars {
		if !strings.HasPrefix(key, "__loom_physical_projection_") || !strings.HasSuffix(key, "_name") {
			continue
		}
		projectionCount++
		if value != "category" {
			t.Fatalf("source category scan retained projection %q=%#v", key, value)
		}
	}
	if projectionCount != 1 || !strings.Contains(scanned.Query, "root.payload.gender") || strings.Contains(scanned.Query, "root.payload.id") || strings.Contains(scanned.Query, "root.payload.multipleBirthInteger") {
		t.Fatalf("source category scan should return only the selected source field (found %d projection names):\n%s", projectionCount, scanned.Query)
	}
	sourceStage, found := compiledStageByID(output.Stages, recipe.ConstructionSourceProjectionID)
	if !found {
		t.Fatal("compiled output lost its source projection stage")
	}
	wantSchemaDigest, err := categoryHash(sourceStage.Columns)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.Proof.OutputSchemaDigest != wantSchemaDigest || scanned.Proof.Version != 2 ||
		scanned.Proof.StageID != recipe.ConstructionSourceProjectionID || scanned.Proof.ColumnID != "category_id" ||
		scanned.Proof.ValueColumnID != "amount_id" || scanned.Proof.PlanFingerprint == "" ||
		scanned.Proof.QueryFingerprint == "" || scanned.Proof.Fingerprint == "" {
		t.Fatalf("narrowed source scan lost its complete stage/pair proof: %#v", scanned.Proof)
	}
	if !strings.Contains(scanned.Query, "root_scope_allowed") || !strings.Contains(scanned.Query, "auth_resource_paths") {
		t.Fatalf("narrowed source scan lost root authorization scope:\n%s", scanned.Query)
	}
	if strings.Contains(scanned.Query, "indexHint:") {
		t.Fatalf("category scan should expose prewarm metadata without changing its query:\n%s", scanned.Query)
	}

	withoutPair, err := CompileCategoryScanOutputWithPolicy(output, "category", 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if withoutPair.PreviewCoveringIndex != nil {
		t.Fatalf("category-only discovery without explicit Pivot value intent emitted prewarm metadata: %+v", withoutPair.PreviewCoveringIndex)
	}
}
