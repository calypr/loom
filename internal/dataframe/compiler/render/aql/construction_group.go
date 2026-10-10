package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type constructionGroupInput struct {
	RowsVariable     string
	SourceRowInScope bool
}

func (r *physicalPlanRenderer) renderConstructionGroupStage(stage ir.PhysicalConstructionStage, input constructionGroupInput) ([]string, error) {
	group := stage.Group
	if group == nil {
		return nil, fmt.Errorf("group stage is missing payload")
	}
	countRowsOnly := len(group.Aggregates) != 0
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" {
			countRowsOnly = false
			break
		}
	}
	if group.RootContributorInputColumn != "" {
		// Root contributor collection needs the exact grouped rows. Keep the
		// streaming COUNT_ROWS shortcut for ordinary Groups, but materialize
		// this demanded-only lineage alongside the existing group collection.
		countRowsOnly = false
	}
	aggregateRowValues := countRowsOnly && (input.RowsVariable != "" || input.SourceRowInScope) && constructionGroupRowValuesCanAggregate(group.RowValues)
	if len(group.RowValues) != 0 && !aggregateRowValues {
		countRowsOnly = false
	}
	if input.SourceRowInScope && (input.RowsVariable != "" ||
		(len(group.Keys) == 0 && !(countRowsOnly && aggregateRowValues))) {
		return nil, fmt.Errorf("in-scope source row requires a keyed Group or a keyless COUNT_ROWS Group with row values")
	}
	lines := make([]string, 0, 8+len(group.Keys)*2+len(group.Aggregates)+len(group.RowValues))
	rowSource := input.RowsVariable
	inputRowVariable := stage.InputRowVariable
	var projectedRowsVariable string
	if !countRowsOnly && !aggregateRowValues {
		projected, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.InputProjections})
		if err != nil {
			return nil, fmt.Errorf("input projection: %w", err)
		}
		if input.SourceRowInScope {
			inputRowVariable = r.newInternalVariable("construction_group_projected_input")
			lines = append(lines, fmt.Sprintf("  LET %s = %s", inputRowVariable, projected))
		} else {
			projectedRowsVariable = r.newInternalVariable("construction_group_input_rows")
			lines = append(lines, fmt.Sprintf("  LET %s = (FOR %s IN %s RETURN %s)", projectedRowsVariable, stage.InputRowVariable, input.RowsVariable, projected))
			rowSource = projectedRowsVariable
		}
		if len(group.Keys) == 0 {
			if !input.SourceRowInScope {
				rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", projectedRowsVariable, projectedRowsVariable)
			}
		}
	}
	if aggregateRowValues && len(group.Keys) == 0 && input.RowsVariable != "" {
		rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", input.RowsVariable, input.RowsVariable)
	}
	if input.RowsVariable != "" {
		lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, rowSource))
	} else if !input.SourceRowInScope && (!countRowsOnly || len(group.Keys) != 0) {
		return nil, fmt.Errorf("only keyless COUNT_ROWS groups can consume the current source scope")
	}
	collectKeys := make([]string, 0, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	keyTypeAssertions := make([]string, 0, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("construction_group_key_column_%d", index))
		r.bindVars[columnBind] = key.InputColumn
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return nil, fmt.Errorf("group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKey(fmt.Sprintf("construction_group_key_type_%d", index))
		r.bindVars[typeBind] = typeName
		value := fmt.Sprintf("%s[@%s]", inputRowVariable, columnBind)
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", value))
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		default:
			return nil, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
		}
		keyTypeAssertions = append(keyTypeAssertions, fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", key.Variable, key.Variable, typeBind))
		collectKeys = append(collectKeys, fmt.Sprintf("%s = %s", key.Variable, value))
		sortKeys = append(sortKeys, key.Variable+" ASC")
		nameBind := r.newInternalBindKey(fmt.Sprintf("construction_group_identity_key_%d", index))
		for _, column := range stage.OutputColumns {
			if column.Name == key.OutputColumn {
				r.bindVars[nameBind] = column.ID
				break
			}
		}
		identityParts = append(identityParts, "[@"+nameBind+", "+key.Variable+"]")
	}
	if countRowsOnly && !aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		collect := "COLLECT"
		if len(collectKeys) != 0 {
			collect += " " + strings.Join(collectKeys, ", ")
		}
		lines = append(lines, fmt.Sprintf("  %s WITH COUNT INTO %s", collect, countVariable))
		lines = append(lines, keyTypeAssertions...)
		if len(sortKeys) != 0 {
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
		}
		for _, aggregate := range group.Aggregates {
			lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, countVariable))
		}
	} else if aggregateRowValues {
		countVariable := r.newInternalVariable("construction_group_count_rows")
		aggregateParts := []string{fmt.Sprintf("%s = SUM(%s != null ? 1 : 0)", countVariable, inputRowVariable)}
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
		lines = append(lines, rowValueVariables...)
	} else {
		groupMembers := fmt.Sprintf("{row: %s, present: %s != null}", inputRowVariable, inputRowVariable)
		if len(collectKeys) == 0 {
			allRowsVariable := r.newInternalVariable("construction_group_all_rows")
			lines = append(lines, fmt.Sprintf("  COLLECT %s = null INTO %s = %s", allRowsVariable, group.GroupRowsVariable, groupMembers))
		} else {
			lines = append(lines, fmt.Sprintf("  COLLECT %s INTO %s = %s", strings.Join(collectKeys, ", "), group.GroupRowsVariable, groupMembers))
			lines = append(lines, keyTypeAssertions...)
			lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
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
	}
	identity := "TO_STRING([" + strings.Join(identityParts, ", ") + "])"
	lines = append(lines, fmt.Sprintf("  LET %s = %s", group.IdentityVariable, identity))
	output, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return nil, fmt.Errorf("output projection: %w", err)
	}
	lines = append(lines,
		fmt.Sprintf("  LET %s = %s", stage.OutputRowVariable, output),
		"  RETURN "+stage.OutputRowVariable,
	)
	return lines, nil
}

