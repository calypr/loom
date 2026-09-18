package aql

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (r *physicalPlanRenderer) renderObject(expression ir.PhysicalExpression) (string, error) {
	object := expression.Object
	if object == nil {
		return "", fmt.Errorf("OBJECT expression is missing payload")
	}
	fields := append([]ir.PhysicalExpressionProjection(nil), object.Fields...)
	sort.SliceStable(fields, func(left, right int) bool {
		return fields[left].Name < fields[right].Name
	})

	type renderedField struct {
		nameKey string
		value   string
		omit    bool
	}
	rendered := make([]renderedField, 0, len(fields))
	for index, field := range fields {
		value, err := r.renderExpression(field.Expression)
		if err != nil {
			return "", fmt.Errorf("object field %q: %w", field.Name, err)
		}
		nameKey := r.newInternalBindKey("object_field_" + strconv.Itoa(index) + "_name")
		r.bindVars[nameKey] = field.Name
		rendered = append(rendered, renderedField{
			nameKey: nameKey,
			value:   value,
			omit:    field.Expression.NullBehavior == ir.PhysicalOmitNulls,
		})
	}

	hasOmittedField := false
	for _, field := range rendered {
		if field.omit {
			hasOmittedField = true
			break
		}
	}
	if !hasOmittedField {
		parts := make([]string, 0, len(rendered))
		for _, field := range rendered {
			parts = append(parts, fmt.Sprintf("[@%s]: %s", field.nameKey, field.value))
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}

	items := make([]string, 0, len(rendered))
	for _, field := range rendered {
		items = append(items, fmt.Sprintf("{ __loom_object_name: @%s, __loom_object_value: %s, __loom_object_omit: %t }", field.nameKey, field.value, field.omit))
	}
	return fmt.Sprintf(`MERGE(
  FOR __loom_object_field IN [%s]
    FILTER __loom_object_field.__loom_object_omit == false OR __loom_object_field.__loom_object_value != null
    RETURN { [__loom_object_field.__loom_object_name]: __loom_object_field.__loom_object_value }
)`, strings.Join(items, ", ")), nil
}

// renderSlice emits a correlated, bounded array projection. Sort and the
// _key tie-break are rendered inside the subquery so representative values
// are deterministic even when traversal order changes.
func (r *physicalPlanRenderer) renderSlice(expression ir.PhysicalExpression) (string, error) {
	slice := expression.Slice
	if slice == nil {
		return "", fmt.Errorf("SLICE expression is missing payload")
	}
	source, err := r.renderValue(slice.Source)
	if err != nil {
		return "", err
	}
	items := source
	preparedVariable := slicePreparedVariable(slice)
	if preparedVariable != "" {
		items = preparedVariable
	}
	setSource := slice.Source.Variable != "" && r.setVariables[slice.Source.Variable] != ""
	if !setSource {
		items = "[" + source + "]"
	}
	item := r.newInternalVariable("slice_item")
	lines := []string{"(FOR " + item + " IN " + items}
	if slice.Predicate != nil {
		if slice.Predicate.Kind != ir.PhysicalComparisonPredicate || slice.Predicate.Comparison == nil {
			return "", fmt.Errorf("slice predicate must be a comparison")
		}
		comparison := *slice.Predicate.Comparison
		if comparison.LeftExpression != nil && comparison.LeftExpression.Extract != nil {
			left := *comparison.LeftExpression
			extract := *left.Extract
			extract.Source = ir.PhysicalValue{Variable: item, Path: []string{"payload"}}
			left.Extract = &extract
			comparison.LeftExpression = &left
		} else {
			comparison.Left = ir.PhysicalValue{Variable: item}
		}
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		predicate, err := r.renderPredicate(comparison)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", err
		}
		lines = append(lines, "  FILTER "+predicate)
	}
	if slice.Sort == nil {
		return "", fmt.Errorf("slice requires sort expression")
	}
	sortExpression := *slice.Sort
	if sortExpression.Kind == ir.PhysicalValueExpression && sortExpression.Value != nil {
		value := *sortExpression.Value
		value.Variable = item
		value.BindKey = ""
		sortExpression.Value = &value
	}
	sortValue, err := r.renderExpression(sortExpression)
	if err != nil {
		return "", err
	}
	lines = append(lines, "  SORT "+sortValue+" ASC, "+item+"._key ASC")
	lines = append(lines, "  LIMIT @"+slice.LimitBindKey)
	fields := make([]string, 0, len(slice.Projections))
	for index, projection := range slice.Projections {
		projectionExpression := projection.Expression
		if projectionExpression.Kind == ir.PhysicalExtractExpression && projectionExpression.Extract != nil {
			extract := *projectionExpression.Extract
			extract.Source = ir.PhysicalValue{Variable: item, Path: []string{"payload"}}
			projectionExpression.Extract = &extract
		}
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		value, err := r.renderExpression(projectionExpression)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", fmt.Errorf("slice projection %d (%s): %w", index, projection.Name, err)
		}
		nameKey := r.newInternalBindKey("slice_projection_" + strconv.Itoa(index) + "_name")
		r.bindVars[nameKey] = projection.Name
		fields = append(fields, "["+"@"+nameKey+"]: "+value)
	}
	lines = append(lines, "  RETURN { "+strings.Join(fields, ", ")+" }")
	return strings.Join(lines, "\n") + "\n)", nil
}

