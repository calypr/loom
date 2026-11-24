package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
)

func assertAuthScopedPivotRows(t *testing.T, rows []map[string]any) {
	t.Helper()
	want := map[string]map[string]float64{
		"/scope-a": {"alpha": 10, "zero": 2, "alpha_plus_zero": 12},
		"/scope-b": {"alpha": 20, "zero": 3, "alpha_plus_zero": 23},
	}
	wantIdentity := map[string]string{
		"/scope-a": `["GROUPED_PIVOT","s04-auth-scope-pivot",["STRING","final"],["INTEGER",1],["STRING","/scope-a"]]`,
		"/scope-b": `["GROUPED_PIVOT","s04-auth-scope-pivot",["STRING","final"],["INTEGER",1],["STRING","/scope-b"]]`,
	}
	if len(rows) != len(want) {
		t.Fatalf("auth-scoped pivot rows = %#v, want one row per scope (%d)", rows, len(want))
	}
	for _, row := range rows {
		scope, ok := row["auth_resource_path"].(string)
		if !ok {
			t.Errorf("auth_resource_path projection = %#v, want string scope", row["auth_resource_path"])
			continue
		}
		values, ok := want[scope]
		if !ok {
			t.Errorf("unexpected auth scope %q in row %#v", scope, row)
			continue
		}
		if identity, ok := row["__loom_row_id"].(string); !ok || identity != wantIdentity[scope] {
			t.Errorf("scope %s row identity = %#v, want %s", scope, row["__loom_row_id"], wantIdentity[scope])
		}
		for column, expected := range values {
			if got, ok := row[column].(float64); !ok || got != expected {
				t.Errorf("scope %s %s = %#v, want %v", scope, column, row[column], expected)
			}
		}
		delete(want, scope)
		delete(wantIdentity, scope)
	}
	if len(want) != 0 {
		t.Errorf("auth-scoped pivot omitted scopes: %#v", want)
	}
}

