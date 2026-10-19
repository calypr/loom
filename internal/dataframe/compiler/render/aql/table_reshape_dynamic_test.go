package aql

import (
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestDynamicPivotPreviewQuerySizeDoesNotScaleWithCategoryCount(t *testing.T) {
	render := func(categoryCount int, dynamic bool) string {
		t.Helper()
		bindVars := map[string]any{"construction": "pivot", "limit": 20}
		categories := make([]ir.PhysicalGroupedPivotCategory, 0, categoryCount)
		for index := range categoryCount {
			bind := fmt.Sprintf("category_%d", index)
			bindVars[bind] = fmt.Sprintf("category-%d", index)
			categories = append(categories, ir.PhysicalGroupedPivotCategory{
				Output: fmt.Sprintf("output_%d", index), MatchKind: ir.PhysicalPivotCategoryValueMatch,
				ValueBindKey: bind, ValueKind: "STRING",
			})
		}
		pivot := ir.PhysicalGroupedPivot{
			ConstructionID: "pivot", ConstructionIDBindKey: "construction",
			InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
			InputProjections: []ir.PhysicalProjection{
				{Name: "group", Value: ir.PhysicalValue{Variable: "source"}},
				{Name: "category", Value: ir.PhysicalValue{Variable: "source"}},
				{Name: "value", Value: ir.PhysicalValue{Variable: "source"}},
			},
			GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group", Variable: "group_value", Kind: "STRING"}},
			CategoryColumn: "category", ValueColumn: "value", CategoryType: "STRING", ValueType: "INTEGER",
			Categories: categories, DuplicatePolicy: "SUM", MissingCellPolicy: "NULL",
			UnlistedCategoryPolicy: "ERROR",
		}
		renderer := physicalPlanRenderer{
			bindVars: bindVars, collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
			dynamicPivotPreview: dynamic,
		}
		lines, err := renderer.renderGroupedTablePivot(pivot, true, "limit")
		if err != nil {
			t.Fatalf("render %d-category pivot (dynamic=%t): %v", categoryCount, dynamic, err)
		}
		return strings.Join(lines, "\n")
	}

	smallDynamic := render(2, true)
	largeDynamic := render(136, true)
	if len(largeDynamic) != len(smallDynamic) {
		t.Fatalf("dynamic query text grew with category count: 2 categories=%d bytes, 136 categories=%d bytes", len(smallDynamic), len(largeDynamic))
	}
	if strings.Count(largeDynamic, "FOR __loom_physical_reshape_category_spec IN @") != 1 {
		t.Fatalf("dynamic renderer did not use one category loop:\n%s", largeDynamic)
	}
	if !strings.Contains(largeDynamic, "[__loom_physical_reshape_category_spec.output]") ||
		!strings.Contains(largeDynamic, "TABLE_PIVOT_CELL_MISSING") ||
		!strings.Contains(largeDynamic, "TABLE_PIVOT_UNLISTED_CATEGORY") {
		t.Fatalf("dynamic query omitted its dynamic output or policy checks:\n%s", largeDynamic)
	}
	cellsIndex := strings.Index(largeDynamic, "LET __loom_physical_reshape_dynamic_cells =")
	unlistedIndex := strings.Index(largeDynamic, "FILTER (IS_OBJECT(__loom_physical_reshape_dynamic_cells) ? ASSERT(")
	if cellsIndex < 0 || unlistedIndex < 0 || cellsIndex > unlistedIndex {
		t.Fatalf("unlisted assertion must depend on evaluated category cells:\n%s", largeDynamic)
	}

	smallStatic := render(2, false)
	largeStatic := render(136, false)
	if len(largeStatic) < 3*len(largeDynamic) || len(largeStatic) < 3*len(smallStatic) {
		t.Fatalf("dynamic query did not materially reduce text: small static=%d, large static=%d, dynamic=%d", len(smallStatic), len(largeStatic), len(largeDynamic))
	}
}

func TestDynamicPivotPreviewKeepsTypedAndPresenceCategoryMatching(t *testing.T) {
	match, err := groupedPivotDynamicCategoryMatchPredicate("spec", "row", "category", "presence", "category_type")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`spec.matchKind == "VALUE"`, "TYPENAME(row[@category]) == @category_type",
		"row[@presence] == true", "row[@category] == spec.value",
		`spec.matchKind == "NULL"`, `spec.matchKind == "MISSING"`,
		"NOT (row[@presence] == true)",
	} {
		if !strings.Contains(match, fragment) {
			t.Errorf("dynamic match %q does not contain %q", match, fragment)
		}
	}

	cell, err := groupedPivotCellResultExpression(ir.PhysicalGroupedPivot{
		DuplicatePolicy: "ERROR", MissingCellPolicy: "ERROR",
	}, "values", "typed_values")
	if err != nil {
		t.Fatal(err)
	}
	cardinalityIndex := strings.Index(cell, "TABLE_PIVOT_CELL_CARDINALITY")
	missingIndex := strings.Index(cell, "TABLE_PIVOT_CELL_MISSING")
	if cardinalityIndex < 0 || missingIndex < 0 || missingIndex > cardinalityIndex {
		t.Fatalf("cell assertions do not preserve missing-then-cardinality nesting: %s", cell)
	}
	if _, err := groupedPivotCellResultExpression(ir.PhysicalGroupedPivot{
		DuplicatePolicy: "UNKNOWN", MissingCellPolicy: "NULL",
	}, "values", "typed_values"); err == nil {
		t.Fatal("unsupported duplicate policy silently rendered as a null cell")
	}
}

