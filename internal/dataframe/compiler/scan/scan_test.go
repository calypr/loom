package scan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
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
	compiled, err := Compile(output, OutputColumn{Column: "doubled"}, 256, ir.DefaultPhysicalOptimizationPolicy())
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
		code categoryScanRefusalCode
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
			_, err := Compile(base, OutputColumn{Column: test.name}, 256, ir.DefaultPhysicalOptimizationPolicy())
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
	left, err := Compile(output, OutputColumn{Column: "gender"}, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rightOutput := output
	rightOutput.OutputSchema = lower.CloneCompiledOutputSchema(output.OutputSchema)
	rightOutput.OutputSchema[0].SemanticPath += ":changed"
	right, err := Compile(rightOutput, OutputColumn{Column: "gender"}, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if left.Proof.OutputSchemaDigest == right.Proof.OutputSchemaDigest || left.Proof.Fingerprint == right.Proof.Fingerprint {
		t.Fatalf("proof was not bound to exact schema: %#v %#v", left.Proof, right.Proof)
	}
}

func TestCompileCategoryScanUsesExactConstructionStagePrefixAndBindsPivotPair(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	scanned, err := Compile(output, StageColumn{StageID: "derive_total", ColumnID: "status_id", ValueColumnID: "total_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
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
	otherPair, err := Compile(output, StageColumn{StageID: "derive_total", ColumnID: "status_id", ValueColumnID: "amount_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if otherPair.Proof.Fingerprint == proof.Fingerprint {
		t.Fatal("proof fingerprint did not change with the selected value column")
	}

	for _, test := range []struct {
		stage, category, value string
		want                   categoryScanRefusalCode
	}{
		{stage: "stale_stage", category: "status_id", value: "total_id", want: CategoryScanStageUnknown},
		{stage: "derive_total", category: "stale_category", value: "total_id", want: CategoryScanColumnUnknown},
		{stage: "derive_total", category: "status_id", value: "stale_value", want: CategoryScanColumnUnknown},
	} {
		_, err := Compile(output, StageColumn{StageID: test.stage, ColumnID: test.category, ValueColumnID: test.value}, 256, ir.DefaultPhysicalOptimizationPolicy())
		var refusal *CategoryScanRefusal
		if !errors.As(err, &refusal) || refusal.Code != test.want {
			t.Errorf("stage scan (%q, %q, %q) error = %v, want refusal %s", test.stage, test.category, test.value, err, test.want)
		}
	}
}

func TestCompileColumnPopulationNarrowsSourceProjectionAndKeepsStagePrefix(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	source, found := compiledStageByID(output.Stages, recipe.ConstructionSourceProjectionID)
	if !found {
		t.Fatal("source stage missing")
	}
	var sourceColumnID string
	for _, column := range source.Columns {
		if column.Name == "status" {
			sourceColumnID = column.ID
		}
	}
	compiledSource, err := CompileColumnPopulationScan(output, recipe.ConstructionSourceProjectionID, []string{sourceColumnID}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiledSource.Query, "COLLECT AGGREGATE") || !strings.Contains(compiledSource.Query, "= SUM(") ||
		!strings.Contains(compiledSource.Query, "root_scope_allowed") {
		t.Fatalf("source population query must aggregate exact authorized source rows:\n%s", compiledSource.Query)
	}
	if strings.Contains(compiledSource.Query, "@__loom_population_column_1") {
		t.Fatalf("source query should contain only requested population fields:\n%s", compiledSource.Query)
	}

	filtered, err := CompileColumnPopulationScan(output, "keep_positive", []string{"status_id", "total_id"}, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(filtered.Query, "LET __loom_construction_stage_") != 2 ||
		!strings.Contains(filtered.Query, "FILTER __loom_construction_input_2.total > @construction_filter_value") ||
		!strings.Contains(filtered.Query, "@__loom_population_column_1") {
		t.Fatalf("filtered population scan must preserve the exact two-stage prefix and select requested fields:\n%s", filtered.Query)
	}
}

func TestCompileColumnPopulationRejectsInvalidBatchesAndNonscalarColumns(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	for _, ids := range [][]string{nil, {}, make([]string, MaxColumnPopulationBatch+1)} {
		if _, err := CompileColumnPopulationScan(output, recipe.ConstructionSourceProjectionID, ids, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
			t.Fatalf("accepted invalid population batch of length %d", len(ids))
		}
	}
	if _, err := CompileColumnPopulationScan(output, "missing_stage", []string{"status_id"}, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("accepted unknown stage")
	}
	if _, err := CompileColumnPopulationScan(output, "keep_positive", []string{"stale_column"}, ir.DefaultPhysicalOptimizationPolicy()); err == nil {
		t.Fatal("accepted unknown column")
	}
}

func TestCompileCategoryScanRequiringValueBindsPresencePolicyAndStageSchema(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	ordinary, err := Compile(output, StageColumn{StageID: "keep_positive", ColumnID: "status_id", ValueColumnID: "amount_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	valueAware, err := Compile(output, StageColumn{StageID: "keep_positive", ColumnID: "status_id", ValueColumnID: "amount_id", RequireValue: true}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !valueAware.Proof.PresenceTracked || !valueAware.Proof.ValuePresenceRequired {
		t.Fatalf("value-aware proof = %+v, want tracked presence and required-value policy", valueAware.Proof)
	}
	if ordinary.Proof.ValuePresenceRequired || ordinary.Proof.Fingerprint == valueAware.Proof.Fingerprint {
		t.Fatalf("value-presence policy did not change the proof fingerprint: ordinary=%+v value-aware=%+v", ordinary.Proof, valueAware.Proof)
	}
	if !strings.Contains(valueAware.Query, "FILTER __loom_category_value_row[@__loom_category_value_column] != null") ||
		!strings.Contains(valueAware.Query, "__loom_category_scan_presence") {
		t.Fatalf("value-aware query did not filter the value and carry category presence from the source projection:\n%s", valueAware.Query)
	}
	stage, found := compiledStageByID(output.Stages, "keep_positive")
	if !found {
		t.Fatal("compiled output lost the scanned stage schema")
	}
	wantSchemaDigest, err := categoryHash(stage.Columns)
	if err != nil {
		t.Fatal(err)
	}
	if valueAware.Proof.OutputSchemaDigest != wantSchemaDigest {
		t.Fatalf("value-aware proof schema digest = %q, want input stage schema digest %q", valueAware.Proof.OutputSchemaDigest, wantSchemaDigest)
	}

	changed := output
	changed.Stages = append([]lower.CompiledStageDescriptor(nil), output.Stages...)
	for index := range changed.Stages {
		if changed.Stages[index].ID != "keep_positive" {
			continue
		}
		changed.Stages[index].Columns = append([]lower.CompiledOutputColumn(nil), changed.Stages[index].Columns...)
		changed.Stages[index].Columns[0].SemanticPath += ":changed"
	}
	changedScan, err := Compile(changed, StageColumn{StageID: "keep_positive", ColumnID: "status_id", ValueColumnID: "amount_id", RequireValue: true}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if changedScan.Proof.OutputSchemaDigest == valueAware.Proof.OutputSchemaDigest || changedScan.Proof.Fingerprint == valueAware.Proof.Fingerprint {
		t.Fatalf("value-aware proof was not bound to the input stage schema: before=%+v after=%+v", valueAware.Proof, changedScan.Proof)
	}
}

func TestCompileCategoryScanTracksPresenceThroughFilterForOrdinaryDiscoveryProbe(t *testing.T) {
	output := categoryScanConstructionRecipeOutput()
	output.Fields = append(output.Fields, recipe.Field{Name: "group", ColumnID: "group_id", Expr: recipe.Expression{Select: "root.id"}})
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep(nil), output.Construction.Steps...)
	construction.SourceColumns = append(append([]recipe.StageColumn(nil), construction.SourceColumns...), recipe.StageColumn{ID: "group_id", Name: "group"})
	for index := range construction.Steps {
		construction.Steps[index].Outputs = append(construction.Steps[index].Outputs, recipe.StageColumn{ID: "group_id", Name: "group"})
	}
	probeCategory := "active"
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID: "temporary_probe_pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_positive"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "temporary_probe_pivot", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "status_id", ValueColumnID: "amount_id",
			Categories: []recipe.ConstructionPivotCategory{{
				Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &probeCategory}, OutputColumnID: "probe_value_id",
			}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group"}, {ID: "probe_value_id", Name: "probe_value"}},
	})
	output.Construction = &construction
	compiled := compilePopulationMappingOutput(t, output)

	scanned, err := Compile(compiled, StageColumn{StageID: "keep_positive", ColumnID: "status_id", ValueColumnID: "amount_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	companion, ok := scanned.BindVars["__loom_category_presence_column"].(string)
	if !ok || companion == "" || !scanned.Proof.PresenceTracked {
		t.Fatalf("ordinary temporary Pivot probe lost source presence across its filtered prefix: proof=%#v binds=%#v\n%s", scanned.Proof, scanned.BindVars, scanned.Query)
	}
	for _, marker := range []string{"__loom_construction_stage_", "FILTER __loom_construction_input_2.total", companion, "__loom_category_presence_column"} {
		if !strings.Contains(scanned.Query, marker) {
			t.Fatalf("filtered source-presence category query omitted %q:\n%s", marker, scanned.Query)
		}
	}
}

func TestCompileCategoryScanCollapsesParentPreservingRelatedPrefixToRootCategories(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanRelatedRecipeOutput(recipe.ExpansionPreserveParent, false, false))
	scanned, err := Compile(output, StageColumn{StageID: "add_status", ColumnID: "category_id", ValueColumnID: "value_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(scanned.Query, "__loom_construction_stage_") || strings.Contains(scanned.Query, "related_expand_records") ||
		strings.Contains(scanned.Query, "related_field_document") {
		t.Fatalf("parent-preserving related prefix was not narrowed to the direct root category scan:\n%s", scanned.Query)
	}
	if strings.Contains(scanned.Query, "LET __loom_category_rows =") {
		t.Fatalf("direct root category scan should stream its terminal source projection:\n%s", scanned.Query)
	}
	wantFields := []string{"project", "dataset_generation", "payload.gender"}
	if scanned.CategoryIndex == nil || !reflect.DeepEqual(scanned.CategoryIndex.Fields, wantFields) || !reflect.DeepEqual(scanned.CategoryIndex.StoredValues, []string{"auth_resource_path", "resourceType"}) {
		t.Fatalf("direct root category index = %+v, want the shared exact category index", scanned.CategoryIndex)
	}
	if !strings.Contains(scanned.Query, "forceIndexHint: false") || !strings.Contains(scanned.Query, scanned.CategoryIndex.Name) ||
		!strings.Contains(scanned.Query, "root.resourceType == @__loom_category_resource_type") || scanned.BindVars["__loom_category_resource_type"] != "Patient" {
		t.Fatalf("direct root category query omitted its non-forcing shared index hint or exact root type guard:\n%s\n%#v", scanned.Query, scanned.BindVars)
	}
	if scanned.OverflowWitness == nil {
		t.Fatal("direct root category scan omitted its exact missing-value witness")
	}
	for _, guard := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER root_scope_allowed == @scope_allowed",
		"FILTER root.resourceType == @__loom_category_resource_type",
	} {
		if !strings.Contains(scanned.Query, guard) || !strings.Contains(scanned.OverflowWitness.Query, guard) {
			t.Errorf("root category query or missing witness lost the exact scope/type guard %q:\nquery:\n%s\nwitness:\n%s", guard, scanned.Query, scanned.OverflowWitness.Query)
		}
	}
	if scanned.BindVars["@root_collection"] != "Patient" || scanned.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("root category query changed its typed collection or auth-scope bindings: %#v", scanned.BindVars)
	}
	if !strings.Contains(scanned.Query, "LET __loom_category_nonnull = (") || !strings.Contains(scanned.Query, "LET __loom_category_null = (") {
		t.Fatalf("direct root category query omitted its indexed null/non-null branches:\n%s", scanned.Query)
	}
	assertCategoryScanBindVarsMatchQuery(t, scanned.Query, scanned.BindVars)
	if !strings.Contains(scanned.OverflowWitness.Query, "FILTER NOT (") {
		t.Fatalf("direct root category scan omitted its exact missing-value witness: %+v", scanned.OverflowWitness)
	}
	assertCategoryScanBindVarsMatchQuery(t, scanned.OverflowWitness.Query, scanned.OverflowWitness.BindVars)
	terminalStage, found := compiledStageByID(output.Stages, "add_status")
	if !found {
		t.Fatal("compiled output lost terminal related-field stage")
	}
	wantSchemaDigest, err := categoryHash(terminalStage.Columns)
	if err != nil {
		t.Fatal(err)
	}
	if scanned.Proof.Version != 2 || scanned.Proof.StageID != "add_status" || scanned.Proof.ColumnID != "category_id" ||
		scanned.Proof.ValueColumnID != "value_id" || scanned.Proof.OutputSchemaDigest != wantSchemaDigest ||
		scanned.Proof.PlanFingerprint == "" || scanned.Proof.QueryFingerprint == "" || scanned.Proof.Fingerprint == "" {
		t.Fatalf("root-category optimization lost exact stage and pivot-pair proof identity: %#v", scanned.Proof)
	}
}

func TestCompileCategoryScanUsesLegacyIndexFallbackWhenRootCollectionTypeIsUnproven(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	output.Plan = ir.ClonePhysicalPlan(output.Plan)
	output.OptimizedPlan = nil
	root, _, ok := categoryScanRootProjectionSource(output.Plan)
	if !ok {
		t.Fatal("compiled fixture has no direct root projection")
	}
	output.Plan.BindVars[root.CollectionBindKey] = "ObservationShadow"

	scanned, err := Compile(
		output, StageColumn{StageID: recipe.ConstructionSourceProjectionID, ColumnID: "status_id", ValueColumnID: "amount_id"},
		256,
		ir.DefaultPhysicalOptimizationPolicy())

	if err != nil {
		t.Fatal(err)
	}
	wantFields := []string{"project", "dataset_generation", "payload.status", "auth_resource_path"}
	if scanned.CategoryIndex == nil || scanned.CategoryIndex.Collection != "ObservationShadow" ||
		!reflect.DeepEqual(scanned.CategoryIndex.Fields, wantFields) {
		t.Fatalf("unproven collection/type pair should retain the field-only index: %+v", scanned.CategoryIndex)
	}
	if strings.Contains(scanned.Query, "root.resourceType == @__loom_category_resource_type") {
		t.Fatalf("unproven collection/type pair received a narrowing type predicate:\n%s", scanned.Query)
	}
	if _, exists := scanned.BindVars["__loom_category_resource_type"]; exists {
		t.Fatalf("unproven collection/type pair received a resourceType bind: %#v", scanned.BindVars)
	}
}

func assertCategoryScanBindVarsMatchQuery(t *testing.T, query string, bindVars map[string]any) {
	t.Helper()
	want := map[string]struct{}{}
	for index := 0; index < len(query); index++ {
		if query[index] != '@' {
			continue
		}
		collection := false
		if index+1 < len(query) && query[index+1] == '@' {
			collection = true
			index++
		}
		start := index + 1
		end := start
		for end < len(query) && categoryScanIdentifierByte(query[end]) {
			end++
		}
		if end > start {
			key := query[start:end]
			if collection {
				key = "@" + key
			}
			want[key] = struct{}{}
			index = end - 1
		}
	}
	got := make(map[string]struct{}, len(bindVars))
	for key := range bindVars {
		got[key] = struct{}{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query bind vars = %v, query references = %v\n%s", got, want, query)
	}
}

func TestCategoryScanRootCategoryFastPathRejectsUnsafePrefixes(t *testing.T) {
	tests := []struct {
		name   string
		output recipe.Output
	}{
		{name: "related expand excludes empty parents", output: categoryScanRelatedRecipeOutput(recipe.ExpansionExclude, false, false)},
		{name: "filter drops parents", output: categoryScanRelatedRecipeOutput(recipe.ExpansionPreserveParent, true, false)},
		{name: "category projection is renamed", output: categoryScanRelatedRecipeOutput(recipe.ExpansionPreserveParent, false, true)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := compilePopulationMappingOutput(t, test.output)
			stageID := "add_status"
			categoryID := "category_id"
			scanned, err := Compile(output, StageColumn{StageID: stageID, ColumnID: categoryID, ValueColumnID: "value_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(scanned.Query, "__loom_construction_stage_") || scanned.CategoryIndex != nil {
				t.Fatalf("unsafe stage prefix bypassed construction evaluation or got a direct root index:\n%s", scanned.Query)
			}
		})
	}
}

func categoryScanRelatedRecipeOutput(emptyPolicy recipe.ExpansionEmptyPolicy, withFilter, renameCategory bool) recipe.Output {
	categoryName := "category"
	if renameCategory {
		categoryName = "renamed_category"
	}
	sourceColumns := []recipe.StageColumn{
		{ID: "category_id", Name: "category"},
		{ID: "value_id", Name: "value"},
	}
	expandedColumns := append(append([]recipe.StageColumn(nil), sourceColumns...), recipe.StageColumn{
		ID: "related_id", Name: "related_id", Label: "Related resource ID", Type: "string",
		Nullable: emptyPolicy == recipe.ExpansionPreserveParent,
	})
	steps := []recipe.ConstructionStep{{
		ID: "expand_related", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "related-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}}, ContributorPolicy: "ALL_MATCHES", EmptyPolicy: emptyPolicy, RelatedRecordColumnID: "related_id",
		}}, Outputs: expandedColumns,
	}}
	priorStageID := "expand_related"
	if withFilter {
		steps = append(steps, recipe.ConstructionStep{
			ID: "keep_value", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: priorStageID}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "value_id", Operator: recipe.FilterExists,
			}},
			Outputs: expandedColumns,
		})
		priorStageID = "keep_value"
	}
	fieldColumns := append(append([]recipe.StageColumn(nil), expandedColumns...), recipe.StageColumn{
		ID: "related_status", Name: "related_status", Label: "Unrelated related status", Type: "string", Nullable: true,
	})
	steps = append(steps, recipe.ConstructionStep{
		ID: "add_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: priorStageID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedFieldOp, RelatedField: &recipe.ConstructionRelatedField{
			ChoiceID: "related-status-choice", OutputColumnID: "related_status",
			Source: recipe.ConstructionRelatedFieldSource{
				CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
				Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
			},
		}}, Outputs: fieldColumns,
	})
	for index := range steps {
		for columnIndex := range steps[index].Outputs {
			if steps[index].Outputs[columnIndex].ID == "category_id" {
				steps[index].Outputs[columnIndex].Name = categoryName
			}
		}
	}
	return recipe.Output{
		Name: "related_category_scan", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "value", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: sourceColumns, Steps: steps},
	}
}

