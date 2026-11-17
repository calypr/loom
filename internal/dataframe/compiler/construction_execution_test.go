package compiler

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestConstructionPreviewLimitAppliesAfterFinalStage(t *testing.T) {
	value := "active"
	output := recipe.Output{
		Name: "construction_preview", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "gender"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "filter_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "status_id", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}},
				}},
				Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction_preview", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlan(ArangoTarget(), resolved, 3, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled queries = %d, want one", len(queries))
	}
	query := queries[0]
	finalRows := strings.Index(testAQLText(t, query.Command), "FOR __loom_construction_final_row IN")
	limit := strings.Index(testAQLText(t, query.Command), "LIMIT @limit")
	if finalRows < 0 || limit < finalRows {
		t.Fatalf("preview limit is not applied after the final stage rows:\n%s", testAQLText(t, query.Command))
	}
	if got, exists := testAQLBinds(t, query.Command)["limit"]; !exists || got != 3 {
		t.Fatalf("preview limit bind = %#v, exists=%t", got, exists)
	}
	if got := testAQLBinds(t, query.Command)["@root_collection"]; got != "Patient" {
		t.Fatalf("root collection bind = %#v, want collection-bind form with Patient", got)
	}
	if _, exists := testAQLBinds(t, query.Command)["root_collection"]; exists {
		t.Fatal("plain root collection bind leaked into executable AQL bind variables")
	}
	if strings.Contains(testAQLText(t, query.Command)[:finalRows], "LIMIT @limit") {
		t.Fatalf("preview limit leaked into source scan before construction stages:\n%s", testAQLText(t, query.Command))
	}
}

