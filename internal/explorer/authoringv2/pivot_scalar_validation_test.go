package authoringv2

import (
	"strings"
	"testing"
)

func TestConstructionPivotRejectsNonScalarOperands(t *testing.T) {
	tests := []struct {
		name        string
		columnID    string
		logicalType string
		want        string
	}{
		{name: "group key array", columnID: "group_id", logicalType: "array", want: `pivot group key "group_id" type "array" is not a scalar`},
		{name: "category array", columnID: "category_id", logicalType: "array", want: `pivot categoryColumnId column "category_id" type "array" is not a scalar`},
		{name: "value object", columnID: "value_id", logicalType: "object", want: `pivot valueColumnId column "value_id" type "object" is not a scalar`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := stagedConstructionDocument()
			for index := range document.Columns {
				if document.Columns[index].ColumnID == test.columnID {
					document.Columns[index].LogicalType = test.logicalType
				}
			}
			err := document.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("non-scalar pivot operand error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConstructionPivotRejectsCategoryKeyKindMismatch(t *testing.T) {
	document := stagedConstructionDocument()
	for index := range document.Columns {
		if document.Columns[index].ColumnID == "category_id" {
			document.Columns[index].LogicalType = "integer"
		}
	}
	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), `does not match category column type "integer"`) {
		t.Fatalf("category key kind mismatch error = %v", err)
	}
}

func TestConstructionPivotSumRequiresNumericValueColumn(t *testing.T) {
	document := stagedConstructionDocument()
	for index := range document.Columns {
		if document.Columns[index].ColumnID == "value_id" {
			document.Columns[index].LogicalType = "string"
		}
	}
	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), `pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got "string"`) {
		t.Fatalf("SUM string value error = %v", err)
	}
}

func TestConstructionPivotSumAcceptsNumericValueColumns(t *testing.T) {
	for _, logicalType := range []string{"integer", "decimal"} {
		document := stagedConstructionDocument()
		for index := range document.Columns {
			if document.Columns[index].ColumnID == "value_id" {
				document.Columns[index].LogicalType = logicalType
			}
		}
		if err := document.Validate(); err != nil {
			t.Errorf("SUM %s value column rejected: %v", logicalType, err)
		}
	}
}

func pivotTableShapeForTypes(groupType, categoryType, valueType, duplicatePolicy string) (*storedTableShape, []Column) {
	columns := []Column{
		{Column: "group", LogicalType: groupType},
		{Column: "category", LogicalType: categoryType},
		{Column: "measure", LogicalType: valueType},
	}
	shape := &storedTableShape{Reshape: &storedTableReshape{Kind: "PIVOT", Pivot: &storedPivotConstruction{
		ConstructionID: "pivot_1", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "measure",
		Categories:      []storedPivotCategory{{Key: stringScalar("a"), Output: ColumnOutput{Column: "amount_a", Label: "Amount A"}}},
		DuplicatePolicy: duplicatePolicy, MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}}}
	return shape, columns
}

func TestTableShapePivotRejectsNonScalarOperands(t *testing.T) {
	tests := []struct {
		name        string
		groupType   string
		categoryTyp string
		valueType   string
		want        string
	}{
		{name: "group key array", groupType: "array", categoryTyp: "string", valueType: "decimal", want: `pivot group key "group" type "array" is not a scalar`},
		{name: "category object", groupType: "string", categoryTyp: "object", valueType: "decimal", want: `pivot categoryColumn column "category" type "object" is not a scalar`},
		{name: "value array", groupType: "string", categoryTyp: "string", valueType: "array", want: `pivot valueColumn column "measure" type "array" is not a scalar`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shape, columns := pivotTableShapeForTypes(test.groupType, test.categoryTyp, test.valueType, "ERROR")
			err := shape.Validate(columns)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("non-scalar table shape pivot operand error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestTableShapePivotRejectsCategoryKeyKindMismatch(t *testing.T) {
	shape, columns := pivotTableShapeForTypes("string", "integer", "decimal", "ERROR")
	err := shape.Validate(columns)
	if err == nil || !strings.Contains(err.Error(), `does not match category column type "integer"`) {
		t.Fatalf("category key kind mismatch error = %v", err)
	}
}

func TestTableShapePivotSumRequiresNumericValueColumn(t *testing.T) {
	shape, columns := pivotTableShapeForTypes("string", "string", "string", "SUM")
	err := shape.Validate(columns)
	if err == nil || !strings.Contains(err.Error(), `pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got "string"`) {
		t.Fatalf("SUM string value error = %v", err)
	}
}

func TestTableShapePivotSumAcceptsNumericValueColumns(t *testing.T) {
	for _, logicalType := range []string{"integer", "decimal"} {
		shape, columns := pivotTableShapeForTypes("string", "string", logicalType, "SUM")
		if err := shape.Validate(columns); err != nil {
			t.Errorf("SUM %s value column rejected: %v", logicalType, err)
		}
	}
}
