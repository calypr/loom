package aql

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderCellTraceReturn(terminal ir.PhysicalCellTraceReturn) ([]string, error) {
	value, err := r.renderExpression(terminal.Value)
	if err != nil {
		return nil, fmt.Errorf("trace value: %w", err)
	}
	contributors, statusCandidates, lossy, omission, err := r.renderTraceContributors(terminal)
	if err != nil {
		return nil, err
	}
	if terminal.OmissionCode != "" {
		omission = terminal.OmissionCode
	}
	identityField := ir.PhysicalCellTraceIdentityPartsField
	identityValue := ""
	if terminal.ExplicitIdentity != nil {
		identityField = ir.PhysicalCellTraceExplicitIdentityField
		identityValue, err = r.renderExpression(*terminal.ExplicitIdentity)
		if err != nil {
			return nil, fmt.Errorf("trace explicit identity: %w", err)
		}
	} else {
		parts := make([]string, 0, len(terminal.IdentityParts))
		for _, part := range terminal.IdentityParts {
			rendered, partErr := r.renderExpression(part.Expression)
			if partErr != nil {
				return nil, fmt.Errorf("trace identity part %q: %w", part.Name, partErr)
			}
			parts = append(parts, rendered)
		}
		identityValue = "[" + strings.Join(parts, ", ") + "]"
	}
	name := func(prefix, value string) string {
		key := r.newInternalBindKey(prefix)
		r.bindVars[key] = value
		return "@" + key
	}
	valueName := name("trace_value_name", ir.PhysicalCellTraceValueField)
	contributionsName := name("trace_contributions_name", ir.PhysicalCellTraceContributionsField)
	identityName := name("trace_identity_name", identityField)
	statusName := name("trace_status_name", ir.PhysicalCellTraceStatusField)
	hasMoreName := name("trace_has_more_name", ir.PhysicalCellTraceHasMoreField)
	omissionName := name("trace_omission_name", ir.PhysicalCellTraceOmissionField)
	statusVariable := r.newInternalVariable("trace_status_candidates")
	pageVariable := r.newInternalVariable("trace_contribution_page")
	status := fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : %s == null ? "RECORDED_NULL" : "VALUE"`, statusVariable, value)
	if terminal.Construction != nil {
		if terminal.Construction.RelatedSource != nil {
			status = fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : "VALUE"`, statusVariable)
		} else {
			status = fmt.Sprintf(`%s == null ? "RECORDED_NULL" : "VALUE"`, value)
		}
	} else if lossy {
		status = fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : %s == null ? "RECORDED_NULL" : LENGTH(%s) > 1 ? "AMBIGUOUS" : "VALUE"`, statusVariable, value, statusVariable)
	}
	return []string{
		"LET " + statusVariable + " = " + statusCandidates,
		"LET " + pageVariable + " = " + contributors,
		fmt.Sprintf("RETURN { [%s]: %s, [%s]: SLICE(%s, 0, @%s), [%s]: %s, [%s]: %s, [%s]: LENGTH(%s) > @%s, [%s]: %q }", valueName, value, contributionsName, pageVariable, terminal.LimitBindKey, identityName, identityValue, statusName, status, hasMoreName, pageVariable, terminal.LimitBindKey, omissionName, omission),
	}, nil
}

func (r *physicalPlanRenderer) renderTraceContributors(terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	if terminal.Construction != nil {
		return r.renderConstructionTraceContributors(terminal, *terminal.Construction)
	}
	if terminal.Contribution != nil {
		return r.renderReducedSetTraceContributors(*terminal.Contribution, terminal)
	}
	if terminal.Reshape != nil {
		return r.renderReshapeTraceContributors(*terminal.Reshape, terminal)
	}
	expression := terminal.Value
	switch expression.Kind {
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil || expression.Extract.Prepared != nil {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		items := "[" + expression.Extract.Source.Variable + "]"
		if r.setVariables[expression.Extract.Source.Variable] != "" {
			items = expression.Extract.Source.Variable
		}
		return r.renderTraceContributorQueries(items, expression, terminal, expression.Cardinality == ir.PhysicalScalarCardinality)
	case ir.PhysicalAggregateExpression:
		aggregate := expression.Aggregate
		if aggregate == nil {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		items, _, itemsErr := r.renderAggregateItems(aggregate)
		if itemsErr != nil {
			return "", "", false, "", itemsErr
		}
		if aggregate.Ordering != nil {
			return r.renderFirstOrderedTraceContributors(aggregate, items, terminal)
		}
		if aggregate.Value == nil {
			return r.renderTraceContributorQueries(items, ir.PhysicalExpression{}, terminal, false)
		}
		lossy := aggregate.Operation == ir.PhysicalFirstAggregate || aggregate.Operation == ir.PhysicalRequireOneAggregate
		if aggregate.Operation == ir.PhysicalSumAggregate || aggregate.Operation == ir.PhysicalMeanAggregate {
			return r.renderNumericAggregateTraceContributors(items, *aggregate.Value, terminal)
		}
		if aggregate.Operation == ir.PhysicalCountAggregate || aggregate.Operation == ir.PhysicalExistsAggregate || aggregate.Operation == ir.PhysicalMinAggregate || aggregate.Operation == ir.PhysicalMaxAggregate {
			return r.renderScalarAggregateTraceContributors(items, *aggregate.Value, aggregate.Operation, terminal)
		}
		return r.renderTraceContributorQueries(items, *aggregate.Value, terminal, lossy)
	case ir.PhysicalOwnerRecordsExpression:
		records, renderErr := r.renderExpression(expression)
		if renderErr != nil {
			return "", "", false, "", renderErr
		}
		record := r.newInternalVariable("trace_owner_record")
		contribution := fmt.Sprintf(`{ resourceType: %s.source.resourceType, resourceId: %s.source.resourceId, value: %s }`, record, record, record)
		page := fmt.Sprintf("(FOR %s IN %s LIMIT @%s, @%s RETURN %s)", record, records, terminal.OffsetBindKey, terminal.FetchLimitBindKey, contribution)
		status := fmt.Sprintf("(FOR %s IN %s LIMIT 2 RETURN %s)", record, records, contribution)
		return page, status, false, "", nil
	case ir.PhysicalCallExpression:
		source, ok := r.categoryRecodeTraceSource(expression)
		if !ok {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		terminal.Value = source
		return r.renderTraceContributors(terminal)
	default:
		return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
	}
}

func (r *physicalPlanRenderer) renderConstructionTraceContributors(terminal ir.PhysicalCellTraceReturn, lineage ir.PhysicalCellTraceConstruction) (page, status string, lossy bool, omission string, err error) {
	if lineage.OmissionCode != "" {
		return "[]", "[]", false, lineage.OmissionCode, nil
	}
	if terminal.Value.Kind != ir.PhysicalValueExpression || terminal.Value.Value == nil || terminal.Value.Value.Variable == "" {
		return "", "", false, "", fmt.Errorf("construction trace value must be a final-row column")
	}
	finalRow := terminal.Value.Value.Variable
	if lineage.RelatedSource != nil {
		return r.renderRelatedSourceTraceContributors(finalRow, lineage, terminal)
	}
	if len(lineage.Inputs) == 0 {
		return "[]", "[]", false, "CONSTRUCTION_TRACE_INPUTS_UNAVAILABLE", nil
	}
	items := make([]string, 0, len(lineage.Inputs))
	bind := func(prefix, value string) string {
		key := r.newInternalBindKey(prefix)
		r.bindVars[key] = value
		return "@" + key
	}
	for _, input := range lineage.Inputs {
		columnBind := bind("trace_construction_input_column", input.FinalValueColumn)
		items = append(items, fmt.Sprintf(
			"{inputStageId: %s, inputColumnId: %s, inputColumn: %s, outputStageId: %s, outputColumnId: %s, outputColumn: %s, finalStageId: %s, constructionId: %s, operation: %s, value: %s[%s]}",
			bind("trace_construction_input_stage", input.StageID),
			bind("trace_construction_input_column_id", input.ColumnID),
			bind("trace_construction_input_column_name", input.Column),
			bind("trace_construction_output_stage", lineage.ProducerStageID),
			bind("trace_construction_output_column_id", lineage.OutputColumnID),
			bind("trace_construction_output_column_name", lineage.OutputColumn),
			bind("trace_construction_final_stage", lineage.FinalStageID),
			bind("trace_construction_id", lineage.ConstructionID),
			bind("trace_construction_operation", lineage.Operation),
			finalRow, columnBind,
		))
	}
	array := "[" + strings.Join(items, ", ") + "]"
	page = fmt.Sprintf("SLICE(%s, @%s, @%s)", array, terminal.OffsetBindKey, terminal.FetchLimitBindKey)
	return page, array, false, "", nil
}

func (r *physicalPlanRenderer) renderRelatedSourceTraceContributors(finalRow string, construction ir.PhysicalCellTraceConstruction, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	lineage := construction.RelatedSource
	if lineage == nil || lineage.InputRowVariable == "" || lineage.AnchorColumn != "_key" || lineage.ResourceType == "" || len(lineage.Subplan.Captures) != 1 || lineage.Subplan.Captures[0] != lineage.InputRowVariable {
		return "", "", false, "", fmt.Errorf("related-source trace requires its exact captured root identity and route")
	}
	subplan := ir.ClonePhysicalSubplan(lineage.Subplan)
	rebound := 0
	for index := range subplan.Operations {
		operation := &subplan.Operations[index]
		if operation.Kind != ir.PhysicalFilterOp || operation.Filter == nil || operation.Filter.Expression != nil {
			continue
		}
		comparison := &operation.Filter.Predicate
		if comparison.Right == nil || comparison.Right.Variable != lineage.InputRowVariable {
			continue
		}
		if len(comparison.Right.Path) != 1 || comparison.Right.Path[0] != lineage.AnchorColumn {
			return "", "", false, "", fmt.Errorf("related-source trace capture is not anchored by the retained root identity")
		}
		comparison.Right.Variable = finalRow
		rebound++
	}
	if rebound != 1 {
		return "", "", false, "", fmt.Errorf("related-source trace expected one exact root-identity capture, found %d", rebound)
	}
	subplan.Captures = []string{finalRow}
	var sourceVariable string
	for _, operation := range subplan.Operations {
		if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
			sourceVariable = operation.Traversal.TargetVariable
		}
	}
	if sourceVariable == "" {
		return "", "", false, "", fmt.Errorf("related-source trace route has no traversed resource")
	}
	value := subplan.Return
	fields := []ir.PhysicalExpressionProjection{
		{Name: "resourceType", Expression: physicalTraceValue(sourceVariable, "resourceType")},
		{Name: "resourceId", Expression: physicalTraceValue(sourceVariable, "id")},
		{Name: "outputStageId", Expression: r.physicalTraceString(construction.ProducerStageID)},
		{Name: "outputColumnId", Expression: r.physicalTraceString(construction.OutputColumnID)},
		{Name: "outputColumn", Expression: r.physicalTraceString(construction.OutputColumn)},
		{Name: "finalStageId", Expression: r.physicalTraceString(construction.FinalStageID)},
		{Name: "operation", Expression: r.physicalTraceString(construction.Operation)},
		{Name: "value", Expression: value},
	}
	subplan.Return = ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
		Object: &ir.PhysicalObject{Fields: fields},
	}
	route, err := r.renderSubplan(subplan, "  ", false)
	if err != nil {
		return "", "", false, "", fmt.Errorf("render related-source trace route: %w", err)
	}
	item := r.newInternalVariable("trace_related_source")
	page = fmt.Sprintf("(FOR %s IN %s LIMIT @%s, @%s RETURN %s)", item, route, terminal.OffsetBindKey, terminal.FetchLimitBindKey, item)
	status = fmt.Sprintf("(FOR %s IN %s LIMIT 2 RETURN %s)", item, route, item)
	return page, status, false, "", nil
}

func (r *physicalPlanRenderer) physicalTraceString(value string) ir.PhysicalExpression {
	key := r.newInternalBindKey("trace_related_source_metadata")
	r.bindVars[key] = value
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: key},
	}
}

func physicalTraceValue(variable, field string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: []string{field}},
	}
}

func (r *physicalPlanRenderer) renderReshapeTraceContributors(lineage ir.PhysicalCellTraceReshape, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	queries := make([]string, 0, len(lineage.Sources))
	omission = lineage.OmissionCode
	for _, source := range lineage.Sources {
		if source.OmissionCode != "" && omission == "" {
			omission = source.OmissionCode
		}
		if source.OmissionCode != "" {
			continue
		}
		switch source.Kind {
		case ir.PhysicalCellTracePivotGroupKey:
			query, queryErr := r.renderPivotGroupKeyTraceQuery(source)
			if queryErr != nil {
				return "", "", false, "", queryErr
			}
			queries = append(queries, query)
		case ir.PhysicalCellTracePivotCell:
			query, queryErr := r.renderPivotCellTraceQuery(source, terminal)
			if queryErr != nil {
				return "", "", false, "", queryErr
			}
			queries = append(queries, query)
		case ir.PhysicalCellTraceUnpivotValue, ir.PhysicalCellTraceUnpivotField:
			query, queryErr := r.renderUnpivotValueTraceQuery(lineage.OutputVariable, terminal)
			if queryErr != nil {
				return "", "", false, "", queryErr
			}
			queries = append(queries, query)
			// Each output row came from exactly one unpivot input. Do not
			// duplicate its evidence once for every configured input column.
			goto contributorsReady
		default:
			return "", "", false, "", fmt.Errorf("unsupported reshape trace source kind %q", source.Kind)
		}
	}

contributorsReady:
	if len(queries) == 0 {
		return "[]", "[]", false, omission, nil
	}
	all := "UNIQUE(FLATTEN([" + strings.Join(queries, ", ") + "]))"
	sorted := fmt.Sprintf("(FOR __loom_trace_source IN %s SORT __loom_trace_source.resourceType ASC, __loom_trace_source.resourceId ASC RETURN __loom_trace_source)", all)
	page = fmt.Sprintf("SLICE(%s, @%s, @%s)", sorted, terminal.OffsetBindKey, terminal.FetchLimitBindKey)
	status = fmt.Sprintf("SLICE(%s, 0, 2)", sorted)
	return page, status, false, omission, nil
}

func (r *physicalPlanRenderer) renderPivotGroupKeyTraceQuery(source ir.PhysicalCellTraceReshapeSource) (string, error) {
	item := r.newInternalVariable("trace_pivot_group_key_source")
	columnBind := r.newInternalBindKey("trace_pivot_group_key_column")
	r.bindVars[columnBind] = source.SourceColumn
	presenceBind := r.newInternalBindKey("trace_pivot_group_key_presence")
	r.bindVars[presenceBind] = source.SourcePresenceField
	documentBind := r.newInternalBindKey("trace_pivot_group_key_document")
	r.bindVars[documentBind] = ir.PhysicalCellTraceSourceDocumentField
	value := fmt.Sprintf("%s[@%s]", item, columnBind)
	document := fmt.Sprintf("%s[@%s]", item, documentBind)
	return fmt.Sprintf("(FOR %s IN %s FILTER %s[@%s] == true RETURN {resourceType: %s.resourceType, resourceId: %s.id, value: %s})", item, source.GroupRowsVariable, item, presenceBind, document, document, value), nil
}

func (r *physicalPlanRenderer) renderPivotCellTraceQuery(source ir.PhysicalCellTraceReshapeSource, terminal ir.PhysicalCellTraceReturn) (string, error) {
	if source.Category == nil {
		return "", fmt.Errorf("pivot cell trace requires a category")
	}
	item := r.newInternalVariable("trace_pivot_cell_source")
	columnBind := r.newInternalBindKey("trace_pivot_cell_column")
	r.bindVars[columnBind] = source.SourceColumn
	presenceBind := r.newInternalBindKey("trace_pivot_cell_presence")
	r.bindVars[presenceBind] = source.SourcePresenceField
	documentBind := r.newInternalBindKey("trace_pivot_cell_document")
	r.bindVars[documentBind] = ir.PhysicalCellTraceSourceDocumentField
	categoryColumnBind := r.newInternalBindKey("trace_pivot_category_column")
	r.bindVars[categoryColumnBind] = source.CategoryColumn
	categoryPresenceBind := ""
	if source.CategoryPresenceField != "" {
		categoryPresenceBind = r.newInternalBindKey("trace_pivot_category_presence")
		r.bindVars[categoryPresenceBind] = source.CategoryPresenceField
	}
	categoryTypeBind := ""
	if source.Category.MatchKind == ir.PhysicalPivotCategoryValueMatch {
		categoryType, err := aqlTableScalarType(source.CategoryType)
		if err != nil {
			return "", err
		}
		categoryTypeBind = r.newInternalBindKey("trace_pivot_category_type")
		r.bindVars[categoryTypeBind] = categoryType
	}
	match, err := groupedPivotCategoryMatchPredicate(*source.Category, item, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
	if err != nil {
		return "", err
	}
	value := fmt.Sprintf("%s[@%s]", item, columnBind)
	filters := []string{
		"FILTER " + match,
		fmt.Sprintf("FILTER %s[@%s] == true", item, presenceBind),
	}
	valueTypeBind := ""
	if source.DuplicatePolicy != "ERROR" {
		valueType, typeErr := aqlTableScalarType(source.ValueColumnType)
		if typeErr != nil {
			return "", typeErr
		}
		valueTypeBind = r.newInternalBindKey("trace_pivot_value_type")
		r.bindVars[valueTypeBind] = valueType
		filters = append(filters, "FILTER "+value+" != null", "FILTER ASSERT(TYPENAME("+value+") == @"+valueTypeBind+", \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\")")
	}
	if source.DuplicatePolicy == "MIN" || source.DuplicatePolicy == "MAX" {
		result, renderErr := r.renderExpression(terminal.Value)
		if renderErr != nil {
			return "", renderErr
		}
		filters = append(filters, "FILTER "+value+" == ("+result+")")
	}
	document := fmt.Sprintf("%s[@%s]", item, documentBind)
	return fmt.Sprintf("(FOR %s IN %s %s RETURN {resourceType: %s.resourceType, resourceId: %s.id, value: %s})", item, source.GroupRowsVariable, strings.Join(filters, " "), document, document, value), nil
}

func (r *physicalPlanRenderer) renderUnpivotValueTraceQuery(outputVariable string, terminal ir.PhysicalCellTraceReturn) (string, error) {
	item := r.newInternalVariable("trace_unpivot_value_source")
	documentField := ir.PhysicalCellTraceSourceDocumentField
	presenceField := ir.PhysicalCellTraceSourcePresenceField
	supportedField := ir.PhysicalCellTraceSourceSupportedField
	for _, source := range terminal.Reshape.Sources {
		if source.Kind == ir.PhysicalCellTraceUnpivotField {
			documentField = ir.PhysicalCellTracePassSourceDocumentField
			presenceField = ir.PhysicalCellTracePassSourcePresenceField
			supportedField = ir.PhysicalCellTracePassSourceSupportedField
			break
		}
	}
	document := fmt.Sprintf("%s.%s", item, documentField)
	value, err := r.renderExpression(terminal.Value)
	if err != nil {
		return "", fmt.Errorf("trace unpivot value: %w", err)
	}
	return fmt.Sprintf("(FOR %s IN [%s] FILTER %s.%s == true FILTER %s.%s == true RETURN {resourceType: %s.resourceType, resourceId: %s.id, value: %s})", item, outputVariable, item, supportedField, item, presenceField, document, document, value), nil
}

func (r *physicalPlanRenderer) categoryRecodeTraceSource(expression ir.PhysicalExpression) (ir.PhysicalExpression, bool) {
	call := expression.Call
	if expression.Kind != ir.PhysicalCallExpression || call == nil || strings.ToLower(call.Name) != "case" || len(call.Args) < 5 || len(call.Args)%2 == 0 {
		return ir.PhysicalExpression{}, false
	}
	source, nullValue, ok := r.traceEquality(call.Args[0])
	if !ok || !traceableCategoryRecodeSource(source) || nullValue != nil {
		return ir.PhysicalExpression{}, false
	}
	firstMappedValue, exists := r.traceLiteralValue(call.Args[1])
	if !exists || firstMappedValue != nil {
		return ir.PhysicalExpression{}, false
	}
	for index := 2; index < len(call.Args)-1; index += 2 {
		mappedSource, mappedFrom, matches := r.traceEquality(call.Args[index])
		mappedTo, mappedToExists := r.traceLiteralValue(call.Args[index+1])
		if !matches || !reflect.DeepEqual(source, mappedSource) || !mappedToExists {
			return ir.PhysicalExpression{}, false
		}
		if _, ok := mappedFrom.(string); !ok {
			return ir.PhysicalExpression{}, false
		}
		if _, ok := mappedTo.(string); !ok {
			return ir.PhysicalExpression{}, false
		}
	}
	defaultValue := call.Args[len(call.Args)-1]
	if reflect.DeepEqual(source, defaultValue) || r.isCategoryRecodeErrorDefault(defaultValue) {
		return source, true
	}
	return ir.PhysicalExpression{}, false
}

func traceableCategoryRecodeSource(source ir.PhysicalExpression) bool {
	switch source.Kind {
	case ir.PhysicalExtractExpression:
		return source.Extract != nil
	case ir.PhysicalAggregateExpression:
		return source.Aggregate != nil
	default:
		return false
	}
}

func (r *physicalPlanRenderer) traceEquality(expression ir.PhysicalExpression) (ir.PhysicalExpression, any, bool) {
	call := expression.Call
	if expression.Kind != ir.PhysicalCallExpression || call == nil || strings.ToLower(call.Name) != "eq" || len(call.Args) != 2 {
		return ir.PhysicalExpression{}, nil, false
	}
	value, ok := r.traceLiteralValue(call.Args[1])
	if !ok {
		return ir.PhysicalExpression{}, nil, false
	}
	return call.Args[0], value, true
}

func (r *physicalPlanRenderer) traceLiteralValue(expression ir.PhysicalExpression) (any, bool) {
	if expression.Kind != ir.PhysicalLiteralExpression || expression.Literal == nil {
		return nil, false
	}
	value, ok := r.bindVars[expression.Literal.BindKey]
	return value, ok
}

func (r *physicalPlanRenderer) isCategoryRecodeErrorDefault(expression ir.PhysicalExpression) bool {
	call := expression.Call
	if expression.Kind != ir.PhysicalCallExpression || call == nil || strings.ToLower(call.Name) != "assert" || len(call.Args) != 2 {
		return false
	}
	condition, conditionExists := r.traceLiteralValue(call.Args[0])
	errorCode, errorExists := r.traceLiteralValue(call.Args[1])
	return conditionExists && condition == false && errorExists && errorCode == "CATEGORY_RECODE_UNKNOWN_VALUE"
}

func (r *physicalPlanRenderer) renderNumericAggregateTraceContributors(items string, valueExpression ir.PhysicalExpression, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_numeric_contributor")
	value, err := r.renderAggregateItemValue(valueExpression, item)
	if err != nil {
		return "", "", false, "", err
	}
	numeric := r.newInternalVariable("trace_numeric_value")
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, numeric)
	page = fmt.Sprintf("(FOR %s IN %s FOR %s IN FLATTEN([%s]) FILTER %s != null FILTER ASSERT(IS_NUMBER(%s), \"NUMERIC_AGGREGATE_NON_NUMERIC\") LIMIT @%s, @%s RETURN %s)", item, items, numeric, value, numeric, numeric, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s FOR %s IN FLATTEN([%s]) FILTER %s != null FILTER ASSERT(IS_NUMBER(%s), \"NUMERIC_AGGREGATE_NON_NUMERIC\") LIMIT 2 RETURN %s)", item, items, numeric, value, numeric, numeric, record)
	return page, status, false, "", nil
}

func (r *physicalPlanRenderer) renderScalarAggregateTraceContributors(items string, valueExpression ir.PhysicalExpression, operation ir.PhysicalAggregateOperation, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_contributor")
	value, err := r.renderAggregateItemValue(valueExpression, item)
	if err != nil {
		return "", "", false, "", err
	}
	scalar := r.newInternalVariable("trace_scalar_value")
	filters := []string{"FILTER " + scalar + " != null"}
	if operation == ir.PhysicalMinAggregate || operation == ir.PhysicalMaxAggregate {
		result, resultErr := r.renderExpression(terminal.Value)
		if resultErr != nil {
			return "", "", false, "", resultErr
		}
		filters = append(filters, "FILTER "+scalar+" == ("+result+")")
	}
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, scalar)
	filter := " " + strings.Join(filters, " ")
	page = fmt.Sprintf("(FOR %s IN %s FOR %s IN FLATTEN([%s])%s LIMIT @%s, @%s RETURN %s)", item, items, scalar, value, filter, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s FOR %s IN FLATTEN([%s])%s LIMIT 2 RETURN %s)", item, items, scalar, value, filter, record)
	return page, status, false, "", nil
}

func (r *physicalPlanRenderer) renderFirstOrderedTraceContributors(aggregate *ir.PhysicalAggregate, items string, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	selection, err := r.renderFirstOrderedSelection(aggregate, items)
	if err != nil {
		return "", "", false, "", err
	}
	selected := r.newInternalVariable("trace_temporal_selected")
	contribution := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.resourceId, value: %s.value }`, selected, selected, selected)
	page = fmt.Sprintf("(FOR %s IN [%s] FILTER %s != null LIMIT @%s, @%s RETURN %s)", selected, selection, selected, terminal.OffsetBindKey, terminal.FetchLimitBindKey, contribution)
	status = fmt.Sprintf("(FOR %s IN [%s] FILTER %s != null LIMIT 2 RETURN %s)", selected, selection, selected, contribution)
	return page, status, false, "", nil
}