func slicePreparedVariable(slice *ir.PhysicalSlice) string {
	if slice == nil {
		return ""
	}
	if slice.Predicate != nil && slice.Predicate.Comparison != nil && slice.Predicate.Comparison.LeftExpression != nil && slice.Predicate.Comparison.LeftExpression.Extract != nil && slice.Predicate.Comparison.LeftExpression.Extract.Prepared != nil {
		return slice.Predicate.Comparison.LeftExpression.Extract.Prepared.SetVariable
	}
	for _, projection := range slice.Projections {
		if projection.Expression.Extract != nil && projection.Expression.Extract.Prepared != nil {
			return projection.Expression.Extract.Prepared.SetVariable
		}
	}
	return ""
}

// renderPivot emits a bounded sparse object keyed by the requested catalog
// columns. Values from all matching resources are combined per key and reduced
// deterministically to the first sorted value while keeping selectors and
// column values typed.
func (r *physicalPlanRenderer) renderPivot(expression ir.PhysicalExpression) (string, error) {
	pivot := expression.Pivot
	if pivot == nil {
		return "", fmt.Errorf("PIVOT expression is missing payload")
	}
	if pivot.Correlation != nil {
		return r.renderCorrelatedPivot(*pivot.Correlation, pivot.ColumnsBindKey, pivot.StringifyValue, pivot.FlattenSingleColumn, pivot.ProjectionMode)
	}
	if _, collection := r.collectionKeys[pivot.ColumnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", pivot.ColumnsBindKey)
	}
	columns, ok := r.bindVars[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", pivot.ColumnsBindKey)
	}
	items, err := r.renderValue(pivot.Source)
	if err != nil {
		return "", err
	}
	if pivot.Source.Variable == "" || r.setVariables[pivot.Source.Variable] == "" {
		items = "[" + items + "]"
	}
	if pivot.PreparedKey != nil {
		items = pivot.PreparedKey.SetVariable
	}
	item := r.newInternalVariable("pivot_item")
	itemExpression := item
	itemLoop := fmt.Sprintf("FOR %s IN %s", item, items)
	if pivot.ItemResourceType != "" {
		if pivot.ItemSource.CanonicalPath() == "" {
			return "", fmt.Errorf("pivot item source is required when item resource type is set")
		}
		itemValues, sourceErr := r.renderSelectorArrayFromSource(item+".payload", pivot.ItemSource, false, false)
		if sourceErr != nil {
			return "", fmt.Errorf("pivot item source: %w", sourceErr)
		}
		itemExpression = r.newInternalVariable("pivot_item_value")
		// A selector ending in an iterated array is represented by the
		// selector renderer as one subquery row containing that array.  Pivot
		// item sources need the repeated elements themselves so key/value
		// selectors run against each component/backbone item, not against the
		// wrapper array.  Flatten exactly that one selector-result layer;
		// deeper nesting remains owned by the selector's own iteration steps.
		itemLoop += fmt.Sprintf("\n      FOR %s IN FLATTEN(%s)", itemExpression, itemValues)
	}
	previousPreparedItem := r.preparedItem
	r.preparedItem = itemExpression
	keyExpr, err := r.renderPivotSelector(item, itemExpression, pivot.PreparedKey, pivot.KeySelector, pivot.ItemResourceType != "")
	if err != nil {
		r.preparedItem = previousPreparedItem
		return "", err
	}
	valueSelectors := append([]spec.Selector{pivot.ValueSelector}, pivot.ValueFallbacks...)
	valueExpressions := make([]string, 0, len(valueSelectors))
	for _, selector := range valueSelectors {
		value, valueErr := r.renderPivotSelector(item, itemExpression, nil, selector, pivot.ItemResourceType != "")
		if valueErr != nil {
			r.preparedItem = previousPreparedItem
			return "", valueErr
		}
		valueExpressions = append(valueExpressions, value)
	}
	if pivot.PreparedValue != nil {
		valueExpressions = []string{itemExpression + "." + pivot.PreparedValue.Field}
	}
	r.preparedItem = previousPreparedItem
	if len(valueExpressions) == 0 {
		return "", fmt.Errorf("pivot value selector is required")
	}
	valueExpr := valueExpressions[0]
	if len(valueExpressions) > 1 {
		valueExpr = "FIRST(FOR __pivot_candidate IN [" + strings.Join(valueExpressions, ", ") + "] FILTER LENGTH(__pivot_candidate) > 0 RETURN __pivot_candidate)"
	}
	pairs := fmt.Sprintf(`FOR __pair IN (
    %s
      LET __pivot_keys = UNIQUE(%s)
      LET __pivot_values = %s
      FILTER LENGTH(__pivot_values) > 0
      FOR __pivot_key IN __pivot_keys
        FILTER POSITION(@%s, __pivot_key)
        RETURN { key: __pivot_key, values: __pivot_values }
  )`, itemLoop, keyExpr, valueExpr, pivot.ColumnsBindKey)
	if pivot.FlattenSingleColumn {
		value := "FIRST(__pivot_flat_values)"
		if pivot.StringifyValue {
			value = "TO_STRING(" + value + ")"
		}
		return fmt.Sprintf(`FIRST(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN %s
)`, pairs, value), nil
	}
	value := "FIRST(__pivot_flat_values)"
	if pivot.StringifyValue {
		value = "TO_STRING(" + value + ")"
	}
	return fmt.Sprintf(`MERGE(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN { [__pivot_key]: %s }
)`, pairs, value), nil
}