func TestStreamingMissingCategoryPivotUsesStoredParentProjectionIndex(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "loom_dev_cda_fhir", DatasetGeneration: "cda-fhir-v1"}
	output := streamingMissingCategoryPivotTestOutput()
	id := recipe.StageColumn{ID: "id_id", Name: "id", Type: "string", Nullable: true}
	subject := recipe.StageColumn{ID: "subject_id", Name: "subject", Type: "string", Nullable: true}
	output.Fields = append([]recipe.Field{
		{Name: "id", ColumnID: id.ID, Expr: recipe.Expression{Select: "root.id"}},
		{Name: "subject", ColumnID: subject.ID, Expr: recipe.Expression{Select: "root.subject.reference"}},
	}, output.Fields...)
	output.Construction.SourceColumns = append([]recipe.StageColumn{id, subject}, output.Construction.SourceColumns...)
	pivot := output.Construction.Steps[0].Operation.Pivot
	pivot.GroupKeyIDs = []string{subject.ID, "status_id"}
	output.Construction.Steps[0].Outputs = append([]recipe.StageColumn{subject}, output.Construction.Steps[0].Outputs...)
	bindings.IncludeSourceIdentity = true
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
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want one", len(compiled.Outputs))
	}
	if !outputHasCompositeSource(compiled.Outputs[0]) {
		t.Fatal("production-shaped Pivot lost its composite-source classification under IncludeSourceIdentity")
	}
	query, err := CompileRecipeOutput(ArangoTarget(), compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if query.PreviewCoveringIndex == nil {
		t.Fatalf("direct-root typed-MISSING Pivot has no stored-values index contract: %s", testAQLText(t, query.Command))
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[0].Plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if bindings.IncludeSourceIdentity {
		physical, _, err = withPreviewSourceResourceID(compiled.Outputs[0], physical)
		if err != nil {
			t.Fatal(err)
		}
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 25)
	if err != nil {
		t.Fatal(err)
	}
	physical = withoutUnusedTerminalPivotPresenceCompanions(physical)
	legacyIndex := previewCoveringIndexSpec(physical)
	if legacyIndex != nil {
		t.Fatalf("MISSING-presence plan unexpectedly has a field-only pre-override index proof: %+v", legacyIndex)
	}
	if query.PreviewCoveringIndex.Supersedes != nil {
		t.Fatalf("compiler claimed replacement without an exact pre-override spec: %+v", query.PreviewCoveringIndex.Supersedes)
	}
	wantFields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status", "payload.subject.reference"}
	wantStoredValues := []string{"payload.valueQuantity"}
	if !reflect.DeepEqual(query.PreviewCoveringIndex.Fields, wantFields) || !reflect.DeepEqual(query.PreviewCoveringIndex.StoredValues, wantStoredValues) {
		t.Fatalf("stored-values index = %+v, want fields=%v storedValues=%v", query.PreviewCoveringIndex, wantFields, wantStoredValues)
	}
	unhinted, err := aql.RenderPhysicalPlanWithStreamingMissingCategoryPivotPreview(physical)
	if err != nil {
		t.Fatalf("render unhinted streaming Pivot: %v", err)
	}
	if strings.Contains(unhinted.Query, "FIRST([root.payload.valueQuantity])") {
		t.Fatalf("stored-parent access wrapper leaked into unhinted rendering:\n%s", unhinted.Query)
	}
	genericFields := []string{"project", "dataset_generation", "auth_resource_path", "_key"}
	if previewCoveringIndexNameWithStoredValues("Observation", wantFields, wantStoredValues) ==
		previewCoveringIndexNameWithStoredValues("Observation", genericFields, wantStoredValues) {
		t.Fatal("scalar key fields did not participate in deterministic index identity")
	}
	if previewCoveringIndexNameWithStoredValues("Observation", wantFields, wantStoredValues) ==
		previewCoveringIndexNameWithStoredValues("Observation", wantFields, []string{"payload.valueQuantity.code"}) {
		t.Fatal("different stored-value definition reused the same index identity")
	}
	for _, field := range query.PreviewCoveringIndex.Fields {
		for _, stored := range query.PreviewCoveringIndex.StoredValues {
			if previewCoveringIndexPathsOverlap(field, stored) {
				t.Fatalf("index field %q overlaps stored-value parent %q", field, stored)
			}
		}
	}
	for _, required := range []string{
		"OPTIONS { indexHint: \"" + query.PreviewCoveringIndex.Name + "\", forceIndexHint: false }",
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER root_scope_allowed == @scope_allowed",
		"LET __loom_physical_construction_reshape_stream_source_parent = FIRST([root.payload.valueQuantity])",
		"__loom_physical_construction_reshape_stream_source_parent.code",
		"__loom_physical_construction_reshape_stream_source_parent.value",
		"IS_OBJECT(__loom_physical_construction_reshape_stream_source_parent)",
		"HAS(__loom_physical_construction_reshape_stream_source_parent, @",
		"TABLE_PIVOT_UNLISTED_CATEGORY",
	} {
		if !strings.Contains(testAQLText(t, query.Command), required) {
			t.Fatalf("stored-parent streaming query omitted %q:\n%s", required, testAQLText(t, query.Command))
		}
	}
	compiledPivot := compiled.Outputs[0].Plan.StageSequence.Stages[0].GroupedPivot
	if streamingTestBindKey(testAQLBinds(t, query.Command), "reshape_category_column", compiledPivot.CategoryColumn) != "" ||
		streamingTestBindKey(testAQLBinds(t, query.Command), "reshape_category_presence_column", compiledPivot.CategoryPresenceColumn) != "" {
		t.Fatalf("direct scalar Pivot retained field-name binds: %#v", testAQLBinds(t, query.Command))
	}
	categoryValue := streamingTestDirectScalarVariable(testAQLText(t, query.Command), "__loom_physical_construction_reshape_stream_source_parent.code")
	presenceValue := streamingTestDirectScalarVariable(testAQLText(t, query.Command), "IS_OBJECT(__loom_physical_construction_reshape_stream_source_parent)")
	if categoryValue == "" || presenceValue == "" {
		t.Fatalf("direct scalar category or presence projection missing: category=%q presence=%q\n%s", categoryValue, presenceValue, testAQLText(t, query.Command))
	}
	missingMatch := fmt.Sprintf("NOT (%s == true)", presenceValue)
	nullMatch := fmt.Sprintf("(%s == true AND %s == null)", presenceValue, categoryValue)
	if !strings.Contains(testAQLText(t, query.Command), missingMatch) || !strings.Contains(testAQLText(t, query.Command), nullMatch) {
		t.Fatalf("stored-parent projection merged MISSING and explicit NULL categories: missing=%q null=%q\n%s", missingMatch, nullMatch, testAQLText(t, query.Command))
	}
	if strings.Contains(testAQLText(t, query.Command), "HAS(root.payload.valueQuantity.code)") {
		t.Fatalf("presence test inspected the leaf instead of its stored parent object:\n%s", testAQLText(t, query.Command))
	}
	if !strings.Contains(testAQLText(t, query.Command), " AGGREGATE ") || strings.Contains(testAQLText(t, query.Command), "INTO __loom_reshape_pivot_group_rows_") {
		t.Fatalf("stored-parent preview stopped using complete-population streaming aggregation:\n%s", testAQLText(t, query.Command))
	}
}

func TestRootGroupedPivotPreviewBoundsParentsBeforeRelatedExpansion(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	expand := relatedExpandOracleOutput(recipe.ExpansionPreserveParent).Construction.Steps[1]
	expand.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
	expand.Outputs = append(append([]recipe.StageColumn(nil), output.Construction.SourceColumns...), recipe.StageColumn{ID: "observation-id", Name: "observation_id", Type: "string", Nullable: true})
	pivot := output.Construction.Steps[0]
	pivot.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: expand.ID}}
	output.Construction.Steps = []recipe.ConstructionStep{expand, pivot}
	query := compileConstructionPivotPreview(t, output, 3)
	limit := strings.Index(testAQLText(t, query.Command), "LIMIT @limit")
	stage := strings.Index(testAQLText(t, query.Command), "LET __loom_construction_stage_1")
	if !query.PartialValidation || limit < 0 || stage < limit || !strings.Contains(testAQLText(t, query.Command)[stage:], "COLLECT") {
		t.Fatalf("preview must bound parents but retain complete related contributions and grouping:\n%s", testAQLText(t, query.Command))
	}
	full := compileConstructionPivotPreview(t, output, 0)
	if strings.Contains(testAQLText(t, full.Command), "LIMIT @limit") || full.PartialValidation {
		t.Fatal("preview window leaked into full execution")
	}
	expand.Operation.RelatedExpand.EmptyPolicy = recipe.ExpansionExclude
	expand.Outputs[len(expand.Outputs)-1].Nullable = false
	output.Construction.Steps[0] = expand
	query = compileConstructionPivotPreview(t, output, 3)
	if strings.Contains(testAQLText(t, query.Command)[:strings.Index(testAQLText(t, query.Command), "LET __loom_construction_stage_1")], "LIMIT @limit") {
		t.Fatal("dropping unmatched parents must not use a source-root window")
	}
}

