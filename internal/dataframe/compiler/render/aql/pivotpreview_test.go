package aql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
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
		var preview Preview
		if dynamic {
			preview = DynamicCategoryPivotPreview{}
		}
		renderer := physicalPlanRenderer{renderContext: renderContext{
			bindVars: bindVars, collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
			renderPreview: renderPreview{preview},
		}}
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
	renderer := physicalPlanRenderer{renderContext: renderContext{
		bindVars: bindVars, collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
		renderPreview: renderPreview{DynamicCategoryPivotPreview{}},
	}}
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

func TestStreamingMissingCategoryPivotUsesDirectScalarInputs(t *testing.T) {
	render := func(duplicatePolicy, missingCellPolicy, unlistedPolicy string) (string, physicalPlanRenderer) {
		t.Helper()
		renderer := physicalPlanRenderer{renderContext: renderContext{
			bindVars: map[string]any{
				"construction":   "pivot",
				"limit":          10,
				"category_false": false,
			},
			collectionKeys:           map[string]struct{}{},
			reservedVars:             map[string]struct{}{},
			rootVariable:             "root",
			storedPresenceParentPath: []string{"payload", "valueQuantity"},
			renderPreview:            renderPreview{StreamingMissingCategoryPivotPreview{}},
		}}
		pivot := ir.PhysicalGroupedPivot{
			ConstructionID: "pivot", ConstructionIDBindKey: "construction",
			InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
			InputProjections: []ir.PhysicalProjection{
				{Name: "group_key", Expression: directScalarProjection("root", "payload", "valueQuantity", "group_key")},
				{Name: "category_code", Expression: directScalarProjection("root", "payload", "valueQuantity", "category_code")},
				{Name: "measurement", Expression: directScalarProjection("root", "payload", "valueQuantity", "measurement")},
				{
					Name: "category_present", PresenceOutput: true,
					Presence: &ir.PhysicalProjectionPresence{
						Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						Paths:  [][]string{{"valueQuantity", "category_code"}},
					},
				},
			},
			GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group_key", Variable: "group_value", Kind: "STRING"}},
			CategoryColumn: "category_code", CategoryPresenceColumn: "category_present",
			CategoryPresenceFromInput: true,
			ValueColumn:               "measurement", CategoryType: "BOOLEAN", ValueType: "INTEGER",
			Categories: []ir.PhysicalGroupedPivotCategory{
				{Output: "missing_category", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
				{Output: "null_category", MatchKind: ir.PhysicalPivotCategoryNullMatch},
				{Output: "false_category", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "category_false", ValueKind: "BOOLEAN"},
			},
			DuplicatePolicy: duplicatePolicy, MissingCellPolicy: missingCellPolicy,
			UnlistedCategoryPolicy: unlistedPolicy, UnlistedEvidenceColumn: "unlisted_count",
		}
		lines, err := renderer.renderGroupedTablePivot(pivot, true, "limit")
		if err != nil {
			t.Fatalf("render %s/%s/%s Pivot: %v", duplicatePolicy, missingCellPolicy, unlistedPolicy, err)
		}
		return strings.Join(lines, "\n"), renderer
	}

	query, renderer := render("SUM", "ERROR", "EXCLUDE_WITH_EVIDENCE")

	for _, fragment := range []string{
		"LET __loom_physical_reshape_stream_source_parent = FIRST([root.payload.valueQuantity])",
		"LET __loom_physical_reshape_stream_input_0 = __loom_physical_reshape_stream_source_parent.group_key",
		"LET __loom_physical_reshape_stream_input_1 = __loom_physical_reshape_stream_source_parent.category_code",
		"LET __loom_physical_reshape_stream_input_2 = __loom_physical_reshape_stream_source_parent.measurement",
		"IS_OBJECT(__loom_physical_reshape_stream_source_parent)",
		"HAS(__loom_physical_reshape_stream_source_parent, @__loom_physical_reshape_presence_path)",
		"COLLECT group_value = __loom_physical_reshape_stream_input_0 AGGREGATE",
		"NOT (__loom_physical_reshape_stream_input_3 == true)",
		"(__loom_physical_reshape_stream_input_3 == true AND __loom_physical_reshape_stream_input_1 == null)",
		"TYPENAME(__loom_physical_reshape_stream_input_1)",
		"__loom_physical_reshape_stream_input_1 == @category_false",
		"__loom_physical_reshape_stream_input_2 != null",
		"TYPENAME(__loom_physical_reshape_stream_input_2)",
		"TABLE_PIVOT_VALUE_TYPE_MISMATCH",
		"TABLE_PIVOT_CELL_MISSING",
		"__loom_physical_reshape_stream_category_value_count_0 == 0 ? null : __loom_physical_reshape_stream_category_reduced_0",
		"SORT __loom_physical_reshape_stream_identity ASC",
		"LIMIT @limit",
		"__loom_physical_reshape_stream_unlisted_count",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("direct scalar Pivot query is missing %q:\n%s", fragment, query)
		}
	}
	if strings.Contains(query, "LET input = {") || strings.Contains(query, "input[@") {
		t.Fatalf("direct scalar Pivot retained its per-row input object or bound reads:\n%s", query)
	}
	if count := strings.Count(query, "FIRST([root.payload.valueQuantity])"); count != 1 {
		t.Errorf("direct scalar Pivot rendered the shared stored parent %d times, want 1:\n%s", count, query)
	}
	for _, key := range []string{"reshape_category_column", "reshape_value_column", "reshape_category_presence_column", "reshape_group_column_0"} {
		for bindKey := range renderer.bindVars {
			if strings.Contains(bindKey, key) {
				t.Errorf("direct scalar Pivot retained unnecessary column bind %q", bindKey)
			}
		}
	}
	if category, ok := renderer.bindVars["category_false"].(bool); !ok || category {
		t.Errorf("falsey category bind = %#v, want bool(false)", renderer.bindVars["category_false"])
	}
	for _, outputName := range []string{"unlisted_count", "__loom_row_id"} {
		found := false
		for _, value := range renderer.bindVars {
			if value == outputName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("direct scalar Pivot output projection %q is missing", outputName)
		}
	}

	for _, duplicatePolicy := range []string{"ERROR", "SUM", "MIN", "MAX"} {
		t.Run(duplicatePolicy, func(t *testing.T) {
			policyQuery, _ := render(duplicatePolicy, "NULL", "ERROR")
			reducer := duplicatePolicy
			if reducer == "ERROR" {
				reducer = "MAX"
				if !strings.Contains(policyQuery, "TABLE_PIVOT_CELL_CARDINALITY") {
					t.Errorf("ERROR policy omitted null-inclusive cell cardinality assertion:\n%s", policyQuery)
				}
			}
			if !strings.Contains(policyQuery, fmt.Sprintf("= %s(__loom_physical_reshape_stream_category_value_0)", reducer)) {
				t.Errorf("%s policy did not reduce direct scalar values with %s:\n%s", duplicatePolicy, reducer, policyQuery)
			}
			if !strings.Contains(policyQuery, "TABLE_PIVOT_UNLISTED_CATEGORY") {
				t.Errorf("ERROR unlisted policy omitted its assertion:\n%s", policyQuery)
			}
		})
	}
}

