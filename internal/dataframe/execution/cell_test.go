package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	explorerdb "github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/testkit/compilerfixtures"

	celltrace "github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/spec"

	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionPivotCellTraceUsesPreviewLiteralIdentity(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-pivot-identity",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{constructionPivotIdentityRecipeOutput()},
	}
	bindings := recipe.RuntimeBindings{Project: "trace-project", DatasetGeneration: "generation-a"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "trace-project", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Outputs[0]
	if output.RowIdentity == nil || len(output.RowIdentity.Fields) != 2 || output.RowIdentity.Fields[0] != "project" || output.RowIdentity.Fields[1] != "_key" {
		t.Fatalf("normal grouped-Pivot lowering identity = %#v", output.RowIdentity)
	}
	preview, err := aql.Render(output.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Query, `TO_STRING(["GROUPED_PIVOT"`) {
		t.Fatalf("Preview query does not emit the compiler-generated string identity:\n%s", preview.Query)
	}
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if trace.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || trace.IdentityPartsColumn != "" || trace.ExplicitIdentityObject ||
		!strings.Contains(trace.Query, ".__loom_row_id") {
		t.Fatalf("cell trace does not read Preview's explicit identity directly:\n%s\n%#v", trace.Query, trace)
	}

	previewID := `["GROUPED_PIVOT","pivot_identity",["STRING","final"]]`
	previewRow := map[string]any{"__loom_row_id": previewID}
	if err := ensureStableRowIdentity(previewRow, output.RowIdentity, preview.BindVars); err != nil {
		t.Fatalf("normalize Preview identity: %v", err)
	}
	if previewRow["__loom_row_id"] != previewID {
		t.Fatalf("Preview changed the literal identity: %#v", previewRow["__loom_row_id"])
	}
	traceID, err := cellTraceRowIdentity(trace, map[string]any{trace.ExplicitIdentityColumn: previewID})
	if err != nil || traceID != previewID {
		t.Fatalf("CellTrace identity = %q err=%v, want unchanged Preview identity %q", traceID, err, previewID)
	}
}

func constructionPivotIdentityRecipeOutput() recipe.Output {
	alpha := "alpha"
	return recipe.Output{
		Name: "pivot_identity", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "group", ColumnID: "group_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_id", Name: "group"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "pivot_identity", GroupKeyIDs: []string{"group_id"},
					CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []recipe.ConstructionPivotCategory{{
						Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id",
					}},
					DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group"}, {ID: "alpha_id", Name: "alpha"}},
			}},
		},
	}
}

func TestComposedCohortCellTraceIdentityMismatchAgainstPreviewObject(t *testing.T) {
	output := compileComposedCohortIdentityOutput(t)
	if output.RowIdentity == nil || len(output.RowIdentity.Fields) != 1 || output.RowIdentity.Fields[0] != "__loom_row_id" {
		t.Fatalf("composed cohort identity = %#v, want its compiler-owned final identity", output.RowIdentity)
	}
	if output.Plan.StageSequence == nil || output.Plan.StageSequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatalf("composed cohort final stage identity = %#v", output.Plan.StageSequence)
	}
	var finalIdentity *ir.PhysicalStageColumn
	for index := range output.Plan.StageSequence.FinalColumns {
		column := &output.Plan.StageSequence.FinalColumns[index]
		if column.Name == "__loom_row_id" {
			finalIdentity = column
			break
		}
	}
	if finalIdentity == nil || finalIdentity.Kind != "object" || finalIdentity.Cardinality != "required_one" || !finalIdentity.Internal || !finalIdentity.Identity {
		t.Fatalf("composed cohort Preview identity schema = %#v, want a required compiler-owned object", finalIdentity)
	}

	preview, err := aql.Render(output.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Query, "__loom_row_id: {group_revision_id: revision._key, group_id:") {
		t.Fatalf("ordinary Preview lowering does not emit its structured cohort identity:\n%s", preview.Query)
	}
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, "group_label", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if trace.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField ||
		!strings.Contains(trace.Query, ".__loom_row_id") {
		t.Fatalf("ordinary CellTrace lowering does not read the same structured identity:\n%s\n%#v", trace.Query, trace)
	}
	if !trace.ExplicitIdentityObject || trace.RowIdentity == nil || trace.RowIdentity.Grain != spec.RowGrainGroups ||
		len(trace.RowIdentity.Fields) != 1 || trace.RowIdentity.Fields[0] != "__loom_row_id" {
		t.Fatalf("CellTrace did not retain the compiler's composed-cohort identity proof: %#v", trace)
	}

	previewObject := map[string]any{
		"group_revision_id": "grouprev_cell_trace_identity",
		"group_id":          "cohort-a",
	}
	previewRow := map[string]any{"__loom_row_id": previewObject}
	if err := ensureStableRowIdentity(previewRow, output.RowIdentity, preview.BindVars); err != nil {
		t.Fatalf("normalize Preview identity: %v", err)
	}
	if got := previewRow["__loom_row_id"]; fmt.Sprint(got) != fmt.Sprint(previewObject) {
		t.Fatalf("Preview changed its already-published object identity: %#v", got)
	}
	previewIdentityBytes, err := json.Marshal(previewRow["__loom_row_id"])
	if err != nil {
		t.Fatal(err)
	}
	previewRowID := string(previewIdentityBytes)
	for _, rowID := range []string{
		previewRowID,
		`{"group_id":"cohort-a","group_revision_id":"grouprev_cell_trace_identity"}`,
	} {
		engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{
				trace.ExplicitIdentityColumn: previewRow["__loom_row_id"],
				trace.ValueColumn:            "Pair",
				trace.StatusColumn:           "VALUE",
				trace.ContributionsColumn:    []any{},
				trace.HasMoreColumn:          false,
				trace.OmissionColumn:         "",
			})
		}}
		result, traceErr := engine.cellTraceCompiled(context.Background(), trace, CellTraceRequest{
			Output: "cohort_cell_trace_identity", RowID: rowID, Column: "group_label", Limit: 10,
		})
		if traceErr != nil || result.Status != CellTraceValue || result.Value != "Pair" {
			t.Fatalf("CellTrace did not match Preview object %s: result=%#v err=%v", rowID, result, traceErr)
		}
	}
}