func (r *physicalPlanRenderer) renderCorrelatedPivot(correlation ir.PhysicalCorrelation, columnsBindKey string, stringify, flattenSingle bool, projectionMode string) (string, error) {
	if len(correlation.ExtensionURLSelectors) > 0 {
		return r.renderExtensionCorrelatedPivot(correlation, columnsBindKey, stringify, flattenSingle, projectionMode)
	}
	if columnsBindKey == "" {
		return "", fmt.Errorf("correlated pivot columns bind is required")
	}
	if _, collection := r.collectionKeys[columnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", columnsBindKey)
	}
	columns, ok := r.bindVars[columnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", columnsBindKey)
	}
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	owner := r.newInternalVariable("correlation_pivot_owner")
	coding := r.newInternalVariable("correlation_pivot_coding")
	codings, err := r.renderSelectorArrayFromSource(owner, correlation.KeySelector, false, false)
	if err != nil {
		return "", err
	}
	system, err := r.renderCorrelationScalar(coding, correlation.SystemSelector)
	if err != nil {
		return "", err
	}
	code, err := r.renderCorrelationScalar(coding, correlation.CodeSelector)
	if err != nil {
		return "", err
	}
	values, err := r.renderCorrelationValues(owner, correlation.ValueSelector, correlation.ValueFallbacks)
	if err != nil {
		return "", err
	}
	unsupportedValues, err := r.renderCorrelationUnsupportedChoiceValues(owner, correlation)
	if err != nil {
		return "", err
	}
	value, err := renderCorrelatedReduction(projectionMode, stringify, correlation.ValuePrimitive)
	if err != nil {
		return "", err
	}
	// Reduce repeated Coding aliases within each owner before the outer
	// reduction. Distinct owners remain separate, so ALL retains multiplicity
	// across components while duplicate Coding entries cannot duplicate a
	// component's value.
	pairs := fmt.Sprintf(`FOR %s IN %s
  LET __correlation_owner_pairs = (
    FOR %s IN FLATTEN(%s)
      LET __correlation_system = %s
      LET __correlation_code = %s
      FILTER __correlation_system != null AND __correlation_system != ""
      FILTER __correlation_code != null AND __correlation_code != ""
      FILTER __correlation_system == @%s
      FILTER POSITION(@%s, __correlation_code)
      LET __correlation_values = %s
      LET __correlation_flat_values = FLATTEN(__correlation_values)
      LET __correlation_unsupported_values = %s
      FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
      RETURN { key: __correlation_code, values: __correlation_values, unsupported: __correlation_unsupported_values }
  )
  FOR __correlation_owner_pair IN (
    FOR __correlation_candidate IN __correlation_owner_pairs
      COLLECT __correlation_owner_key = __correlation_candidate.key INTO __correlation_owner_group
        LET __correlation_owner_values = UNIQUE(FLATTEN(__correlation_owner_group[*].__correlation_candidate.values))
        LET __correlation_owner_unsupported = UNIQUE(FLATTEN(__correlation_owner_group[*].__correlation_candidate.unsupported))
        RETURN { key: __correlation_owner_key, values: __correlation_owner_values, unsupported: __correlation_owner_unsupported }
  )
    RETURN __correlation_owner_pair`, owner, owners, coding, codings, system, code, correlation.SystemBindKey, columnsBindKey, values, unsupportedValues)
	if flattenSingle {
		return fmt.Sprintf(`FIRST(
	  FOR __correlation_pair IN (
	    %s
	  )
	  COLLECT __correlation_key = __correlation_pair.key INTO __correlation_group
	    LET __correlation_flat_values = FLATTEN(__correlation_group[*].__correlation_pair.values)
	    LET __correlation_unsupported_values = FLATTEN(__correlation_group[*].__correlation_pair.unsupported)
	    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
	    RETURN %s
)`, pairs, value), nil
	}
	return fmt.Sprintf(`MERGE(
  FOR __correlation_pair IN (
    %s
	  )
	  COLLECT __correlation_key = __correlation_pair.key INTO __correlation_group
	    LET __correlation_flat_values = FLATTEN(__correlation_group[*].__correlation_pair.values)
	    LET __correlation_unsupported_values = FLATTEN(__correlation_group[*].__correlation_pair.unsupported)
	    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0
	    RETURN { [__correlation_key]: %s }
)`, pairs, value), nil
}