func TestStreamingMissingCategoryPivotFallsBackWithoutDirectScalarProof(t *testing.T) {
	filteredCategory := &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Selector: spec.Selector{
				Steps:  []spec.SelectorStep{{Field: "category_code"}},
				Filter: &spec.ContainsFilter{Field: "usage", Needle: "pivot"},
			},
			ExecutionMode: ir.PhysicalSelectorGeneric,
		},
	}
	tests := []struct {
		name               string
		rootVariable       string
		groupProjection    ir.PhysicalProjection
		categoryProjection ir.PhysicalProjection
		wantFragment       string
	}{
		{
			name: "missing source-root proof", rootVariable: "",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "group_key"}}},
			wantFragment:    "root.payload.group_key",
		},
		{
			name: "derived group projection", rootVariable: "root",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "derived_group"}},
			wantFragment:    "derived_group",
		},
		{
			name: "related group projection", rootVariable: "root",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "related_group"}},
			wantFragment:    "related_group",
		},
		{
			name: "filtered selector projection", rootVariable: "root",
			groupProjection:    ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "group_key"}}},
			categoryProjection: ir.PhysicalProjection{Name: "category_code", Expression: filteredCategory},
			wantFragment:       "FILTER CONTAINS(__root.usage ? __root.usage : \"\", @__loom_physical_selector_contains)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			categoryProjection := test.categoryProjection
			if categoryProjection.Name == "" {
				categoryProjection = ir.PhysicalProjection{Name: "category_code", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "category_code"}}}
			}
			renderer := physicalPlanRenderer{renderContext: renderContext{
				bindVars:       map[string]any{"construction": "pivot", "limit": 10},
				collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
				rootVariable: test.rootVariable, renderPreview: renderPreview{StreamingMissingCategoryPivotPreview{}},
			}}
			pivot := ir.PhysicalGroupedPivot{
				ConstructionID: "pivot", ConstructionIDBindKey: "construction",
				InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
				InputProjections: []ir.PhysicalProjection{
					test.groupProjection,
					categoryProjection,
					{Name: "measurement", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "measurement"}}},
					{
						Name: "category_present", PresenceOutput: true,
						Presence: &ir.PhysicalProjectionPresence{
							Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
							Paths:  [][]string{{"category_code"}},
						},
					},
				},
				GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group_key", Variable: "group_value", Kind: "STRING"}},
				CategoryColumn: "category_code", CategoryPresenceColumn: "category_present",
				CategoryPresenceFromInput: true,
				ValueColumn:               "measurement", CategoryType: "STRING", ValueType: "INTEGER",
				Categories:      []ir.PhysicalGroupedPivotCategory{{Output: "missing_category", MatchKind: ir.PhysicalPivotCategoryMissingMatch}},
				DuplicatePolicy: "SUM", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
			}
			scalarLines, scalarInputs, err := renderer.renderStreamingPivotDirectScalarInputs(pivot)
			if err != nil {
				t.Fatalf("check direct scalar eligibility: %v", err)
			}
			if len(scalarLines) != 0 || scalarInputs != nil {
				t.Fatalf("ineligible projections selected the direct scalar path: lines=%v inputs=%v", scalarLines, scalarInputs)
			}
			lines, err := renderer.renderGroupedTablePivot(pivot, true, "limit")
			if err != nil {
				t.Fatalf("render generic streaming fallback: %v", err)
			}
			query := strings.Join(lines, "\n")
			if !strings.Contains(query, "LET input = {") || !strings.Contains(query, "input[@") {
				t.Fatalf("ineligible projections did not retain generic input-object rendering:\n%s", query)
			}
			if !strings.Contains(query, test.wantFragment) {
				t.Fatalf("generic input rendering lost %q:\n%s", test.wantFragment, query)
			}
		})
	}
}

