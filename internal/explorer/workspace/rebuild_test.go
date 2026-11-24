package workspace

import (
	"strings"
	"testing"
)

func TestPivotGroupKeepsDeclaredPublicPresentationAndSourceType(t *testing.T) {
	const groupID = "pivot_status"
	step := ConstructionStep{
		Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
			GroupKeyIDs: []string{groupID},
		}},
		Outputs: []StageColumn{{ID: groupID, Name: "status", Label: "Status", Type: "declared-wrong-type"}},
	}
	input := []StageColumn{{
		ID: groupID, Name: ConstructionSourceProjectionName(groupID), Label: "Observation.status",
		Type: "string", Nullable: true,
	}}

	outputs, err := rebuildStageColumns(step, input, map[string]struct{}{groupID: {}})
	if err != nil {
		t.Fatalf("rebuild Pivot output schema: %v", err)
	}
	if len(outputs) != 1 {
		t.Fatalf("Pivot outputs = %#v, want one group key", outputs)
	}
	got := outputs[0]
	if got.ID != groupID || got.Name != "status" || got.Label != "Status" {
		t.Fatalf("Pivot group presentation = %#v, want stable ID %q with public name/label status/Status", got, groupID)
	}
	if got.Type != "string" || !got.Nullable {
		t.Fatalf("Pivot group type metadata = %#v, want source-authoritative nullable string", got)
	}
	if strings.Contains(got.Name, "__construction_source_") {
		t.Fatalf("private source alias leaked into Pivot output: %#v", got)
	}
}

func TestPivotRebuildPreservesConstructedGroupKeyPresentation(t *testing.T) {
	zero, one := 0, 1
	step := ConstructionStep{
		Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
			GroupKeyIDs: []string{"constructed_key"},
			Categories:  []ConstructionPivotCategory{{OutputColumnID: "d"}},
		}},
		Outputs: []StageColumn{
			{ID: "constructed_key", Name: "stale_name", Label: "Terminal label", Type: "boolean", Table: &TablePresentation{Order: &one}},
			{ID: "d", Name: "d", Label: "d", Type: "integer", Table: &TablePresentation{Order: &zero}},
		},
	}
	input := []StageColumn{{ID: "constructed_key", Name: "derived_key", Label: "Upstream label", Type: "string"}}
	outputs, err := rebuildStageColumns(step, input, map[string]struct{}{})
	if err != nil {
		t.Fatalf("rebuild Pivot with a constructed group key: %v", err)
	}
	groupKey, found := findStageColumnByID(outputs, "constructed_key")
	if !found || groupKey.Name != "derived_key" || groupKey.Type != "string" || groupKey.Label != "Terminal label" ||
		groupKey.Table == nil || groupKey.Table.Order == nil || *groupKey.Table.Order != 1 {
		t.Fatalf("constructed Pivot group-key identity/presentation = %#v, found=%v", groupKey, found)
	}
}