func TestCompileCategoryScanReturnsOnlyTerminalCategoryAfterFilter(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	scanned, err := Compile(output, StageColumn{StageID: "keep_positive", ColumnID: "status_id", ValueColumnID: "total_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
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

func categoryScanConstructionRecipeOutput() recipe.Output {
	two := int64(2)
	zero := int64(0)
	return recipe.Output{
		Name: "category_scan_construction_fixture", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "derive_total", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{
						ConstructionID: "calc_total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd,
						Left:               recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "amount_id"},
						Right:              recipe.ConstructionOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &two}},
						MissingInputPolicy: recipe.MissingInputPropagateNull,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
				{
					ID: "keep_positive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive_total"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "total_id", Operator: recipe.FilterGreaterThan,
						Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &zero}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "amount_id", Name: "amount"}, {ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"},
					},
				},
			},
		},
	}
}

func TestCompileCategoryScanPushesDirectScalarEqualityToSourceAndKeepsFallbacks(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanFilterTestOutput(recipe.FilterEquals))
	scanned, err := Compile(output, StageColumn{StageID: "keep_active", ColumnID: "birth_date_id", ValueColumnID: "patient_id"}, 32, ir.DefaultPhysicalOptimizationPolicy())
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
	ineligible, err := Compile(ineligibleOutput, StageColumn{StageID: "keep_active", ColumnID: "birth_date_id", ValueColumnID: "patient_id"}, 32, ir.DefaultPhysicalOptimizationPolicy())
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
	scanned, err := Compile(output, StageColumn{StageID: recipe.ConstructionSourceProjectionID, ColumnID: "category_id", ValueColumnID: "amount_id"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	index := scanned.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("direct source category discovery did not emit an explicit-pair covering-index spec:\n%s", scanned.Query)
	}
	wantFields := []string{"project", "dataset_generation", "_key", "payload.gender", "payload.id", "payload.multipleBirthInteger"}
	if index.Collection != "Patient" || !strings.HasPrefix(index.Name, model.PreviewCoveringIndexNamePrefix) || !reflect.DeepEqual(index.Fields, wantFields) {
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
	if projectionCount != 0 || !strings.Contains(scanned.Query, "COLLECT value = root.payload.gender") || strings.Contains(scanned.Query, "root.payload.id") || strings.Contains(scanned.Query, "root.payload.multipleBirthInteger") {
		t.Fatalf("source category scan should group the selected source field directly (found %d projection names):\n%s", projectionCount, scanned.Query)
	}
	for _, guard := range []string{"root.project == @project", "root.dataset_generation == @dataset_generation", "root.auth_resource_path IN @auth_resource_paths", "FILTER root_scope_allowed == @scope_allowed"} {
		if !strings.Contains(scanned.Query, guard) {
			t.Fatalf("direct source category scan lost scope guard %q:\n%s", guard, scanned.Query)
		}
	}
	if strings.Contains(scanned.Query, "SORT root._key") {
		t.Fatalf("complete category discovery must not sort source rows before grouping categories:\n%s", scanned.Query)
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
	if !scanned.Proof.PresenceTracked || !strings.Contains(scanned.Query, "present") {
		t.Fatalf("exact root selector presence is not bound into the category proof/query: proof=%#v\n%s", scanned.Proof, scanned.Query)
	}
	if !strings.Contains(scanned.Query, "root_scope_allowed") || !strings.Contains(scanned.Query, "auth_resource_paths") {
		t.Fatalf("narrowed source scan lost root authorization scope:\n%s", scanned.Query)
	}
	if !strings.Contains(scanned.Query, `indexHint: "`+model.PreviewCoveringIndexNamePrefix) || !strings.Contains(scanned.Query, "forceIndexHint: false") || strings.Contains(scanned.Query, "forceIndexHint: true") {
		t.Fatalf("category scan should use its optional covering index without requiring it:\n%s", scanned.Query)
	}

	withoutPair, err := Compile(output, OutputColumn{Column: "category"}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if withoutPair.PreviewCoveringIndex != nil {
		t.Fatalf("category-only discovery without explicit Pivot value intent emitted prewarm metadata: %+v", withoutPair.PreviewCoveringIndex)
	}
}

func TestCompileCategoryScanTracksPresenceThroughExpandForDiscoveryProbe(t *testing.T) {
	probe := "__expand_category_probe"
	output := constructionExpandOutput()
	construction := *output.Construction
	construction.Steps = append([]recipe.ConstructionStep(nil), construction.Steps...)
	construction.Steps = append(construction.Steps, recipe.ConstructionStep{
		ID:     "pivot_after_expand",
		Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "pivot_after_expand", GroupKeyIDs: []string{"ordinal_id"},
			CategoryColumnID: "status_id", ValueColumnID: "tag_id",
			Categories: []recipe.ConstructionPivotCategory{{
				Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &probe}, OutputColumnID: "probe_value_id",
			}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "ordinal_id", Name: "ordinal"},
			{ID: "probe_value_id", Name: "__category_discovery_probe"},
		},
	})
	output.Construction = &construction
	compiled := compilePopulationMappingOutput(t, output)

	scanned, err := Compile(compiled, StageColumn{StageID: "expand_tags", ColumnID: "status_id", ValueColumnID: "tag_id"}, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !scanned.Proof.PresenceTracked {
		t.Fatalf("source presence was dropped through Expand: proof=%#v\n%s", scanned.Proof, scanned.Query)
	}
	companion, ok := scanned.BindVars["__loom_category_presence_column"].(string)
	if !ok || companion == "" {
		t.Fatalf("Expand category scan did not consume its source presence companion: %#v", scanned.BindVars)
	}
	if !strings.Contains(scanned.Query, companion) {
		t.Fatalf("Expand category query omitted presence companion %q:\n%s", companion, scanned.Query)
	}
}

func TestCompileCategoryScanRequiringValueFiltersEmptyPivotColumns(t *testing.T) {
	output := compilePopulationMappingOutput(t, categoryScanConstructionRecipeOutput())
	scanned, err := Compile(output, StageColumn{StageID: "derive_total", ColumnID: "status_id", ValueColumnID: "total_id", RequireValue: true}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !scanned.Proof.ValuePresenceRequired {
		t.Fatalf("requiring-value proof did not record its value policy: %#v", scanned.Proof)
	}
	if !strings.Contains(scanned.Query, "FILTER __loom_category_value_row[@__loom_category_value_column] != null") {
		t.Fatalf("requiring-value category scan omitted its non-null value filter:\n%s", scanned.Query)
	}
	if !scanned.Proof.PresenceTracked {
		t.Fatalf("requiring-value category scan did not bind category presence: %#v", scanned.Proof)
	}
	if got := scanned.BindVars["__loom_category_value_column"]; got != "total" {
		t.Fatalf("requiring-value category scan bound value column to %#v, want total", got)
	}
	if strings.Contains(scanned.Query, "__loom_category_nonnull") {
		t.Fatalf("requiring-value category scan used the category-only indexed shortcut:\n%s", scanned.Query)
	}
	if scanned.OverflowWitness != nil {
		t.Fatalf("requiring-value category scan emitted a category-only overflow witness: %+v", scanned.OverflowWitness)
	}
	projected := map[string]bool{}
	for key, value := range scanned.BindVars {
		if !strings.Contains(key, "projection") {
			continue
		}
		name, ok := value.(string)
		if !ok || !strings.Contains(scanned.Query, "[@"+key+"]") {
			continue
		}
		projected[name] = true
	}
	if !projected["total"] || !projected["status"] {
		t.Fatalf("requiring-value terminal projection = %v, want both category status and value total by name:\n%s", projected, scanned.Query)
	}
}

func TestSourceCategoryPairScanProjectsOnlyRequestedValuesAndPresence(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "pairs", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", ColumnID: "category", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "value", ColumnID: "value", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
			{Name: "unrelated", ColumnID: "unrelated", Expr: recipe.Expression{Select: "root.birthDate"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "category", Name: "category"}, {ID: "value", Name: "value"}, {ID: "unrelated", Name: "unrelated"}}},
	})
	scan, err := Compile(output, StageColumn{StageID: recipe.ConstructionSourceProjectionID, ColumnID: "category", ValueColumnID: "value", RequireValue: true}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for key, value := range scan.BindVars {
		if strings.HasPrefix(key, "__loom_physical_projection_") && strings.HasSuffix(key, "_name") {
			name, ok := value.(string)
			if ok {
				names[name] = true
			}
		}
	}
	if !names["category"] || !names["value"] || names["unrelated"] || names["id"] {
		t.Fatalf("category pair projection = %#v", names)
	}
	for _, guard := range []string{"root.project == @project", "root.dataset_generation == @dataset_generation", "root.auth_resource_path IN @auth_resource_paths", "FILTER root_scope_allowed == @scope_allowed"} {
		if !strings.Contains(scan.Query, guard) {
			t.Fatalf("missing authorization/filter guard %s", guard)
		}
	}
	if !scan.Proof.ValuePresenceRequired || !scan.Proof.PresenceTracked {
		t.Fatalf("pair scan lost presence proof: %+v", scan.Proof)
	}
	if strings.Contains(scan.Query, "SORT root._key") {
		t.Fatal("unbounded category scan retains unnecessary root ordering")
	}
}

func TestPivotPresenceArangoAudit(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point at a disposable Arango database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}, {Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_presence_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	const generation = "presence-audit-generation"
	const allowedPath = "/presence-audit/allowed"
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		for _, collection := range []string{"Patient", "Observation"} {
			if err := client.ExecuteAQL(cleanupCtx, fmt.Sprintf(`
FOR document IN %s
  FILTER document.project == @project
  REMOVE document IN %s`, collection, collection), map[string]any{"project": project}); err != nil {
				t.Errorf("remove %s presence audit fixtures: %v", collection, err)
			}
		}
	})
	seedPresenceAuditDocuments(t, ctx, client, project, generation, allowedPath)

	fixtures := []struct {
		name   string
		output recipe.Output
	}{
		{name: "source-derive-filter", output: categoryScanConstructionRecipeOutput()},
		{name: "expand", output: constructionExpandOutput()},
		{name: "group", output: constructionGroupRowValuesOutput()},
		{name: "group-pivot", output: constructionCountRowsGroupPivotLineageOutput()},
		{name: "pivot", output: constructionPreviewMultiKeyPivotOutput()},
		{name: "unpivot", output: constructionOracleOutput()},
		{name: "simple-unpivot", output: presenceAuditUnpivotOutput()},
	}
	for _, fixture := range fixtures {
		base := compilePresenceAuditOutput(t, fixture.output, project, generation, []string{allowedPath})
		for _, stage := range base.Stages {
			eligible := eligibleCategoryColumns(stage.Columns)
			for _, category := range eligible {
				var value *lower.CompiledOutputColumn
				for index := range eligible {
					if eligible[index].ID != category.ID {
						value = &eligible[index]
						break
					}
				}
				if value == nil {
					continue
				}
				audited := appendPresenceAuditPivot(fixture.output, stage, category, *value)
				compiled := compilePresenceAuditOutput(t, audited, project, generation, []string{allowedPath})
				scanned, err := Compile(compiled, StageColumn{StageID: stage.ID, ColumnID: category.ID, ValueColumnID: value.ID}, MaxCategoryScanValues, ir.DefaultPhysicalOptimizationPolicy())
				if err != nil {
					t.Logf("%s\t%s\t%s\tCOMPILE_REFUSED\t%v", fixture.name, stage.ID, category.Name, err)
					continue
				}
				presentFalse, missing, conclusive, rows, err := executePresenceAuditScan(ctx, client, scanned)
				if err != nil {
					t.Logf("%s\t%s\t%s\tEXECUTION_ERROR\t%v", fixture.name, stage.ID, category.Name, err)
					continue
				}
				outcome := "COMPLETE"
				if conclusive {
					outcome = "MISSING_UNSUPPORTED"
				} else if missing {
					outcome = "MISSING_TRACKED"
				}
				t.Logf("%s\t%s\t%s\t%s\ttracked=%t\tpresentFalse=%d\trows=%d",
					fixture.name, stage.ID, category.Name, outcome, scanned.Proof.PresenceTracked, presentFalse, rows)
			}
		}
	}
}

