package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderTableReshape(operation ir.PhysicalOperation) ([]string, error) {
	switch operation.Kind {
	case ir.PhysicalGroupedPivotOp:
		if operation.GroupedPivot == nil {
			return nil, fmt.Errorf("grouped pivot is missing payload")
		}
		return r.renderGroupedTablePivot(*operation.GroupedPivot)
	case ir.PhysicalUnpivotOp:
		if operation.Unpivot == nil {
			return nil, fmt.Errorf("unpivot is missing payload")
		}
		return r.renderTableUnpivot(*operation.Unpivot)
	default:
		return nil, fmt.Errorf("unsupported table reshape operation %q", operation.Kind)
	}
}

func (r *physicalPlanRenderer) renderGroupedTablePivot(pivot ir.PhysicalGroupedPivot) ([]string, error) {
	input, err := r.renderReturn(ir.PhysicalReturn{Projections: pivot.InputProjections})
	if err != nil {
		return nil, err
	}
	lines := []string{fmt.Sprintf("  LET %s = %s", pivot.InputRowVariable, input)}
	collect := make([]string, 0, len(pivot.GroupKeys))
	sort := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		collect = append(collect, fmt.Sprintf("%s = %s.%s", key.Variable, pivot.InputRowVariable, key.Column))
		sort = append(sort, key.Variable+" ASC")
	}
	collect = append(collect, fmt.Sprintf("INTO %s = %s", pivot.GroupRowsVariable, pivot.InputRowVariable))
	lines = append(lines,
		"  COLLECT "+strings.Join(collect, ", "),
		"  SORT "+strings.Join(sort, ", "),
	)

	categoryType, err := aqlTableScalarType(pivot.CategoryType)
	if err != nil {
		return nil, err
	}
	categoryTypeBind := r.newInternalBindKey("reshape_category_type")
	r.bindVars[categoryTypeBind] = categoryType
	valueType, err := aqlTableScalarType(pivot.ValueType)
	if err != nil {
		return nil, err
	}
	valueTypeBind := r.newInternalBindKey("reshape_value_type")
	r.bindVars[valueTypeBind] = valueType
	outputProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+len(pivot.Categories)+2)
	for _, key := range pivot.GroupKeys {
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: key.Column, Value: ir.PhysicalValue{Variable: key.Variable}})
	}
	for index, category := range pivot.Categories {
		cellVariable := r.newInternalVariable(fmt.Sprintf("reshape_cell_%d", index))
		valueVariable := r.newInternalVariable(fmt.Sprintf("reshape_cell_value_%d", index))
		valuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_values_%d", index))
		typedValuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_typed_values_%d", index))
		match := fmt.Sprintf("TYPENAME(%s.%s) == @%s AND %s.%s == @%s", cellVariable, pivot.CategoryColumn, categoryTypeBind, cellVariable, pivot.CategoryColumn, category.KeyBindKey)
		lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s RETURN %s.%s)", valuesVariable, cellVariable, pivot.GroupRowsVariable, match, cellVariable, pivot.ValueColumn))
		lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\") RETURN %s)",
			typedValuesVariable, valueVariable, valuesVariable, valueVariable, valueVariable, valueTypeBind, valueVariable))
		cellValue := "null"
		switch pivot.DuplicatePolicy {
		case "ERROR":
			cardinality := fmt.Sprintf("ASSERT(LENGTH(%s) <= 1, \"TABLE_PIVOT_CELL_CARDINALITY\") ? FIRST(%s) : null", valuesVariable, valuesVariable)
			if pivot.MissingCellPolicy == "ERROR" {
				cellValue = fmt.Sprintf("(ASSERT(LENGTH(%s) > 0, \"TABLE_PIVOT_CELL_MISSING\") ? (%s) : null)", valuesVariable, cardinality)
			} else {
				cellValue = "(" + cardinality + ")"
			}
		case "SUM", "MIN", "MAX":
			condition := "true"
			if pivot.MissingCellPolicy == "ERROR" {
				condition = fmt.Sprintf("LENGTH(%s) > 0", valuesVariable)
			}
			reducer := pivot.DuplicatePolicy + "(" + typedValuesVariable + ")"
			cellValue = fmt.Sprintf("(ASSERT(%s, \"TABLE_PIVOT_CELL_MISSING\") ? (LENGTH(%s) == 0 ? null : %s) : null)", condition, typedValuesVariable, reducer)
		default:
			return nil, fmt.Errorf("unsupported grouped pivot duplicate policy %q", pivot.DuplicatePolicy)
		}
		cellValueVariable := r.newInternalVariable(fmt.Sprintf("reshape_cell_result_%d", index))
		lines = append(lines, fmt.Sprintf("  LET %s = %s", cellValueVariable, cellValue))
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: category.Output, Value: ir.PhysicalValue{Variable: cellValueVariable}})
	}

	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		listed := groupedPivotListedPredicate(pivot, itemVariable, categoryTypeBind)
		lines = append(lines, fmt.Sprintf("  LET %s = LENGTH((FOR %s IN %s FILTER NOT %s RETURN 1))",
			unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed))
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: pivot.UnlistedEvidenceColumn, Value: ir.PhysicalValue{Variable: unlistedVariable}})
	} else if pivot.UnlistedCategoryPolicy == "ERROR" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		listed := groupedPivotListedPredicate(pivot, itemVariable, categoryTypeBind)
		lines = append(lines,
			fmt.Sprintf("  LET %s = LENGTH((FOR %s IN %s FILTER NOT %s RETURN 1))", unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed),
			fmt.Sprintf("  FILTER ASSERT(%s == 0, \"TABLE_PIVOT_UNLISTED_CATEGORY\")", unlistedVariable),
		)
	}

	constructionBind := pivot.ConstructionIDBindKey
	if _, ok := r.bindVars[constructionBind]; !ok {
		return nil, fmt.Errorf("grouped pivot construction ID bind %q is missing", pivot.ConstructionIDBindKey)
	}
	identityParts := make([]string, 0, 2+len(pivot.GroupKeys))
	identityParts = append(identityParts, `"GROUPED_PIVOT"`, "@"+constructionBind)
	for _, key := range pivot.GroupKeys {
		identityParts = append(identityParts, "[\""+key.Kind+"\", "+key.Variable+"]")
	}
	identityVariable := r.newInternalVariable("reshape_identity")
	lines = append(lines, fmt.Sprintf("  LET %s = TO_STRING([%s])", identityVariable, strings.Join(identityParts, ", ")))
	outputProjections = append(outputProjections, ir.PhysicalProjection{Name: "__loom_row_id", Value: ir.PhysicalValue{Variable: identityVariable}})
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = %s", pivot.OutputRowVariable, output))
	return lines, nil
}

