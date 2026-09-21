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
	if lossy {
		status = fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : %s == null ? "RECORDED_NULL" : LENGTH(%s) > 1 ? "AMBIGUOUS" : "VALUE"`, statusVariable, value, statusVariable)
	}
	return []string{
		"LET " + statusVariable + " = " + statusCandidates,
		"LET " + pageVariable + " = " + contributors,
		fmt.Sprintf("RETURN { [%s]: %s, [%s]: SLICE(%s, 0, @%s), [%s]: %s, [%s]: %s, [%s]: LENGTH(%s) > @%s, [%s]: %q }", valueName, value, contributionsName, pageVariable, terminal.LimitBindKey, identityName, identityValue, statusName, status, hasMoreName, pageVariable, terminal.LimitBindKey, omissionName, omission),
	}, nil
}

func (r *physicalPlanRenderer) renderTraceContributors(terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	if terminal.Contribution != nil {
		return r.renderReducedSetTraceContributors(*terminal.Contribution, terminal)
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
		if aggregate.Temporal != nil {
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

func (r *physicalPlanRenderer) categoryRecodeTraceSource(expression ir.PhysicalExpression) (ir.PhysicalExpression, bool) {
	call := expression.Call
	if expression.Kind != ir.PhysicalCallExpression || call == nil || strings.ToLower(call.Name) != "case" || len(call.Args) < 5 || len(call.Args)%2 == 0 {
		return ir.PhysicalExpression{}, false
	}
	source, nullValue, ok := r.traceEquality(call.Args[0])
	if !ok || source.Kind != ir.PhysicalExtractExpression || source.Extract == nil || nullValue != nil {
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