func TestConstructionPreviewBoundsProvenRootIDPivotBeforeSourceMaterialization(t *testing.T) {
	query := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.id", false), 3)
	sourceStart := strings.Index(testAQLText(t, query.Command), "LET __loom_construction_source_projection = (")
	stageStart := strings.Index(testAQLText(t, query.Command), "LET __loom_construction_stage_1 = (")
	if sourceStart < 0 || stageStart <= sourceStart {
		t.Fatalf("construction query has no source projection followed by terminal stage:\n%s", testAQLText(t, query.Command))
	}
	sourceQuery := testAQLText(t, query.Command)[sourceStart:stageStart]
	sortAt := strings.Index(sourceQuery, "SORT root.id ASC")
	limitAt := strings.Index(sourceQuery, "LIMIT @limit")
	returnAt := strings.Index(sourceQuery, "RETURN ")
	if sortAt < 0 || limitAt <= sortAt || returnAt <= limitAt {
		t.Fatalf("root-id sort/limit did not run before source projection materialization:\n%s", sourceQuery)
	}
	if got, exists := testAQLBinds(t, query.Command)["limit"]; !exists || got != 3 {
		t.Fatalf("preview limit bind = %#v, exists=%t", got, exists)
	}
	if !query.PartialValidation {
		t.Fatal("preview with a source window was not marked as partial validation")
	}
	stageQuery := testAQLText(t, query.Command)[stageStart:]
	if strings.Contains(stageQuery, "COLLECT") {
		t.Fatalf("proven root-ID pivot still materialized group rows:\n%s", testAQLText(t, query.Command))
	}
	if strings.Count(stageQuery, "FOR ") != 2 {
		t.Fatalf("root-ID pivot emitted per-category group-row scans:\n%s", testAQLText(t, query.Command))
	}
	if strings.Contains(stageQuery, "SORT __loom_construction_pivot_group") {
		t.Fatalf("root-ID pivot retained an intermediate sort before the final identity sort:\n%s", testAQLText(t, query.Command))
	}
	for _, validation := range []string{"TYPENAME(", "TABLE_PIVOT_VALUE_TYPE_MISMATCH", "TABLE_PIVOT_CELL_CARDINALITY", "TABLE_PIVOT_UNLISTED_CATEGORY"} {
		if !strings.Contains(stageQuery, validation) {
			t.Fatalf("root-ID pivot dropped %q validation:\n%s", validation, testAQLText(t, query.Command))
		}
	}

	full := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.id", false), 0)
	if strings.Contains(testAQLText(t, full.Command), "LIMIT @limit") || strings.Contains(testAQLText(t, full.Command), "SORT root.id ASC") {
		t.Fatalf("full construction execution received a preview-only source window:\n%s", testAQLText(t, full.Command))
	}
	if strings.Contains(testAQLText(t, full.Command), "COLLECT") || strings.Count(testAQLText(t, full.Command), "FOR ") != 3 {
		t.Fatalf("full direct-ID pivot did not retain the one-input-per-group renderer:\n%s", testAQLText(t, full.Command))
	}
	if !strings.Contains(testAQLText(t, full.Command), "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("full construction output lost deterministic final row-identity ordering:\n%s", testAQLText(t, full.Command))
	}
	if full.PartialValidation {
		t.Fatal("full construction execution was marked as partial validation")
	}
}

