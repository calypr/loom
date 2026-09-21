package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
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
		t.Fatal("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
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

	assertReshapeOracleRepeatedIdentities(t, ctx, client, query, rows)
	assertReshapeOraclePivotWindow(t, ctx, client, output, project, generation)
	assertReshapeOraclePivotReducers(t, ctx, client, project, generation)
	assertReshapeOracleNumericCategories(t, ctx, client, project, generation)
	assertReshapeOracleBooleanValues(t, ctx, client, project, generation)
	assertReshapeOracleEmptyStringValue(t, ctx, client, project, generation)
	assertReshapeOraclePivotErrors(t, ctx, client, project, generation)
	assertReshapeOracleUnpivot(t, ctx, client, project, generation)
	assertReshapeOracleUnpivotRejectsIncompatibleInputs(t, project, generation)
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
