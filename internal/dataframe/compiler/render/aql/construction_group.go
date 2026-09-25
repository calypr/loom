package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionGroupStage(stage ir.PhysicalConstructionStage, inputRows string) ([]string, error) {
	group := stage.Group
	if group == nil {
		return nil, fmt.Errorf("group stage is missing payload")
	}
	projected, err := r.renderReturn(ir.PhysicalReturn{Projections: stage.InputProjections})
	if err != nil {
		return nil, fmt.Errorf("input projection: %w", err)
	}
	rowsVariable := r.newInternalVariable("construction_group_input_rows")
	lines := []string{
		fmt.Sprintf("  LET %s = (FOR %s IN %s RETURN %s)", rowsVariable, stage.InputRowVariable, inputRows, projected),
	}
	rowSource := rowsVariable
	if len(group.Keys) == 0 {
		rowSource = fmt.Sprintf("(LENGTH(%s) == 0 ? [null] : %s)", rowsVariable, rowsVariable)
	}
	lines = append(lines, fmt.Sprintf("  FOR %s IN %s", stage.InputRowVariable, rowSource))
	collectKeys := make([]string, 0, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
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
		value := fmt.Sprintf("%s[@%s]", stage.InputRowVariable, columnBind)
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", value))
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		default:
			return nil, fmt.Errorf("unsupported missing-key policy %q", group.MissingKeyPolicy)
		}
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind))
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
	groupMembers := fmt.Sprintf("{row: %s, present: %s != null}", stage.InputRowVariable, stage.InputRowVariable)
	if len(collectKeys) == 0 {
		allRowsVariable := r.newInternalVariable("construction_group_all_rows")
		lines = append(lines, fmt.Sprintf("  COLLECT %s = null INTO %s = %s", allRowsVariable, group.GroupRowsVariable, groupMembers))
	} else {
		lines = append(lines, fmt.Sprintf("  COLLECT %s INTO %s = %s", strings.Join(collectKeys, ", "), group.GroupRowsVariable, groupMembers))
		lines = append(lines, "  SORT "+strings.Join(sortKeys, ", "))
	}
	for index, aggregate := range group.Aggregates {
		expression, err := r.renderConstructionGroupAggregate(group, aggregate, index)
		if err != nil {
			return nil, fmt.Errorf("aggregate %q: %w", aggregate.Output, err)
		}
		lines = append(lines, fmt.Sprintf("  LET %s = %s", aggregate.Variable, expression))
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