func directScalarProjection(rootVariable, sourceField string, selectorFields ...string) *ir.PhysicalExpression {
	steps := make([]spec.SelectorStep, 0, len(selectorFields))
	for _, field := range selectorFields {
		steps = append(steps, spec.SelectorStep{Field: field})
	}
	return &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: rootVariable, Path: []string{sourceField}},
			Selector:      spec.Selector{Steps: steps},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func TestRebaseStreamingPivotExtractToStoredParentRequiresSourcePrefix(t *testing.T) {
	parentPath := []string{"payload", "valueQuantity"}
	valid := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
	if !rebaseStreamingPivotExtractToStoredParent(valid, "root", parentPath, "source_parent") {
		t.Fatal("valid root.payload + valueQuantity.code selector did not rebase to the stored parent")
	}
	if valid.Source.Variable != "source_parent" || len(valid.Source.Path) != 0 ||
		!reflect.DeepEqual(valid.Selector.Steps, []spec.SelectorStep{{Field: "code"}}) {
		t.Fatalf("valid shared-parent selector rebased incorrectly: %+v", valid)
	}

	invalid := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"other"}},
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
	before := *invalid
	before.Source.Path = append([]string(nil), invalid.Source.Path...)
	before.Selector.Steps = append([]spec.SelectorStep(nil), invalid.Selector.Steps...)
	if rebaseStreamingPivotExtractToStoredParent(invalid, "root", parentPath, "source_parent") {
		t.Fatal("mismatched root.other + valueQuantity.code selector was rebased to the stored parent")
	}
	if !reflect.DeepEqual(invalid, &before) {
		t.Fatalf("failed source-prefix proof mutated the selector: got %+v, want unchanged %+v", invalid, before)
	}
}

