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
		return r.renderGroupedTablePivot(*operation.GroupedPivot, true, "")
	case ir.PhysicalUnpivotOp:
		if operation.Unpivot == nil {
			return nil, fmt.Errorf("unpivot is missing payload")
		}
		return r.renderTableUnpivot(*operation.Unpivot)
	default:
		return nil, fmt.Errorf("unsupported table reshape operation %q", operation.Kind)
	}
}

func (r *physicalPlanRenderer) renderGroupedTablePivot(pivot ir.PhysicalGroupedPivot, sortGroups bool, previewLimitBindKey string) ([]string, error) {
	input, err := r.renderReturn(ir.PhysicalReturn{Projections: pivot.InputProjections})
	if err != nil {
		return nil, err
	}
	input, err = r.renderTraceSourceInput(input, pivot.InputProjections, pivot.OutputRowVariable)
	if err != nil {
		return nil, err
	}
	categoryColumnBind, valueColumnBind := "", ""
	if pivot.CodedCorrelation == nil {
		categoryColumnBind = r.newInternalBindKey("reshape_category_column")
		r.bindVars[categoryColumnBind] = pivot.CategoryColumn
		valueColumnBind = r.newInternalBindKey("reshape_value_column")
		r.bindVars[valueColumnBind] = pivot.ValueColumn
	}
	categoryPresenceBind := ""
	if pivot.CategoryPresence != nil {
		presence, err := r.renderProjectionPresence(*pivot.CategoryPresence)
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
		if pivot.OneInputRowPerGroup {
			lines = append(lines, fmt.Sprintf("  LET %s = %s[@%s]", key.Variable, pivot.InputRowVariable, columnBind))
		}
	}
	if !pivot.OneInputRowPerGroup {
		collectClause := fmt.Sprintf(
			"  COLLECT %s INTO %s = %s", strings.Join(collect, ", "), pivot.GroupRowsVariable, pivot.InputRowVariable,
		)
		lines = append(lines, collectClause)
	}
	previewIdentityVariable := ""
	if previewLimitBindKey != "" && !pivot.OneInputRowPerGroup {
		groupValues := make([]string, 0, len(pivot.GroupKeys))
		for _, key := range pivot.GroupKeys {
			groupValues = append(groupValues, key.Variable)
		}
		identity, identityErr := r.renderGroupedPivotIdentity(pivot, groupValues)
		if identityErr != nil {
			return nil, identityErr
		}
		previewIdentityVariable = r.newInternalVariable("reshape_preview_identity")
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", previewIdentityVariable, identity),
			fmt.Sprintf("  SORT %s ASC", previewIdentityVariable),
			"  LIMIT @"+previewLimitBindKey,
		)
	}
	if sortGroups && previewIdentityVariable == "" {
		lines = append(lines, "  SORT "+strings.Join(sort, ", "))
	}

	categoryTypeBind := ""
	if pivot.CodedCorrelation == nil {
		categoryType, err := aqlTableScalarType(pivot.CategoryType)
		if err != nil {
			return nil, err
		}
		categoryTypeBind = r.newInternalBindKey("reshape_category_type")
		r.bindVars[categoryTypeBind] = categoryType
	}
	valueType, err := aqlTableScalarType(pivot.ValueType)
	if err != nil {
		return nil, err
	}
	valueTypeBind := r.newInternalBindKey("reshape_value_type")
	r.bindVars[valueTypeBind] = valueType
	if r.dynamicPivotPreview && !pivot.OneInputRowPerGroup {
		return r.renderGroupedTablePivotDynamicPreview(
			pivot, lines, categoryColumnBind, valueColumnBind, categoryPresenceBind,
			categoryTypeBind, valueTypeBind, previewIdentityVariable,
		)
	}
	categoryCount := len(pivot.Categories)
	if pivot.CodedCorrelation != nil {
		categoryCount = len(pivot.CodedCategories)
	}
	outputProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+categoryCount+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: name, Hidden: key.Hidden, Value: ir.PhysicalValue{Variable: key.Variable}})
	}
	for index, category := range pivot.Categories {
		cellVariable := r.newInternalVariable(fmt.Sprintf("reshape_cell_%d", index))
		valueVariable := r.newInternalVariable(fmt.Sprintf("reshape_cell_value_%d", index))
		valuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_values_%d", index))
		typedValuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_typed_values_%d", index))
		matchVariable := ""
		matchRowVariable := cellVariable
		if pivot.OneInputRowPerGroup {
			matchVariable = r.newInternalVariable(fmt.Sprintf("reshape_category_match_%d", index))
			matchRowVariable = pivot.InputRowVariable
		}
		match, err := groupedPivotCategoryMatchPredicate(category, matchRowVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category.Output, err)
		}
		if pivot.OneInputRowPerGroup {
			value := fmt.Sprintf("%s[@%s]", pivot.InputRowVariable, valueColumnBind)
			lines = append(lines,
				fmt.Sprintf("  LET %s = %s", matchVariable, match),
				fmt.Sprintf("  LET %s = (%s ? [%s] : [])", valuesVariable, matchVariable, value),
				fmt.Sprintf("  LET %s = (%s ? (%s == null ? [] : [ASSERT(TYPENAME(%s) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\") ? %s : null]) : [])",
					typedValuesVariable, matchVariable, value, value, valueTypeBind, value),
			)
		} else {
			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s RETURN %s[@%s])", valuesVariable, cellVariable, pivot.GroupRowsVariable, match, cellVariable, valueColumnBind))
			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\") RETURN %s)",
				typedValuesVariable, valueVariable, valuesVariable, valueVariable, valueVariable, valueTypeBind, valueVariable))
		}
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
	for index, category := range pivot.CodedCategories {
		valuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_coded_values_%d", index))
		typedValuesVariable := r.newInternalVariable(fmt.Sprintf("reshape_coded_typed_values_%d", index))
		correlation := *pivot.CodedCorrelation
		correlation.SystemBindKey, correlation.CodeBindKey = category.SystemBindKey, category.CodeBindKey
		values, err := r.renderCodedPivotCategoryValues(correlation)
		if err != nil {
			return nil, fmt.Errorf("coded category %q: %w", category.Output, err)
		}
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", valuesVariable, values),
			fmt.Sprintf("  LET %s = (FOR __coded_value IN %s FILTER __coded_value != null FILTER ASSERT(TYPENAME(__coded_value) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\") RETURN __coded_value)",
				typedValuesVariable, valuesVariable, valueTypeBind),
		)
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
				condition = fmt.Sprintf("LENGTH(%s) > 0", typedValuesVariable)
			}
			reducer := pivot.DuplicatePolicy + "(" + typedValuesVariable + ")"
			cellValue = fmt.Sprintf("(ASSERT(%s, \"TABLE_PIVOT_CELL_MISSING\") ? (LENGTH(%s) == 0 ? null : %s) : null)", condition, typedValuesVariable, reducer)
		default:
			return nil, fmt.Errorf("unsupported coded Pivot duplicate policy %q", pivot.DuplicatePolicy)
		}
		cellValueVariable := r.newInternalVariable(fmt.Sprintf("reshape_coded_cell_result_%d", index))
		lines = append(lines, fmt.Sprintf("  LET %s = %s", cellValueVariable, cellValue))
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: category.Output, Value: ir.PhysicalValue{Variable: cellValueVariable}})
	}

	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		if pivot.OneInputRowPerGroup {
			itemVariable = pivot.InputRowVariable
		}
		listed, err := groupedPivotListedPredicate(pivot, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, err
		}
		if pivot.OneInputRowPerGroup {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s ? 0 : 1)", unlistedVariable, listed))
		} else {
			lines = append(lines, fmt.Sprintf("  LET %s = LENGTH((FOR %s IN %s FILTER NOT %s RETURN 1))",
				unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed))
		}
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: pivot.UnlistedEvidenceColumn, Value: ir.PhysicalValue{Variable: unlistedVariable}})
	} else if pivot.UnlistedCategoryPolicy == "ERROR" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		if pivot.OneInputRowPerGroup {
			itemVariable = pivot.InputRowVariable
		}
		listed, err := groupedPivotListedPredicate(pivot, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if err != nil {
			return nil, err
		}
		if pivot.OneInputRowPerGroup {
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s, \"TABLE_PIVOT_UNLISTED_CATEGORY\")", listed))
		} else {
			lines = append(lines,
				fmt.Sprintf("  LET %s = LENGTH((FOR %s IN %s FILTER NOT %s RETURN 1))", unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed),
				fmt.Sprintf("  FILTER ASSERT(%s == 0, \"TABLE_PIVOT_UNLISTED_CATEGORY\")", unlistedVariable),
			)
		}
	}

	groupValues := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		groupValues = append(groupValues, key.Variable)
	}
	identityVariable := previewIdentityVariable
	if identityVariable == "" {
		identity, identityErr := r.renderGroupedPivotIdentity(pivot, groupValues)
		if identityErr != nil {
			return nil, identityErr
		}
		identityVariable = r.newInternalVariable("reshape_identity")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", identityVariable, identity))
	}
	outputProjections = append(outputProjections, ir.PhysicalProjection{Name: "__loom_row_id", Value: ir.PhysicalValue{Variable: identityVariable}})
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = %s", pivot.OutputRowVariable, output))
	return lines, nil
}

