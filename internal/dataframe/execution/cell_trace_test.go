package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestConstructionPivotCellTraceUsesPreviewLiteralIdentity(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "construction-pivot-identity",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{constructionPivotIdentityRecipeOutput()},
	}
	bindings := recipe.RuntimeBindings{Project: "trace-project", DatasetGeneration: "generation-a"}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "trace-project", "generation-a")
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
	preview, err := aql.RenderPhysicalPlan(output.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Query, `TO_STRING(["GROUPED_PIVOT"`) {
		t.Fatalf("Preview query does not emit the compiler-generated string identity:\n%s", preview.Query)
	}
	trace, err := compiler.CompileCellTraceOutputWithPolicy(output, "alpha", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if trace.ExplicitIdentityColumn != ir.PhysicalCellTraceExplicitIdentityField || trace.IdentityPartsColumn != "" ||
		!strings.Contains(trace.Query, "__loom_construction_final_row.__loom_row_id") {
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

func TestCellTraceCompiledFindsPublishedDefaultIdentityAndReturnsEvidence(t *testing.T) {
	parts := []any{"Patient/123", "generation-a"}
	encoded, _ := json.Marshal(parts)
	digest := sha256.Sum256(encoded)
	rowID := hex.EncodeToString(digest[:])
	query := compiler.CompiledCellTraceQuery{
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
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: rowID, Column: "gender", Offset: 5, Limit: 1})
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
	query := compiler.CompiledCellTraceQuery{
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
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "patient-key", Column: "observation_status", Limit: 10})
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
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"identity": "patient-7", "value": nil, "status": "NO_MATCH", "contributors": []any{}, "hasMore": false})
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "patient-7", Column: "condition", Limit: 10})
	if err != nil || result.Status != CellTraceNoMatch || !result.Complete {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReportsWitnessBoundAsIncomplete(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for _, id := range []string{"one", "two"} {
			if err := visit(map[string]any{"identity": id}); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "later", Column: "gender", MaxWitnessRows: 1})
	if err != nil || result.Complete || result.Status != CellTraceIncomplete || result.OmissionCode == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReturnsTypedSemanticFailureStatuses(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
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
			result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-1", Column: "feature"})
			if err != nil || !result.Complete || result.Status != test.status || result.OmissionCode != string(test.code) || result.Contributions == nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestCellTraceCompiledRejectsMalformedEvidenceAndMissingRows(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	t.Run("malformed", func(t *testing.T) {
		engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"identity": "row", "status": "MAYBE", "contributors": []any{}})
		}}
		if _, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"}); err == nil {
			t.Fatal("expected malformed trace error")
		}
	})
	t.Run("missing", func(t *testing.T) {
		engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
		_, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"})
		if err == nil || errors.Is(err, errCellTraceFound) {
			t.Fatalf("missing row error = %v", err)
		}
	})
}