func TestConstructionPreviewRootIDProofUsesLoweredProjectionNotSemanticAlias(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	output.Fields[0].FieldRef = "source-column:specimen-id"
	query := compileConstructionPivotPreview(t, output, 3)
	if !strings.Contains(testAQLText(t, query.Command), "SORT root.id ASC") || !strings.Contains(testAQLText(t, query.Command), "LIMIT @limit") {
		t.Fatalf("direct lowered root id with opaque semantic identity did not qualify for source window:\n%s", testAQLText(t, query.Command))
	}
}

func TestConstructionPreviewRootIDSourceMetadataIsFHIROptionalScalar(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
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
	for _, column := range compiled.Outputs[0].Stages[0].Columns {
		if column.ID != "id_id" {
			continue
		}
		if column.Kind != "string" || column.Cardinality != "optional_one" || !column.Nullable {
			t.Fatalf("FHIR id source metadata = %+v; proof must account for optional FHIR schema cardinality", column)
		}
		return
	}
	t.Fatalf("compiled source stage has no id_id column: %+v", compiled.Outputs[0].Stages[0].Columns)
}

func TestConstructionPreviewUsesTerminalPivotWindowWithoutRootIDProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output recipe.Output
	}{
		{name: "non-identity group key", output: constructionPreviewPivotOutput("root.gender", false)},
		{name: "prior filter stage", output: constructionPreviewPivotOutput("root.id", true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := compileConstructionPivotPreview(t, tc.output, 3)
			if strings.Contains(testAQLText(t, query.Command), "SORT root.id ASC") {
				t.Fatalf("unproven plan received the early source order:\n%s", testAQLText(t, query.Command))
			}
			if strings.Count(testAQLText(t, query.Command), "LIMIT @limit") != 2 {
				t.Fatalf("terminal Pivot preview should limit complete groups and retain the final limit:\n%s", testAQLText(t, query.Command))
			}
			if !query.PartialValidation {
				t.Fatal("preview group window was not marked as partial validation")
			}
			if tc.name == "non-identity group key" && !strings.Contains(testAQLText(t, query.Command), "COLLECT") {
				t.Fatalf("nonunique group key bypassed canonical COLLECT:\n%s", testAQLText(t, query.Command))
			}
		})
	}
}

