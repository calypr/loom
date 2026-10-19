package compiler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestConstructionPivotRowLineageFailsWhenAnotherGroupViolatesGlobalPolicyAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	for _, test := range []struct {
		name          string
		duplicate     recipe.PivotDuplicatePolicy
		missing       recipe.PivotMissingCellPolicy
		otherRows     []map[string]any
		wantErrorCode string
	}{
		{
			name:      "unlisted category in unrelated group",
			duplicate: recipe.PivotDuplicateError, missing: recipe.PivotMissingCellNull,
			otherRows:     []map[string]any{pivotLineagePolicyFixtureRow("other-unlisted", "other", int64(1))},
			wantErrorCode: "TABLE_PIVOT_UNLISTED_CATEGORY",
		},
		{
			name:      "duplicate cell in unrelated group",
			duplicate: recipe.PivotDuplicateError, missing: recipe.PivotMissingCellNull,
			otherRows: []map[string]any{
				pivotLineagePolicyFixtureRow("other-duplicate-a", "other", int64(0)),
				pivotLineagePolicyFixtureRow("other-duplicate-b", "other", int64(0)),
			},
			wantErrorCode: "TABLE_PIVOT_CELL_CARDINALITY",
		},
		{
			name:      "missing cell in unrelated group",
			duplicate: recipe.PivotDuplicateError, missing: recipe.PivotMissingCellError,
			otherRows:     []map[string]any{pivotLineagePolicyFixtureRow("other-missing", "other", int64(0))},
			wantErrorCode: "TABLE_PIVOT_CELL_MISSING",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := "loom_pivot_lineage_policy_" + uuid.NewString()
			generation := "generation-pivot-lineage-policy"
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := client.ExecuteAQL(cleanupCtx,
					"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
					map[string]any{"project": project},
				); err != nil {
					t.Errorf("remove Pivot row-lineage policy fixture: %v", err)
				}
			})

			payloads := []map[string]any{
				pivotLineagePolicyFixtureRow("target-zero", "wanted", int64(0)),
				pivotLineagePolicyFixtureRow("target-null", "wanted", nil),
			}
			payloads = append(payloads, test.otherRows...)
			insertConstructionReshapeRows(t, ctx, client, project, generation, payloads)

			output := constructionPivotLineageOutput()
			pivot := output.Construction.Steps[0].Operation.Pivot
			pivot.DuplicatePolicy, pivot.MissingCellPolicy = test.duplicate, test.missing
			if err := executeConstructionOutputError(t, ctx, client, output, project, generation); err == nil || !strings.Contains(err.Error(), test.wantErrorCode) {
				t.Fatalf("full Pivot error = %v, want global error %s", err, test.wantErrorCode)
			}

			compiledOutput := lowerConstructionOutput(t, output, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation})
			rowID, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_pivot", []any{"STRING", "wanted"}})
			if err != nil {
				t.Fatal(err)
			}
			lineage, err := CompileRowLineageOutput(compiledOutput, string(rowID), 0, 5, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile Pivot row lineage: %v", err)
			}
			err = client.QueryRows(ctx, lineage.Query, 32, lineage.BindVars, func(map[string]any) error { return nil })
			if err == nil || !strings.Contains(err.Error(), test.wantErrorCode) {
				t.Fatalf("row lineage error = %v, want global Pivot policy error %s", err, test.wantErrorCode)
			}
		})
	}
}

func pivotLineagePolicyFixtureRow(id, group string, category any) map[string]any {
	return map[string]any{
		"id": id, "status": group, "valueInteger": category,
		"valueQuantity": map[string]any{"value": float64(2)},
	}
}

func TestConstructionPivotRowLineagePagesTypedGroupPreimageAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project := "loom_pivot_lineage_pages_" + uuid.NewString()
	generation := "generation-pivot-lineage-pages"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove Pivot row-lineage paging fixture: %v", err)
		}
	})

	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		pivotLineagePolicyFixtureRow("target-zero", "wanted", int64(0)),
		pivotLineagePolicyFixtureRow("target-null", "wanted", nil),
		pivotLineagePolicyFixtureRow("other-zero", "other", int64(0)),
		pivotLineagePolicyFixtureRow("other-null", "other", nil),
	})

	output := constructionPivotLineageOutput()
	compiledOutput := lowerConstructionOutput(t, output, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation})
	rows := executeConstructionOutput(t, ctx, client, output, project, generation)
	if len(rows) != 2 {
		t.Fatalf("full Pivot rows = %#v, want the exact wanted and other groups", rows)
	}
	var target map[string]any
	for _, row := range rows {
		if row["group_text"] == "wanted" {
			target = row
		}
	}
	if target == nil || !constructionNumericEqual(target["zero"], 2) || !constructionNumericEqual(target["null_value"], 2) {
		t.Fatalf("typed Pivot target row = %#v, want both the zero and NULL category values", target)
	}

	rowIDBytes, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_pivot", []any{"STRING", "wanted"}})
	if err != nil {
		t.Fatal(err)
	}
	rowID := string(rowIDBytes)
	compilePage := func(requestedRowID string, offset int) CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := CompileRowLineageOutput(compiledOutput, requestedRowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile Pivot row-lineage page at %d: %v", offset, compileErr)
		}
		for _, want := range []string{"root.project == @project", "root.dataset_generation == @dataset_generation", "LIMIT @row_lineage_offset, @row_lineage_fetch_limit"} {
			if !strings.Contains(query.Query, want) {
				t.Errorf("Pivot row-lineage query lost source scope or bounded paging contract %q:\n%s", want, query.Query)
			}
		}
		return query
	}
	assertPage := func(requestedRowID string, offset int, wantID string, wantMore bool) {
		t.Helper()
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(requestedRowID, offset))
		contributors, _ := result["contributors"].([]any)
		if result["found"] != true || result["hasMore"] != wantMore || len(contributors) != 1 {
			t.Fatalf("Pivot lineage page at offset %d = %#v, want one exact contributor and hasMore=%t", offset, result, wantMore)
		}
		contributor, _ := contributors[0].(map[string]any)
		if contributor["resourceType"] != "Observation" || contributor["resourceId"] != wantID || contributor["occurrenceKey"] != project+"_"+wantID {
			t.Fatalf("Pivot contributor at offset %d = %#v, want Observation/%s at %s", offset, contributor, wantID, project+"_"+wantID)
		}
	}
	assertPage(rowID, 0, "target-null", true)
	assertPage(rowID, 1, "target-zero", false)
	pastEnd := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, 2))
	if pastEnd["found"] != true || pastEnd["hasMore"] != false || len(pastEnd["contributors"].([]any)) != 0 {
		t.Fatalf("past-end Pivot lineage page = %#v, want found row and empty final page", pastEnd)
	}
	missingIDBytes, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_pivot", []any{"STRING", "absent"}})
	if err != nil {
		t.Fatal(err)
	}
	missing := executeRowLineageOracleQuery(t, ctx, client, compilePage(string(missingIDBytes), 0))
	if missing["found"] != false || missing["hasMore"] != false || len(missing["contributors"].([]any)) != 0 {
		t.Fatalf("nonexistent typed Pivot identity disclosed contributors: %#v", missing)
	}
}