func presenceAuditUnpivotOutput() recipe.Output {
	return recipe.Output{
		Name: "presence_audit_unpivot", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "subject_ref", ColumnID: "subject_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "subject_id", Name: "subject_ref"}, {ID: "status_id", Name: "status"}, {ID: "category_id", Name: "category"},
			},
			Steps: []recipe.ConstructionStep{{
				ID:     "unpivot",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
					ConstructionID: "presence_audit_unpivot",
					Inputs: []recipe.ConstructionUnpivotInput{
						{ColumnID: "status_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: presenceAuditString("status")}},
						{ColumnID: "category_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: presenceAuditString("category")}},
					},
					KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id",
					NullRowPolicy: recipe.UnpivotNullPreserve,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "subject_id", Name: "subject_ref"}, {ID: "measure_id", Name: "measure"}, {ID: "value_id", Name: "value"},
				},
			}},
		},
	}
}

func presenceAuditString(value string) *string { return &value }

func compilePresenceAuditOutput(t *testing.T, output recipe.Output, project, generation string, authPaths []string) lower.CompiledRecipeOutput {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: append([]string(nil), authPaths...),
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name,
		TranslationVersion: "presence-audit", Outputs: []recipe.Output{output},
	}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}

func executePresenceAuditScan(ctx context.Context, client *store.Client, scanned CompiledCategoryScanQuery) (presentFalse int, missing, conclusive bool, rows int, err error) {
	if witness := scanned.OverflowWitness; witness != nil {
		witnessRows := 0
		witnessMissing := false
		if err := client.QueryRows(ctx, witness.Query, 100, witness.BindVars, func(row map[string]any) error {
			present, ok := row[scanned.PresentColumn].(bool)
			if !ok {
				return fmt.Errorf("overflow witness has invalid presence marker")
			}
			witnessMissing = witnessMissing || !present
			witnessRows++
			return nil
		}); err != nil {
			return 0, false, false, 0, fmt.Errorf("execute overflow witness: %w", err)
		}
		if witnessRows > scanned.Proof.MaxValues {
			return 0, false, false, witnessRows, nil
		}
		if witnessMissing && !scanned.Proof.PresenceTracked {
			return 0, true, true, witnessRows, nil
		}
	}
	err = client.QueryRows(ctx, scanned.Query, 100, scanned.BindVars, func(row map[string]any) error {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			return fmt.Errorf("category scan row has invalid presence marker")
		}
		if !present {
			presentFalse++
		}
		rows++
		return nil
	})
	if err != nil {
		return 0, false, false, 0, fmt.Errorf("execute category scan: %w", err)
	}
	return presentFalse, presentFalse > 0, false, rows, nil
}

