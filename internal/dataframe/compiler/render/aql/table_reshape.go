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
	categoryColumnBind := r.newInternalBindKey("reshape_category_column")
	r.bindVars[categoryColumnBind] = pivot.CategoryColumn
	valueColumnBind := r.newInternalBindKey("reshape_value_column")
	r.bindVars[valueColumnBind] = pivot.ValueColumn
	categoryPresenceBind := ""
	if pivot.CategoryPresence != nil {
		presence, err := r.renderGroupedPivotPresence(*pivot.CategoryPresence)
		if err != nil {
			return nil, fmt.Errorf("category presence: %w", err)
		}
		presenceNameBind := r.newInternalBindKey("reshape_category_presence_column")
		r.bindVars[presenceNameBind] = pivot.CategoryPresenceColumn
		categoryPresenceBind = presenceNameBind
		input = fmt.Sprintf("MERGE(%s, {[@%s]: %s})", input, presenceNameBind, presence)
	}
	lines := []string{fmt.Sprintf("  LET %s = %s", pivot.InputRowVariable, input)}
	collect := make([]string, 0, len(pivot.GroupKeys))
	sort := make([]string, 0, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("reshape_group_column_%d", index))
		r.bindVars[columnBind] = key.Column
		collect = append(collect, fmt.Sprintf("%s = %s[@%s]", key.Variable, pivot.InputRowVariable, columnBind))
		sort = append(sort, key.Variable+" ASC")
	}
	collectClause := fmt.Sprintf(
		"  COLLECT %s INTO %s = %s", strings.Join(collect, ", "), pivot.GroupRowsVariable, pivot.InputRowVariable,
	)
	lines = append(lines,
		collectClause,
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
		match, err := groupedPivotCategoryMatchPredicate(category, cellVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category.Output, err)
		}
		lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s RETURN %s[@%s])", valuesVariable, cellVariable, pivot.GroupRowsVariable, match, cellVariable, valueColumnBind))
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
		listed, err := groupedPivotListedPredicate(pivot, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("  LET %s = LENGTH((FOR %s IN %s FILTER NOT %s RETURN 1))",
			unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed))
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: pivot.UnlistedEvidenceColumn, Value: ir.PhysicalValue{Variable: unlistedVariable}})
	} else if pivot.UnlistedCategoryPolicy == "ERROR" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		listed, err := groupedPivotListedPredicate(pivot, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, err
		}
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

func groupedPivotListedPredicate(pivot ir.PhysicalGroupedPivot, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind string) (string, error) {
	matches := make([]string, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		match, err := groupedPivotCategoryMatchPredicate(category, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return "", fmt.Errorf("category %q: %w", category.Output, err)
		}
		matches = append(matches, match)
	}
	return "(" + strings.Join(matches, " OR ") + ")", nil
}

func groupedPivotCategoryMatchPredicate(category ir.PhysicalGroupedPivotCategory, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind string) (string, error) {
	column := fmt.Sprintf("%s[@%s]", itemVariable, categoryColumnBind)
	present := fmt.Sprintf("HAS(%s, @%s)", itemVariable, categoryColumnBind)
	if categoryPresenceBind != "" {
		present = fmt.Sprintf("%s[@%s] == true", itemVariable, categoryPresenceBind)
	}
	switch category.MatchKind {
	case ir.PhysicalPivotCategoryValueMatch:
		if category.ValueBindKey == "" || categoryTypeBind == "" {
			return "", fmt.Errorf("ordinary category requires value and type binds")
		}
		return fmt.Sprintf("(%s AND TYPENAME(%s) == @%s AND %s == @%s)", present, column, categoryTypeBind, column, category.ValueBindKey), nil
	case ir.PhysicalPivotCategoryNullMatch:
		return fmt.Sprintf("(%s AND %s == null)", present, column), nil
	case ir.PhysicalPivotCategoryMissingMatch:
		if categoryPresenceBind == "" {
			return "", fmt.Errorf("MISSING category requires a preserved category presence contract")
		}
		return "NOT (" + present + ")", nil
	default:
		return "", fmt.Errorf("unsupported category match kind %q", category.MatchKind)
	}
}

func (r *physicalPlanRenderer) renderGroupedPivotPresence(presence ir.PhysicalProjectionPresence) (string, error) {
	source, err := r.renderValue(presence.Source)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(presence.Paths))
	for _, path := range presence.Paths {
		pathExpression, err := r.renderPropertyPathPresence(source, path)
		if err != nil {
			return "", err
		}
		paths = append(paths, pathExpression)
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	return "(" + strings.Join(paths, " OR ") + ")", nil
}

func (r *physicalPlanRenderer) renderPropertyPathPresence(source string, path []string) (string, error) {
	if len(path) == 0 {
		return "", fmt.Errorf("property-presence path is empty")
	}
	key := r.newInternalBindKey("reshape_presence_path")
	r.bindVars[key] = path[0]
	object := "IS_OBJECT(" + source + ")"
	has := fmt.Sprintf("HAS(%s, @%s)", source, key)
	present := "(" + object + " ? " + has + " : false)"
	if len(path) == 1 {
		return present, nil
	}
	remaining, err := r.renderPropertyPathPresence(source+"[@"+key+"]", path[1:])
	if err != nil {
		return "", err
	}
	return "(" + object + " ? (" + has + " ? " + remaining + " : false) : false)", nil
}

func (r *physicalPlanRenderer) renderTableUnpivot(unpivot ir.PhysicalUnpivot) ([]string, error) {
	input, err := r.renderReturn(ir.PhysicalReturn{Projections: unpivot.InputProjections})
	if err != nil {
		return nil, err
	}
	lines := []string{fmt.Sprintf("  LET %s = %s", unpivot.InputRowVariable, input)}
	slots := make([]string, 0, len(unpivot.Inputs))
	for index, selected := range unpivot.Inputs {
		columnBind := r.newInternalBindKey(fmt.Sprintf("reshape_unpivot_column_%d", index))
		r.bindVars[columnBind] = selected.Column
		slots = append(slots, fmt.Sprintf("{key: @%s, value: %s[@%s]}", selected.KeyBindKey, unpivot.InputRowVariable, columnBind))
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
		columnBind := r.newInternalBindKey("reshape_unpivot_preserved_column")
		r.bindVars[columnBind] = projection.Name
		lookup := ir.PhysicalExpression{Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: unpivot.InputRowVariable, KeyBindKey: columnBind}}
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: projection.Name, Hidden: projection.Hidden, Expression: &lookup})
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