func TestDynamicPivotPreviewBindsMissingCategoryWithPresenceProof(t *testing.T) {
	bindVars := map[string]any{"construction": "pivot", "limit": 10}
	pivot := ir.PhysicalGroupedPivot{
		ConstructionID: "pivot", ConstructionIDBindKey: "construction",
		InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
		InputProjections: []ir.PhysicalProjection{
			{Name: "group", Value: ir.PhysicalValue{Variable: "source"}},
			{Name: "category", Value: ir.PhysicalValue{Variable: "source"}},
			{Name: "value", Value: ir.PhysicalValue{Variable: "source"}},
		},
		GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group", Variable: "group_value", Kind: "STRING"}},
		CategoryColumn: "category", CategoryPresenceColumn: "category_present",
		CategoryPresence: &ir.PhysicalProjectionPresence{Source: ir.PhysicalValue{Variable: "source"}, Paths: [][]string{{"category"}}},
		ValueColumn:      "value", CategoryType: "STRING", ValueType: "INTEGER",
		Categories:      []ir.PhysicalGroupedPivotCategory{{Output: "missing", MatchKind: ir.PhysicalPivotCategoryMissingMatch}},
		DuplicatePolicy: "SUM", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}
	renderer := physicalPlanRenderer{
		bindVars: bindVars, collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
		dynamicPivotPreview: true,
	}
	lines, err := renderer.renderGroupedTablePivot(pivot, true, "limit")
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	var specs []map[string]any
	presenceBind := ""
	for key, value := range renderer.bindVars {
		if strings.Contains(key, "reshape_category_specs") {
			var ok bool
			specs, ok = value.([]map[string]any)
			if !ok {
				t.Fatalf("category specs bind type = %T, want []map[string]any", value)
			}
		}
		if value == "category_present" {
			presenceBind = key
		}
	}
	if presenceBind == "" || !strings.Contains(query, "[@"+presenceBind+"] == true") || !strings.Contains(query, "NOT (") {
		t.Fatalf("dynamic query did not preserve presence-aware MISSING matching:\n%s", query)
	}
	if len(specs) != 1 || specs[0]["matchKind"] != string(ir.PhysicalPivotCategoryMissingMatch) || specs[0]["output"] != "missing" {
		t.Fatalf("MISSING spec = %#v, want presence-aware output spec", specs)
	}
}