func TestComposedCohortCellTraceObjectIdentityRejectsMalformedAndExtraKeys(t *testing.T) {
	output := compileComposedCohortIdentityOutput(t)
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, "group_label", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		rowID   string
		wantErr string
		queried bool
	}{
		{name: "malformed", rowID: `{"group_revision_id":`, wantErr: "invalid object identity"},
		{name: "duplicate key", rowID: `{"group_id":"attacker","group_id":"cohort-a","group_revision_id":"grouprev_cell_trace_identity"}`, wantErr: "invalid object identity"},
		{name: "extra key", rowID: `{"group_revision_id":"grouprev_cell_trace_identity","group_id":"cohort-a","extra":"forged"}`, wantErr: "invalid object identity"},
		{name: "missing key", rowID: `{"group_revision_id":"grouprev_cell_trace_identity"}`, wantErr: "invalid object identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			queried := false
			engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				queried = true
				return visit(map[string]any{
					trace.ExplicitIdentityColumn: map[string]any{
						"group_revision_id": "grouprev_cell_trace_identity",
						"group_id":          "cohort-a",
					},
				})
			}}
			_, traceErr := engine.cellTraceCompiled(context.Background(), trace, CellTraceRequest{
				Output: "cohort_cell_trace_identity", RowID: test.rowID, Column: "group_label", Limit: 10,
			})
			if traceErr == nil || !strings.Contains(traceErr.Error(), test.wantErr) {
				t.Fatalf("CellTrace error = %v, want it to contain %q", traceErr, test.wantErr)
			}
			if queried != test.queried {
				t.Fatalf("query invoked = %t, want %t", queried, test.queried)
			}
		})
	}
}