func TestConstructionPreviewTerminalPivotLimitsCompleteGroupsBeforeCellReduction(t *testing.T) {
	preview := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 3)
	if !preview.PartialValidation {
		t.Fatal("terminal Pivot preview window was not marked as partial validation")
	}
	selectedStart := strings.Index(testAQLText(t, preview.Command), "LET __loom_physical_construction_reshape_preview_selected_group_keys = (")
	if selectedStart < 0 {
		t.Fatalf("terminal Pivot preview has no first-pass group selection:\n%s", testAQLText(t, preview.Command))
	}
	selectedLimit := strings.Index(testAQLText(t, preview.Command)[selectedStart:], "LIMIT @limit") + selectedStart
	sourceStart := strings.Index(testAQLText(t, preview.Command), "LET __loom_construction_source_projection = (")
	filterAt := strings.Index(testAQLText(t, preview.Command), "FILTER root.payload.gender IN __loom_physical_construction_reshape_preview_selected_group_keys")
	stageStart := strings.Index(testAQLText(t, preview.Command), "LET __loom_construction_stage_1 = (")
	if selectedLimit <= selectedStart || sourceStart <= selectedLimit || filterAt <= sourceStart || stageStart <= filterAt {
		t.Fatalf("preview must select complete group tuples, filter the direct source scan before its wide projection, then run the Pivot stage:\n%s", testAQLText(t, preview.Command))
	}
	keySource := testAQLText(t, preview.Command)[selectedStart:sourceStart]
	if strings.Contains(keySource, "root.payload.multipleBirthInteger") || strings.Contains(keySource, "root.payload.category") ||
		strings.Count(keySource, "RETURN {") != 1 {
		t.Fatalf("first pass materialized non-key source projections:\n%s", keySource)
	}
	sourceQuery := testAQLText(t, preview.Command)[sourceStart:stageStart]
	groupFilterAt := strings.Index(sourceQuery, "FILTER root.payload.gender IN __loom_physical_construction_reshape_preview_selected_group_keys")
	projectionAt := strings.Index(sourceQuery, "RETURN ")
	if !strings.Contains(sourceQuery, "root.payload.multipleBirthInteger") || groupFilterAt < 0 || projectionAt <= groupFilterAt {
		t.Fatalf("second pass must filter raw source keys before materializing the full Pivot contributor projection:\n%s", sourceQuery)
	}
	for _, scope := range []string{
		"root.project == @project",
		"root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths",
	} {
		if strings.Count(testAQLText(t, preview.Command), scope) != 2 {
			t.Fatalf("source scope %q appears %d times, want both scans:\n%s", scope, strings.Count(testAQLText(t, preview.Command), scope), testAQLText(t, preview.Command))
		}
	}
	stage := testAQLText(t, preview.Command)[stageStart:]
	collectAt := strings.Index(stage, "COLLECT ")
	cellValidationAt := strings.Index(stage, "TABLE_PIVOT_CELL_CARDINALITY")
	if collectAt < 0 || cellValidationAt <= collectAt {
		t.Fatalf("preview must collect selected groups completely before computing cells:\n%s", stage)
	}
	if strings.Count(stage, "LIMIT @limit") != 1 || strings.Count(testAQLText(t, preview.Command), "LIMIT @limit") != 2 {
		t.Fatalf("expected first-pass group and final output limits:\n%s", testAQLText(t, preview.Command))
	}
	if got := testAQLBinds(t, preview.Command)["limit"]; got != 3 {
		t.Fatalf("preview limit bind = %#v, want 3", got)
	}

	full := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 0)
	if full.PartialValidation || strings.Contains(testAQLText(t, full.Command), "LIMIT @limit") || strings.Contains(testAQLText(t, full.Command), "reshape_preview_identity") {
		t.Fatalf("full execution received the preview-only group window:\n%s", testAQLText(t, full.Command))
	}
}

