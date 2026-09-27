package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
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
	returnAt := strings.Index(sourceQuery, "RETURN {")
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
		// One FOR iterates source rows inside the stage and one emits final rows.
		// Category matching must not add a FOR over group rows per output cell.
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
		t.Fatalf("terminal Pivot stage is missing:\n%s", preview.Query)
	}
	stage := preview.Query[stageStart:]
	collectAt := strings.Index(stage, "COLLECT ")
	identityAt := strings.Index(stage, "LET __loom_physical_construction_reshape_preview_identity = TO_STRING([\"GROUPED_PIVOT\"")
	sortAt := strings.Index(stage, "SORT __loom_physical_construction_reshape_preview_identity ASC")
	limitAt := strings.Index(stage, "LIMIT @limit")
	cellValidationAt := strings.Index(stage, "TABLE_PIVOT_CELL_CARDINALITY")
	if collectAt < 0 || identityAt <= collectAt || sortAt <= identityAt || limitAt <= sortAt || cellValidationAt <= limitAt {
		t.Fatalf("preview must collect complete groups, select them by final row identity, then compute cells:\n%s", stage)
	}
	if strings.Count(stage, "LIMIT @limit") != 2 {
		t.Fatalf("expected group and final output limits:\n%s", stage)
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
	if strings.Count(query.Query, wantHint) != 1 {
		t.Fatalf("query root scan does not have exactly one non-forcing index hint %q:\n%s", wantHint, query.Query)
	}
	if !strings.Contains(query.Query, "FOR root IN @@root_collection "+wantHint) {
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
