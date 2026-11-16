package recipe

import (
	"encoding/json"
	"strings"
	"testing"
)

func pivotDiscoveryConstruction(pivot ConstructionPivot) Construction {
	return Construction{
		Version: 1,
		SourceColumns: []StageColumn{
			{ID: "group", Name: "group", Type: "string"},
			{ID: "category", Name: "category", Type: "string"},
			{ID: "value", Name: "value", Type: "integer"},
		},
		Steps: []ConstructionStep{{
			ID:        "pivot",
			Inputs:    []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &pivot},
			Outputs:   []StageColumn{{ID: "group", Name: "group", Type: "string"}, {ID: "amount_a", Name: "amount_a", Type: "integer"}},
		}},
	}
}

func explicitPivotCategory(key string) TableScalar {
	return TableScalar{Kind: "STRING", String: &key}
}

func TestConstructionPivotExplicitCategoriesRemainByteStable(t *testing.T) {
	pivot := ConstructionPivot{
		ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
		CategoryColumnID: "category", ValueColumnID: "value",
		Categories:      []ConstructionPivotCategory{{Key: explicitPivotCategory("A"), OutputColumnID: "amount_a"}},
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull, UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}
	if err := pivotDiscoveryConstruction(pivot).Validate(nil); err != nil {
		t.Fatalf("legacy explicit-category pivot rejected: %v", err)
	}
	raw, err := json.Marshal(pivot)
	if err != nil {
		t.Fatalf("marshal pivot: %v", err)
	}
	want := `{"constructionId":"pivot","groupKeyIds":["group"],"categoryColumnId":"category","valueColumnId":"value","categories":[{"key":{"kind":"STRING","string":"A"},"outputColumnId":"amount_a"}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}`
	if string(raw) != want {
		t.Fatalf("explicit-category pivot JSON changed:\n got: %s\nwant: %s", raw, want)
	}
}

func TestConstructionPivotDiscoveryCannotCompileWithFabricatedCategories(t *testing.T) {
	base := ConstructionPivot{
		ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
		CategoryColumnID: "category", ValueColumnID: "value",
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull, UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}

	withCategories := base
	withCategories.Discovery = &ConstructionPivotDiscovery{MaxCategories: 4}
	withCategories.Categories = []ConstructionPivotCategory{{Key: explicitPivotCategory("A"), OutputColumnID: "amount_a"}}
	if err := pivotDiscoveryConstruction(withCategories).Validate(nil); err == nil || !strings.Contains(err.Error(), "explicit categories with discovery") {
		t.Fatalf("fabricated categories with discovery error = %v", err)
	}

	unresolved := base
	unresolved.Discovery = &ConstructionPivotDiscovery{MaxCategories: 4}
	construction := pivotDiscoveryConstruction(unresolved)
	construction.Steps[0].Outputs = []StageColumn{{ID: "group", Name: "group", Type: "string"}}
	if err := construction.Validate(nil); err == nil || !strings.Contains(err.Error(), "must be resolved to concrete categories") {
		t.Fatalf("unresolved discovery compile error = %v", err)
	}

	invalidBound := base
	invalidBound.Discovery = &ConstructionPivotDiscovery{MaxCategories: maxPivotColumns + 1}
	if err := pivotDiscoveryConstruction(invalidBound).Validate(nil); err == nil || !strings.Contains(err.Error(), "maxCategories must be 1..") {
		t.Fatalf("invalid discovery bound error = %v", err)
	}
}