func TestConstructionPreviewTwoScanUsesSeparateProjectionBindNamespaces(t *testing.T) {
	output := constructionPreviewPivotOutput("root.gender", false)
	output.Fields[0], output.Fields[1] = output.Fields[1], output.Fields[0]
	query := compileConstructionPivotPreview(t, output, 3)
	if got := testAQLBinds(t, query.Command)["__loom_physical_preview_key_projection_0_name"]; got != "resource_id" {
		t.Fatalf("first-pass projection-name bind = %#v, want resource_id", got)
	}
	if got := testAQLBinds(t, query.Command)["__loom_physical_projection_0_name"]; got != "_key" {
		t.Fatalf("second-pass projection-name bind = %#v, want _key", got)
	}
	if _, ok := testAQLBinds(t, query.Command)["__loom_physical_reshape_preview_source_group_column"]; ok ||
		!strings.Contains(testAQLText(t, query.Command), "FILTER root.payload.gender IN __loom_physical_construction_reshape_preview_selected_group_keys") {
		t.Fatalf("second-pass filter did not use the compiler-proven direct group source path:\n%s", testAQLText(t, query.Command))
	}
}

func TestConstructionPreviewCoveringIndexMetadataAndRootHint(t *testing.T) {
	query := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 3)
	index := query.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("eligible terminal Pivot preview has no covering-index spec:\n%s", testAQLText(t, query.Command))
	}
	if index.PrepareAfterPreview {
		t.Fatal("Pivot covering index must remain prepared before the preview query")
	}
	if index.Collection != "Patient" {
		t.Fatalf("index collection = %q, want Patient", index.Collection)
	}
	if !strings.HasPrefix(index.Name, previewCoveringIndexNamePrefix) {
		t.Fatalf("index name = %q, want prefix %q", index.Name, previewCoveringIndexNamePrefix)
	}
	wantFields := []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.gender", "payload.multipleBirthInteger"}
	if !reflect.DeepEqual(index.Fields, wantFields) {
		t.Fatalf("index fields = %#v, want %#v", index.Fields, wantFields)
	}
	wantHint := "OPTIONS { indexHint: \"" + index.Name + "\", forceIndexHint: false }"
	if strings.Count(testAQLText(t, query.Command), wantHint) != 2 {
		t.Fatalf("both source scans must use the same non-forcing index hint %q:\n%s", wantHint, testAQLText(t, query.Command))
	}
	if strings.Count(testAQLText(t, query.Command), "FOR root IN @@root_collection "+wantHint) != 2 {
		t.Fatalf("index hint was not placed on both root source scans:\n%s", testAQLText(t, query.Command))
	}

	repeated := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 3)
	if repeated.PreviewCoveringIndex == nil || repeated.PreviewCoveringIndex.Name != index.Name || !reflect.DeepEqual(repeated.PreviewCoveringIndex.Fields, index.Fields) {
		t.Fatalf("covering-index metadata is not deterministic: first=%+v second=%+v", index, repeated.PreviewCoveringIndex)
	}
}

func TestConstructionPreviewCoveringIndexIneligiblePlansStayUnhinted(t *testing.T) {
	tests := []struct {
		name   string
		output recipe.Output
		limit  int
	}{
		{name: "full execution", output: constructionPreviewPivotOutput("root.gender", false), limit: 0},
		{name: "prior stage", output: constructionPreviewPivotOutput("root.gender", true), limit: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := compileConstructionPivotPreview(t, test.output, test.limit)
			if query.PreviewCoveringIndex != nil {
				t.Fatalf("ineligible plan produced index metadata: %+v", query.PreviewCoveringIndex)
			}
			if strings.Contains(testAQLText(t, query.Command), "indexHint:") {
				t.Fatalf("ineligible plan received an index hint:\n%s", testAQLText(t, query.Command))
			}
		})
	}
}

