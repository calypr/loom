package execution

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func compiledTableShapeExclusionPage(offset, limit int) compiler.CompiledTableShapeExclusionQuery {
	return compiler.CompiledTableShapeExclusionQuery{
		Query: "exclusions", BindVars: map[string]any{"offset": offset, "limit": limit}, Offset: offset, Limit: limit,
		ResourceTypeField: "resourceType", ResourceIDField: "resourceId", IdentityStatusField: "identityStatus",
		CategoryValueField: "categoryValue", CategoryPresentField: "categoryPresent", CategoryTypeField: "categoryType",
		OutputRowIDField: "outputRowId", ReasonField: "reason", OmissionField: "omission",
	}
}

func TestTableShapeExclusionsCompiledPagesExactTypedRows(t *testing.T) {
	rows := []map[string]any{
		tableShapeExclusionRow("Observation", "false", true, false, "BOOLEAN", "row-bool"),
		tableShapeExclusionRow("Observation", "zero", true, float64(0), "INTEGER", "row-zero"),
		tableShapeExclusionRow("Observation", "empty", true, "", "STRING", "row-empty"),
		tableShapeExclusionRow("Observation", "null", true, nil, "STRING", "row-null"),
		tableShapeExclusionRow("Observation", "missing", false, nil, "STRING", "row-missing"),
	}
	engine := &Engine{queryRows: func(_ context.Context, query string, _ int, binds map[string]any, visit func(map[string]any) error) error {
		if query != "exclusions" || binds["offset"] != 0 || binds["limit"] != 10 {
			t.Fatalf("unexpected query invocation %q %#v", query, binds)
		}
		for _, row := range rows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 10))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != TableShapeExclusionComplete || !result.Complete || result.HasMore || result.NextOffset != 0 || len(result.Exclusions) != len(rows) {
		t.Fatalf("result metadata = %#v", result)
	}
	wants := []struct {
		present bool
		value   any
	}{
		{true, false}, {true, float64(0)}, {true, ""}, {true, nil}, {false, nil},
	}
	for index, want := range wants {
		got := result.Exclusions[index].Category
		if got.Present != want.present || !reflect.DeepEqual(got.Value, want.value) {
			t.Errorf("category %d = %#v, want present=%t value=%#v", index, got, want.present, want.value)
		}
	}
}

func TestTableShapeExclusionsCompiledPagesAndMarksIdentityOmissionIncomplete(t *testing.T) {
	allRows := []map[string]any{
		tableShapeExclusionRow("Observation", "one", true, "one", "STRING", "row-one"),
		tableShapeExclusionRow("Observation", "two", true, "two", "STRING", "row-two"),
		tableShapeExclusionRow("", "", false, "not-a-real-id", "STRING", "row-three"),
	}
	makeEngine := func() *Engine {
		return &Engine{queryRows: func(_ context.Context, query string, _ int, binds map[string]any, visit func(map[string]any) error) error {
			if query != "exclusions" {
				t.Fatalf("query = %q", query)
			}
			offset := binds["offset"].(int)
			for _, row := range allRows[offset:] {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		}}
	}
	first, err := makeEngine().TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(0, 2))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != TableShapeExclusionIncomplete || first.Complete || !first.HasMore || first.NextOffset != 2 || len(first.Exclusions) != 2 {
		t.Fatalf("first page = %#v", first)
	}
	last, err := makeEngine().TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(2, 2))
	if err != nil {
		t.Fatal(err)
	}
	if last.Status != TableShapeExclusionIncomplete || last.Complete || last.HasMore || last.NextOffset != 2 || len(last.Exclusions) != 1 {
		t.Fatalf("identity-omission page = %#v", last)
	}
	if last.Exclusions[0].SourceIdentity != nil || last.Exclusions[0].OmissionCode != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("missing source identity must be an explicit omission, not synthesized: %#v", last.Exclusions[0])
	}
}

func TestTableShapeExclusionsCompiledRejectsMalformedRowsWithoutPartialSuccess(t *testing.T) {
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		_ = visit(tableShapeExclusionRow("Observation", "good", true, "a", "STRING", "row-a"))
		return visit(map[string]any{"categoryPresent": "not-bool"})
	}}
	result, err := engine.TableShapeExclusionsCompiled(context.Background(), compiledTableShapeExclusionPage(4, 10))
	if err == nil || result.Complete || len(result.Exclusions) > 0 {
		t.Fatalf("malformed partial page was presented as usable: result=%#v error=%v", result, err)
	}
}

func TestTableShapeExclusionsReportsUnsupportedAndNoPolicy(t *testing.T) {
	engine := testEngine(func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
		t.Fatal("refused table-shape diagnostics must not execute a query")
		return nil
	})
	for _, test := range []struct {
		name   string
		bundle recipe.Bundle
		want   TableShapeExclusionStatus
	}{
		{name: "no reshape", bundle: exclusionStatusBundle(nil), want: TableShapeExclusionUnsupported},
		{name: "missing policy", bundle: exclusionStatusBundle(&recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: stringPtr("known")}, Output: "known", Label: "Known"}},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		}}), want: TableShapeExclusionNoExclusionPolicy},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := engine.CompileResolvedBundle(context.Background(), test.bundle, recipe.RuntimeBindings{Project: "project"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.TableShapeExclusions(context.Background(), resolved, TableShapeExclusionRequest{Output: "Observations", Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want || result.Complete || result.HasMore || len(result.Exclusions) != 0 {
				t.Fatalf("refusal result = %#v, want status %s", result, test.want)
			}
		})
	}
}

func exclusionStatusBundle(reshape *recipe.TableReshape) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "exclusion-status", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "Observations", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
			Fields: []recipe.Field{
				{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "group", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "category", Expr: recipe.Expression{Select: "root.code.text"}},
				{Name: "value", Expr: recipe.Expression{Select: "root.valueInteger"}},
			},
			TableReshape: reshape,
		}},
	}
}

func stringPtr(value string) *string { return &value }

func tableShapeExclusionRow(resourceType, resourceID string, present bool, value any, categoryType, rowID string) map[string]any {
	identityStatus := "EXACT"
	var omission any
	var outputResourceType, outputResourceID any = resourceType, resourceID
	if resourceType == "" || resourceID == "" {
		identityStatus = "UNAVAILABLE"
		omission = "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"
		outputResourceType, outputResourceID = nil, nil
	}
	return map[string]any{
		"resourceType": outputResourceType, "resourceId": outputResourceID, "identityStatus": identityStatus,
		"categoryPresent": present, "categoryValue": value, "categoryType": categoryType,
		"outputRowId": rowID, "reason": "UNLISTED_CATEGORY", "omission": omission,
	}
}