func (r *physicalPlanRenderer) renderRootContributorSet(
	inputColumn string,
	inputMany bool,
	rowsVariable string,
	rowField string,
	outputVariable string,
) ([]string, error) {
	if inputColumn == "" || rowsVariable == "" || outputVariable == "" {
		return nil, fmt.Errorf("root contributor set requires input, rows, and output")
	}
	columnBind := r.newInternalBindKey("root_contributor_column")
	r.bindVars[columnBind] = inputColumn
	rowVariable := r.newInternalVariable("root_contributor_row")
	value := fmt.Sprintf("%s.%s[@%s]", rowVariable, rowField, columnBind)
	if !inputMany {
		value = "[" + value + "]"
	}
	return []string{fmt.Sprintf(
		"  LET %s = SORTED_UNIQUE(FLATTEN((FOR %s IN %s FILTER %s.present RETURN %s), 1))",
		outputVariable, rowVariable, rowsVariable, rowVariable, value,
	)}, nil
}

func constructionGroupRowValuesCanAggregate(rowValues []ir.PhysicalStageRowValue) bool {
	if len(rowValues) == 0 {
		return false
	}
	for _, rowValue := range rowValues {
		if rowValue.Policy != "ALL" && rowValue.Policy != "ONE" {
			return false
		}
	}
	return true
}

func (r *physicalPlanRenderer) renderConstructionGroupAggregate(group *ir.PhysicalStageGroup, aggregate ir.PhysicalStageGroupAggregate, index int) (string, error) {
	if aggregate.Operation == "COUNT_ROWS" {
		member := r.newInternalVariable(fmt.Sprintf("construction_group_count_rows_%d", index))
		return fmt.Sprintf("LENGTH((FOR %s IN %s FILTER %s.present RETURN 1))", member, group.GroupRowsVariable, member), nil
	}
	columnBind := r.newInternalBindKey(fmt.Sprintf("construction_group_aggregate_column_%d", index))
	r.bindVars[columnBind] = aggregate.InputColumn
	typeName, err := aqlTableScalarType(aggregate.InputKind)
	if err != nil {
		return "", err
	}
	typeBind := r.newInternalBindKey(fmt.Sprintf("construction_group_aggregate_type_%d", index))
	r.bindVars[typeBind] = typeName
	member := r.newInternalVariable(fmt.Sprintf("construction_group_member_%d", index))
	value := fmt.Sprintf("%s.row[@%s]", member, columnBind)
	valuesVariable := r.newInternalVariable(fmt.Sprintf("construction_group_values_%d", index))
	values := fmt.Sprintf("(FOR %s IN %s FILTER %s.present FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH\") RETURN %s)",
		member, group.GroupRowsVariable, member, value, value, typeBind, value)
	if aggregate.Operation == "COUNT_NON_NULL" {
		return "LENGTH(" + values + ")", nil
	}
	if aggregate.Operation == "COUNT_DISTINCT" {
		return "LENGTH(SORTED_UNIQUE(" + values + "))", nil
	}
	if aggregate.Operation != "SUM" && aggregate.Operation != "MIN" && aggregate.Operation != "MAX" && aggregate.Operation != "MEAN" {
		return "", fmt.Errorf("unsupported aggregate operation %q", aggregate.Operation)
	}
	// Materialize each group's typed values once, then apply null behavior:
	// numeric reducers over no non-null values return null rather than zero.
	reducer := aggregate.Operation
	if reducer == "MEAN" {
		reducer = "SUM"
	}
	result := reducer + "(" + valuesVariable + ")"
	if aggregate.Operation == "MEAN" {
		result += " / LENGTH(" + valuesVariable + ")"
	}
	return fmt.Sprintf("FIRST(FOR %s IN [%s] RETURN LENGTH(%s) == 0 ? null : %s)",
		valuesVariable, values, valuesVariable, result), nil
}
