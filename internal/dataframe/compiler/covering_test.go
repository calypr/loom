package compiler

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
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
					Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &value}},
				}},
				Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction_preview", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 3, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled queries = %d, want one", len(queries))
	}
	query := queries[0]
	finalRows := strings.Index(query.Query, "FOR __loom_construction_final_row IN")
	limit := strings.Index(query.Query, "LIMIT @limit")
	if finalRows < 0 || limit < finalRows {
		t.Fatalf("preview limit is not applied after the final stage rows:\n%s", query.Query)
	}
	if got, exists := query.BindVars["limit"]; !exists || got != 3 {
		t.Fatalf("preview limit bind = %#v, exists=%t", got, exists)
	}
	if got := query.BindVars["@root_collection"]; got != "Patient" {
		t.Fatalf("root collection bind = %#v, want collection-bind form with Patient", got)
	}
	if _, exists := query.BindVars["root_collection"]; exists {
		t.Fatal("plain root collection bind leaked into executable AQL bind variables")
	}
	if strings.Contains(query.Query[:finalRows], "LIMIT @limit") {
		t.Fatalf("preview limit leaked into source scan before construction stages:\n%s", query.Query)
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
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
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if query.PreviewCoveringIndex == nil {
		t.Fatalf("direct-root typed-MISSING Pivot has no stored-values index contract: %s", query.Query)
	}
	physical, err := optimize.OptimizePhysicalPlanWithPolicy(compiled.Outputs[0].Plan, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if bindings.IncludeSourceIdentity {
		physical, _ = withPreviewSourceResourceID(compiled.Outputs[0], physical)
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
	wantFields := []string{"project", "dataset_generation", "_key", "payload.id", "payload.status", "payload.subject.reference"}
	wantStoredValues := model.PreviewQuantityCategoryStoredValues()
	if !reflect.DeepEqual(query.PreviewCoveringIndex.Fields, wantFields) || !reflect.DeepEqual(query.PreviewCoveringIndex.StoredValues, wantStoredValues) {
		t.Fatalf("stored-values index = %+v, want fields=%v storedValues=%v", query.PreviewCoveringIndex, wantFields, wantStoredValues)
	}
	unhinted, err := aql.Render(physical, aql.Options{Preview: aql.StreamingMissingCategoryPivotPreview{}})
	if err != nil {
		t.Fatalf("render unhinted streaming Pivot: %v", err)
	}
	if strings.Contains(unhinted.Query, "FIRST([root.payload.valueQuantity])") {
		t.Fatalf("stored-parent access wrapper leaked into unhinted rendering:\n%s", unhinted.Query)
	}
	genericFields := []string{"project", "dataset_generation", "_key"}
	if model.PreviewCoveringIndexNameWithStoredValues("Observation", wantFields, wantStoredValues) ==
		model.PreviewCoveringIndexNameWithStoredValues("Observation", genericFields, wantStoredValues) {
		t.Fatal("scalar key fields did not participate in deterministic index identity")
	}
	if model.PreviewCoveringIndexNameWithStoredValues("Observation", wantFields, wantStoredValues) ==
		model.PreviewCoveringIndexNameWithStoredValues("Observation", wantFields, []string{"payload.valueQuantity.code"}) {
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
		if !strings.Contains(query.Query, required) {
			t.Fatalf("stored-parent streaming query omitted %q:\n%s", required, query.Query)
		}
	}
	compiledPivot := compiled.Outputs[0].Plan.StageSequence.Stages[0].GroupedPivot
	if streamingTestBindKey(query.BindVars, "reshape_category_column", compiledPivot.CategoryColumn) != "" ||
		streamingTestBindKey(query.BindVars, "reshape_category_presence_column", compiledPivot.CategoryPresenceColumn) != "" {
		t.Fatalf("direct scalar Pivot retained field-name binds: %#v", query.BindVars)
	}
	categoryValue := streamingTestDirectScalarVariable(query.Query, "__loom_physical_construction_reshape_stream_source_parent.code")
	presenceValue := streamingTestDirectScalarVariable(query.Query, "IS_OBJECT(__loom_physical_construction_reshape_stream_source_parent)")
	if categoryValue == "" || presenceValue == "" {
		t.Fatalf("direct scalar category or presence projection missing: category=%q presence=%q\n%s", categoryValue, presenceValue, query.Query)
	}
	missingMatch := fmt.Sprintf("NOT (%s == true)", presenceValue)
	nullMatch := fmt.Sprintf("(%s == true AND %s == null)", presenceValue, categoryValue)
	if !strings.Contains(query.Query, missingMatch) || !strings.Contains(query.Query, nullMatch) {
		t.Fatalf("stored-parent projection merged MISSING and explicit NULL categories: missing=%q null=%q\n%s", missingMatch, nullMatch, query.Query)
	}
	if strings.Contains(query.Query, "HAS(root.payload.valueQuantity.code)") {
		t.Fatalf("presence test inspected the leaf instead of its stored parent object:\n%s", query.Query)
	}
	if !strings.Contains(query.Query, " AGGREGATE ") || strings.Contains(query.Query, "INTO __loom_reshape_pivot_group_rows_") {
		t.Fatalf("stored-parent preview stopped using complete-population streaming aggregation:\n%s", query.Query)
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
	limit := strings.Index(query.Query, "LIMIT @limit")
	stage := strings.Index(query.Query, "LET __loom_construction_stage_1")
	if !query.PartialValidation || limit < 0 || stage < limit || !strings.Contains(query.Query[stage:], "COLLECT") {
		t.Fatalf("preview must bound parents but retain complete related contributions and grouping:\n%s", query.Query)
	}
	full := compileConstructionPivotPreview(t, output, 0)
	if strings.Contains(full.Query, "LIMIT @limit") || full.PartialValidation {
		t.Fatal("preview window leaked into full execution")
	}
	expand.Operation.RelatedExpand.EmptyPolicy = recipe.ExpansionExclude
	expand.Outputs[len(expand.Outputs)-1].Nullable = false
	output.Construction.Steps[0] = expand
	query = compileConstructionPivotPreview(t, output, 3)
	if strings.Contains(query.Query[:strings.Index(query.Query, "LET __loom_construction_stage_1")], "LIMIT @limit") {
		t.Fatal("dropping unmatched parents must not use a source-root window")
	}
}

func TestConstructionPreviewBoundsProvenRootIDPivotBeforeSourceMaterialization(t *testing.T) {
	query := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.id", false), 3)
	sourceStart := strings.Index(query.Query, "LET __loom_construction_source_projection = (")
	stageStart := strings.Index(query.Query, "LET __loom_construction_stage_1 = (")
	if sourceStart < 0 || stageStart <= sourceStart {
		t.Fatalf("construction query has no source projection followed by terminal stage:\n%s", query.Query)
	}
	sourceQuery := query.Query[sourceStart:stageStart]
	sortAt := strings.Index(sourceQuery, "SORT root.id ASC")
	limitAt := strings.Index(sourceQuery, "LIMIT @limit")
	returnAt := strings.Index(sourceQuery, "RETURN ")
	if sortAt < 0 || limitAt <= sortAt || returnAt <= limitAt {
		t.Fatalf("root-id sort/limit did not run before source projection materialization:\n%s", sourceQuery)
	}
	if got, exists := query.BindVars["limit"]; !exists || got != 3 {
		t.Fatalf("preview limit bind = %#v, exists=%t", got, exists)
	}
	if !query.PartialValidation {
		t.Fatal("preview with a source window was not marked as partial validation")
	}
	stageQuery := query.Query[stageStart:]
	if strings.Contains(stageQuery, "COLLECT") {
		t.Fatalf("proven root-ID pivot still materialized group rows:\n%s", query.Query)
	}
	if strings.Count(stageQuery, "FOR ") != 2 {

		t.Fatalf("root-ID pivot emitted per-category group-row scans:\n%s", query.Query)
	}
	if strings.Contains(stageQuery, "SORT __loom_construction_pivot_group") {
		t.Fatalf("root-ID pivot retained an intermediate sort before the final identity sort:\n%s", query.Query)
	}
	for _, validation := range []string{"TYPENAME(", "TABLE_PIVOT_VALUE_TYPE_MISMATCH", "TABLE_PIVOT_CELL_CARDINALITY", "TABLE_PIVOT_UNLISTED_CATEGORY"} {
		if !strings.Contains(stageQuery, validation) {
			t.Fatalf("root-ID pivot dropped %q validation:\n%s", validation, query.Query)
		}
	}

	full := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.id", false), 0)
	if strings.Contains(full.Query, "LIMIT @limit") || strings.Contains(full.Query, "SORT root.id ASC") {
		t.Fatalf("full construction execution received a preview-only source window:\n%s", full.Query)
	}
	if strings.Contains(full.Query, "COLLECT") || strings.Count(full.Query, "FOR ") != 3 {
		t.Fatalf("full direct-ID pivot did not retain the one-input-per-group renderer:\n%s", full.Query)
	}
	if !strings.Contains(full.Query, "SORT __loom_construction_final_row.__loom_row_id ASC") {
		t.Fatalf("full construction output lost deterministic final row-identity ordering:\n%s", full.Query)
	}
	if full.PartialValidation {
		t.Fatal("full construction execution was marked as partial validation")
	}
}

func TestConstructionPreviewRootIDProofUsesLoweredProjectionNotSemanticAlias(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	output.Fields[0].FieldRef = "source-column:specimen-id"
	query := compileConstructionPivotPreview(t, output, 3)
	if !strings.Contains(query.Query, "SORT root.id ASC") || !strings.Contains(query.Query, "LIMIT @limit") {
		t.Fatalf("direct lowered root id with opaque semantic identity did not qualify for source window:\n%s", query.Query)
	}
}

func TestConstructionPreviewRootIDSourceMetadataIsFHIROptionalScalar(t *testing.T) {
	output := constructionPreviewPivotOutput("root.id", false)
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
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
			if strings.Contains(query.Query, "SORT root.id ASC") {
				t.Fatalf("unproven plan received the early source order:\n%s", query.Query)
			}
			if strings.Count(query.Query, "LIMIT @limit") != 2 {
				t.Fatalf("terminal Pivot preview should limit complete groups and retain the final limit:\n%s", query.Query)
			}
			if !query.PartialValidation {
				t.Fatal("preview group window was not marked as partial validation")
			}
			if tc.name == "non-identity group key" && !strings.Contains(query.Query, "COLLECT") {
				t.Fatalf("nonunique group key bypassed canonical COLLECT:\n%s", query.Query)
			}
		})
	}
}