func (r *physicalPlanRenderer) renderCodedPivotCategoryValues(correlation ir.PhysicalCorrelation) (string, error) {
	if correlation.SystemBindKey == "" || correlation.CodeBindKey == "" {
		return "", fmt.Errorf("coded category requires system and code binds")
	}
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	owner := r.newInternalVariable("coded_pivot_owner")
	coding := r.newInternalVariable("coded_pivot_coding")
	codings, err := r.renderSelectorArrayFromSource(owner, correlation.KeySelector, false, false)
	if err != nil {
		return "", fmt.Errorf("correlation key selector: %w", err)
	}
	system, err := r.renderCorrelationScalar(coding, correlation.SystemSelector)
	if err != nil {
		return "", fmt.Errorf("correlation system selector: %w", err)
	}
	code, err := r.renderCorrelationScalar(coding, correlation.CodeSelector)
	if err != nil {
		return "", fmt.Errorf("correlation code selector: %w", err)
	}
	values, err := r.renderCorrelationValues(owner, correlation.ValueSelector, correlation.ValueFallbacks)
	if err != nil {
		return "", err
	}
	unsupported, err := r.renderCorrelationUnsupportedChoiceValues(owner, correlation)
	if err != nil {
		return "", err
	}
	matched := r.newInternalVariable("coded_pivot_match")
	return fmt.Sprintf(`FLATTEN(
  FOR %s IN %s
    LET %s = LENGTH((
      FOR %s IN FLATTEN(%s)
        LET __coded_system = %s
        LET __coded_code = %s
        FILTER __coded_system != null AND __coded_system != ""
        FILTER __coded_code != null AND __coded_code != ""
        FILTER __coded_system == @%s
        FILTER __coded_code == @%s
        LIMIT 1
        RETURN 1
    )) > 0
    FILTER %s
    LET __coded_unsupported = %s
    FILTER ASSERT(LENGTH(__coded_unsupported) == 0, "INVALID_CHOICE_ARM")
    LET __coded_values = FLATTEN(%s)
    FILTER LENGTH(__coded_values) > 0
    RETURN __coded_values
)`, owner, owners, matched, coding, codings, system, code, correlation.SystemBindKey, correlation.CodeBindKey, matched, unsupported, values), nil
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

func (r *physicalPlanRenderer) renderProjectionPresence(presence ir.PhysicalProjectionPresence) (string, error) {
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
	input, err = r.renderTraceSourceInput(input, unpivot.InputProjections, unpivot.OutputRowVariable)
	if err != nil {
		return nil, err
	}
	lines := []string{fmt.Sprintf("  LET %s = %s", unpivot.InputRowVariable, input)}
	slots := make([]string, 0, len(unpivot.Inputs))
	for index, selected := range unpivot.Inputs {
		columnBind := r.newInternalBindKey(fmt.Sprintf("reshape_unpivot_column_%d", index))
		r.bindVars[columnBind] = selected.Column
		slot := fmt.Sprintf("{key: @%s, value: %s[@%s]}", selected.KeyBindKey, unpivot.InputRowVariable, columnBind)
		if r.traceReshapeApplies(unpivot.OutputRowVariable) {
			presenceField, supported := r.traceUnpivotInput(selected.Column)
			if supported {
				presenceBind := r.newInternalBindKey("reshape_unpivot_trace_presence")
				r.bindVars[presenceBind] = presenceField
				documentBind := r.newInternalBindKey("reshape_unpivot_trace_document")
				r.bindVars[documentBind] = ir.PhysicalCellTraceSourceDocumentField
				slot = fmt.Sprintf("MERGE(%s, {sourceDocument: %s[@%s], sourcePresence: %s[@%s], sourceSupported: true})", slot, unpivot.InputRowVariable, documentBind, unpivot.InputRowVariable, presenceBind)
			} else {
				slot = fmt.Sprintf("MERGE(%s, {sourceDocument: null, sourcePresence: false, sourceSupported: false})", slot)
			}
		}
		slots = append(slots, slot)
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
	preservedNames := make(map[string]string, len(unpivot.PreservedOutputs))
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	outputProjections := make([]ir.PhysicalProjection, 0, len(unpivot.InputProjections)+3)
	for _, projection := range unpivot.InputProjections {
		if selected[projection.Name] || projection.Name == "__loom_row_id" {
			continue
		}
		outputName := projection.Name
		if renamed, ok := preservedNames[projection.Name]; ok {
			outputName = renamed
		}
		columnBind := r.newInternalBindKey("reshape_unpivot_preserved_column")
		r.bindVars[columnBind] = projection.Name
		lookup := ir.PhysicalExpression{Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: unpivot.InputRowVariable, KeyBindKey: columnBind}}
		outputProjections = append(outputProjections, ir.PhysicalProjection{Name: outputName, Hidden: projection.Hidden, Expression: &lookup})
	}
	outputProjections = append(outputProjections,
		ir.PhysicalProjection{Name: unpivot.KeyOutput, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"key"}}},
		ir.PhysicalProjection{Name: unpivot.ValueOutput, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"value"}}},
		ir.PhysicalProjection{Name: "__loom_row_id", Hidden: true, Value: ir.PhysicalValue{Variable: identityVariable}},
	)
	if r.traceReshapeApplies(unpivot.OutputRowVariable) {
		outputProjections = append(outputProjections,
			ir.PhysicalProjection{Name: ir.PhysicalCellTraceSourceDocumentField, Hidden: true, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"sourceDocument"}}},
			ir.PhysicalProjection{Name: ir.PhysicalCellTraceSourcePresenceField, Hidden: true, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"sourcePresence"}}},
			ir.PhysicalProjection{Name: ir.PhysicalCellTraceSourceSupportedField, Hidden: true, Value: ir.PhysicalValue{Variable: unpivot.SlotVariable, Path: []string{"sourceSupported"}}},
		)
		for _, source := range r.cellTrace.Reshape.Sources {
			if source.Kind != ir.PhysicalCellTraceUnpivotField {
				continue
			}
			if source.OmissionCode == "" {
				outputProjections = append(outputProjections,
					ir.PhysicalProjection{Name: ir.PhysicalCellTracePassSourceDocumentField, Hidden: true, Value: ir.PhysicalValue{Variable: unpivot.InputRowVariable, Path: []string{ir.PhysicalCellTraceSourceDocumentField}}},
					ir.PhysicalProjection{Name: ir.PhysicalCellTracePassSourcePresenceField, Hidden: true, Value: ir.PhysicalValue{Variable: unpivot.InputRowVariable, Path: []string{source.SourcePresenceField}}},
					r.traceLiteralProjection(ir.PhysicalCellTracePassSourceSupportedField, true),
				)
			} else {
				outputProjections = append(outputProjections,
					r.traceLiteralProjection(ir.PhysicalCellTracePassSourceDocumentField, nil),
					r.traceLiteralProjection(ir.PhysicalCellTracePassSourcePresenceField, false),
					r.traceLiteralProjection(ir.PhysicalCellTracePassSourceSupportedField, false),
				)
			}
		}
	}
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = %s", unpivot.OutputRowVariable, output))
	return lines, nil
}

