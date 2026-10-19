package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionRowValueLets(
	rowValues []ir.PhysicalStageRowValue,
	contributors, valueField string,
) ([]string, error) {
	lines := make([]string, 0, len(rowValues)*3)
	for index, rowValue := range rowValues {
		columnBind := r.newInternalBindKeyWithValue("construction_row_value_column", rowValue.InputColumn)
		typeName, err := aqlTableScalarType(rowValue.InputKind)
		if rowValue.InputKind == "OBJECT" {
			typeName, err = "object", nil
		}
		if err != nil {
			return nil, fmt.Errorf("row value %q input type: %w", rowValue.Output, err)
		}
		typeBind := r.newInternalBindKeyWithValue("construction_row_value_type", typeName)
		member := r.newInternalVariable(fmt.Sprintf("construction_row_value_member_%d", index))
		source := member
		if valueField != "" {
			source += "." + valueField
		}
		source += "[@" + columnBind + "]"
		values := r.newInternalVariable(fmt.Sprintf("construction_row_value_values_%d", index))
		if rowValue.InputMany {
			lines = append(lines, fmt.Sprintf(
				"  LET %s = FLATTEN((FOR %s IN %s LET row_value = %s FILTER row_value != null RETURN ASSERT(IS_ARRAY(row_value), \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") ? row_value : []), 1)",
				values, member, contributors, source,
			))
		} else {
			lines = append(lines, fmt.Sprintf(
				"  LET %s = (FOR %s IN %s LET row_value = %s FILTER row_value != null RETURN row_value)",
				values, member, contributors, source,
			))
		}
		item := r.newInternalVariable(fmt.Sprintf("construction_row_value_item_%d", index))
		unique := r.newInternalVariable(fmt.Sprintf("construction_row_value_unique_%d", index))
		lines = append(lines, fmt.Sprintf(
			"  LET %s = SORTED_UNIQUE((FOR %s IN %s FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") RETURN %s))",
			unique, item, values, item, item, typeBind, item,
		))
		if rowValue.Policy == "ALL" {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", rowValue.Variable, unique))
			continue
		}
		if rowValue.Policy != "ONE" {
			return nil, fmt.Errorf("row value %q has unsupported policy %q", rowValue.Output, rowValue.Policy)
		}
		lines = append(lines, fmt.Sprintf(
			"  LET %s = (ASSERT(LENGTH(%s) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\") ? (LENGTH(%s) == 0 ? null : FIRST(%s)) : null)",
			rowValue.Variable, unique, unique, unique,
		))
	}
	return lines, nil
}

func (r *physicalPlanRenderer) renderConstructionGroupRowValueAggregate(
	rowValue ir.PhysicalStageRowValue,
	inputRow string,
	index int,
) (string, string, string, error) {
	columnBind := r.newInternalBindKeyWithValue("construction_row_value_column", rowValue.InputColumn)
	typeName, err := aqlTableScalarType(rowValue.InputKind)
	if rowValue.InputKind == "OBJECT" {
		typeName, err = "object", nil
	}
	if err != nil {
		return "", "", "", fmt.Errorf("input type: %w", err)
	}
	typeBind := r.newInternalBindKeyWithValue("construction_row_value_type", typeName)
	value := fmt.Sprintf("%s[@%s]", inputRow, columnBind)
	values := "[]"
	if rowValue.InputMany {
		values = fmt.Sprintf("(%s == null ? [] : (ASSERT(IS_ARRAY(%s), \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") ? %s : []))", value, value, value)
	} else {
		values = fmt.Sprintf("(%s == null ? [] : [%s])", value, value)
	}
	aggregateVariable := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_arrays_%d", index))
	item := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_item_%d", index))
	unique := r.newInternalVariable(fmt.Sprintf("construction_group_row_value_unique_%d", index))
	uniqueLine := fmt.Sprintf(
		"  LET %s = SORTED_UNIQUE((FOR %s IN FLATTEN(%s, 1) FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") RETURN %s))",
		unique, item, aggregateVariable, item, item, typeBind, item,
	)
	var outputLine string
	switch rowValue.Policy {
	case "ALL":
		outputLine = fmt.Sprintf("  LET %s = %s", rowValue.Variable, unique)
	case "ONE":
		outputLine = fmt.Sprintf(
			"  LET %s = (ASSERT(LENGTH(%s) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\") ? (LENGTH(%s) == 0 ? null : FIRST(%s)) : null)",
			rowValue.Variable, unique, unique, unique,
		)
	default:
		return "", "", "", fmt.Errorf("unsupported policy %q", rowValue.Policy)
	}
	return aggregateVariable, values, uniqueLine + "\n" + outputLine, nil
}
