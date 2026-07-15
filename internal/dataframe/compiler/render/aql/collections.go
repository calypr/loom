package aql

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func (r *physicalPlanRenderer) renderObject(expression PhysicalExpression) (string, error) {
	object := expression.Object
	if object == nil {
		return "", fmt.Errorf("OBJECT expression is missing payload")
	}
	fields := append([]PhysicalExpressionProjection(nil), object.Fields...)
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
			omit:    field.Expression.NullBehavior == PhysicalOmitNulls,
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
func (r *physicalPlanRenderer) renderSlice(expression PhysicalExpression) (string, error) {
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
		if slice.Predicate.Kind != PhysicalComparisonPredicate || slice.Predicate.Comparison == nil {
			return "", fmt.Errorf("slice predicate must be a comparison")
		}
		comparison := *slice.Predicate.Comparison
		if comparison.LeftExpression != nil && comparison.LeftExpression.Extract != nil {
			left := *comparison.LeftExpression
			extract := *left.Extract
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
			left.Extract = &extract
			comparison.LeftExpression = &left
		} else {
			comparison.Left = PhysicalValue{Variable: item}
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
	if sortExpression.Kind == PhysicalValueExpression && sortExpression.Value != nil {
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
		if projectionExpression.Kind == PhysicalExtractExpression && projectionExpression.Extract != nil {
			extract := *projectionExpression.Extract
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
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

func slicePreparedVariable(slice *PhysicalSlice) string {
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
func (r *physicalPlanRenderer) renderPivot(expression PhysicalExpression) (string, error) {
	pivot := expression.Pivot
	if pivot == nil {
		return "", fmt.Errorf("PIVOT expression is missing payload")
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
		itemValues, sourceErr := r.renderSelectorArrayFromSource(item+".payload", pivot.ItemSource, false)
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
	valueSelectors := append([]Selector{pivot.ValueSelector}, pivot.ValueFallbacks...)
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
		return fmt.Sprintf(`FIRST(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN FIRST(__pivot_flat_values)
)`, pairs), nil
	}
	return fmt.Sprintf(`MERGE(
  %s
  COLLECT __pivot_key = __pair.key INTO __pivot_group
    LET __pivot_flat_values = SORTED_UNIQUE(FLATTEN(__pivot_group[*].__pair.values))
    FILTER LENGTH(__pivot_flat_values) > 0
    RETURN { [__pivot_key]: FIRST(__pivot_flat_values) }
)`, pairs), nil
}

// renderPivotSelector evaluates a selector against either the resource
// document or a correlated repeated item. Keeping the item scope explicit
// prevents keys and values from different repeated elements being paired.
func (r *physicalPlanRenderer) renderPivotSelector(resourceItem, item string, prepared *PhysicalPreparedReference, selector Selector, itemScoped bool) (string, error) {
	if prepared != nil {
		return item + "." + prepared.Field, nil
	}
	source := resourceItem + ".payload"
	if itemScoped {
		source = item
	}
	return r.renderSelectorArrayFromSource(source, selector, false)
}

// renderAggregate emits reductions over either a correlated PhysicalSet or a
// singleton root document. The source is kept typed in the IR; this method is
// the only place that decides the AQL collection expression (`set` versus
// `[root]`).
func (r *physicalPlanRenderer) renderAggregate(expression PhysicalExpression) (string, error) {
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
		if aggregate.Predicate.Kind != PhysicalComparisonPredicate || aggregate.Predicate.Comparison == nil {
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
			extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
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
	case PhysicalCountAggregate:
		return "LENGTH(" + items + ")", nil
	case PhysicalExistsAggregate:
		if aggregate.Value == nil {
			return "LENGTH(" + items + ") > 0", nil
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		return "LENGTH(FOR __value IN FLATTEN(" + values + ") FILTER __value != null LIMIT 1 RETURN 1) > 0", nil
	case PhysicalCountDistinctAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
		if aggregate.Value == nil {
			return "", fmt.Errorf("aggregate operation %q requires a value expression", aggregate.Operation)
		}
		values, err := r.renderAggregateValue(*aggregate.Value, items, perItem)
		if err != nil {
			return "", err
		}
		flattened := "FLATTEN(" + values + ")"
		switch aggregate.Operation {
		case PhysicalCountDistinctAggregate:
			return "LENGTH(SORTED_UNIQUE(" + flattened + "))", nil
		case PhysicalDistinctValuesAggregate:
			return "SORTED_UNIQUE(" + flattened + ")", nil
		case PhysicalMinAggregate:
			return "MIN(" + flattened + ")", nil
		case PhysicalMaxAggregate:
			return "MAX(" + flattened + ")", nil
		case PhysicalFirstAggregate:
			return "FIRST(" + flattened + ")", nil
		}
	}
	return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
}

func aggregatePreparedVariable(aggregate *PhysicalAggregate) string {
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

func (r *physicalPlanRenderer) renderAggregateValue(expression PhysicalExpression, items string, perItem bool) (string, error) {
	if !perItem {
		if expression.Extract != nil && expression.Extract.Prepared != nil {
			return "(FOR __loom_prepared_value IN " + expression.Extract.Prepared.SetVariable + " RETURN __loom_prepared_value." + expression.Extract.Prepared.Field + ")", nil
		}
		return r.renderExpression(expression)
	}
	if expression.Kind != PhysicalExtractExpression || expression.Extract == nil {
		return "", fmt.Errorf("aggregate predicates require an extract value expression")
	}
	item := r.newInternalVariable("aggregate_value_item")
	clone := expression
	extract := *expression.Extract
	if extract.Prepared == nil {
		extract.Source = PhysicalValue{Variable: item, Path: []string{"payload"}}
	}
	clone.Extract = &extract
	previousPreparedItem := r.preparedItem
	r.preparedItem = item
	value, err := r.renderExtract(clone)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", err
	}
	return "(FOR " + item + " IN " + items + " RETURN " + value + ")", nil
}