func (r *physicalPlanRenderer) traceReshapeApplies(outputVariable string) bool {
	if r.cellTrace != nil && r.cellTrace.Reshape != nil && r.cellTrace.Reshape.OutputVariable == outputVariable {
		return true
	}
	return r.tableShapeExclusion != nil && r.tableShapeExclusion.Pivot.OutputRowVariable == outputVariable
}

func (r *physicalPlanRenderer) renderTraceSourceInput(input string, projections []ir.PhysicalProjection, outputVariable string) (string, error) {
	if !r.traceReshapeApplies(outputVariable) {
		return input, nil
	}
	if r.cellTrace != nil && r.cellTrace.Reshape != nil && !traceReshapeHasSupportedSource(*r.cellTrace.Reshape) {
		return input, nil
	}
	if r.rootVariable == "" {
		return "", fmt.Errorf("cell trace reshape source has no root resource variable")
	}
	document, err := r.renderValue(ir.PhysicalValue{Variable: r.rootVariable, Path: []string{"payload"}})
	if err != nil {
		return "", fmt.Errorf("render trace source document: %w", err)
	}
	entries := []string{}
	documentBind := r.newInternalBindKey("reshape_trace_document_field")
	r.bindVars[documentBind] = ir.PhysicalCellTraceSourceDocumentField
	entries = append(entries, fmt.Sprintf("[@%s]: %s", documentBind, document))
	for index, projection := range projections {
		if projection.Presence == nil {
			continue
		}
		present, err := r.renderProjectionPresence(*projection.Presence)
		if err != nil {
			return "", fmt.Errorf("render trace source presence for %q: %w", projection.Name, err)
		}
		keyBind := r.newInternalBindKey("reshape_trace_presence_field")
		r.bindVars[keyBind] = fmt.Sprintf("%s%d", ir.PhysicalCellTraceSourcePresencePrefix, index)
		entries = append(entries, fmt.Sprintf("[@%s]: %s", keyBind, present))
	}
	return fmt.Sprintf("MERGE(%s, {%s})", input, strings.Join(entries, ", ")), nil
}

func traceReshapeHasSupportedSource(lineage ir.PhysicalCellTraceReshape) bool {
	for _, source := range lineage.Sources {
		if source.OmissionCode == "" {
			return true
		}
	}
	return false
}

func (r *physicalPlanRenderer) traceUnpivotInput(column string) (string, bool) {
	if r.cellTrace == nil || r.cellTrace.Reshape == nil {
		return "", false
	}
	for _, source := range r.cellTrace.Reshape.Sources {
		if source.Kind == ir.PhysicalCellTraceUnpivotValue && source.SourceColumn == column && source.OmissionCode == "" {
			return source.SourcePresenceField, source.SourcePresenceField != ""
		}
	}
	return "", false
}

func (r *physicalPlanRenderer) traceLiteralProjection(name string, value any) ir.PhysicalProjection {
	key := r.newInternalBindKey("unpivot_trace_literal")
	r.bindVars[key] = value
	expression := ir.PhysicalExpression{
		Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: key},
	}
	return ir.PhysicalProjection{Name: name, Hidden: true, Expression: &expression}
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