func (r *physicalPlanRenderer) renderReducedSetTraceContributors(contribution ir.PhysicalCellTraceContribution, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_contributor")
	value := r.newInternalVariable("trace_contributor_value")
	values := fmt.Sprintf("(%s.%s == null ? [null] : TO_ARRAY(%s.%s))", item, contribution.ValueField, item, contribution.ValueField)
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, value)
	page = fmt.Sprintf("(FOR %s IN %s FOR %s IN %s LIMIT @%s, @%s RETURN %s)", item, contribution.SetVariable, value, values, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s FOR %s IN %s LIMIT 2 RETURN %s)", item, contribution.SetVariable, value, values, record)
	return page, status, contribution.Lossy, "", nil
}

func (r *physicalPlanRenderer) renderTraceContributorQueries(items string, valueExpression ir.PhysicalExpression, terminal ir.PhysicalCellTraceReturn, lossy bool) (page, status string, resultLossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_contributor")
	value := "null"
	if valueExpression.Kind != "" {
		if valueExpression.Kind != ir.PhysicalExtractExpression || valueExpression.Extract == nil {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		value, err = r.renderAggregateItemValue(valueExpression, item)
		if err != nil {
			return "", "", false, "", err
		}
	}
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, value)
	filter := ""
	if valueExpression.Kind == ir.PhysicalExtractExpression && valueExpression.Cardinality == ir.PhysicalScalarCardinality {
		filter = " FILTER " + value + " != null"
	}
	page = fmt.Sprintf("(FOR %s IN %s%s LIMIT @%s, @%s RETURN %s)", item, items, filter, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s%s LIMIT 2 RETURN %s)", item, items, filter, record)
	return page, status, lossy, "", nil
}