func seedPresenceAuditDocuments(t *testing.T, ctx context.Context, client *store.Client, project, generation, authPath string) {
	t.Helper()
	base := func(key, resourceType string) map[string]any {
		return map[string]any{
			"_key": project + "_" + key, "id": key, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": resourceType, "auth_resource_path": authPath,
		}
	}
	observations := []map[string]any{
		base("obs-all", "Observation"),
		base("obs-missing-status", "Observation"),
		base("obs-null-status", "Observation"),
		base("obs-active-status", "Observation"),
		base("obs-missing-integer", "Observation"),
		base("obs-null-integer", "Observation"),
		base("obs-missing-code", "Observation"),
		base("obs-null-code", "Observation"),
		base("obs-missing-quantity", "Observation"),
		base("obs-null-quantity", "Observation"),
		base("obs-missing-note", "Observation"),
		base("obs-null-note", "Observation"),
		base("obs-empty-note", "Observation"),
		base("obs-null-note-text", "Observation"),
	}
	for index := range observations {
		payload := map[string]any{"id": observations[index]["id"], "resourceType": "Observation", "subject": map[string]any{"reference": "Patient/subject"}}
		switch {
		case index == 0:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = map[string]any{"value": 3, "code": "d"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 1:
			payload["valueInteger"] = int64(5)
		case index == 2:
			payload["status"] = nil
			payload["valueInteger"] = int64(5)
		case index == 3:
			payload["status"], payload["valueInteger"] = "active", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["code"] = "final", map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"] = "final", nil
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", int64(5)
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"], payload["code"] = "final", int64(5), nil
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 13:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 14:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["valueQuantity"] = nil
			payload["note"] = []any{map[string]any{"text": "one"}}
		case index == 15:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 16:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
		case index == 17:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 18:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = nil
		case index == 19:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 20:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{}
		case index == 21:
			payload["status"], payload["valueInteger"] = "final", int64(5)
			payload["code"] = map[string]any{"text": "alpha"}
			payload["note"] = []any{map[string]any{"text": nil}, map[string]any{}}
		}
		observations[index]["payload"] = payload
	}
	patients := []map[string]any{
		base("pat-all", "Patient"),
		base("pat-missing-birth", "Patient"),
		base("pat-null-birth", "Patient"),
		base("pat-missing-active", "Patient"),
		base("pat-null-active", "Patient"),
		base("pat-missing-gender", "Patient"),
		base("pat-null-gender", "Patient"),
		base("pat-missing-multiple", "Patient"),
		base("pat-null-multiple", "Patient"),
	}
	for index := range patients {
		payload := map[string]any{"id": patients[index]["id"], "resourceType": "Patient"}
		if index != 1 && index != 3 && index != 5 && index != 7 {
			payload["birthDate"] = "2000-01-01"
			payload["active"] = true
			payload["gender"] = "female"
			payload["multipleBirthInteger"] = int64(2)
		}
		switch index {
		case 2:
			payload["birthDate"] = nil
		case 4:
			payload["active"] = nil
		case 6:
			payload["gender"] = nil
		case 8:
			payload["multipleBirthInteger"] = nil
		}
		patients[index]["payload"] = payload
	}
	insertPresenceAuditBatch(t, ctx, client, "Observation", observations)
	insertPresenceAuditBatch(t, ctx, client, "Patient", patients)
}

func insertPresenceAuditBatch(t *testing.T, ctx context.Context, client *store.Client, collection string, rows []map[string]any) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		document, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
		t.Fatalf("insert %s presence audit fixtures: %v", collection, err)
	}
}

