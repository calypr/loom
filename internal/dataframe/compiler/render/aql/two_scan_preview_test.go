package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

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
	renderer := physicalPlanRenderer{bindVars: map[string]any{}, internalPrefix: "preview_"}
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
	renderer := physicalPlanRenderer{bindVars: map[string]any{}}
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

func TestMergeRenderedBindVarsRejectsConflictingSourceScanBindings(t *testing.T) {
	merged := map[string]any{"__loom_physical_projection_0_name": "_key"}
	err := mergeRenderedBindVars(merged, map[string]any{"__loom_physical_projection_0_name": "resource_id"})
	if err == nil {
		t.Fatal("conflicting source-scan projection bindings were merged")
	}
}