func TestSourceOnlyGroupRowsCellTraceMatchesPreviewObjectAndReturnsMemberField(t *testing.T) {
	output := recipe.Output{
		Name: "grouped_specimens", RootResourceType: "Specimen", RowGrain: "groups",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{{Name: "resourceType", ColumnID: "resource_type", Expr: recipe.Expression{Select: "root.resourceType"}}},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_cell_trace", UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "resource_type", Policy: recipe.ConstructionRowValueOne}},
		},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "grouped cell trace", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: "trace-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "trace-project", DatasetGeneration: "generation-a"}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "trace-project", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(compiled.Outputs[0], "resourceType", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile ordinary group-row CellTrace: %v", err)
	}
	if !trace.ExplicitIdentityObject || trace.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField {
		t.Fatalf("group-row CellTrace identity = %#v, want Preview's typed object identity", trace)
	}
	rowIdentity := map[string]any{"group_revision_id": "grouprev_cell_trace", "group_id": "cohort-a"}
	engine := &Engine{queryRows: func(_ context.Context, query string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		if query != trace.Query {
			t.Fatalf("CellTrace executed a query other than its compiled terminal")
		}
		return visit(map[string]any{
			trace.ExplicitIdentityColumn: rowIdentity,
			trace.ValueColumn:            "Specimen",
			trace.StatusColumn:           "VALUE",
			trace.ContributionsColumn: []any{
				map[string]any{"resourceType": "Specimen", "resourceId": "specimen-a", "value": "Specimen"},
				map[string]any{"resourceType": "Specimen", "resourceId": "specimen-b", "value": "Specimen"},
			},
			trace.HasMoreColumn:  false,
			trace.OmissionColumn: "",
		})
	}}
	result, err := engine.cellTraceCompiled(context.Background(), trace, CellTraceRequest{
		Output: "grouped_specimens", RowID: `{"group_id":"cohort-a","group_revision_id":"grouprev_cell_trace"}`, Column: "resourceType", Limit: 10,
	})
	if err != nil || result.Status != CellTraceValue || result.Value != "Specimen" {
		t.Fatalf("CellTrace did not match Preview's reordered object identity and return the member field: result=%#v err=%v", result, err)
	}
	if result.OmissionCode != "" || len(result.Contributions) != 2 ||
		result.Contributions[0].ResourceID != "specimen-a" || result.Contributions[0].Value != "Specimen" ||
		result.Contributions[1].ResourceID != "specimen-b" || result.Contributions[1].Value != "Specimen" {
		t.Fatalf("CellTrace should return exact ONE member contributors: %#v", result)
	}
	for _, rowID := range []string{
		`{"group_revision_id":"grouprev_cell_trace"}`,
		`{"group_revision_id":"grouprev_cell_trace","group_id":"cohort-a","extra":"forged"}`,
		`["grouprev_cell_trace","cohort-a"]`,
	} {
		queried := false
		malformedEngine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			queried = true
			return visit(map[string]any{})
		}}
		_, traceErr := malformedEngine.cellTraceCompiled(context.Background(), trace, CellTraceRequest{
			Output: "grouped_specimens", RowID: rowID, Column: "resourceType", Limit: 10,
		})
		if traceErr == nil || !strings.Contains(traceErr.Error(), "invalid object identity") || queried {
			t.Fatalf("malformed group-row identity %s reached query execution or was accepted: queried=%t err=%v", rowID, queried, traceErr)
		}
	}
}

func TestComposedCohortPivotKeepsCompilerProvenScalarIdentity(t *testing.T) {
	output := compileComposedCohortPivotOutput(t)
	if output.Plan.StageSequence == nil || output.Plan.StageSequence.FinalRowIdentity != "__loom_row_id" {
		t.Fatalf("cohort Pivot final identity = %#v", output.Plan.StageSequence)
	}
	var finalIdentity *ir.PhysicalStageColumn
	for index := range output.Plan.StageSequence.FinalColumns {
		column := &output.Plan.StageSequence.FinalColumns[index]
		if column.Name == "__loom_row_id" {
			finalIdentity = column
			break
		}
	}
	if finalIdentity == nil || finalIdentity.Kind != "string" || finalIdentity.Cardinality != "required_one" || !finalIdentity.Internal || !finalIdentity.Identity {
		t.Fatalf("cohort Pivot Preview identity schema = %#v, want a required compiler-owned scalar", finalIdentity)
	}
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, "ordinal_pair", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if trace.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || trace.ExplicitIdentityObject ||
		trace.RowIdentity == nil || len(trace.RowIdentity.Fields) != 1 || trace.RowIdentity.Fields[0] != "__loom_row_id" {
		t.Fatalf("cohort Pivot CellTrace identity = %#v, want scalar explicit identity", trace)
	}
	rowID := `["GROUPED_PIVOT","cohort_pivot_identity",["STRING","pair"]]`
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			trace.ExplicitIdentityColumn: rowID,
			trace.ValueColumn:            int64(2),
			trace.StatusColumn:           "VALUE",
			trace.ContributionsColumn:    []any{},
			trace.HasMoreColumn:          false,
			trace.OmissionColumn:         "",
		})
	}}
	result, err := engine.cellTraceCompiled(context.Background(), trace, CellTraceRequest{
		Output: "cohort_cell_trace_identity", RowID: rowID, Column: "ordinal_pair", Limit: 10,
	})
	if err != nil || result.Status != CellTraceValue || result.Value != int64(2) {
		t.Fatalf("CellTrace scalar cohort-Pivot identity = %#v err=%v", result, err)
	}
}