func TestConstructionGroupPreviewUsesProjectionFieldsAndOneRootHint(t *testing.T) {
	query := compileConstructionOutputQuery(t, constructionGroupRowValuesOutput(), "group-covering-project", "group-covering-generation")
	index := query.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("eligible terminal Group preview has no covering-index spec:\n%s", testAQLText(t, query.Command))
	}
	if index.Collection != "Observation" {
		t.Fatalf("index collection = %q, want Observation", index.Collection)
	}
	if !index.PrepareAfterPreview {
		t.Fatal("eligible Group covering index should be prepared after the preview query")
	}
	wantFields := []string{"project", "dataset_generation", "auth_resource_path", "payload.note", "payload.status"}
	if !reflect.DeepEqual(index.Fields, wantFields) {
		t.Fatalf("index fields = %#v, want %#v", index.Fields, wantFields)
	}
	wantHint := "OPTIONS { indexHint: \"" + index.Name + "\", forceIndexHint: false }"
	if strings.Count(testAQLText(t, query.Command), wantHint) != 1 || !strings.Contains(testAQLText(t, query.Command), "FOR root IN @@root_collection "+wantHint) {
		t.Fatalf("Group source should receive one non-forcing root hint %q:\n%s", wantHint, testAQLText(t, query.Command))
	}
	if strings.Count(testAQLText(t, query.Command), "FOR root IN @@root_collection") != 1 {
		t.Fatalf("Group preview unexpectedly changed its source scan count:\n%s", testAQLText(t, query.Command))
	}
	sequential := query.PreviewGroupScan
	if sequential == nil || sequential.Collection != index.Collection {
		t.Fatalf("eligible direct Group preview has no scan alternatives: %+v", sequential)
	}
	if !strings.Contains(testAQLText(t, sequential.Sequential), "OPTIONS { disableIndex: true }") || strings.Contains(testAQLText(t, sequential.Sequential), "indexHint:") {
		t.Fatalf("sequential alternative did not disable only its direct root index:\n%s", testAQLText(t, sequential.Sequential))
	}
	for _, scope := range []string{
		"root.project == @project", "root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths", "FILTER root_scope_allowed == @scope_allowed",
	} {
		if !strings.Contains(testAQLText(t, sequential.ScopeCount), scope) {
			t.Fatalf("scope count omitted canonical filter %q:\n%s", scope, testAQLText(t, sequential.ScopeCount))
		}
	}
	if !strings.Contains(testAQLText(t, sequential.ScopeCount), "COLLECT WITH COUNT INTO") ||
		!strings.Contains(testAQLText(t, sequential.ScopeCount), "RETURN {count:") ||
		strings.Contains(testAQLText(t, sequential.ScopeCount), "payload.note") || strings.Contains(testAQLText(t, sequential.ScopeCount), "construction_group") {
		t.Fatalf("scope count should return a named count without projecting or grouping source values:\n%s", testAQLText(t, sequential.ScopeCount))
	}
	for _, bind := range []string{"project", "dataset_generation", "auth_resource_paths"} {
		if _, ok := testAQLBinds(t, sequential.ScopeCount)[bind]; !ok {
			t.Fatalf("scope count omitted %q bind", bind)
		}
	}

	existingName := "loom_pivot_preview_existing_compatible"
	queryWithExistingIndexCommand, err := RenderPreviewPhysicalPlanWithRootIndexHint(query.PhysicalPlan, existingName)
	if err != nil {
		t.Fatalf("render Group preview with existing compatible index: %v", err)
	}
	queryWithExistingIndex, existingBinds := testAQLView(t, queryWithExistingIndexCommand)
	if !strings.Contains(queryWithExistingIndex, "OPTIONS { indexHint: \""+existingName+"\", forceIndexHint: false }") ||
		strings.Contains(queryWithExistingIndex, "forceIndexHint: true") {
		t.Fatalf("existing index hint is not non-forcing:\n%s", queryWithExistingIndex)
	}
	if !reflect.DeepEqual(existingBinds, testAQLBinds(t, query.Command)) {
		t.Fatalf("existing-index render changed query binds: got=%v want=%v", existingBinds, testAQLBinds(t, query.Command))
	}

	repeated := compileConstructionOutputQuery(t, constructionGroupRowValuesOutput(), "group-covering-project", "group-covering-generation")
	if repeated.PreviewCoveringIndex == nil || repeated.PreviewCoveringIndex.Name != index.Name || !repeated.PreviewCoveringIndex.PrepareAfterPreview ||
		!reflect.DeepEqual(repeated.PreviewCoveringIndex.Fields, index.Fields) {
		t.Fatalf("Group covering-index metadata is not deterministic: first=%+v second=%+v", index, repeated.PreviewCoveringIndex)
	}

	fullOutput := lowerConstructionOutput(t, constructionGroupRowValuesOutput(), recipe.RuntimeBindings{
		Project: "group-covering-project", DatasetGeneration: "group-covering-generation",
	})
	full, err := CompileRecipeOutput(ArangoTarget(), fullOutput, recipe.RuntimeBindings{
		Project: "group-covering-project", DatasetGeneration: "group-covering-generation",
	}, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if full.PreviewCoveringIndex != nil || strings.Contains(testAQLText(t, full.Command), "indexHint:") {
		t.Fatalf("full Group execution received preview index metadata or a hint: %+v\n%s", full.PreviewCoveringIndex, testAQLText(t, full.Command))
	}
}

