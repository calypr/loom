package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type reshapeOracleGroupTuple struct {
	Text    recipe.TableScalar
	Numeric recipe.TableScalar
}

type reshapeOracleSourceRow struct {
	SourceID        string
	Group           reshapeOracleGroupTuple
	StringCategory  recipe.TableScalar
	NumericCategory recipe.TableScalar
	Number          *float64
	Flag            bool
	Text            string
	Unrelated       string
}

type reshapeOraclePivotWant struct {
	GroupText       string
	GroupNumber     int64
	Alpha           *float64
	Beta            *float64
	Zero            *float64
	EmptyCategory   *float64
	UnlistedCount   int64
	DerivedPlusZero *float64
	RowID           string
}

type reshapeOracleUnpivotWant struct {
	SourceID        string
	GroupText       string
	StringCategory  string
	NumericCategory int64
	Flag            bool
	Text            string
	Unrelated       string
	Key             string
	Value           any
	RowID           string
}

var reshapeOracleSourceRows = []reshapeOracleSourceRow{
	{
		SourceID: "alpha-a", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("alpha"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(2.5),
		Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a",
	},
	{
		SourceID: "alpha-b", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("alpha"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(4.5),
		Flag: true, Text: "alpha-b", Unrelated: "keep-alpha-b",
	},
	{
		SourceID: "beta", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("beta"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(3.5),
		Flag: false, Text: "beta", Unrelated: "keep-beta",
	},
	{
		SourceID: "zero", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("zero"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(0),
		Flag: false, Text: "zero", Unrelated: "keep-zero",
	},
	{
		SourceID: "unlisted", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("unlisted"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(9),
		Flag: true, Text: "unlisted", Unrelated: "keep-unlisted",
	},
	{
		SourceID: "final-two", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("final"), Numeric: reshapeOracleInteger(2)},
		StringCategory: reshapeOracleString("alpha"), NumericCategory: reshapeOracleInteger(2), Number: reshapeOracleFloat(12),
		Flag: true, Text: "final-two", Unrelated: "keep-final-two",
	},
	{
		SourceID: "preliminary-one", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("preliminary"), Numeric: reshapeOracleInteger(1)},
		StringCategory: reshapeOracleString("alpha"), NumericCategory: reshapeOracleInteger(1), Number: reshapeOracleFloat(8),
		Flag: false, Text: "preliminary-one", Unrelated: "keep-preliminary-one",
	},
	{
		SourceID: "empty-and-null", Group: reshapeOracleGroupTuple{Text: reshapeOracleString("preliminary"), Numeric: reshapeOracleInteger(2)},
		StringCategory: reshapeOracleString(""), NumericCategory: reshapeOracleInteger(2), Number: nil,
		Flag: false, Text: "", Unrelated: "keep-empty-and-null",
	},
}

var reshapeOraclePivotSumWant = []reshapeOraclePivotWant{
	{
		GroupText: "final", GroupNumber: 1, Alpha: reshapeOracleFloat(7), Beta: reshapeOracleFloat(3.5),
		Zero: reshapeOracleFloat(0), UnlistedCount: 1, DerivedPlusZero: reshapeOracleFloat(7),
		RowID: `["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",1]]`,
	},
	{
		GroupText: "final", GroupNumber: 2, Alpha: reshapeOracleFloat(12), UnlistedCount: 0,
		RowID: `["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",2]]`,
	},
	{
		GroupText: "preliminary", GroupNumber: 1, Alpha: reshapeOracleFloat(8), UnlistedCount: 0,
		RowID: `["GROUPED_PIVOT","s04-pivot-sum",["STRING","preliminary"],["INTEGER",1]]`,
	},
	{
		GroupText: "preliminary", GroupNumber: 2, UnlistedCount: 0,
		RowID: `["GROUPED_PIVOT","s04-pivot-sum",["STRING","preliminary"],["INTEGER",2]]`,
	},
}

func reshapeOracleFloat(value float64) *float64 { return &value }

func reshapeOracleValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func reshapeOracleString(value string) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarString, String: &value}
}

func reshapeOracleInteger(value int64) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &value}
}