func TestConstructionPreviewTerminalPivotLimitsCompleteGroupsBeforeCellReduction(t *testing.T) {
	preview := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 3)
	if !preview.PartialValidation {
		t.Fatal("terminal Pivot preview window was not marked as partial validation")
	}
	stageStart := strings.Index(preview.Query, "LET __loom_construction_stage_1 = (")
	if stageStart < 0 {
		t.Fatalf("terminal Pivot stage missing:\n%s", preview.Query)
	}
	stage := preview.Query[stageStart:]
	collectAt := strings.Index(stage, "COLLECT ")
	cellValidationAt := strings.Index(stage, "TABLE_PIVOT_CELL_CARDINALITY")
	if collectAt < 0 || cellValidationAt <= collectAt {
		t.Fatalf("preview must collect selected groups completely before computing cells:\n%s", stage)
	}
	if got := preview.BindVars["limit"]; got != 3 {
		t.Fatalf("preview limit bind = %#v, want 3", got)
	}

	full := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 0)
	if full.PartialValidation || strings.Contains(full.Query, "LIMIT @limit") || strings.Contains(full.Query, "reshape_preview_identity") {
		t.Fatalf("full execution received the preview-only group window:\n%s", full.Query)
	}
}

func TestConstructionPreviewCoveringIndexMetadataAndRootHint(t *testing.T) {
	query := compileConstructionPivotPreview(t, constructionPreviewPivotOutput("root.gender", false), 3)
	index := query.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("eligible terminal Pivot preview has no covering-index spec:\n%s", query.Query)
	}
	if index.PrepareAfterPreview {
		t.Fatal("Pivot covering index must remain prepared before the preview query")
	}
	if index.Collection != "Patient" {
		t.Fatalf("index collection = %q, want Patient", index.Collection)
	}
	if !strings.HasPrefix(index.Name, model.PreviewCoveringIndexNamePrefix) {
		t.Fatalf("index name = %q, want prefix %q", index.Name, model.PreviewCoveringIndexNamePrefix)
	}
	wantFields := []string{"project", "dataset_generation", "_key", "payload.gender", "payload.multipleBirthInteger"}
	wantStoredValues := []string{"auth_resource_path"}
	if !reflect.DeepEqual(index.Fields, wantFields) {
		t.Fatalf("index fields = %#v, want %#v", index.Fields, wantFields)
	}
	if !reflect.DeepEqual(index.StoredValues, wantStoredValues) {
		t.Fatalf("index stored values = %#v, want %#v", index.StoredValues, wantStoredValues)
	}
	wantHint := "OPTIONS { indexHint: \"" + index.Name + "\", forceIndexHint: false }"
	if strings.Count(query.Query, wantHint) != 1 {
		t.Fatalf("the source scan must use the non-forcing index hint %q:\n%s", wantHint, query.Query)
	}
	if strings.Count(query.Query, "FOR root IN @@root_collection "+wantHint) != 1 {
		t.Fatalf("index hint was not placed on the root source scan:\n%s", query.Query)
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
			if strings.Contains(query.Query, "indexHint:") {
				t.Fatalf("ineligible plan received an index hint:\n%s", query.Query)
			}
		})
	}
}