func compileComposedCohortIdentityOutput(t *testing.T) lower.CompiledRecipeOutput {
	return compileComposedCohortOutput(t, false)
}

func compileComposedCohortPivotOutput(t *testing.T) lower.CompiledRecipeOutput {
	return compileComposedCohortOutput(t, true)
}

func compileComposedCohortOutput(t *testing.T, appendPivot bool) lower.CompiledRecipeOutput {
	t.Helper()
	selectedID, selectedGroup := "a", "pair"
	output := recipe.Output{
		Name: "cohort_cell_trace_identity", RootResourceType: "Patient", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "id", ColumnID: "id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1,
			SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
			Steps: []recipe.ConstructionStep{
				{
					ID: "keep_a", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "id", Operator: recipe.FilterEquals, Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &selectedID}},
					}},
					Outputs: []recipe.StageColumn{{ID: "id", Name: "id", Type: "string"}},
				},
				{
					ID: "keep_pair", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "group_id", Operator: recipe.FilterEquals, Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &selectedGroup}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group_id", Type: "string"},
						{ID: "group_label", Name: "group_label", Type: "string"},
						{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
						{ID: "members", Name: "members", Type: "array"},
						{ID: "id", Name: "id", Type: "array"},
					},
				},
			},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_cell_trace_identity", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "keep_a",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	if appendPivot {
		category := "pair"
		output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
			ID: "cohort_pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "keep_pair"}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
				ConstructionID: "cohort_pivot_identity", GroupKeyIDs: []string{"group_id"},
				CategoryColumnID: "group_label", ValueColumnID: "group_ordinal",
				Categories: []recipe.ConstructionPivotCategory{{
					Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &category}, OutputColumnID: "ordinal_pair_id",
				}},
				DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
				UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
			}},
			Outputs: []recipe.StageColumn{{ID: "group_id", Name: "group_id", Type: "string"}, {ID: "ordinal_pair_id", Name: "ordinal_pair", Type: "integer"}},
		})
	}
	plan, err := recipeplan.BuildRecipePlan(recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "cohort identity mismatch", TranslationVersion: "test",
		Outputs: []recipe.Output{output},
	}, recipe.RuntimeBindings{Project: "project-a", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled.Outputs[0]
}

func TestCellTraceCompiledFindsPublishedDefaultIdentityAndReturnsEvidence(t *testing.T) {
	parts := []any{"Patient/123", "generation-a"}
	encoded, _ := json.Marshal(parts)
	digest := sha256.Sum256(encoded)
	rowID := hex.EncodeToString(digest[:])
	query := celltrace.CompiledCellTraceQuery{
		Query: "trace", BindVars: map[string]any{"project": "p"}, ValueColumn: "value", ContributionsColumn: "contributors",
		StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id", "generation"}}, ContributionLimit: 1,
	}
	engine := &Engine{queryRows: func(_ context.Context, queryText string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
		if queryText != "trace" || bindVars["project"] != "p" {
			t.Fatalf("unexpected query invocation: %q %#v", queryText, bindVars)
		}
		for _, row := range []map[string]any{
			{"identity": []any{"other", "generation-a"}},
			{"identity": parts, "value": "female", "status": "AMBIGUOUS", "hasMore": true, "omission": "", "contributors": []any{map[string]any{"resourceType": "Patient", "resourceId": "123", "inputStageId": "source_projection", "inputColumnId": "amount_id", "inputColumn": "amount", "outputStageId": "derive_total", "outputColumnId": "total_id", "outputColumn": "total", "finalStageId": "keep_positive", "constructionId": "calc_total", "operation": "DERIVE", "value": "female"}}},
		} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: rowID, Column: "gender", Offset: 5, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Status != CellTraceAmbiguous || result.Value != "female" || !result.HasMore || result.NextOffset != 6 {
		t.Fatalf("unexpected trace result: %#v", result)
	}
	if len(result.Contributions) != 1 || result.Contributions[0].ResourceID != "123" {
		t.Fatalf("unexpected contributions: %#v", result.Contributions)
	}
	contribution := result.Contributions[0]
	if contribution.InputStageID != "source_projection" || contribution.InputColumnID != "amount_id" || contribution.InputColumn != "amount" || contribution.OutputStageID != "derive_total" || contribution.OutputColumnID != "total_id" || contribution.OutputColumn != "total" || contribution.FinalStageID != "keep_positive" || contribution.ConstructionID != "calc_total" || contribution.Operation != "DERIVE" {
		t.Fatalf("construction provenance was not retained: %#v", contribution)
	}
}

func TestCellTraceRelatedSourcePreservesDuplicateOccurrencesAndNullValues(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{
		Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors",
		StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity",
	}
	contributor := func(resourceID string, value any) map[string]any {
		return map[string]any{
			"resourceType": "Observation", "resourceId": resourceID,
			"outputStageId": "add_observation_status", "outputColumnId": "observation_status",
			"outputColumn": "observation_status", "finalStageId": "add_observation_status",
			"operation": "RELATED_SOURCE", "value": value,
		}
	}
	contributors := []any{contributor("observation-1", nil), contributor("observation-1", nil), contributor("observation-2", "final")}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{
			"identity": "patient-key", "value": []any{nil, nil, "final"}, "status": "VALUE",
			"contributors": contributors, "hasMore": false, "omission": "",
		})
	}}
	result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "patient-key", Column: "observation_status", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != CellTraceValue || len(result.Contributions) != 3 {
		t.Fatalf("related-source trace lost the populated list occurrences: %#v", result)
	}
	if result.Contributions[0].ResourceID != "observation-1" || result.Contributions[1].ResourceID != "observation-1" ||
		result.Contributions[0].Value != nil || result.Contributions[1].Value != nil || result.Contributions[2].Value != "final" {
		t.Fatalf("related-source trace changed duplicate or null contributor evidence: %#v", result.Contributions)
	}
	if result.Contributions[0].Operation != "RELATED_SOURCE" || result.Contributions[0].OutputColumnID != "observation_status" {
		t.Fatalf("related-source stage identity was not retained: %#v", result.Contributions[0])
	}
}