func TestS04TableReshapeOracleAgainstArango(t *testing.T) {
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

	project := "loom_s04_table_reshape_" + uuid.NewString()
	t.Logf("fixture project: %s", project)
	const generation = "generation-s04-table-reshape-oracle"
	documents := make([]json.RawMessage, 0, len(reshapeOracleSourceRows))
	for _, row := range reshapeOracleSourceRows {
		if !reflect.DeepEqual(row.Group.Numeric, row.NumericCategory) {
			t.Fatalf("fixture %q numeric category %v differs from group tuple component %v", row.SourceID, row.NumericCategory, row.Group.Numeric)
		}
		payload := map[string]any{
			"id": row.SourceID, "resourceType": "Observation", "status": *row.Group.Text.String,
			"valueInteger": *row.NumericCategory.Integer, "valueString": row.Text,
			"code":         map[string]any{"text": *row.StringCategory.String},
			"valueBoolean": row.Flag, "issued": row.Unrelated,
		}
		if row.Number != nil {
			payload["valueQuantity"] = map[string]any{"value": *row.Number}
		} else {
			payload["valueQuantity"] = map[string]any{"value": nil}
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + row.SourceID, "id": row.SourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert reshape fixture: %v", err)
	}

	directIDOutput := reshapeOracleOutput("direct_id", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-direct-id", GroupKeys: []string{"source_id"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
				{Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
				{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
				{Key: reshapeOracleString(""), Output: "empty_category", Label: "Empty category"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	directIDCompiled, directIDQuery, err := compileReshapeOracle(directIDOutput, project, generation, 100)
	if err != nil {
		t.Fatalf("compile direct-ID grouped pivot oracle: %v", err)
	}
	directIDFastPath := false
	for _, operation := range directIDCompiled.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp && operation.GroupedPivot != nil {
			directIDFastPath = operation.GroupedPivot.OneInputRowPerGroup
		}
	}
	if !directIDFastPath {
		t.Fatal("direct root-ID pivot did not lower with its one-input-per-group proof")
	}
	if strings.Contains(directIDQuery.Query, "COLLECT") || strings.Contains(directIDQuery.Query, "FOR __loom_physical_reshape_cell_") {
		t.Fatalf("direct root-ID pivot retained group materialization or per-category scans:\n%s", directIDQuery.Query)
	}
	directIDRows := executeReshapeOracleQuery(t, ctx, client, directIDQuery)
	if len(directIDRows) != len(reshapeOracleSourceRows) {
		t.Fatalf("direct-ID pivot rows = %d, want one for each of %d source records: %#v", len(directIDRows), len(reshapeOracleSourceRows), directIDRows)
	}
	directByID := make(map[string]map[string]any, len(directIDRows))
	for _, row := range directIDRows {
		id, ok := row["source_id"].(string)
		if !ok || id == "" {
			t.Errorf("direct-ID pivot group key = %#v, want string resource ID", row["source_id"])
			continue
		}
		directByID[id] = row
	}
	for _, source := range reshapeOracleSourceRows {
		row, ok := directByID[source.SourceID]
		if !ok {
			t.Errorf("direct-ID pivot omitted Observation/%s: %#v", source.SourceID, directIDRows)
			continue
		}
		for outputColumn, category := range map[string]string{
			"alpha": "alpha", "beta": "beta", "zero": "zero", "empty_category": "",
		} {
			var expected any
			if *source.StringCategory.String == category {
				expected = reshapeOracleValue(source.Number)
			}
			if got := row[outputColumn]; !reflect.DeepEqual(got, expected) {
				t.Errorf("Observation/%s.%s = %#v, want %#v", source.SourceID, outputColumn, got, expected)
			}
		}
		wantUnlisted := float64(0)
		if *source.StringCategory.String == "unlisted" {
			wantUnlisted = 1
		}
		if got := row["__loom_reshape_unlisted_count"]; got != wantUnlisted {
			t.Errorf("Observation/%s unlisted evidence = %#v, want %v", source.SourceID, got, wantUnlisted)
		}
		wantID := fmt.Sprintf(`["GROUPED_PIVOT","s04-pivot-direct-id",["STRING",%q]]`, source.SourceID)
		if got := row["__loom_row_id"]; got != wantID {
			t.Errorf("Observation/%s row identity = %#v, want %s", source.SourceID, got, wantID)
		}
	}

	output := reshapeOracleOutput("sum", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-sum", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
				{Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
				{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
				{Key: reshapeOracleString(""), Output: "empty_category", Label: "Empty category"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "s04-derived-after-pivot", Name: "alpha_plus_zero", Label: "Alpha plus zero", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	compiled, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile grouped pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	t.Logf("pivot SUM literal rows: %#v", rows)
	if len(rows) != len(reshapeOraclePivotSumWant) {
		t.Fatalf("pivot rows = %#v, want %d grouped rows", rows, len(reshapeOraclePivotSumWant))
	}
	if outputColumn(compiled.OutputSchema, "alpha") != "decimal" {
		t.Fatalf("alpha pivot output kind = %v, want decimal", outputColumn(compiled.OutputSchema, "alpha"))
	}
	byGroup := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		key := fmt.Sprintf("%s/%v", row["group_text"], row["group_number"])
		byGroup[key] = row
	}
	for _, want := range reshapeOraclePivotSumWant {
		key := fmt.Sprintf("%s/%d", want.GroupText, want.GroupNumber)
		row, ok := byGroup[key]
		if !ok {
			t.Fatalf("pivot omitted group %q: %#v", key, rows)
		}
		for column, expected := range map[string]any{
			"alpha": reshapeOracleValue(want.Alpha), "beta": reshapeOracleValue(want.Beta),
			"zero": reshapeOracleValue(want.Zero), "empty_category": reshapeOracleValue(want.EmptyCategory),
			"__loom_reshape_unlisted_count": float64(want.UnlistedCount),
			"alpha_plus_zero":               reshapeOracleValue(want.DerivedPlusZero), "__loom_row_id": want.RowID,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("%s.%s = %#v, want literal %#v", key, column, got, expected)
			}
		}
	}
	assertReshapeOraclePivotCellTrace(t, ctx, client, compiled)

	assertReshapeOracleRepeatedIdentities(t, ctx, client, query, rows)
	assertReshapeOraclePivotWindow(t, ctx, client, output, project, generation)
	assertReshapeOraclePivotReducers(t, ctx, client, project, generation)
	assertReshapeOracleNumericCategories(t, ctx, client, project, generation)
	assertReshapeOracleBooleanValues(t, ctx, client, project, generation)
	assertReshapeOracleEmptyStringValue(t, ctx, client, project, generation)
	assertReshapeOraclePivotErrors(t, ctx, client, project, generation)
	assertReshapeOraclePivotErrorTrace(t, ctx, client, project, generation)
	assertReshapeOracleUnpivot(t, ctx, client, project, generation)
	assertReshapeOracleUnpivotRejectsIncompatibleInputs(t, project, generation)
	assertReshapeOracleExclusions(t, ctx, client, project, generation)
}

func TestS04GroupedPivotMaterializationPreservesAuthResourcePathAgainstArango(t *testing.T) {
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

	project := "loom_s04_auth_path_pivot_" + uuid.NewString()
	const generation = "generation-s04-auth-path-pivot"
	fixtures := []struct {
		id, scope, category string
		value               float64
	}{
		{id: "a-alpha", scope: "/scope-a", category: "alpha", value: 10},
		{id: "a-zero", scope: "/scope-a", category: "zero", value: 2},
		{id: "b-alpha", scope: "/scope-b", category: "alpha", value: 20},
		{id: "b-zero", scope: "/scope-b", category: "zero", value: 3},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		payload := map[string]any{
			"resourceType": "Observation", "id": fixture.id, "status": "final", "valueInteger": 1,
			"code": map[string]any{"text": fixture.category}, "valueQuantity": map[string]any{"value": fixture.value},
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + fixture.id, "id": fixture.id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": fixture.scope, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert auth-scope pivot fixture: %v", err)
	}

	output := reshapeOracleOutput("auth_scope_pivot", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-auth-scope-pivot", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
				{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.DerivedColumns = []recipe.DerivedColumn{{
		ConstructionID: "s04-auth-scope-derived", Name: "alpha_plus_zero", Label: "Alpha plus zero", Operation: recipe.DerivedAdd,
		Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "alpha"},
		Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "zero"},
		MissingInputPolicy: recipe.MissingInputPropagateNull,
	}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-auth-scope-pivot", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation, IncludeAuthResourcePath: true}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	compiledOutput := compiled.Outputs[0]
	query, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile auth-scoped grouped pivot: %v", err)
	}
	for _, column := range query.PublicColumns {
		if column == "auth_resource_path" || column == "__loom_auth_resource_path" {
			t.Fatalf("internal authorization projection leaked into public columns: %#v", query.PublicColumns)
		}
	}
	assertAuthScopedPivotRows(t, executeReshapeOracleQuery(t, ctx, client, query))

	page, err := CompileRecipeOutputPageWithPolicy(compiledOutput, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile auth-scoped grouped pivot materialization page: %v", err)
	}
	rootKeys := make([]string, 0, len(fixtures))
	if err := client.QueryRows(ctx, page.RootKeysQuery, 500, page.RootKeysBindVars, func(row map[string]any) error {
		key, ok := row["_key"].(string)
		if !ok || key == "" {
			return fmt.Errorf("root-key page returned invalid _key %v", row["_key"])
		}
		rootKeys = append(rootKeys, key)
		return nil
	}); err != nil {
		t.Fatalf("execute auth-scoped root-key page: %v\n%s", err, page.RootKeysQuery)
	}
	if len(rootKeys) != len(fixtures) {
		t.Fatalf("root-key page returned %d keys, want %d", len(rootKeys), len(fixtures))
	}
	rowBindVars := make(map[string]any, len(page.RowsBindVars)+1)
	for key, value := range page.RowsBindVars {
		rowBindVars[key] = value
	}
	rowBindVars[RootPageKeysBind] = rootKeys
	rows := make([]map[string]any, 0, 2)
	if err := client.QueryRows(ctx, page.RowsQuery, 500, rowBindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute auth-scoped grouped pivot materialization page: %v\n%s", err, page.RowsQuery)
	}
	assertAuthScopedPivotRows(t, rows)
}

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

	stringOutput := reshapeOracleOutput("string_exclusions", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
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

	missingNullOutput := reshapeOracleOutput("direct_missing_null", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-direct-missing-null", GroupKeys: []string{"source_id"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
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
	for _, operation := range missingNullCompiled.Plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp && operation.GroupedPivot != nil {
			missingNullFastPath = operation.GroupedPivot.OneInputRowPerGroup
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
	booleanOutput := reshapeOracleOutput("false_exclusions", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-false-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "flag_value", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarBoolean, Boolean: &trueValue}, Output: "true", Label: "True"}},
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
	numericOutput := reshapeOracleOutput("zero_exclusions", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-zero-exclusion", GroupKeys: []string{"group_text", "group_number"},
			CategoryColumn: "numeric_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
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

func reshapeOracleOutput(name string, reshape *recipe.TableReshape) recipe.Output {
	return recipe.Output{
		Name: name, RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "source_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "group_text", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "group_number", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "string_category", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "numeric_category", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "numeric_value", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
			{Name: "flag_value", Expr: recipe.Expression{Select: "root.valueBoolean"}},
			{Name: "text_value", Expr: recipe.Expression{Select: "root.valueString"}},
			{Name: "unrelated", Expr: recipe.Expression{Select: "root.issued"}},
		},
		TableReshape: reshape,
	}
}

func compileReshapeOracle(output recipe.Output, project, generation string, limit int) (lower.CompiledRecipeOutput, CompiledQuery, error) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "s04-table-reshape-oracle", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return lower.CompiledRecipeOutput{}, CompiledQuery{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		return lower.CompiledRecipeOutput{}, CompiledQuery{}, err
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return lower.CompiledRecipeOutput{}, CompiledQuery{}, err
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return lower.CompiledRecipeOutput{}, CompiledQuery{}, err
	}
	return compiled.Outputs[0], query, nil
}

func executeReshapeOracleQuery(t *testing.T, ctx context.Context, client *store.Client, query CompiledQuery) []map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute compiler-generated reshape query: %v\n%s", err, query.Query)
	}
	return rows
}

func outputColumn(columns []lower.CompiledOutputColumn, name string) any {
	for _, column := range columns {
		if column.Name == name {
			return column.Kind
		}
	}
	return nil
}

func assertReshapeOracleRepeatedIdentities(t *testing.T, ctx context.Context, client *store.Client, query CompiledQuery, first []map[string]any) {
	t.Helper()
	second := executeReshapeOracleQuery(t, ctx, client, query)
	firstIDs, secondIDs := reshapeOracleIdentities(first), reshapeOracleIdentities(second)
	if !reflect.DeepEqual(secondIDs, firstIDs) {
		t.Fatalf("repeated pivot identity order = %#v, want %#v", secondIDs, firstIDs)
	}
}

func reshapeOracleIdentities(rows []map[string]any) []string {
	identities := make([]string, 0, len(rows))
	for _, row := range rows {
		identity, _ := row["__loom_row_id"].(string)
		identities = append(identities, identity)
	}
	return identities
}

func assertReshapeOraclePivotWindow(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string) {
	t.Helper()
	_, query, err := compileReshapeOracle(output, project, generation, 2)
	if err != nil {
		t.Fatalf("compile limited grouped pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 2 {
		t.Fatalf("limited pivot rows = %#v, want first two of four grouped rows", rows)
	}
	wantIDs := []string{
		`["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",1]]`,
		`["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",2]]`,
	}
	if got := reshapeOracleIdentities(rows); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("limited pivot identities = %#v, want %#v", got, wantIDs)
	}
	if got := rows[0]["alpha"]; !reflect.DeepEqual(got, float64(7)) {
		t.Fatalf("first limited pivot alpha = %#v, want 7 after both duplicate source rows collapse", got)
	}
	if got := rows[1]["alpha"]; !reflect.DeepEqual(got, float64(12)) {
		t.Fatalf("second limited pivot alpha = %#v, want 12", got)
	}
}

func assertReshapeOraclePivotReducers(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	tests := []struct {
		policy recipe.PivotDuplicatePolicy
		alpha  []any
		beta   []any
		zero   []any
		empty  []any
	}{
		{policy: recipe.PivotDuplicateMin, alpha: []any{2.5, 12.0, 8.0, nil}, beta: []any{3.5, nil, nil, nil}, zero: []any{0.0, nil, nil, nil}, empty: []any{nil, nil, nil, nil}},
		{policy: recipe.PivotDuplicateMax, alpha: []any{4.5, 12.0, 8.0, nil}, beta: []any{3.5, nil, nil, nil}, zero: []any{0.0, nil, nil, nil}, empty: []any{nil, nil, nil, nil}},
	}
	groupOrder := []struct {
		text   string
		number int64
	}{
		{text: "final", number: 1}, {text: "final", number: 2},
		{text: "preliminary", number: 1}, {text: "preliminary", number: 2},
	}
	for _, test := range tests {
		t.Run(string(test.policy), func(t *testing.T) {
			output := reshapeOracleOutput("pivot_"+strings.ToLower(string(test.policy)), &recipe.TableReshape{
				Kind: recipe.TableReshapeGroupedPivot,
				GroupedPivot: &recipe.GroupedPivot{
					ConstructionID: "s04-pivot-" + strings.ToLower(string(test.policy)), GroupKeys: []string{"group_text", "group_number"},
					CategoryColumn: "string_category", ValueColumn: "numeric_value",
					Categories: []recipe.GroupedPivotCategory{
						{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"},
						{Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
						{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"},
						{Key: reshapeOracleString(""), Output: "empty_category", Label: "Empty category"},
					},
					DuplicatePolicy: test.policy, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
				},
			})
			_, query, err := compileReshapeOracle(output, project, generation, 100)
			if err != nil {
				t.Fatalf("compile %s pivot: %v", test.policy, err)
			}
			rows := executeReshapeOracleQuery(t, ctx, client, query)
			if len(rows) != len(groupOrder) {
				t.Fatalf("%s pivot rows = %#v, want %d", test.policy, rows, len(groupOrder))
			}
			for index, group := range groupOrder {
				row := rows[index]
				if row["group_text"] != group.text || row["group_number"] != float64(group.number) {
					t.Errorf("%s pivot row %d group = %#v/%#v, want %q/%d", test.policy, index, row["group_text"], row["group_number"], group.text, group.number)
				}
				for column, expected := range map[string]any{
					"alpha": test.alpha[index], "beta": test.beta[index], "zero": test.zero[index], "empty_category": test.empty[index],
				} {
					if got := row[column]; !reflect.DeepEqual(got, expected) {
						t.Errorf("%s pivot %s/%d.%s = %#v, want literal %#v", test.policy, group.text, group.number, column, got, expected)
					}
				}
			}
			assertReshapeOraclePivotReducerTrace(t, ctx, client, output, project, generation, test.policy)
		})
	}
}

func assertReshapeOracleNumericCategories(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("numeric_categories", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-numeric-categories", GroupKeys: []string{"group_text"},
			CategoryColumn: "numeric_category", ValueColumn: "numeric_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleInteger(1), Output: "one", Label: "One"},
				{Key: reshapeOracleInteger(2), Output: "two", Label: "Two"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	_, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile numeric-category pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	wants := []struct {
		group string
		one   any
		two   any
		id    string
	}{
		{group: "final", one: 19.5, two: 12.0, id: `["GROUPED_PIVOT","s04-pivot-numeric-categories",["STRING","final"]]`},
		{group: "preliminary", one: 8.0, two: nil, id: `["GROUPED_PIVOT","s04-pivot-numeric-categories",["STRING","preliminary"]]`},
	}
	if len(rows) != len(wants) {
		t.Fatalf("numeric-category pivot rows = %#v, want %d", rows, len(wants))
	}
	for index, want := range wants {
		row := rows[index]
		for column, expected := range map[string]any{"group_text": want.group, "one": want.one, "two": want.two, "__loom_row_id": want.id} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("numeric-category row %d.%s = %#v, want literal %#v", index, column, got, expected)
			}
		}
	}
}

func assertReshapeOracleBooleanValues(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("boolean_values", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-bool", GroupKeys: []string{"source_id", "group_number"},
			CategoryColumn: "group_text", ValueColumn: "flag_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("final"), Output: "final", Label: "Final"},
				{Key: reshapeOracleString("preliminary"), Output: "preliminary", Label: "Preliminary"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	output.Filters = []recipe.Filter{
		{Select: "root.id", Operator: recipe.FilterIn, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a"), reshapeOracleStringFilter("alpha-b")}},
	}
	_, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile boolean-value pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	wants := []struct {
		source string
		value  bool
	}{
		{source: "alpha-a", value: false}, {source: "alpha-b", value: true},
	}
	if len(rows) != len(wants) {
		t.Fatalf("boolean-value pivot rows = %#v, want %d", rows, len(wants))
	}
	for index, want := range wants {
		wantID := fmt.Sprintf(`["GROUPED_PIVOT","s04-pivot-bool",["STRING","%s"],["INTEGER",1]]`, want.source)
		for column, expected := range map[string]any{"source_id": want.source, "final": want.value, "preliminary": nil, "__loom_row_id": wantID} {
			if got := rows[index][column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("boolean pivot %s.%s = %#v, want literal %#v", want.source, column, got, expected)
			}
		}
	}
}

func assertReshapeOracleEmptyStringValue(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("empty_string_value", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-empty-string", GroupKeys: []string{"source_id", "group_number"},
			CategoryColumn: "group_text", ValueColumn: "text_value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: reshapeOracleString("final"), Output: "final", Label: "Final"},
				{Key: reshapeOracleString("preliminary"), Output: "preliminary", Label: "Preliminary"},
			},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	output.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{reshapeOracleStringFilter("empty-and-null")}}}
	_, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile empty-string pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 1 {
		t.Fatalf("empty-string pivot rows = %#v, want one", rows)
	}
	for column, expected := range map[string]any{"final": nil, "preliminary": "", "__loom_row_id": `["GROUPED_PIVOT","s04-pivot-empty-string",["STRING","empty-and-null"],["INTEGER",2]]`} {
		if got := rows[0][column]; !reflect.DeepEqual(got, expected) {
			t.Errorf("empty-string pivot.%s = %#v, want literal %#v", column, got, expected)
		}
	}
}

func assertReshapeOraclePivotErrors(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	tests := []struct {
		name      string
		pivot     recipe.GroupedPivot
		stableErr string
	}{
		{
			name: "duplicate error",
			pivot: recipe.GroupedPivot{
				ConstructionID: "s04-pivot-duplicate-error", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value",
				Categories: []recipe.GroupedPivotCategory{
					{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}, {Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
					{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"}, {Key: reshapeOracleString(""), Output: "empty_category", Label: "Empty category"},
					{Key: reshapeOracleString("unlisted"), Output: "unlisted", Label: "Unlisted"},
				},
				DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
			},
			stableErr: "TABLE_PIVOT_CELL_CARDINALITY",
		},
		{
			name: "missing cell error",
			pivot: recipe.GroupedPivot{
				ConstructionID: "s04-pivot-missing-error", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value",
				Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("ghost"), Output: "ghost", Label: "Ghost"}},
				DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellError, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
			},
			stableErr: "TABLE_PIVOT_CELL_MISSING",
		},
		{
			name: "unlisted category error",
			pivot: recipe.GroupedPivot{
				ConstructionID: "s04-pivot-unlisted-error", GroupKeys: []string{"group_text", "group_number"}, CategoryColumn: "string_category", ValueColumn: "numeric_value",
				Categories: []recipe.GroupedPivotCategory{
					{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}, {Key: reshapeOracleString("beta"), Output: "beta", Label: "Beta"},
					{Key: reshapeOracleString("zero"), Output: "zero", Label: "Zero"}, {Key: reshapeOracleString(""), Output: "empty_category", Label: "Empty category"},
				},
				DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
			},
			stableErr: "TABLE_PIVOT_UNLISTED_CATEGORY",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := reshapeOracleOutput("expected_error", &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &test.pivot})
			_, query, err := compileReshapeOracle(output, project, generation, 100)
			if err != nil {
				t.Fatalf("compile error case: %v", err)
			}
			_, err = queryReshapeOracle(ctx, client, query)
			if err == nil || !strings.Contains(err.Error(), test.stableErr) {
				t.Fatalf("runtime error = %v, want stable code %s", err, test.stableErr)
			}
		})
	}
}

func assertReshapeOracleUnpivot(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot")
	output.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterIn, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a"), reshapeOracleStringFilter("empty-and-null")}}}
	compiled, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile preserving unpivot: %v", err)
	}
	if got := outputColumn(compiled.OutputSchema, "amount"); got != "decimal" {
		t.Fatalf("unpivot amount schema kind = %v, want decimal after INTEGER+DECIMAL widening", got)
	}
	if got := outputColumn(compiled.OutputSchema, "measure"); got != "string" {
		t.Fatalf("unpivot key schema kind = %v, want string", got)
	}
	if !reshapeOracleColumnNullable(compiled.OutputSchema, "amount") {
		t.Fatal("PRESERVE amount schema is not nullable")
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	t.Logf("unpivot PRESERVE identities: %q", reshapeOracleIdentities(rows))
	wantPreserve := []reshapeOracleUnpivotWant{
		{SourceID: "alpha-a", GroupText: "final", StringCategory: "alpha", NumericCategory: 1, Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a", Key: "a-decimal", Value: 2.5, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "a-decimal")},
		{SourceID: "alpha-a", GroupText: "final", StringCategory: "alpha", NumericCategory: 1, Flag: false, Text: "alpha-a", Unrelated: "keep-alpha-a", Key: "z-integer", Value: 1.0, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "z-integer")},
		{SourceID: "empty-and-null", GroupText: "preliminary", StringCategory: "", NumericCategory: 2, Flag: false, Text: "", Unrelated: "keep-empty-and-null", Key: "a-decimal", Value: nil, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "a-decimal")},
		{SourceID: "empty-and-null", GroupText: "preliminary", StringCategory: "", NumericCategory: 2, Flag: false, Text: "", Unrelated: "keep-empty-and-null", Key: "z-integer", Value: 2.0, RowID: reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "z-integer")},
	}
	assertReshapeOracleUnpivotRows(t, rows, wantPreserve)
	assertReshapeOracleUnpivotCellTrace(t, ctx, client, compiled, project)
	_, limitedQuery, err := compileReshapeOracle(output, project, generation, 3)
	if err != nil {
		t.Fatalf("compile limited unpivot: %v", err)
	}
	limitedRows := executeReshapeOracleQuery(t, ctx, client, limitedQuery)
	assertReshapeOracleUnpivotRows(t, limitedRows, wantPreserve[:3])

	dropOutput := reshapeOracleUnpivotOutput(recipe.UnpivotNullDrop, "s04-unpivot-drop")
	dropOutput.Filters = append([]recipe.Filter(nil), output.Filters...)
	dropCompiled, dropQuery, err := compileReshapeOracle(dropOutput, project, generation, 100)
	if err != nil {
		t.Fatalf("compile dropping unpivot: %v", err)
	}
	if got := outputColumn(dropCompiled.OutputSchema, "amount"); got != "decimal" || reshapeOracleColumnNullable(dropCompiled.OutputSchema, "amount") {
		t.Fatalf("DROP amount schema = %v nullable=%t, want decimal non-null", got, reshapeOracleColumnNullable(dropCompiled.OutputSchema, "amount"))
	}
	dropRows := executeReshapeOracleQuery(t, ctx, client, dropQuery)
	t.Logf("unpivot DROP identities: %q", reshapeOracleIdentities(dropRows))
	wantDrop := []reshapeOracleUnpivotWant{wantPreserve[0], wantPreserve[1], wantPreserve[3]}
	for index := range wantDrop {
		wantDrop[index].RowID = reshapeOracleUnpivotID(project, "s04-unpivot-drop", wantDrop[index].SourceID, wantDrop[index].Key)
	}
	assertReshapeOracleUnpivotRows(t, dropRows, wantDrop)
	droppedNullIdentity := reshapeOracleUnpivotID(project, "s04-unpivot-drop", "empty-and-null", "a-decimal")
	for _, identity := range reshapeOracleIdentities(dropRows) {
		if identity == droppedNullIdentity {
			t.Fatalf("DROP output unexpectedly retained null-row identity %q", droppedNullIdentity)
		}
	}

	repeated := executeReshapeOracleQuery(t, ctx, client, query)
	if got, want := reshapeOracleIdentities(repeated), reshapeOracleIdentities(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("repeated unpivot identity order = %#v, want %#v", got, want)
	}

	ordered := reshapeOracleUnpivotOutput(recipe.UnpivotNullPreserve, "s04-unpivot-order")
	ordered.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a")}}}
	compiledOrder, orderedQuery, err := compileReshapeOracle(ordered, project, generation, 100)
	if err != nil {
		t.Fatalf("compile ordered unpivot: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiledOrder.Plan)
	if err != nil {
		t.Fatalf("render unwindowed compiler plan: %v", err)
	}
	rawRows, err := queryReshapeOracle(ctx, client, CompiledQuery{Query: rendered.Query, BindVars: rendered.BindVars})
	if err != nil {
		t.Fatalf("execute unwindowed compiler plan: %v\n%s", err, rendered.Query)
	}
	if len(rawRows) != 2 || rawRows[0]["measure"] != "z-integer" || rawRows[1]["measure"] != "a-decimal" {
		t.Fatalf("raw unpivot emitted keys in %#v, want authored [z-integer, a-decimal]", rawRows)
	}
	windowRows := executeReshapeOracleQuery(t, ctx, client, orderedQuery)
	if len(windowRows) != 2 || windowRows[0]["measure"] != "a-decimal" || windowRows[1]["measure"] != "z-integer" {
		t.Fatalf("stable-sorted unpivot keys = %#v, want [a-decimal, z-integer]", windowRows)
	}
	for _, row := range windowRows {
		if got, ok := row["__loom_row_id"].(string); !ok || !strings.Contains(got, fmt.Sprintf(`["STRING","%v"]`, row["measure"])) {
			t.Errorf("unpivot identity %v does not include its selected input key %v", row["__loom_row_id"], row["measure"])
		}
	}
}

func assertReshapeOracleUnpivotCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, project string) {
	t.Helper()
	_, amount := executeReshapeOracleTrace(t, ctx, client, output, "amount")
	alphaID := reshapeOracleUnpivotID(project, "s04-unpivot", "alpha-a", "a-decimal")
	assertReshapeTraceResult(t, amount[alphaID], float64(2.5), "VALUE", reshapeOracleContributors("alpha-a", float64(2.5)), "")
	nullID := reshapeOracleUnpivotID(project, "s04-unpivot", "empty-and-null", "a-decimal")
	assertReshapeTraceResult(t, amount[nullID], nil, "RECORDED_NULL", reshapeOracleContributors("empty-and-null", nil), "")
	trace, measure := executeReshapeOracleTrace(t, ctx, client, output, "measure")
	keyCell := measure[alphaID]
	if keyCell.value != "a-decimal" || keyCell.status != "NO_MATCH" || keyCell.omission != ir.PhysicalCellTraceGeneratedKeyOmission || len(keyCell.contributors) != 0 {
		t.Fatalf("unpivot generated-key evidence = %#v, want explicit no-source omission", keyCell)
	}
	if trace.ExplicitIdentityColumn == "" {
		t.Fatal("unpivot trace does not preserve stable row identity")
	}
}

func reshapeOracleUnpivotOutput(policy recipe.UnpivotNullRowPolicy, constructionID string) recipe.Output {
	return reshapeOracleOutput("unpivot", &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: constructionID,
			Inputs: []recipe.UnpivotInput{
				{Column: "group_number", Key: reshapeOracleString("z-integer")},
				{Column: "numeric_value", Key: reshapeOracleString("a-decimal")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: policy,
		},
	})
}

func reshapeOracleUnpivotID(project, constructionID, sourceID, key string) string {
	return fmt.Sprintf(`[["project","%s"],["_key","%s_%s"],["%s","TABLE_UNPIVOT"],["STRING","%s"]]`, project, project, sourceID, constructionID, key)
}

func assertReshapeOracleUnpivotRows(t *testing.T, rows []map[string]any, wants []reshapeOracleUnpivotWant) {
	t.Helper()
	if len(rows) != len(wants) {
		t.Fatalf("unpivot rows = %#v, want %d typed rows", rows, len(wants))
	}
	for index, want := range wants {
		row := rows[index]
		for column, expected := range map[string]any{
			"source_id": want.SourceID, "group_text": want.GroupText,
			"string_category": want.StringCategory, "numeric_category": float64(want.NumericCategory), "flag_value": want.Flag,
			"text_value": want.Text, "unrelated": want.Unrelated, "measure": want.Key, "amount": want.Value, "__loom_row_id": want.RowID,
		} {
			if got := row[column]; !reflect.DeepEqual(got, expected) {
				t.Errorf("unpivot row %d.%s = %#v, want literal %#v", index, column, got, expected)
			}
		}
		if _, ok := row["amount"].(string); ok {
			t.Errorf("unpivot numeric value %q was coerced to string: %#v", want.Key, row["amount"])
		}
	}
}

func reshapeOracleColumnNullable(columns []lower.CompiledOutputColumn, name string) bool {
	for _, column := range columns {
		if column.Name == name {
			return column.Nullable
		}
	}
	return false
}

func reshapeOracleStringFilter(value string) recipe.FilterValue {
	return recipe.FilterValue{Kind: recipe.FilterString, String: &value}
}

func assertReshapeOracleUnpivotRejectsIncompatibleInputs(t *testing.T, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("incompatible_unpivot", &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: "s04-unpivot-incompatible",
			Inputs: []recipe.UnpivotInput{
				{Column: "numeric_value", Key: reshapeOracleString("number")},
				{Column: "text_value", Key: reshapeOracleString("text")},
			},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	})
	if _, _, err := compileReshapeOracle(output, project, generation, 100); err == nil || !strings.Contains(err.Error(), "incompatible with previous inputs") {
		t.Fatalf("incompatible unpivot compile error = %v, want incompatible input types", err)
	}
}

func queryReshapeOracle(ctx context.Context, client *store.Client, query CompiledQuery) ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func executeReshapeOracleTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column string) (CompiledCellTraceQuery, map[string]operatorOracleTraceResult) {
	t.Helper()
	trace, err := CompileCellTraceOutputWithPolicy(output, column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile %s cell trace: %v", column, err)
	}
	if trace.ExplicitIdentityColumn == "" {
		t.Fatalf("reshaped %s trace has no explicit output-row identity", column)
	}
	rows := make([]map[string]any, 0)
	if err := client.QueryRows(ctx, trace.Query, 500, trace.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute %s cell trace: %v\n%s", column, err, trace.Query)
	}
	results := make(map[string]operatorOracleTraceResult, len(rows))
	for _, row := range rows {
		identity, ok := row[trace.ExplicitIdentityColumn].(string)
		if !ok || identity == "" {
			t.Fatalf("trace explicit identity = %#v, want stable string", row[trace.ExplicitIdentityColumn])
		}
		items, ok := row[trace.ContributionsColumn].([]any)
		if !ok {
			t.Fatalf("trace contributors = %#v, want array", row[trace.ContributionsColumn])
		}
		contributors := make([]operatorOracleContributor, 0, len(items))
		for _, item := range items {
			contributor, ok := item.(map[string]any)
			if !ok {
				t.Fatalf("trace contributor = %#v, want object", item)
			}
			resourceType, _ := contributor["resourceType"].(string)
			resourceID, _ := contributor["resourceId"].(string)
			contributors = append(contributors, operatorOracleContributor{resourceType: resourceType, resourceID: resourceID, value: contributor["value"]})
		}
		status, _ := row[trace.StatusColumn].(string)
		omission, _ := row[trace.OmissionColumn].(string)
		results[identity] = operatorOracleTraceResult{value: row[trace.ValueColumn], status: status, omission: omission, contributors: contributors}
	}
	return trace, results
}

func assertReshapeTraceResult(t *testing.T, result operatorOracleTraceResult, value any, status string, contributors []operatorOracleContributor, omission string) {
	t.Helper()
	if !reflect.DeepEqual(result.value, value) || result.status != status || result.omission != omission || !reflect.DeepEqual(result.contributors, contributors) {
		t.Fatalf("cell trace = %#v, want value=%#v status=%q omission=%q contributors=%#v", result, value, status, omission, contributors)
	}
}

func reshapeOracleContributors(values ...any) []operatorOracleContributor {
	contributors := make([]operatorOracleContributor, 0, len(values)/2)
	for index := 0; index+1 < len(values); index += 2 {
		contributors = append(contributors, operatorOracleContributor{resourceType: "Observation", resourceID: values[index].(string), value: values[index+1]})
	}
	return contributors
}

func assertReshapeOraclePivotCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput) {
	t.Helper()
	rowID := reshapeOraclePivotSumWant[0].RowID
	_, alpha := executeReshapeOracleTrace(t, ctx, client, output, "alpha")
	assertReshapeTraceResult(t, alpha[rowID], float64(7), "VALUE", reshapeOracleContributors("alpha-a", float64(2.5), "alpha-b", float64(4.5)), "")
	_, zero := executeReshapeOracleTrace(t, ctx, client, output, "zero")
	assertReshapeTraceResult(t, zero[rowID], float64(0), "VALUE", reshapeOracleContributors("zero", float64(0)), "")
	_, derived := executeReshapeOracleTrace(t, ctx, client, output, "alpha_plus_zero")
	assertReshapeTraceResult(t, derived[rowID], float64(7), "VALUE", reshapeOracleContributors("alpha-a", float64(2.5), "alpha-b", float64(4.5), "zero", float64(0)), "")
	_, group := executeReshapeOracleTrace(t, ctx, client, output, "group_text")
	assertReshapeTraceResult(t, group[rowID], "final", "VALUE", reshapeOracleContributors("alpha-a", "final", "alpha-b", "final", "beta", "final", "unlisted", "final", "zero", "final"), "")
}

func assertReshapeOraclePivotReducerTrace(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string, policy recipe.PivotDuplicatePolicy) {
	t.Helper()
	compiled, _, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile %s trace fixture: %v", policy, err)
	}
	_, cells := executeReshapeOracleTrace(t, ctx, client, compiled, "alpha")
	rowID := fmt.Sprintf(`["GROUPED_PIVOT","s04-pivot-%s",["STRING","final"],["INTEGER",1]]`, strings.ToLower(string(policy)))
	var value any
	var contributors []operatorOracleContributor
	switch policy {
	case recipe.PivotDuplicateMin:
		value = float64(2.5)
		contributors = reshapeOracleContributors("alpha-a", float64(2.5))
	case recipe.PivotDuplicateMax:
		value = float64(4.5)
		contributors = reshapeOracleContributors("alpha-b", float64(4.5))
	default:
		t.Fatalf("unexpected reducer trace policy %q", policy)
	}
	assertReshapeTraceResult(t, cells[rowID], value, "VALUE", contributors, "")
}

func assertReshapeOraclePivotErrorTrace(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	output := reshapeOracleOutput("pivot_error_trace", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "s04-pivot-error", GroupKeys: []string{"source_id"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("alpha"), Output: "alpha", Label: "Alpha"}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	output.Filters = []recipe.Filter{{Select: "root.id", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{reshapeOracleStringFilter("alpha-a")}}}
	compiled, _, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile ERROR pivot trace: %v", err)
	}
	_, cells := executeReshapeOracleTrace(t, ctx, client, compiled, "alpha")
	rowID := `["GROUPED_PIVOT","s04-pivot-error",["STRING","alpha-a"]]`
	assertReshapeTraceResult(t, cells[rowID], float64(2.5), "VALUE", reshapeOracleContributors("alpha-a", float64(2.5)), "")
}
