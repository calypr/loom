package ir

import "testing"

func TestStreamingPivotInputProjectsSourceRows(t *testing.T) {
	pivot := PhysicalGroupedPivot{
		InputProjections: []PhysicalProjection{
			{Name: "category", Value: PhysicalValue{Variable: "row", Path: []string{"category"}}},
			{Name: "value", Value: PhysicalValue{Variable: "row", Path: []string{"value"}}},
			{Name: "category_present", Value: PhysicalValue{Variable: "row", Path: []string{"category_present"}}},
			{Name: "group", Value: PhysicalValue{Variable: "row", Path: []string{"group"}}},
		},
		CategoryColumn:         "category",
		ValueColumn:            "value",
		CategoryPresenceColumn: "category_present",
		GroupKeys:              []PhysicalGroupedPivotKey{{Column: "group"}},
	}
	sourceNames := map[string]struct{}{"category": {}, "value": {}, "category_present": {}, "group": {}}
	if !StreamingPivotInputProjectsSourceRows(pivot, "row", sourceNames) {
		t.Fatal("valid direct projections did not match source rows")
	}

	duplicate := pivot
	duplicate.InputProjections = append(append([]PhysicalProjection(nil), pivot.InputProjections...), pivot.InputProjections[0])
	if StreamingPivotInputProjectsSourceRows(duplicate, "row", sourceNames) {
		t.Fatal("duplicate projection name matched source rows")
	}

	wrongVariable := pivot
	wrongVariable.InputProjections = append([]PhysicalProjection(nil), pivot.InputProjections...)
	wrongVariable.InputProjections[0].Value.Variable = "other"
	if StreamingPivotInputProjectsSourceRows(wrongVariable, "row", sourceNames) {
		t.Fatal("projection from a different input variable matched source rows")
	}
}
