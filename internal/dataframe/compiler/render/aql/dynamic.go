package aql

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func groupedPivotDynamicCategoryMatchPredicate(
	specVariable string,
	itemVariable string,
	categoryColumnBind string,
	categoryPresenceBind string,
	categoryTypeBind string,
) (string, error) {
	if specVariable == "" || itemVariable == "" || categoryColumnBind == "" || categoryTypeBind == "" {
		return "", fmt.Errorf("dynamic grouped pivot category match is incomplete")
	}
	column := fmt.Sprintf("%s[@%s]", itemVariable, categoryColumnBind)
	present := fmt.Sprintf("HAS(%s, @%s)", itemVariable, categoryColumnBind)
	if categoryPresenceBind != "" {
		present = fmt.Sprintf("%s[@%s] == true", itemVariable, categoryPresenceBind)
	}
	return fmt.Sprintf(
		`((%s.matchKind == "VALUE" AND %s AND TYPENAME(%s) == @%s AND %s == %s.value) OR `+
			`(%s.matchKind == "NULL" AND %s AND %s == null) OR `+
			`(%s.matchKind == "MISSING" AND NOT (%s)))`,
		specVariable, present, column, categoryTypeBind, column, specVariable,
		specVariable, present, column,
		specVariable, present,
	), nil
}

func groupedPivotCellResultExpression(pivot ir.PhysicalGroupedPivot, valuesVariable, typedValuesVariable string) (string, error) {
	if pivot.MissingCellPolicy != "NULL" && pivot.MissingCellPolicy != "ERROR" {
		return "", fmt.Errorf("unsupported grouped pivot missing-cell policy %q", pivot.MissingCellPolicy)
	}
	switch pivot.DuplicatePolicy {
	case "ERROR":
		cardinality := fmt.Sprintf(`ASSERT(LENGTH(%s) <= 1, "TABLE_PIVOT_CELL_CARDINALITY") ? FIRST(%s) : null`, valuesVariable, valuesVariable)
		if pivot.MissingCellPolicy == "ERROR" {
			return fmt.Sprintf(`(ASSERT(LENGTH(%s) > 0, "TABLE_PIVOT_CELL_MISSING") ? (%s) : null)`, valuesVariable, cardinality), nil
		}
		return "(" + cardinality + ")", nil
	case "SUM", "MIN", "MAX":
		condition := "true"
		if pivot.MissingCellPolicy == "ERROR" {
			condition = fmt.Sprintf("LENGTH(%s) > 0", valuesVariable)
		}
		return fmt.Sprintf(`(ASSERT(%s, "TABLE_PIVOT_CELL_MISSING") ? (LENGTH(%s) == 0 ? null : %s(%s)) : null)`,
			condition, typedValuesVariable, pivot.DuplicatePolicy, typedValuesVariable), nil
	default:
		return "", fmt.Errorf("unsupported grouped pivot duplicate policy %q", pivot.DuplicatePolicy)
	}
}