func TestCellTraceCompiledMatchesExplicitPublishedIdentity(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"identity": "patient-7", "value": nil, "status": "NO_MATCH", "contributors": []any{}, "hasMore": false})
	}}
	result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "patient-7", Column: "condition", Limit: 10})
	if err != nil || result.Status != CellTraceNoMatch || !result.Complete {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReportsWitnessBoundAsIncomplete(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for _, id := range []string{"one", "two"} {
			if err := visit(map[string]any{"identity": id}); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "later", Column: "gender", MaxWitnessRows: 1})
	if err != nil || result.Complete || result.Status != CellTraceIncomplete || result.OmissionCode == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReturnsTypedSemanticFailureStatuses(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	for _, test := range []struct {
		code   dataframeerrors.ErrorCode
		status CellTraceStatus
	}{
		{code: dataframeerrors.CodeTemporalTieAmbiguous, status: CellTraceAmbiguous},
		{code: dataframeerrors.CodeUnitDimensionIncompatible, status: CellTraceInvalidUnit},
		{code: dataframeerrors.CodeInvalidData, status: CellTraceInvalidType},
	} {
		t.Run(string(test.code), func(t *testing.T) {
			engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
				return dataframeerrors.NewError(test.code, "")
			}}
			result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-1", Column: "feature"})
			if err != nil || !result.Complete || result.Status != test.status || result.OmissionCode != string(test.code) || result.Contributions == nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestCellTraceCompiledRejectsMalformedEvidenceAndMissingRows(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	t.Run("malformed", func(t *testing.T) {
		engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"identity": "row", "status": "MAYBE", "contributors": []any{}})
		}}
		if _, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"}); err == nil {
			t.Fatal("expected malformed trace error")
		}
	})
	t.Run("missing", func(t *testing.T) {
		engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
		_, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"})
		if err == nil || errors.Is(err, errCellTraceFound) {
			t.Fatalf("missing row error = %v", err)
		}
	})
}