// renderPivotSelector evaluates a selector against either the resource
// document or a correlated repeated item. Keeping the item scope explicit
// prevents keys and values from different repeated elements being paired.
func (r *physicalPlanRenderer) renderPivotSelector(resourceItem, item string, prepared *ir.PhysicalPreparedReference, selector spec.Selector, itemScoped bool) (string, error) {
	if prepared != nil {
		return item + "." + prepared.Field, nil
	}
	source := resourceItem + ".payload"
	if itemScoped {
		source = item
	}
	return r.renderSelectorArrayFromSource(source, selector, false, false)
}

// renderAggregate emits reductions over either a correlated PhysicalSet or a
// singleton root document. The source is kept typed in the IR; this method is
// the only place that decides the AQL collection expression (`set` versus
// `[root]`).
func (r *physicalPlanRenderer) renderAggregate(expression ir.PhysicalExpression) (string, error) {
	aggregate := expression.Aggregate
	if aggregate == nil {
		return "", fmt.Errorf("AGGREGATE expression is missing payload")
	}
	source, err := r.renderValue(aggregate.Source)
	if err != nil {
		return "", err
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
			return "", fmt.Errorf("aggregate predicate must be a comparison")
		}
		item := r.newInternalVariable("aggregate_item")
		comparison := *aggregate.Predicate.Comparison
		if comparison.LeftExpression == nil || comparison.LeftExpression.Extract == nil {
			return "", fmt.Errorf("aggregate predicate must extract a selector")
		}
		left := *comparison.LeftExpression
		extract := *left.Extract
		if extract.Prepared == nil {
			extract.Source = ir.PhysicalValue{Variable: item, Path: []string{"payload"}}
		}
		left.Extract = &extract
		comparison.LeftExpression = &left
		previousPreparedItem := r.preparedItem
		r.preparedItem = item
		predicate, err := r.renderPredicate(comparison)
		r.preparedItem = previousPreparedItem
		if err != nil {
			return "", err
		}
		items = "(FOR " + item + " IN " + items + " FILTER " + predicate + " RETURN " + item + ")"
	}
	switch aggregate.Operation {
	case ir.PhysicalCountAggregate:
		if aggregate.Value != nil {
			values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
			if err != nil {
				return "", err
			}
			item := r.newInternalVariable("aggregate_count_value")
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
		item := r.newInternalVariable("aggregate_required_value")
		return "LENGTH(" + required + ") == LENGTH(FOR " + item + " IN " + required + " FILTER POSITION(FLATTEN(" + values + "), " + item + ") RETURN 1)", nil
	}
	return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
}