func groupedPivotListedPredicate(pivot ir.PhysicalGroupedPivot, itemVariable, categoryTypeBind string) string {
	matches := make([]string, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		matches = append(matches, fmt.Sprintf("(TYPENAME(%s.%s) == @%s AND %s.%s == @%s)",
			itemVariable, pivot.CategoryColumn, categoryTypeBind, itemVariable, pivot.CategoryColumn, category.KeyBindKey))
	}
	return "(" + strings.Join(matches, " OR ") + ")"
}

func (r *physicalPlanRenderer) renderTableUnpivot(unpivot ir.PhysicalUnpivot) ([]string, error) {
	input, err := r.renderReturn(ir.PhysicalReturn{Projections: unpivot.InputProjections})
	if err != nil {
		return nil, err
	}
	lines := []string{fmt.Sprintf("  LET %s = %s", unpivot.InputRowVariable, input)}
	slots := make([]string, 0, len(unpivot.Inputs))
	for _, selected := range unpivot.Inputs {
		slots = append(slots, fmt.Sprintf("{key: @%s, value: %s.%s}", selected.KeyBindKey, unpivot.InputRowVariable, selected.Column))
	}
	lines = append(lines, fmt.Sprintf("  FOR %s IN [%s]", unpivot.SlotVariable, strings.Join(slots, ", ")))
	valueType, err := aqlTableScalarType(unpivot.ValueType)
	if err != nil {
		return nil, err
	}
	valueTypeBind := r.newInternalBindKey("reshape_unpivot_value_type")
	r.bindVars[valueTypeBind] = valueType
	lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s.value == null OR TYPENAME(%s.value) == @%s, \"TABLE_UNPIVOT_VALUE_TYPE_MISMATCH\")", unpivot.SlotVariable, unpivot.SlotVariable, valueTypeBind))
	if unpivot.NullRowPolicy == "DROP" {
		lines = append(lines, fmt.Sprintf("  FILTER %s.value != null", unpivot.SlotVariable))
	}

	constructionBind := unpivot.ConstructionIDBindKey
	if _, ok := r.bindVars[constructionBind]; !ok {
		return nil, fmt.Errorf("unpivot construction ID bind %q is missing", unpivot.ConstructionIDBindKey)
	}
	identityParts := make([]string, 0, len(unpivot.IdentityParts))
	for index, part := range unpivot.IdentityParts {
		value, err := r.renderValue(part.Value)
		if err != nil {
			return nil, fmt.Errorf("identity part %q: %w", part.Name, err)
		}
		nameBind := r.newInternalBindKey(fmt.Sprintf("reshape_identity_name_%d", index))
		r.bindVars[nameBind] = part.Name
		identityParts = append(identityParts, "[@"+nameBind+", "+value+"]")
	}
	identityParts = append(identityParts, "[@"+constructionBind+", \"TABLE_UNPIVOT\"]", "[\""+unpivot.Inputs[0].KeyKind+"\", "+unpivot.SlotVariable+".key]")
	identityVariable := r.newInternalVariable("reshape_unpivot_identity")
	lines = append(lines, fmt.Sprintf("  LET %s = TO_STRING([%s])", identityVariable, strings.Join(identityParts, ", ")))

	selected := make(map[string]bool, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		selected[input.Column] = true
	}
	outputProjections := make([]ir.PhysicalProjection, 0, len(unpivot.InputProjections)+3)
	for _, projection := range unpivot.InputProjections {
		if selected[projection.Name] || projection.Name == "__loom_row_id" {
			continue
		}
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: projection.Name, Hidden: projection.Hidden, Value: ir.PhysicalValue{Variable: unpivot.InputRowVariable, Path: []string{projection.Name}}})
	}
	outputProjections = append(outputProjections,
		ir.PhysicalProjection{Name: unpivot.KeyOutput, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"key"}}},
		ir.PhysicalProjection{Name: unpivot.ValueOutput, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"value"}}},
		ir.PhysicalProjection{Name: "__loom_row_id", Hidden: true, Value: ir.PhysicalValue{Variable: identityVariable}},
	)
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = %s", unpivot.OutputRowVariable, output))
	return lines, nil
}

func aqlTableScalarType(kind string) (string, error) {
	switch kind {
	case "STRING":
		return "string", nil
	case "INTEGER", "DECIMAL":
		return "number", nil
	case "BOOLEAN":
		return "bool", nil
	default:
		return "", fmt.Errorf("unsupported table scalar kind %q", kind)
	}
}