func TestRelatedCategoryReverseLookupPreservesCrossCollectionResourceTypeMatchAgainstArango(t *testing.T) {
	if os.Getenv("LOOM_TEST_ARANGO_URL") == "" || os.Getenv("LOOM_TEST_ARANGO_DATABASE") == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Patient"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_related_category_collection_identity_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation-related-category-collection-identity"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, collection := range []string{"Specimen", "Patient", "Observation", "fhir_edge"} {
			query := fmt.Sprintf("FOR document IN %s FILTER document.project == @project REMOVE document IN %s", collection, collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"project": project}); err != nil {
				t.Errorf("remove related-category collection-identity fixture from %s: %v", collection, err)
			}
		}
	}()

	key := func(id string) string { return strings.ReplaceAll(project, "-", "") + "_" + id }
	insertDocument := func(collection, resourceType, id string, category any) string {
		t.Helper()
		payload := map[string]any{"id": id, "resourceType": resourceType}
		if category != nil {
			payload["valueQuantity"] = map[string]any{"code": category}
		}
		encoded, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "auth_resource_path": "/all",
			"resourceType": resourceType, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, collection, []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert %s fixture %s claiming resourceType %s: %v", collection, id, resourceType, err)
		}
		return key(id)
	}
	insertEdge := func(id, fromCollection, fromKey, toCollection, toKey, fromType, toType string) {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"_key": key("edge_" + id), "_from": fromCollection + "/" + fromKey,
			"_to": toCollection + "/" + toKey, "project": project, "project_id": project,
			"dataset_generation": generation, "auth_resource_path": "/all",
			"label": "subject_Patient", "from_type": fromType, "to_type": toType,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.InsertBatchRaw(ctx, "fhir_edge", []json.RawMessage{encoded}, false, "document"); err != nil {
			t.Fatalf("insert related-category edge %s: %v", id, err)
		}
	}

	canonicalRoot := insertDocument("Specimen", "Specimen", "root_canonical", nil)
	canonicalPatient := insertDocument("Patient", "Patient", "patient_canonical", nil)
	canonicalObservation := insertDocument("Observation", "Observation", "observation_canonical", "canonical-category")
	insertEdge("canonical_root_patient", "Specimen", canonicalRoot, "Patient", canonicalPatient, "Specimen", "Patient")
	insertEdge("canonical_observation_patient", "Observation", canonicalObservation, "Patient", canonicalPatient, "Observation", "Patient")

	foreignRoot := insertDocument("Specimen", "Specimen", "root_foreign_collection", nil)
	foreignPatient := insertDocument("Observation", "Patient", "patient_stored_as_observation", nil)
	foreignObservation := insertDocument("Observation", "Observation", "observation_foreign_collection", "cross-collection-category")
	insertEdge("foreign_root_patient", "Specimen", foreignRoot, "Observation", foreignPatient, "Specimen", "Patient")
	insertEdge("foreign_observation_patient", "Observation", foreignObservation, "Observation", foreignPatient, "Observation", "Patient")

	missingTargetObservation := insertDocument("Observation", "Observation", "observation_missing_target", "missing-target-category")
	insertEdge("missing_target", "Observation", missingTargetObservation, "Patient", key("patient_missing"), "Observation", "Patient")

	_, _, scanned := compileRelatedCategoryRuntimeScans(t, relatedCategoryScanOutput(relatedCategoryScanOptions{}), project, generation, MaxCategoryScanValues)
	rows := executeReshapeOracleQuery(t, ctx, client, model.CompiledQuery{Query: scanned.Query, BindVars: scanned.BindVars})
	got := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		present, ok := row[scanned.PresentColumn].(bool)
		if !ok {
			t.Fatalf("category presence = %#v, want bool", row[scanned.PresentColumn])
		}
		got[relatedCategoryValueKey(present, row[scanned.ValueColumn])] = struct{}{}
	}
	want := map[string]struct{}{
		relatedCategoryValueKey(true, "canonical-category"):        {},
		relatedCategoryValueKey(true, "cross-collection-category"): {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("related categories = %v, want canonical plus the scoped cross-collection Patient claim and no missing-target category %v; query:\n%s", got, want, scanned.Query)
	}
}