func (r *physicalPlanRenderer) renderFirstOrderedAggregate(aggregate *ir.PhysicalAggregate, items string) (string, error) {
	if aggregate.Temporal == nil || aggregate.Value == nil {
		return "", fmt.Errorf("FIRST_ORDERED requires value and temporal policy")
	}
	item := r.newInternalVariable("temporal_item")
	value, err := r.renderAggregateItemValue(*aggregate.Value, item)
	if err != nil {
		return "", err
	}
	value = "FIRST(FLATTEN([" + value + "]))"
	timestamp, err := r.renderAggregateItemValue(aggregate.Temporal.Timestamp, item)
	if err != nil {
		return "", err
	}
	timestamp = "FIRST(FLATTEN([" + timestamp + "]))"
	anchor, err := r.renderExpression(aggregate.Temporal.Anchor)
	if err != nil {
		return "", err
	}
	patternKey := r.newInternalBindKey("temporal_instant_pattern")
	lowerKey := r.newInternalBindKey("temporal_lower_offset_seconds")
	upperKey := r.newInternalBindKey("temporal_upper_offset_seconds")
	r.bindVars[patternKey] = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$`
	r.bindVars[lowerKey] = aggregate.Temporal.LowerOffset
	r.bindVars[upperKey] = aggregate.Temporal.UpperOffset
	lowerOperator, upperOperator := ">", "<"
	if aggregate.Temporal.LowerInclusive {
		lowerOperator = ">="
	}
	if aggregate.Temporal.UpperInclusive {
		upperOperator = "<="
	}
	direction := strings.ToUpper(aggregate.Temporal.Direction)
	anchorVariable := r.newInternalVariable("temporal_anchor")
	candidatesVariable := r.newInternalVariable("temporal_candidates")
	selectedVariable := r.newInternalVariable("temporal_selected")
	scopeVariable := r.newInternalVariable("temporal_scope")
	candidates := "(FOR " + item + " IN " + items +
		" LET __loom_temporal_value = " + value +
		" LET __loom_temporal_timestamp = " + timestamp +
		" FILTER __loom_temporal_value != null" +
		" FILTER ASSERT(__loom_temporal_timestamp != null AND REGEX_TEST(TO_STRING(__loom_temporal_timestamp), @" + patternKey + "), \"TEMPORAL_PRECISION_UNSUPPORTED\")" +
		" FILTER DATE_TIMESTAMP(__loom_temporal_timestamp) " + lowerOperator + " DATE_TIMESTAMP(DATE_ADD(" + anchorVariable + ", @" + lowerKey + ", \"second\"))" +
		" FILTER DATE_TIMESTAMP(__loom_temporal_timestamp) " + upperOperator + " DATE_TIMESTAMP(DATE_ADD(" + anchorVariable + ", @" + upperKey + ", \"second\"))" +
		" SORT DATE_TIMESTAMP(__loom_temporal_timestamp) " + direction + ", " + item + "._key ASC" +
		" RETURN { value: __loom_temporal_value, timestamp: __loom_temporal_timestamp })"
	result := selectedVariable + " == null ? null : " + selectedVariable + ".value"
	if aggregate.Temporal.TiePolicy == "REQUIRE_UNIQUE" {
		tie := r.newInternalVariable("temporal_tie")
		ties := "LENGTH(FOR " + tie + " IN " + candidatesVariable + " FILTER DATE_TIMESTAMP(" + tie + ".timestamp) == DATE_TIMESTAMP(" + selectedVariable + ".timestamp) LIMIT 2 RETURN 1)"
		result = selectedVariable + " == null ? null : (ASSERT(" + ties + " <= 1, \"TEMPORAL_TIE_AMBIGUOUS\") ? " + selectedVariable + ".value : null)"
	}
	return "FIRST(FOR " + scopeVariable + " IN [1]" +
		" LET " + anchorVariable + " = " + anchor +
		" FILTER ASSERT(" + anchorVariable + " != null AND REGEX_TEST(TO_STRING(" + anchorVariable + "), @" + patternKey + "), \"TEMPORAL_ANCHOR_INVALID\")" +
		" LET " + candidatesVariable + " = " + candidates +
		" LET " + selectedVariable + " = FIRST(" + candidatesVariable + ")" +
		" RETURN " + result + ")", nil
}

func aggregatePreparedVariable(aggregate *ir.PhysicalAggregate) string {
	if aggregate == nil {
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
	item := r.newInternalVariable("aggregate_value_item")
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
		extract.Source = ir.PhysicalValue{Variable: item, Path: []string{"payload"}}
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

// renderExtensionCorrelatedPivot keeps each nested extension URL check in the
// loop that owns the next extension object. A leaf URL can therefore never
// match a value beneath a different ancestor URL.
func (r *physicalPlanRenderer) renderExtensionCorrelatedPivot(correlation ir.PhysicalCorrelation, columnsBindKey string, stringify, flattenSingle bool, projectionMode string) (string, error) {
	if columnsBindKey == "" {
		return "", fmt.Errorf("extension pivot columns bind is required")
	}
	if _, collection := r.collectionKeys[columnsBindKey]; collection {
		return "", fmt.Errorf("pivot columns bind %q cannot be a collection bind", columnsBindKey)
	}
	columns, ok := r.bindVars[columnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return "", fmt.Errorf("pivot columns bind %q is not a non-empty []string", columnsBindKey)
	}
	owners, err := r.renderCorrelationOwners(correlation.Source, correlation.OwnerSelector)
	if err != nil {
		return "", err
	}
	if len(correlation.ExtensionURLSelectors) != len(correlation.ExtensionURLBindKeys) {
		return "", fmt.Errorf("extension URL selectors and bind keys must have equal length")
	}
	extensionSelector, err := spec.ParseSelector("extension[]")
	if err != nil {
		return "", err
	}
	current := "__extension_base"
	lines := []string{"FOR " + current + " IN " + owners}
	for index, urlSelector := range correlation.ExtensionURLSelectors {
		extension := fmt.Sprintf("__correlation_extension_%d", index)
		extensions, selectorErr := r.renderSelectorArrayFromSource(current, extensionSelector, false, false)
		if selectorErr != nil {
			return "", fmt.Errorf("extension owner selector %d: %w", index, selectorErr)
		}
		lines = append(lines, "  FOR "+extension+" IN FLATTEN("+extensions+")")
		url, scalarErr := r.renderCorrelationScalar(extension, urlSelector)
		if scalarErr != nil {
			return "", fmt.Errorf("extension URL selector %d: %w", index, scalarErr)
		}
		lines = append(lines, fmt.Sprintf("    FILTER %s != null AND %s == @%s", url, url, correlation.ExtensionURLBindKeys[index]))
		current = extension
	}
	values, err := r.renderCorrelationValues(current, correlation.ValueSelector, correlation.ValueFallbacks)
	if err != nil {
		return "", err
	}
	unsupportedValues, err := r.renderCorrelationUnsupportedChoiceValues(current, correlation)
	if err != nil {
		return "", err
	}
	lines = append(lines,
		"    LET __correlation_values = "+values,
		"    LET __correlation_flat_values = FLATTEN(__correlation_values)",
		"    LET __correlation_unsupported_values = "+unsupportedValues,
		"    FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0",
		"    RETURN { values: __correlation_values, unsupported: __correlation_unsupported_values }")
	pairs := strings.Join(lines, "\n")
	value, err := renderCorrelatedReduction(projectionMode, stringify, correlation.ValuePrimitive)
	if err != nil {
		return "", err
	}
	// Aggregate every matched terminal extension before applying the declared
	// reduction. Emitting one object per owner would make MERGE overwrite
	// repeated matches and would silently turn VALUE multiplicity into FIRST.
	aggregation := fmt.Sprintf("FOR __extension_result IN [1]\n  LET __extension_pairs = (\n    %s\n  )\n  LET __correlation_flat_values = FLATTEN(__extension_pairs[*].values)\n  LET __correlation_unsupported_values = FLATTEN(__extension_pairs[*].unsupported)\n  FILTER LENGTH(__correlation_flat_values) > 0 OR LENGTH(__correlation_unsupported_values) > 0\n  RETURN ", pairs)
	if flattenSingle {
		return "FIRST(\n  " + aggregation + value + "\n)", nil
	}
	return "MERGE(\n  " + aggregation + "{ [FIRST(@" + columnsBindKey + ")]: " + value + " }\n)", nil
}

// renderCorrelatedReduction is shared by Coding and Extension correlations so
// every projection mode has one explicit multiplicity and invalid-choice
// contract. ALL/DISTINCT preserve arrays; VALUE reports multiplicity; FIRST is
// deterministic but intentionally lossy.
func renderCorrelatedReduction(projectionMode string, stringify bool, primitive string) (string, error) {
	mode := strings.ToUpper(strings.TrimSpace(projectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	valuePrimitive := strings.ToLower(strings.TrimSpace(primitive))
	stringConversion := stringify && valuePrimitive != "" && valuePrimitive != "string"
	value := "__correlation_flat_values"
	switch mode {
	case "ALL":
		if stringConversion {
			value = "(FOR __correlation_value IN __correlation_flat_values RETURN TO_STRING(__correlation_value))"
		}
	case "DISTINCT":
		if stringConversion {
			value = "(FOR __correlation_value IN __correlation_flat_values RETURN TO_STRING(__correlation_value))"
		}
		value = "SORTED_UNIQUE(" + value + ")"
	case "VALUE":
		first := "FIRST(__correlation_flat_values)"
		if stringConversion {
			first = "TO_STRING(" + first + ")"
		}
		value = `LENGTH(__correlation_flat_values) == 1 ? ` + first + ` : { status: "INVALID_MULTIPLE_VALUES", raw: __correlation_flat_values }`
	case "FIRST":
		value = "FIRST(SORTED(__correlation_flat_values))"
		if stringConversion {
			value = "TO_STRING(" + value + ")"
		}
	default:
		return "", fmt.Errorf("correlated pivot projection mode %q is unsupported", projectionMode)
	}
	return `LENGTH(__correlation_unsupported_values) > 0 ? { status: "INVALID_CHOICE_ARM", raw: __correlation_unsupported_values } : ` + value, nil
}
