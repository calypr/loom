package aql

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionGroupCollection(group *ir.PhysicalStageGroup, inputRowVariable string, keys groupKeyRendering, countRowsOnly, aggregateRowValues bool) ([]string, error) {
	lines := keys.lines
	collectKeys := keys.collectKeys
	sortKeys := keys.sortKeys
	keyTypeAssertions := keys.keyTypeAssertions
	semanticDisplayVariable := keys.semanticDisplayVariable
	semanticDisplayAggregateVariable := keys.semanticDisplayAggregateVariable
	codingDisplayVariable := keys.codingDisplayVariable
	codingDisplayAggregateVariable := keys.codingDisplayAggregateVariable
	if countRowsOnly && !aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		collect := "COLLECT"
		if len(collectKeys) != 0 {
			collect += " " + strings.Join(collectKeys, ", ")
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  %s AGGREGATE %s = SUM(1), %s = MIN(%s)", collect, countVariable, semanticDisplayAggregateVariable, semanticDisplayVariable))
		} else if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  %s AGGREGATE %s = SUM(1), %s = MIN(%s)", collect, countVariable, codingDisplayAggregateVariable, codingDisplayVariable))
		} else {
			lines = append(lines, fmt.Sprintf("  %s WITH COUNT INTO %s", collect, countVariable))
		}
		lines = append(lines, keyTypeAssertions...)
		if len(sortKeys) != 0 {
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		for _, aggregate := range group.Aggregates {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, countVariable))
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.SemanticKey.LabelVariable, semanticDisplayAggregateVariable, semanticDisplayAggregateVariable, group.SemanticKey.TupleVariable))
		}
		if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.CodingKey.LabelVariable, codingDisplayAggregateVariable, codingDisplayAggregateVariable, group.CodingKey.TupleVariable))
		}
	} else if aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		aggregateParts := []string{fmt.Sprintf("%s = SUM(%s != null ? 1 : 0)", countVariable, inputRowVariable)}
		if group.SemanticKey != nil {
			aggregateParts = append(aggregateParts, fmt.Sprintf("%s = MIN(%s)", semanticDisplayAggregateVariable, semanticDisplayVariable))
		}
		if group.CodingKey != nil {
			aggregateParts = append(aggregateParts, fmt.Sprintf("%s = MIN(%s)", codingDisplayAggregateVariable, codingDisplayVariable))
		}
		rowValueVariables := make([]string, 0, len(group.RowValues))
		for index, rowValue := range group.RowValues {
			aggregateVariable, aggregateExpression, outputLine, err := r.renderConstructionGroupRowValueAggregate(rowValue, inputRowVariable, index)
			if err != nil {
				return nil, fmt.Errorf("row value %q: %w", rowValue.Output, err)
			}
			aggregateParts = append(aggregateParts, aggregateVariable+" = UNIQUE("+aggregateExpression+")")
			rowValueVariables = append(rowValueVariables, outputLine)
		}
		collect := "COLLECT"
		if len(collectKeys) != 0 {
			collect += " " + strings.Join(collectKeys, ", ")
		}
		lines = append(lines, "  "+collect+" AGGREGATE "+strings.Join(aggregateParts, ", "))
		lines = append(lines, keyTypeAssertions...)
		if len(sortKeys) != 0 {
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		for _, aggregate := range group.Aggregates {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, countVariable))
		}
		if group.SemanticKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.SemanticKey.LabelVariable, semanticDisplayAggregateVariable, semanticDisplayAggregateVariable, group.SemanticKey.TupleVariable))
		}
		if group.CodingKey != nil {
			lines = append(lines, fmt.Sprintf("  LET %s = (%s != null ? %s : %s[2])", group.CodingKey.LabelVariable, codingDisplayAggregateVariable, codingDisplayAggregateVariable, group.CodingKey.TupleVariable))
		}
		lines = append(lines, rowValueVariables...)
	} else if len(group.Aggregates) == 0 && len(group.RowValues) == 0 && group.RootContributorInputColumn == "" {

		if len(collectKeys) == 0 {
			return nil, fmt.Errorf("keyed Group has no collect keys")
		}
		collect := "  COLLECT " + strings.Join(collectKeys, ", ")
		if group.DynamicRuntimeKeysVariable != "" {
			collect += fmt.Sprintf(" AGGREGATE %s = UNIQUE(%s != null ? (%s[\"__loom_dynamic_runtime_keys\"] != null ? %s[\"__loom_dynamic_runtime_keys\"] : []) : [])", group.DynamicRuntimeKeysVariable, inputRowVariable, inputRowVariable, inputRowVariable)
		}
		lines = append(lines, collect)
		lines = append(lines, keyTypeAssertions...)
		lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
	} else {
		groupMembers := fmt.Sprintf("{row: %s, present: %s != null}", inputRowVariable, inputRowVariable)
		if group.SemanticKey != nil {
			groupMembers = fmt.Sprintf("{row: %s, present: %s != null, semanticDisplay: %s}", inputRowVariable, inputRowVariable, semanticDisplayVariable)
		} else if group.CodingKey != nil {
			groupMembers = fmt.Sprintf("{row: %s, present: %s != null, codingDisplay: %s}", inputRowVariable, inputRowVariable, codingDisplayVariable)
		}
		if len(collectKeys) == 0 {
			allRowsVariable := r.newInternalVariable("construction_group_all_rows")
			lines = append(lines, fmt.Sprintf("  COLLECT %s = null INTO %s = %s", allRowsVariable, group.GroupRowsVariable, groupMembers))
		} else {
			lines = append(lines, fmt.Sprintf("  COLLECT %s INTO %s = %s", strings.Join(collectKeys, ", "), group.GroupRowsVariable, groupMembers))
			lines = append(lines, keyTypeAssertions...)
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		if group.SemanticKey != nil {
			displays := r.newInternalVariable("construction_group_semantic_displays")
			member := r.newInternalVariable("construction_group_semantic_display")
			lines = append(lines,
				fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.semanticDisplay != null RETURN %s.semanticDisplay)", displays, member, group.GroupRowsVariable, member, member),
				fmt.Sprintf("  LET %s = (LENGTH(%s) != 0 ? MIN(%s) : %s[2])", group.SemanticKey.LabelVariable, displays, displays, group.SemanticKey.TupleVariable),
			)
		} else if group.CodingKey != nil {
			displays := r.newInternalVariable("construction_group_coding_displays")
			member := r.newInternalVariable("construction_group_coding_display")
			lines = append(lines,
				fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.codingDisplay != null RETURN %s.codingDisplay)", displays, member, group.GroupRowsVariable, member, member),
				fmt.Sprintf("  LET %s = (LENGTH(%s) != 0 ? MIN(%s) : %s[2])", group.CodingKey.LabelVariable, displays, displays, group.CodingKey.TupleVariable),
			)
		}
		for index, aggregate := range group.Aggregates {
			expression, err := r.renderConstructionGroupAggregate(group, aggregate, index)
			if err != nil {
				return nil, fmt.Errorf("aggregate %q: %w", aggregate.Output, err)
			}
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, expression))
		}
		rowValueLines, rowValueErr := r.renderConstructionRowValueLets(group.RowValues, group.GroupRowsVariable, "row")
		if rowValueErr != nil {
			return nil, rowValueErr
		}
		lines = append(lines, rowValueLines...)
		if group.RootContributorInputColumn != "" {
			contributorLines, contributorErr := r.renderRootContributorSet(
				group.RootContributorInputColumn, group.RootContributorInputMany,
				group.GroupRowsVariable, "row", group.RootContributorVariable,
			)
			if contributorErr != nil {
				return nil, contributorErr
			}
			lines = append(lines, contributorLines...)
		}
		if group.DynamicRuntimeKeysVariable != "" {
			member := r.newInternalVariable("construction_group_dynamic_member")

			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.present RETURN %s.row[\"__loom_dynamic_runtime_keys\"])", group.DynamicRuntimeKeysVariable, member, group.GroupRowsVariable, member, member))
		}
	}
	return lines, nil
}

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
			rebindPhysicalExtractSource(&extract, ir.PhysicalValue{Variable: item, Path: []string{"payload"}})
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
			rebindPhysicalExtractSource(&extract, ir.PhysicalValue{Variable: item, Path: []string{"payload"}})
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