func TestConstructionDerivedCellTraceReturnsValueAndStageLineageAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_construction_trace_" + uuid.NewString()
	generation := "generation-construction-trace"
	wants := []struct {
		id     string
		amount int64
		total  float64
	}{{id: "trace-a", amount: 3, total: 5}, {id: "trace-b", amount: 5, total: 7}}
	documents := make([]json.RawMessage, 0, len(wants))
	for _, want := range wants {
		key := project + "_" + want.id
		document, marshalErr := json.Marshal(map[string]any{
			"_key": key, "id": want.id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation",
			"payload": map[string]any{"id": want.id, "resourceType": "Observation", "status": "active", "valueInteger": want.amount},
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction trace fixture: %v", err)
	}

	output := compilerfixtures.ConstructionTraceRecipeOutput("construction_trace")
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "construction-trace", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
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
	compiledOutput := compiled.Outputs[0]
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(compiledOutput, "total", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{batchSize: 100, queryRows: func(queryCtx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
		return client.QueryRows(queryCtx, query, batchSize, bindVars, visit)
	}}
	for _, want := range wants {

		identity := map[string]any{"project": project, "_key": project + "_" + want.id}
		if err := ensureStableRowIdentity(identity, compiledOutput.RowIdentity, nil); err != nil {
			t.Fatalf("build expected row identity for %q: %v", want.id, err)
		}
		rowID, _ := identity["__loom_row_id"].(string)
		result, traceErr := engine.cellTraceCompiled(ctx, trace, CellTraceRequest{
			Output: compiledOutput.Name, RowID: rowID, Column: "total", Limit: 25,
		})
		if traceErr != nil {
			t.Fatalf("trace %q: %v", want.id, traceErr)
		}
		if !result.Complete || result.Status != CellTraceValue || !compilerfixtures.ConstructionNumericEqual(result.Value, want.total) {
			t.Fatalf("trace %q = %#v, want complete value %v", want.id, result, want.total)
		}
		if len(result.Contributions) != 1 {
			t.Fatalf("trace %q contributors = %#v, want one source-column contribution", want.id, result.Contributions)
		}
		contribution := result.Contributions[0]
		if contribution.InputStageID != "source_projection" || contribution.InputColumnID != "amount_id" || contribution.InputColumn != "amount" || contribution.OutputStageID != "derive_total" || contribution.OutputColumnID != "total_id" || contribution.OutputColumn != "total" || contribution.FinalStageID != "keep_positive" || contribution.ConstructionID != "calc_total" || contribution.Operation != "DERIVE" || !compilerfixtures.ConstructionNumericEqual(contribution.Value, float64(want.amount)) {
			t.Fatalf("trace %q provenance = %#v", want.id, contribution)
		}
		if contribution.ResourceID != "" || contribution.ResourceType != "" {
			t.Fatalf("trace %q falsely claims resource provenance: %#v", want.id, contribution)
		}
	}
}

func assertCellTraceMissingRowUserFault(t *testing.T, err error, wantRowID, wantOutput string) {
	t.Helper()
	if err == nil {
		t.Fatal("missing published row must surface typed as a non-retryable user fault")
	}
	if !errors.Is(err, ErrCellTraceRowNotFound) {
		t.Fatalf("missing row must carry the classified sentinel: %v", err)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("missing row escaped as untyped (authoring REST presents raw leaves as INTERNAL_ERROR 500): %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("code=%s want %s", userErr.Code(), dataframeerrors.CodeInvalidRequest)
	}
	if userErr.Retryable() {
		t.Fatal("missing published row must be non-retryable")
	}
	if got := userErr.Details()["rowId"]; got != wantRowID {
		t.Fatalf("details=%v want echoing requested rowId", userErr.Details())
	}
	if got := userErr.Details()["output"]; got != wantOutput {
		t.Fatalf("details=%v want echoing requested output", userErr.Details())
	}
}

func TestEngineCellTraceCompiledMissingRowIsUserFault(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
	_, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-does-not-exist", Column: "gender"})
	assertCellTraceMissingRowUserFault(t, err, "row-does-not-exist", "patients")
}

func TestEngineCellTraceMissingPublishedRowIsUserFault(t *testing.T) {
	output := compileComposedCohortIdentityOutput(t)
	engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
	resolved := Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}}}
	rowID := `{"group_id":"cohort-absent","group_revision_id":"grouprev_absent"}`
	_, err := engine.CellTrace(context.Background(), resolved, CellTraceRequest{Output: output.Name, RowID: rowID, Column: "group_label", Limit: 10})
	assertCellTraceMissingRowUserFault(t, err, rowID, output.Name)
}

func TestEngineCellTraceKnownRowNeverTakesTheNotFoundArm(t *testing.T) {
	query := celltrace.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"identity": "row-7", "value": nil, "status": "NO_MATCH", "contributors": []any{}, "hasMore": false})
	}}
	result, err := engine.cellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-7", Column: "condition"})
	if err != nil || result.Status != CellTraceNoMatch {
		t.Fatalf("matched row must not take the missing-row arm: result=%#v err=%v", result, err)
	}
}
