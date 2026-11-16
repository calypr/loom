package recipe

import (
	"strings"
	"testing"
)

func pivotConstructionForTypes(groupType, categoryType, valueType string, duplicatePolicy PivotDuplicatePolicy) Construction {
	category := "a"
	return Construction{Version: 1, SourceColumns: []StageColumn{
		{ID: "group", Name: "group", Label: "Group", Type: groupType},
		{ID: "category", Name: "category", Label: "Category", Type: categoryType},
		{ID: "value", Name: "value", Label: "Value", Type: valueType},
	}, Steps: []ConstructionStep{{
		ID:     "pivot",
		Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &ConstructionPivot{
			ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
			CategoryColumnID: "category", ValueColumnID: "value",
			Categories:      []ConstructionPivotCategory{{Key: TableScalar{Kind: TableScalarString, String: &category}, OutputColumnID: "amount_a"}},
			DuplicatePolicy: duplicatePolicy, MissingCellPolicy: PivotMissingCellNull,
			UnlistedCategoryPolicy: PivotUnlistedCategoryError,
		}},
		Outputs: []StageColumn{
			{ID: "group", Name: "group", Label: "Group", Type: groupType},
			{ID: "amount_a", Name: "amount_a", Label: "Amount A", Type: valueType},
		},
	}}}
}

func TestConstructionPivotRejectsNonScalarOperands(t *testing.T) {
	tests := []struct {
		name        string
		groupType   string
		categoryTyp string
		valueType   string
		want        string
	}{
		{name: "group key array", groupType: "array", categoryTyp: "string", valueType: "decimal", want: `pivot group key "group" type "array" is not a scalar`},
		{name: "category object", groupType: "string", categoryTyp: "object", valueType: "decimal", want: `pivot categoryColumnId column "category" type "object" is not a scalar`},
		{name: "value array", groupType: "string", categoryTyp: "string", valueType: "array", want: `pivot valueColumnId column "value" type "array" is not a scalar`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			construction := pivotConstructionForTypes(test.groupType, test.categoryTyp, test.valueType, PivotDuplicateError)
			err := construction.Validate(nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("non-scalar pivot operand error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConstructionPivotRejectsCategoryKeyKindMismatch(t *testing.T) {
	construction := pivotConstructionForTypes("string", "integer", "decimal", PivotDuplicateError)
	err := construction.Validate(nil)
	if err == nil || !strings.Contains(err.Error(), `does not match category column type "integer"`) {
		t.Fatalf("category key kind mismatch error = %v", err)
	}
}

func TestConstructionPivotSumRequiresNumericValueColumn(t *testing.T) {
	construction := pivotConstructionForTypes("string", "string", "string", PivotDuplicateSum)
	err := construction.Validate(nil)
	if err == nil || !strings.Contains(err.Error(), `pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got "string"`) {
		t.Fatalf("SUM string value error = %v", err)
	}
}

func TestConstructionPivotSumAcceptsNumericValueColumns(t *testing.T) {
	for _, logicalType := range []string{"integer", "decimal"} {
		construction := pivotConstructionForTypes("string", "string", logicalType, PivotDuplicateSum)
		if err := construction.Validate(nil); err != nil {
			t.Errorf("SUM %s value column rejected: %v", logicalType, err)
		}
	}
}

func TestConstructionPivotSkipsTypeChecksWithoutSchemaTypes(t *testing.T) {
	construction := pivotConstructionForTypes("", "", "", PivotDuplicateSum)
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("pivot with unresolved types rejected: %v", err)
	}
}