func TestConstructionGroupPreviewUsesProjectionFieldsAndOneRootHint(t *testing.T) {
	query := compileConstructionOutputQuery(t, constructionGroupRowValuesOutput(), "group-covering-project", "group-covering-generation")
	index := query.PreviewCoveringIndex
	if index == nil {
		t.Fatalf("eligible terminal Group preview has no covering-index spec:\n%s", query.Query)
	}
	if index.Collection != "Observation" {
		t.Fatalf("index collection = %q, want Observation", index.Collection)
	}
	if !index.PrepareAfterPreview {
		t.Fatal("eligible Group covering index should be prepared after the preview query")
	}
	wantFields := []string{"project", "dataset_generation", "payload.note", "payload.status"}
	if !reflect.DeepEqual(index.Fields, wantFields) {
		t.Fatalf("index fields = %#v, want %#v", index.Fields, wantFields)
	}
	wantHint := "OPTIONS { indexHint: \"" + index.Name + "\", forceIndexHint: false }"
	if strings.Count(query.Query, wantHint) != 1 || !strings.Contains(query.Query, "FOR root IN @@root_collection "+wantHint) {
		t.Fatalf("Group source should receive one non-forcing root hint %q:\n%s", wantHint, query.Query)
	}
	if strings.Count(query.Query, "FOR root IN @@root_collection") != 1 {
		t.Fatalf("Group preview unexpectedly changed its source scan count:\n%s", query.Query)
	}
	sequential := query.PreviewGroupScan
	if sequential == nil || sequential.Collection != index.Collection {
		t.Fatalf("eligible direct Group preview has no scan alternatives: %+v", sequential)
	}
	if !strings.Contains(sequential.SequentialQuery, "OPTIONS { disableIndex: true }") || strings.Contains(sequential.SequentialQuery, "indexHint:") {
		t.Fatalf("sequential alternative did not disable only its direct root index:\n%s", sequential.SequentialQuery)
	}
	for _, scope := range []string{
		"root.project == @project", "root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths", "FILTER root_scope_allowed == @scope_allowed",
	} {
		if !strings.Contains(sequential.ScopeCountQuery, scope) {
			t.Fatalf("scope count omitted canonical filter %q:\n%s", scope, sequential.ScopeCountQuery)
		}
	}
	if !strings.Contains(sequential.ScopeCountQuery, "COLLECT WITH COUNT INTO") ||
		!strings.Contains(sequential.ScopeCountQuery, "RETURN {count:") ||
		strings.Contains(sequential.ScopeCountQuery, "payload.note") || strings.Contains(sequential.ScopeCountQuery, "construction_group") {
		t.Fatalf("scope count should return a named count without projecting or grouping source values:\n%s", sequential.ScopeCountQuery)
	}
	for _, bind := range []string{"project", "dataset_generation", "auth_resource_paths"} {
		if _, ok := sequential.ScopeCountBindVars[bind]; !ok {
			t.Fatalf("scope count omitted %q bind", bind)
		}
	}

	existingName := "loom_pivot_preview_existing_compatible"
	queryWithExistingIndex, existingBinds, err := RenderPreviewPhysicalPlanWithRootIndexHint(query.PhysicalPlan, existingName)
	if err != nil {
		t.Fatalf("render Group preview with existing compatible index: %v", err)
	}
	if !strings.Contains(queryWithExistingIndex, "OPTIONS { indexHint: \""+existingName+"\", forceIndexHint: false }") ||
		strings.Contains(queryWithExistingIndex, "forceIndexHint: true") {
		t.Fatalf("existing index hint is not non-forcing:\n%s", queryWithExistingIndex)
	}
	if !reflect.DeepEqual(existingBinds, query.BindVars) {
		t.Fatalf("existing-index render changed query binds: got=%v want=%v", existingBinds, query.BindVars)
	}

	repeated := compileConstructionOutputQuery(t, constructionGroupRowValuesOutput(), "group-covering-project", "group-covering-generation")
	if repeated.PreviewCoveringIndex == nil || repeated.PreviewCoveringIndex.Name != index.Name || !repeated.PreviewCoveringIndex.PrepareAfterPreview ||
		!reflect.DeepEqual(repeated.PreviewCoveringIndex.Fields, index.Fields) {
		t.Fatalf("Group covering-index metadata is not deterministic: first=%+v second=%+v", index, repeated.PreviewCoveringIndex)
	}

	fullOutput := lowerConstructionOutput(t, constructionGroupRowValuesOutput(), recipe.RuntimeBindings{
		Project: "group-covering-project", DatasetGeneration: "group-covering-generation",
	})
	full, err := CompileRecipeOutputWithPolicy(fullOutput, recipe.RuntimeBindings{
		Project: "group-covering-project", DatasetGeneration: "group-covering-generation",
	}, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if full.PreviewCoveringIndex != nil || strings.Contains(full.Query, "indexHint:") {
		t.Fatalf("full Group execution received preview index metadata or a hint: %+v\n%s", full.PreviewCoveringIndex, full.Query)
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
	query, err := CompileRecipeOutputWithPolicy(output, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
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

func compileConstructionPivotPreview(t *testing.T, output recipe.Output, limit int) model.CompiledQuery {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "construction-preview-project", DatasetGeneration: "construction-preview-generation"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, limit, ir.DefaultPhysicalOptimizationPolicy())
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
				Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &filterGender}},
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

func TestStreamingMissingCategoryPivotPreviewKeepsFullScopedPopulation(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "loom_dev_cda_fhir", DatasetGeneration: "cda-fhir-v1"}
	output := streamingMissingCategoryPivotTestOutput()
	idColumn := recipe.StageColumn{ID: "id_id", Name: "id", Type: "string", Nullable: true}
	output.Fields = append([]recipe.Field{{Name: "id", ColumnID: idColumn.ID, Expr: recipe.Expression{Select: "root.id"}}}, output.Fields...)
	output.Construction.SourceColumns = append([]recipe.StageColumn{idColumn}, output.Construction.SourceColumns...)
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{output},
	}
	semanticPlan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(semanticPlan, bindings.Project, bindings.DatasetGeneration)
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
	index := query.PreviewCoveringIndex
	wantFields := model.PreviewQuantityCategoryFields()
	wantStoredValues := model.PreviewQuantityCategoryStoredValues()
	if index == nil || index.Name != model.PreviewCoveringIndexNameWithStoredValues("Observation", wantFields, wantStoredValues) ||
		!reflect.DeepEqual(index.Fields, wantFields) || !reflect.DeepEqual(index.StoredValues, wantStoredValues) {
		t.Fatalf("streaming missing-category Pivot index = %+v, want fields=%v storedValues=%v", index, wantFields, wantStoredValues)
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
	if streamingTestBindKey(query.BindVars, "reshape_category_column", pivot.CategoryColumn) != "" ||
		streamingTestBindKey(query.BindVars, "reshape_category_presence_column", pivot.CategoryPresenceColumn) != "" {
		t.Fatalf("direct scalar Pivot retained field-name binds: %#v", query.BindVars)
	}
	categoryValue := streamingTestDirectScalarVariable(query.Query, "__loom_physical_construction_reshape_stream_source_parent.code")
	presenceValue := streamingTestDirectScalarVariable(query.Query, "IS_OBJECT(__loom_physical_construction_reshape_stream_source_parent)")
	if categoryValue == "" || presenceValue == "" {
		t.Fatalf("direct scalar category or presence projection missing: category=%q presence=%q\n%s", categoryValue, presenceValue, query.Query)
	}
	missingMatch := fmt.Sprintf("NOT (%s == true)", presenceValue)
	nullMatch := fmt.Sprintf("(%s == true AND %s == null)", presenceValue, categoryValue)
	if !strings.Contains(query.Query, missingMatch) || !strings.Contains(query.Query, nullMatch) {
		t.Fatalf("MISSING and explicit NULL categories are not distinguished by the presence sidecar: missing=%q null=%q\n%s", missingMatch, nullMatch, query.Query)
	}
	if strings.Contains(query.Query, "LET "+pivot.InputRowVariable+" = {") || strings.Contains(query.Query, pivot.InputRowVariable+"[@") {
		t.Fatalf("direct scalar Pivot retained its temporary input row object or field reads:\n%s", query.Query)
	}

	canonicalPivot := compiled.Outputs[0].Plan.StageSequence.Stages[0].GroupedPivot
	if canonicalPivot == nil || len(canonicalPivot.InputProjections) == 0 ||
		canonicalPivot.InputProjections[0].Value.Variable != compiled.Outputs[0].Plan.StageSequence.Stages[0].InputRowVariable {
		t.Fatal("streaming render mutated the canonical Pivot input projections")
	}

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
	identityPlan, err := recipeplan.BuildRecipePlan(identityBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	identityResolved, err := recipeplan.ResolveRecipePlan(identityPlan, bindings.Project, bindings.DatasetGeneration)
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
	assertionPlan, err := recipeplan.BuildRecipePlan(assertionBundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	assertionResolved, err := recipeplan.ResolveRecipePlan(assertionPlan, bindings.Project, bindings.DatasetGeneration)
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
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

func streamingTestDirectScalarVariable(query, expressionFragment string) string {
	for _, line := range strings.Split(query, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LET __loom_physical_construction_reshape_stream_input_") || !strings.Contains(line, expressionFragment) {
			continue
		}
		let := strings.TrimPrefix(line, "LET ")
		if variable, _, ok := strings.Cut(let, " = "); ok {
			return variable
		}
	}
	return ""
}