func TestCompileCategoryScanOutputRequiringValueFiltersEmptyPivotColumns(t *testing.T) {
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Dataframe", RootResourceType: "Patient", RowGrain: "patient", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "birth_date", ColumnID: "birth_date_id", Expr: recipe.Expression{Select: "root.birthDate"}},
		},
	})
	category, value := "gender", "birth_date"
	scanned, err := Compile(output, OutputColumn{Column: category, ValueColumn: value, RequireValue: true}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !scanned.Proof.ValuePresenceRequired {
		t.Fatalf("requiring-value proof did not record its value policy: %#v", scanned.Proof)
	}
	if scanned.Proof.ValueColumnID != value {
		t.Fatalf("requiring-value proof bound value column %q, want public column %q: %#v", scanned.Proof.ValueColumnID, value, scanned.Proof)
	}
	if !strings.Contains(scanned.Query, "FILTER __loom_category_row[@__loom_category_value_column] != null") {
		t.Fatalf("requiring-value category scan omitted its non-null value filter:\n%s", scanned.Query)
	}
	if got := scanned.BindVars["__loom_category_value_column"]; got != value {
		t.Fatalf("requiring-value category scan bound value column to %#v, want %s", got, value)
	}
	baseline, err := Compile(output, OutputColumn{Column: category}, 256, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(baseline.Query, "__loom_category_nonnull") {
		t.Fatalf("fixture does not exercise the category-only indexed shortcut:\n%s", baseline.Query)
	}
	if strings.Contains(scanned.Query, "__loom_category_nonnull") {
		t.Fatalf("requiring-value category scan used the category-only indexed shortcut:\n%s", scanned.Query)
	}
}