func assertReshapeOracleExclusions(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	fixtures := []struct {
		id      string
		code    any
		include bool
	}{
		{id: "missing-category", include: true},
		{id: "null-category", code: map[string]any{"text": nil}, include: true},
		{id: "missing-payload-id", code: map[string]any{"text": "unlisted"}, include: false},
		{id: "zero-category", code: map[string]any{"text": "alpha"}, include: true},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		payload := map[string]any{
			"resourceType": "Observation", "status": "final", "valueInteger": 1,
			"valueQuantity": map[string]any{"value": 11}, "valueBoolean": true,
		}
		if fixture.id == "zero-category" {
			payload["valueInteger"] = 0
		}
		if fixture.code != nil {
			payload["code"] = fixture.code
		}
		if fixture.include {
			payload["id"] = fixture.id
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_diagnostic_" + fixture.id, "id": fixture.id,
			"project": project, "project_id": project, "dataset_generation": generation,
			"resourceType": "Observation", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert exclusion sentinels: %v", err)
	}

	stringOutput := reshapeOracleOutput("string_exclusions", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "s04-pivot-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []oraclePivotCategory{
				{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
				{Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
				{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	stringRows := runReshapeOracleExclusionQuery(t, ctx, client, project, generation, stringOutput)
	stringByID := reshapeOracleExclusionsBySourceID(t, stringRows)
	wantStringSources := map[string]struct{}{"unlisted": {}, "empty-and-null": {}, "missing-category": {}, "null-category": {}}
	if len(stringRows) != 5 || len(stringByID) != len(wantStringSources) {
		t.Fatalf("string exclusion rows = %#v, want 5 rows including one identity omission and exact IDs %#v", stringRows, wantStringSources)
	}
	for sourceID := range wantStringSources {
		if _, ok := stringByID[sourceID]; !ok {
			t.Errorf("string exclusions omitted exact source Observation/%s: %#v", sourceID, stringRows)
		}
	}
	assertExclusionLiteral(t, stringByID["unlisted"], "unlisted", true, "STRING", `["GROUPED_PIVOT","s04-pivot-exclusion",["STRING","final"],["INTEGER",1]]`)
	assertExclusionLiteral(t, stringByID["empty-and-null"], "", true, "STRING", `["GROUPED_PIVOT","s04-pivot-exclusion",["STRING","preliminary"],["INTEGER",2]]`)
	assertExclusionLiteral(t, stringByID["missing-category"], nil, false, "STRING", `["GROUPED_PIVOT","s04-pivot-exclusion",["STRING","final"],["INTEGER",1]]`)
	assertExclusionLiteral(t, stringByID["null-category"], nil, true, "STRING", `["GROUPED_PIVOT","s04-pivot-exclusion",["STRING","final"],["INTEGER",1]]`)
	missingPayloadID := findReshapeOracleExclusionByCategory(t, stringRows, "unlisted", true)
	assertExclusionLiteral(t, missingPayloadID, "unlisted", true, "STRING", `["GROUPED_PIVOT","s04-pivot-exclusion",["STRING","final"],["INTEGER",1]]`)
	if missingPayloadID[ir.PhysicalTableShapeExclusionResourceIDField] != nil || missingPayloadID[ir.PhysicalTableShapeExclusionIdentityStatusField] != ir.PhysicalTableShapeExclusionIdentityUnavailable || missingPayloadID[ir.PhysicalTableShapeExclusionOmissionField] != ir.PhysicalTableShapeExclusionSourceIdentityUnavailable {
		t.Fatalf("source without payload resource ID was fabricated instead of omitted: %#v", missingPayloadID)
	}

	missingNullOutput := reshapeOracleOutput("direct_missing_null", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "s04-pivot-direct-missing-null", GroupKeys: []string{"source_id"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []oraclePivotCategory{
				{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, Output: "missing_category", Label: "Missing"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, Output: "null_category", Label: "Null"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	missingNullOutput.Filters = []recipe.Filter{{
		Select: "root.id", Operator: recipe.FilterIn,
		Values: []recipe.FilterValue{reshapeOracleStringFilter("missing-category"), reshapeOracleStringFilter("null-category")},
	}}
	missingNullCompiled, missingNullQuery, err := compileReshapeOracle(missingNullOutput, project, generation, 100)
	if err != nil {
		t.Fatalf("compile direct-ID missing/null category oracle: %v", err)
	}
	missingNullFastPath := false
	for _, stage := range missingNullCompiled.Plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStagePivotOp && stage.GroupedPivot != nil {
			missingNullFastPath = stage.GroupedPivot.OneInputRowPerGroup
		}
	}
	if !missingNullFastPath || strings.Contains(missingNullQuery.Query, "COLLECT") {
		t.Fatalf("missing/null oracle did not use direct-ID renderer:\n%s", missingNullQuery.Query)
	}
	missingNullRows := executeReshapeOracleQuery(t, ctx, client, missingNullQuery)
	if len(missingNullRows) != 2 {
		t.Fatalf("direct-ID missing/null pivot returned %d rows, want two: %#v", len(missingNullRows), missingNullRows)
	}
	missingNullByID := make(map[string]map[string]any, len(missingNullRows))
	for _, row := range missingNullRows {
		id, ok := row["source_id"].(string)
		if !ok || id == "" {
			t.Errorf("direct-ID missing/null group key = %#v, want string resource ID", row["source_id"])
			continue
		}
		missingNullByID[id] = row
	}
	for _, test := range []struct {
		id            string
		missing, null any
		rowID         string
	}{
		{
			id: "missing-category", missing: float64(11), null: nil,
			rowID: `["GROUPED_PIVOT","s04-pivot-direct-missing-null",["STRING","missing-category"]]`,
		},
		{
			id: "null-category", missing: nil, null: float64(11),
			rowID: `["GROUPED_PIVOT","s04-pivot-direct-missing-null",["STRING","null-category"]]`,
		},
	} {
		row := missingNullByID[test.id]
		if row == nil {
			t.Errorf("direct-ID missing/null pivot omitted Observation/%s: %#v", test.id, missingNullRows)
			continue
		}
		for column, expected := range map[string]any{
			"missing_category": test.missing, "null_category": test.null,
			"__loom_reshape_unlisted_count": float64(0), "__loom_row_id": test.rowID,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("Observation/%s.%s = %#v, want %#v", test.id, column, got, expected)
			}
		}
	}

	trueValue := true
	booleanOutput := reshapeOracleOutput("false_exclusions", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "s04-pivot-false-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "flag_value", ValueColumn: "numeric_value",
			Categories:      []oraclePivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarBoolean, Boolean: &trueValue}, Output: "true", Label: "True"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	booleanRows := runReshapeOracleExclusionQuery(t, ctx, client, project, generation, booleanOutput)
	falseSources := []string{"alpha-a", "beta", "zero", "preliminary-one", "empty-and-null"}
	for _, sourceID := range falseSources {
		row, ok := reshapeOracleExclusionsBySourceID(t, booleanRows)[sourceID]
		if !ok || row[ir.PhysicalTableShapeExclusionCategoryPresentField] != true || row[ir.PhysicalTableShapeExclusionCategoryValueField] != false || row[ir.PhysicalTableShapeExclusionCategoryTypeField] != "BOOLEAN" {
			t.Errorf("false category for Observation/%s was not retained as typed present false: %#v", sourceID, row)
		}
	}
	if len(booleanRows) != len(falseSources) {
		t.Fatalf("boolean false exclusions = %#v, want exactly %d false source rows", booleanRows, len(falseSources))
	}

	zero, one, two := int64(0), int64(1), int64(2)
	numericOutput := reshapeOracleOutput("zero_exclusions", &oracleReshape{
		Kind: oracleReshapePivot,
		GroupedPivot: &oraclePivot{
			ConstructionID: "s04-pivot-zero-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "numeric_category", ValueColumn: "numeric_value",
			Categories: []oraclePivotCategory{
				{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &one}, Output: "one", Label: "One"},
				{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &two}, Output: "two", Label: "Two"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	zeroRows := runReshapeOracleExclusionQuery(t, ctx, client, project, generation, numericOutput)
	zeroByID := reshapeOracleExclusionsBySourceID(t, zeroRows)
	assertExclusionLiteral(t, zeroByID["zero-category"], float64(zero), true, "INTEGER", `["GROUPED_PIVOT","s04-pivot-zero-exclusion",["STRING","final"],["INTEGER",0]]`)
	if len(zeroRows) != 1 {
		t.Fatalf("numeric-zero exclusions = %#v, want only Observation/zero-category", zeroRows)
	}
	t.Logf("literal exclusions: string=%v; boolean false=%v; integer zero=%v; MISSING present=false vs NULL present=true", summarizeReshapeOracleExclusions(stringRows), summarizeReshapeOracleExclusions(booleanRows), summarizeReshapeOracleExclusions(zeroRows))
}

func runReshapeOracleExclusionQuery(t *testing.T, ctx context.Context, client *store.Client, project, generation string, output recipe.Output) []map[string]any {
	t.Helper()
	compiled, _, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile exclusion oracle output: %v", err)
	}
	query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile exact table-shape exclusions: %v", err)
	}
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute exact table-shape exclusions: %v\n%s", err, query.Query)
	}
	return rows
}

func reshapeOracleExclusionsBySourceID(t *testing.T, rows []map[string]any) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		resourceType, _ := row[ir.PhysicalTableShapeExclusionResourceTypeField].(string)
		resourceID, _ := row[ir.PhysicalTableShapeExclusionResourceIDField].(string)
		if resourceType == "" || resourceID == "" {
			continue
		}
		if resourceType != "Observation" {
			t.Fatalf("unexpected source resourceType: %#v", row)
		}
		result[resourceID] = row
	}
	return result
}

func summarizeReshapeOracleExclusions(rows []map[string]any) []string {
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		identity := fmt.Sprintf("%s/%s", row[ir.PhysicalTableShapeExclusionResourceTypeField], row[ir.PhysicalTableShapeExclusionResourceIDField])
		if row[ir.PhysicalTableShapeExclusionIdentityStatusField] != ir.PhysicalTableShapeExclusionIdentityExact {
			identity = "<identity-unavailable>"
		}
		result = append(result, fmt.Sprintf("%s category(type=%v,present=%v,value=%#v) row=%s reason=%s omission=%v",
			identity, row[ir.PhysicalTableShapeExclusionCategoryTypeField], row[ir.PhysicalTableShapeExclusionCategoryPresentField],
			row[ir.PhysicalTableShapeExclusionCategoryValueField], row[ir.PhysicalTableShapeExclusionOutputRowIDField],
			row[ir.PhysicalTableShapeExclusionReasonField], row[ir.PhysicalTableShapeExclusionOmissionField]))
	}
	sort.Strings(result)
	return result
}

func findReshapeOracleExclusionByCategory(t *testing.T, rows []map[string]any, category any, present bool) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row[ir.PhysicalTableShapeExclusionIdentityStatusField] == ir.PhysicalTableShapeExclusionIdentityUnavailable &&
			row[ir.PhysicalTableShapeExclusionCategoryPresentField] == present &&
			reflect.DeepEqual(row[ir.PhysicalTableShapeExclusionCategoryValueField], category) {
			return row
		}
	}
	t.Fatalf("missing identity-omitted category %v (present=%t) in %#v", category, present, rows)
	return nil
}

func assertExclusionLiteral(t *testing.T, row map[string]any, category any, present bool, categoryType, rowIdentity string) {
	t.Helper()
	if row == nil {
		t.Fatal("source exclusion is missing")
	}
	if !reflect.DeepEqual(row[ir.PhysicalTableShapeExclusionCategoryValueField], category) ||
		row[ir.PhysicalTableShapeExclusionCategoryPresentField] != present ||
		row[ir.PhysicalTableShapeExclusionCategoryTypeField] != categoryType ||
		row[ir.PhysicalTableShapeExclusionOutputRowIDField] != rowIdentity ||
		row[ir.PhysicalTableShapeExclusionReasonField] != ir.PhysicalTableShapeExclusionReasonUnlistedCategory {
		t.Errorf("exclusion literal category=%#v present=%#v type=%#v row=%#v reason=%#v, want category=%#v present=%t type=%q row=%q reason=%s",
			row[ir.PhysicalTableShapeExclusionCategoryValueField], row[ir.PhysicalTableShapeExclusionCategoryPresentField],
			row[ir.PhysicalTableShapeExclusionCategoryTypeField], row[ir.PhysicalTableShapeExclusionOutputRowIDField],
			row[ir.PhysicalTableShapeExclusionReasonField], category, present, categoryType, rowIdentity,
			ir.PhysicalTableShapeExclusionReasonUnlistedCategory)
	}
}