func TestConstructionGroupScanAlternativeRejectsExtraSourceFilters(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "group-scan-project", DatasetGeneration: "group-scan-generation"}
	output := lowerConstructionOutput(t, constructionGroupRowValuesOutput(), bindings)
	if len(output.Plan.Operations) < 2 || output.Plan.Operations[1].Kind != ir.PhysicalFilterOp {
		t.Fatalf("expected canonical project scope filter in source plan: %+v", output.Plan.Operations)
	}
	operations := append([]ir.PhysicalOperation(nil), output.Plan.Operations[:len(output.Plan.Operations)-1]...)
	operations = append(operations, output.Plan.Operations[1], output.Plan.Operations[len(output.Plan.Operations)-1])
	output.Plan.Operations = operations
	query, err := CompileRecipeOutput(ArangoTarget(), output, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if query.PreviewCoveringIndex == nil {
		t.Fatal("extra source filter should not disable the independently safe covering-index hint")
	}
	if query.PreviewGroupScan != nil {
		t.Fatalf("scan decision metadata must reject a Group source with a non-scope filter: %+v", query.PreviewGroupScan)
	}
}

func compileConstructionPivotPreview(t *testing.T, output recipe.Output, limit int) CompiledQuery {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlan(ArangoTarget(), resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled queries = %d, want one", len(queries))
	}
	return queries[0]
}

func constructionPreviewPivotOutput(groupSelector string, priorFilter bool) recipe.Output {
	categoryFemale, categoryMale := "female", "male"
	filterGender := "female"
	steps := make([]recipe.ConstructionStep, 0, 2)
	input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
	if priorFilter {
		steps = append(steps, recipe.ConstructionStep{
			ID: "filter_preview_rows", Inputs: []recipe.ConstructionInputRef{input},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "category_id", Operator: recipe.FilterEquals,
				Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterGender}},
			}},
			Outputs: []recipe.StageColumn{{ID: "id_id", Name: "resource_id"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}},
		})
		input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: "filter_preview_rows"}
	}
	steps = append(steps, recipe.ConstructionStep{
		ID: "pivot_preview_rows", Inputs: []recipe.ConstructionInputRef{input},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
			ConstructionID: "preview_pivot", GroupKeyIDs: []string{"id_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id",
			Categories: []recipe.ConstructionPivotCategory{
				{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &categoryFemale}, OutputColumnID: "female_amount_id"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &categoryMale}, OutputColumnID: "male_amount_id"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}},
		Outputs: []recipe.StageColumn{{ID: "id_id", Name: "resource_id"}, {ID: "female_amount_id", Name: "female_amount"}, {ID: "male_amount_id", Name: "male_amount"}},
	})
	return recipe.Output{
		Name: "preview_pivot", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "resource_id", ColumnID: "id_id", Expr: recipe.Expression{Select: groupSelector}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.gender"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.multipleBirthInteger"}},
		},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id_id", Name: "resource_id"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}},
			Steps:         steps,
		},
	}
}
