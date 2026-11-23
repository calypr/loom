package aql

import (
	"fmt"
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
		countVariable := r.navigation().newInternalVariable("construction_group_count_rows")
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
		countVariable := r.navigation().newInternalVariable("construction_group_count_rows")
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
			allRowsVariable := r.navigation().newInternalVariable("construction_group_all_rows")
			lines = append(lines, fmt.Sprintf("  COLLECT %s = null INTO %s = %s", allRowsVariable, group.GroupRowsVariable, groupMembers))
		} else {
			lines = append(lines, fmt.Sprintf("  COLLECT %s INTO %s = %s", strings.Join(collectKeys, ", "), group.GroupRowsVariable, groupMembers))
			lines = append(lines, keyTypeAssertions...)
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		if group.SemanticKey != nil {
			displays := r.navigation().newInternalVariable("construction_group_semantic_displays")
			member := r.navigation().newInternalVariable("construction_group_semantic_display")
			lines = append(lines,
				fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.semanticDisplay != null RETURN %s.semanticDisplay)", displays, member, group.GroupRowsVariable, member, member),
				fmt.Sprintf("  LET %s = (LENGTH(%s) != 0 ? MIN(%s) : %s[2])", group.SemanticKey.LabelVariable, displays, displays, group.SemanticKey.TupleVariable),
			)
		} else if group.CodingKey != nil {
			displays := r.navigation().newInternalVariable("construction_group_coding_displays")
			member := r.navigation().newInternalVariable("construction_group_coding_display")
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
			member := r.navigation().newInternalVariable("construction_group_dynamic_member")

			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s FILTER %s.present RETURN %s.row[\"__loom_dynamic_runtime_keys\"])", group.DynamicRuntimeKeysVariable, member, group.GroupRowsVariable, member, member))
		}
	}
	return lines, nil
}