func TestTwoScanPivotPreviewBindsNonIdentifierGroupColumns(t *testing.T) {
	const groupColumn = "group.key value"
	pivot := ir.PhysicalGroupedPivot{
		ConstructionIDBindKey: "construction_id",
		GroupKeys: []ir.PhysicalGroupedPivotKey{
			{Column: groupColumn, Kind: "STRING"},
			{Column: "another key", Kind: "BOOLEAN"},
		},
	}

	selectionBinds := map[string]any{"construction_id": "pivot-1"}
	selection, err := renderTerminalPivotGroupIdentitySelection(
		"RETURN { value: 1 }\n", pivot, "selected_groups", "key_row", []string{"group_value", "second_value"}, "group_identity",
		selectionBinds, map[string]struct{}{}, "limit",
	)
	if err != nil {
		t.Fatal(err)
	}
	selectionQuery := strings.Join(selection, "\n")
	if !strings.Contains(selectionQuery, "key_row[@__loom_physical_construction_reshape_preview_group_column]") ||
		selectionBinds["__loom_physical_construction_reshape_preview_group_column"] != groupColumn {
		t.Fatalf("first-pass group selection did not bind its arbitrary group column: %s, binds=%#v", selectionQuery, selectionBinds)
	}
	if !strings.Contains(selectionQuery, "RETURN [group_value, second_value]") || !strings.Contains(selectionQuery, "SORT group_identity ASC") {
		t.Fatalf("first pass must sort by canonical identity and return the complete raw multi-key tuple:\n%s", selectionQuery)
	}
}

func TestTwoScanPivotPreviewFiltersDirectSourceGroupTuples(t *testing.T) {
	renderer := physicalPlanRenderer{renderContext: renderContext{bindVars: map[string]any{}, internalPrefix: "preview_"}}
	filter := &pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths: [][]string{
			{"payload", "subject", "reference"},
			{"payload", "active"},
		},
	}
	query, err := renderer.renderPivotGroupTupleFilter(filter, "root")
	if err != nil {
		t.Fatal(err)
	}
	if query != "FILTER POSITION(selected_groups, [root.payload.subject.reference, root.payload.active])" || len(renderer.bindVars) != 0 {
		t.Fatalf("source group tuple filter did not render the complete direct tuple: %s, binds=%#v", query, renderer.bindVars)
	}
}

func TestTwoScanPivotPreviewUsesScalarMembershipForOneGroupKey(t *testing.T) {
	renderer := physicalPlanRenderer{renderContext: renderContext{bindVars: map[string]any{}}}
	query, err := renderer.renderPivotGroupTupleFilter(&pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths:               [][]string{{"payload", "subject", "reference"}},
	}, "root")
	if err != nil {
		t.Fatal(err)
	}
	if query != "FILTER root.payload.subject.reference IN selected_groups" {
		t.Fatalf("single-key filter = %q, want direct scalar membership", query)
	}
	if _, err := renderer.renderPivotGroupTupleFilter(&pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths:               [][]string{{"payload", "bad path"}},
	}, "root"); err == nil {
		t.Fatal("unsafe direct source path was accepted")
	}
}
