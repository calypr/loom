package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderAggregate(expression ir.PhysicalExpression) (string, error) {
	aggregate := expression.Aggregate
	if aggregate == nil {
		return "", fmt.Errorf("AGGREGATE expression is missing payload")
	}
	items, perItem, err := r.renderAggregateItems(aggregate)
	if err != nil {
		return "", err
	}
	switch aggregate.Operation {
	case ir.PhysicalCountAggregate:
		if aggregate.Value != nil {
			values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
			if err != nil {
				return "", err
			}
			item := r.navigation().newInternalVariable("aggregate_count_value")
			return "LENGTH(FOR " + item + " IN FLATTEN(" + values + ") FILTER " + item + " != null RETURN 1)", nil
		}
		return "LENGTH(" + items + ")", nil
	case ir.PhysicalExistsAggregate:
		if aggregate.Value == nil {
			return "LENGTH(" + items + ") > 0", nil
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		return "LENGTH(FOR __value IN FLATTEN(" + values + ") FILTER __value != null LIMIT 1 RETURN 1) > 0", nil
	case ir.PhysicalSumAggregate, ir.PhysicalMeanAggregate:
		if aggregate.Value == nil {
			return "", fmt.Errorf("aggregate operation %q requires a numeric value expression", aggregate.Operation)
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		outer := r.navigation().newInternalVariable("aggregate_numeric_values")
		value := r.navigation().newInternalVariable("aggregate_numeric_value")
		numeric := r.navigation().newInternalVariable("aggregate_numeric_values")
		numericValues := "(FOR " + value + " IN " + outer + " FILTER " + value + " != null FILTER ASSERT(IS_NUMBER(" + value + "), \"NUMERIC_AGGREGATE_NON_NUMERIC\") RETURN " + value + ")"
		reduction := "SUM(" + numeric + ")"
		if aggregate.Operation == ir.PhysicalMeanAggregate {
			reduction = reduction + " / LENGTH(" + numeric + ")"
		}
		return "FIRST(FOR " + outer + " IN [FLATTEN(" + values + ")] LET " + numeric + " = " + numericValues + " RETURN LENGTH(" + numeric + ") == 0 ? null : " + reduction + ")", nil
	case ir.PhysicalCountDistinctAggregate, ir.PhysicalDistinctValuesAggregate, ir.PhysicalMinAggregate, ir.PhysicalMaxAggregate, ir.PhysicalFirstAggregate, ir.PhysicalRequireOneAggregate, ir.PhysicalCollectAggregate, ir.PhysicalFirstOrderedAggregate:
		if aggregate.Value == nil {
			return "", fmt.Errorf("aggregate operation %q requires a value expression", aggregate.Operation)
		}
		if aggregate.Operation == ir.PhysicalFirstOrderedAggregate {
			return r.renderFirstOrderedAggregate(aggregate, items)
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		flattened := "FLATTEN(" + values + ")"
		switch aggregate.Operation {
		case ir.PhysicalCountDistinctAggregate:
			return "LENGTH(SORTED_UNIQUE(" + flattened + "))", nil
		case ir.PhysicalDistinctValuesAggregate:
			return "SORTED_UNIQUE(" + flattened + ")", nil
		case ir.PhysicalMinAggregate:
			return "MIN(" + flattened + ")", nil
		case ir.PhysicalMaxAggregate:
			return "MAX(" + flattened + ")", nil
		case ir.PhysicalFirstAggregate:
			return "FIRST(" + flattened + ")", nil
		case ir.PhysicalRequireOneAggregate:
			nonNull := "(FOR __value IN " + flattened + " FILTER __value != null RETURN __value)"
			return "ASSERT(LENGTH(" + nonNull + ") <= 1, \"RELATIONSHIP_CARDINALITY_VIOLATION\") ? FIRST(" + nonNull + ") : null", nil
		case ir.PhysicalCollectAggregate:
			return flattened, nil
		}
	case ir.PhysicalContainsAllAggregate:
		if aggregate.Value == nil {
			return "", fmt.Errorf("aggregate operation %q requires a value expression", aggregate.Operation)
		}
		if aggregate.RequiredValuesBindKey == "" {
			return "", fmt.Errorf("aggregate operation %q requires required values", aggregate.Operation)
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		required := "@" + aggregate.RequiredValuesBindKey
		item := r.navigation().newInternalVariable("aggregate_required_value")
		return "LENGTH(" + required + ") == LENGTH(FOR " + item + " IN " + required + " FILTER POSITION(FLATTEN(" + values + "), " + item + ") RETURN 1)", nil
	}
	return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
}

func (r *physicalPlanRenderer) renderAggregateItems(aggregate *ir.PhysicalAggregate) (string, bool, error) {
	source, err := r.renderValue(aggregate.Source)
	if err != nil {
		return "", false, err
	}
	items := source
	if preparedVariable := aggregatePreparedVariable(aggregate); preparedVariable != "" {
		items = preparedVariable
	}
	if aggregate.Source.Variable == "" || r.setVariables[aggregate.Source.Variable] == "" {
		items = "[" + source + "]"
	}
	perItem := aggregate.Predicate != nil
	if perItem {
		if aggregate.Predicate.Kind != ir.PhysicalComparisonPredicate || aggregate.Predicate.Comparison == nil {
			return "", false, fmt.Errorf("aggregate predicate must be a comparison")
		}
		item := r.navigation().newInternalVariable("aggregate_item")
		comparison := *aggregate.Predicate.Comparison
		if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
			return "", false, fmt.Errorf("aggregate predicate must extract a selector")
		}
		left := *comparison.LeftExpression
		extract := *left.Extract
		if extract.Prepared == nil {
			rebindPhysicalExtractSource(&extract, ir.PhysicalValue{Variable: item, Path: []string{"payload"}})
		}
		left.Extract = &extract
		comparison.LeftExpression = &left
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		predicate, err := r.renderPredicate(comparison)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", false, err
		}
		items = "(FOR " + item + " IN " + items + " FILTER " + predicate + " RETURN " + item + ")"
	}
	if aggregate.ContributorWindow != nil {
		items, err = r.renderContributorWindowItems(aggregate, items)
		if err != nil {
			return "", false, err
		}
		perItem = true
	}
	return items, perItem, nil
}

func (r *physicalPlanRenderer) renderContributorWindowItems(aggregate *ir.PhysicalAggregate, items string) (string, error) {
	window := aggregate.ContributorWindow
	if window == nil {
		return items, nil
	}
	item := r.navigation().newInternalVariable("contributor_window_item")
	timestamp, err := r.renderAggregateItemValue(window.Timestamp, item)
	if err != nil {
		return "", fmt.Errorf("contributor window timestamp: %w", err)
	}
	anchor, err := r.renderExpression(window.Anchor)
	if err != nil {
		return "", fmt.Errorf("contributor window anchor: %w", err)
	}
	patternKey := r.newInternalBindKey("contributor_window_instant_pattern")
	lowerKey := r.newInternalBindKey("contributor_window_lower_offset_seconds")
	upperKey := r.newInternalBindKey("contributor_window_upper_offset_seconds")
	r.bindVars[patternKey] = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`
	r.bindVars[lowerKey] = window.LowerOffset
	r.bindVars[upperKey] = window.UpperOffset
	lowerOperator, upperOperator := ">", "<"
	if window.LowerInclusive {
		lowerOperator = ">="
	}
	if window.UpperInclusive {
		upperOperator = "<="
	}
	anchorVariable := r.navigation().newInternalVariable("contributor_window_anchor")
	scopeVariable := r.navigation().newInternalVariable("contributor_window_scope")
	eligibleVariable := r.navigation().newInternalVariable("contributor_window_eligible")
	timestampVariable := r.navigation().newInternalVariable("contributor_window_timestamp")
	candidates := "(FOR " + item + " IN " + items +
		" LET " + timestampVariable + " = FIRST(FLATTEN([" + timestamp + "]))" +
		" FILTER " + timestampVariable + " != null" +
		" FILTER ASSERT(REGEX_TEST(TO_STRING(" + timestampVariable + "), @" + patternKey + "), \"TEMPORAL_PRECISION_UNSUPPORTED\")" +
		" FILTER DATE_TIMESTAMP(" + timestampVariable + ") " + lowerOperator + " DATE_TIMESTAMP(DATE_ADD(" + anchorVariable + ", @" + lowerKey + ", \"second\"))" +
		" FILTER DATE_TIMESTAMP(" + timestampVariable + ") " + upperOperator + " DATE_TIMESTAMP(DATE_ADD(" + anchorVariable + ", @" + upperKey + ", \"second\"))" +
		" RETURN " + item + ")"
	return "FIRST(FOR " + scopeVariable + " IN [1]" +
		" LET " + anchorVariable + " = " + anchor +
		" FILTER ASSERT(" + anchorVariable + " != null AND REGEX_TEST(TO_STRING(" + anchorVariable + "), @" + patternKey + "), \"TEMPORAL_ANCHOR_INVALID\")" +
		" LET " + eligibleVariable + " = " + candidates +
		" RETURN " + eligibleVariable + ")", nil
}

func (r *physicalPlanRenderer) renderFirstOrderedAggregate(aggregate *ir.PhysicalAggregate, items string) (string, error) {
	selection, err := r.renderFirstOrderedSelection(aggregate, items)
	if err != nil {
		return "", err
	}
	selected := r.navigation().newInternalVariable("temporal_selected_result")
	return "FIRST(FOR " + selected + " IN [" + selection + "] RETURN " + selected + " == null ? null : " + selected + ".value)", nil
}

func (r *physicalPlanRenderer) renderFirstOrderedSelection(aggregate *ir.PhysicalAggregate, items string) (string, error) {
	if aggregate.Ordering == nil || aggregate.Value == nil {
		return "", fmt.Errorf("FIRST_ORDERED requires value and temporal ordering")
	}
	item := r.navigation().newInternalVariable("temporal_item")
	value, err := r.renderAggregateItemValue(*aggregate.Value, item)
	if err != nil {
		return "", err
	}
	value = "FIRST(FLATTEN([" + value + "]))"
	timestamp, err := r.renderAggregateItemValue(aggregate.Ordering.Timestamp, item)
	if err != nil {
		return "", err
	}
	timestamp = "FIRST(FLATTEN([" + timestamp + "]))"
	patternKey := r.newInternalBindKey("temporal_instant_pattern")
	r.bindVars[patternKey] = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`
	direction := strings.ToUpper(aggregate.Ordering.Direction)
	candidatesVariable := r.navigation().newInternalVariable("temporal_candidates")
	selectedVariable := r.navigation().newInternalVariable("temporal_selected")
	candidates := "(FOR " + item + " IN " + items +
		" LET __loom_temporal_value = " + value +
		" LET __loom_temporal_timestamp = " + timestamp +
		" FILTER __loom_temporal_value != null" +
		" FILTER __loom_temporal_timestamp != null" +
		" FILTER ASSERT(REGEX_TEST(TO_STRING(__loom_temporal_timestamp), @" + patternKey + "), \"TEMPORAL_PRECISION_UNSUPPORTED\")" +
		" SORT DATE_TIMESTAMP(__loom_temporal_timestamp) " + direction +
		", TO_STRING(" + item + ".resourceType) ASC, TO_STRING(" + item + ".id) ASC, " + item + "._key ASC" +
		" RETURN { resourceType: " + item + ".resourceType, resourceId: " + item + ".id, value: __loom_temporal_value, timestamp: __loom_temporal_timestamp })"
	result := selectedVariable
	if aggregate.Ordering.TiePolicy == "REQUIRE_UNIQUE" {
		tie := r.navigation().newInternalVariable("temporal_tie")
		ties := "LENGTH(FOR " + tie + " IN " + candidatesVariable + " FILTER DATE_TIMESTAMP(" + tie + ".timestamp) == DATE_TIMESTAMP(" + selectedVariable + ".timestamp) LIMIT 2 RETURN 1)"
		result = selectedVariable + " == null ? null : (ASSERT(" + ties + " <= 1, \"TEMPORAL_TIE_AMBIGUOUS\") ? " + selectedVariable + " : null)"
	}
	return "FIRST(FOR __loom_temporal_scope IN [1]" +
		" LET " + candidatesVariable + " = " + candidates +
		" LET " + selectedVariable + " = FIRST(" + candidatesVariable + ")" +
		" RETURN " + result + ")", nil
}

func aggregatePreparedVariable(aggregate *ir.PhysicalAggregate) string {
	if aggregate == nil {
		return ""
	}
	if aggregate.ContributorWindow != nil {
		return ""
	}

	if aggregate.Value != nil && aggregate.Value.Extract != nil && aggregate.Value.Extract.UnitNormalization != nil {
		return ""
	}
	if aggregate.Value != nil && aggregate.Value.Extract != nil && aggregate.Value.Extract.Prepared != nil {
		return aggregate.Value.Extract.Prepared.SetVariable
	}
	if aggregate.Predicate != nil && aggregate.Predicate.Comparison != nil && aggregate.Predicate.Comparison.LeftExpression != nil && aggregate.Predicate.Comparison.LeftExpression.Extract != nil && aggregate.Predicate.Comparison.LeftExpression.Extract.Prepared != nil {
		return aggregate.Predicate.Comparison.LeftExpression.Extract.Prepared.SetVariable
	}
	return ""
}

func (r *physicalPlanRenderer) renderAggregateValue(expression ir.PhysicalExpression, items string, perItem bool) (string, error) {
	if !perItem {
		if expression.Extract != nil && expression.Extract.Prepared != nil {
			return "(FOR __loom_prepared_value IN " + expression.Extract.Prepared.SetVariable + " RETURN __loom_prepared_value." + expression.Extract.Prepared.Field + ")", nil
		}
		return r.renderExpression(expression)
	}
	if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
		return "", fmt.Errorf("aggregate predicates require an extract value expression")
	}
	item := r.navigation().newInternalVariable("aggregate_value_item")
	value, err := r.renderAggregateItemValue(expression, item)
	if err != nil {
		return "", err
	}
	return "(FOR " + item + " IN " + items + " RETURN " + value + ")", nil
}

func (r *physicalPlanRenderer) renderAggregateItemValue(expression ir.PhysicalExpression, item string) (string, error) {
	clone := expression
	extract := *expression.Extract
	if extract.Prepared == nil {
		rebindPhysicalExtractSource(&extract, ir.PhysicalValue{Variable: item, Path: []string{"payload"}})
	}
	clone.Extract = &extract
	previousPreparedItem := r.preparedItem
	r.preparedItem = item
	value, err := r.renderExtract(clone)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", err
	}
	return value, nil
}
