package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderGroupedTablePivotDynamicPreview keeps category-dependent work in one
// bound category loop. It is used only by explicitly selected terminal
// previews; publication and full execution retain the static renderer.
func (r *physicalPlanRenderer) renderGroupedTablePivotDynamicPreview(
	pivot ir.PhysicalGroupedPivot,
	lines []string,
	categoryColumnBind string,
	valueColumnBind string,
	categoryPresenceBind string,
	categoryTypeBind string,
	valueTypeBind string,
	previewIdentityVariable string,
) ([]string, error) {
	specs := make([]map[string]any, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		spec := map[string]any{
			"output":    category.Output,
			"matchKind": string(category.MatchKind),
		}
		switch category.MatchKind {
		case ir.PhysicalPivotCategoryValueMatch:
			value, exists := r.bindVars[category.ValueBindKey]
			if !exists {
				return nil, fmt.Errorf("category %q value bind %q is missing", category.Output, category.ValueBindKey)
			}
			spec["value"] = value
		case ir.PhysicalPivotCategoryNullMatch:
		case ir.PhysicalPivotCategoryMissingMatch:
			if categoryPresenceBind == "" {
				return nil, fmt.Errorf("category %q MISSING match requires a preserved category presence contract", category.Output)
			}
		default:
			return nil, fmt.Errorf("category %q has unsupported match kind %q", category.Output, category.MatchKind)
		}
		specs = append(specs, spec)
	}

	specsBind := r.newInternalBindKey("reshape_category_specs")
	r.bindVars[specsBind] = specs
	categorySpecVariable := r.newInternalVariable("reshape_category_spec")
	cellVariable := r.newInternalVariable("reshape_dynamic_cell")
	valueVariable := r.newInternalVariable("reshape_dynamic_value")
	valuesVariable := r.newInternalVariable("reshape_dynamic_values")
	typedValuesVariable := r.newInternalVariable("reshape_dynamic_typed_values")
	cellResultVariable := r.newInternalVariable("reshape_dynamic_cell_result")
	categoryMatch, err := groupedPivotDynamicCategoryMatchPredicate(
		categorySpecVariable, cellVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind,
	)
	if err != nil {
		return nil, err
	}

	categoryCellsVariable := r.newInternalVariable("reshape_dynamic_cells")
	cellResultExpression, err := groupedPivotCellResultExpression(pivot, valuesVariable, typedValuesVariable)
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf(
		"  LET %s = MERGE((FOR %s IN @%s\n"+
			"    LET %s = (FOR %s IN %s FILTER %s RETURN %s[@%s])\n"+
			`    LET %s = (FOR %s IN %s FILTER %s != null FILTER ASSERT(TYPENAME(%s) == @%s, "TABLE_PIVOT_VALUE_TYPE_MISMATCH") RETURN %s)`+"\n"+
			"    LET %s = %s\n"+
			"    RETURN { [%s.output]: %s }\n"+
			"  ))",
		categoryCellsVariable, categorySpecVariable, specsBind,
		valuesVariable, cellVariable, pivot.GroupRowsVariable, categoryMatch, cellVariable, valueColumnBind,
		typedValuesVariable, valueVariable, valuesVariable, valueVariable, valueVariable, valueTypeBind, valueVariable,
		cellResultVariable, cellResultExpression,
		categorySpecVariable, cellResultVariable,
	))

	outputProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		outputProjections = append(outputProjections, ir.PhysicalProjection{
			Name: name, Hidden: key.Hidden, Value: ir.PhysicalValue{Variable: key.Variable},
		})
	}

	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" || pivot.UnlistedCategoryPolicy == "ERROR" {
		unlistedVariable := r.newInternalVariable("reshape_unlisted_count")
		itemVariable := r.newInternalVariable("reshape_unlisted_item")
		listedSpecVariable := r.newInternalVariable("reshape_listed_category_spec")
		listedMatch, matchErr := groupedPivotDynamicCategoryMatchPredicate(
			listedSpecVariable, itemVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind,
		)
		if matchErr != nil {
			return nil, matchErr
		}
		listed := fmt.Sprintf("LENGTH((FOR %s IN @%s FILTER %s RETURN 1)) > 0", listedSpecVariable, specsBind, listedMatch)
		lines = append(lines, fmt.Sprintf(
			"  LET %s = LENGTH((FOR %s IN %s FILTER NOT (%s) RETURN 1))",
			unlistedVariable, itemVariable, pivot.GroupRowsVariable, listed,
		))
		if pivot.UnlistedCategoryPolicy == "ERROR" {
			// Keep the category loop as a dependency of the unlisted assertion.
			// This preserves duplicate and missing-cell error precedence even if
			// AQL moves calculations while optimizing the query.
			lines = append(lines, fmt.Sprintf(
				`  FILTER (IS_OBJECT(%s) ? ASSERT(%s == 0, "TABLE_PIVOT_UNLISTED_CATEGORY") : false)`,
				categoryCellsVariable, unlistedVariable,
			))
		} else {
			outputProjections = append(outputProjections, ir.PhysicalProjection{
				Name: pivot.UnlistedEvidenceColumn, Value: ir.PhysicalValue{Variable: unlistedVariable},
			})
		}
	} else {
		return nil, fmt.Errorf("unsupported grouped pivot unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}

	groupValues := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		groupValues = append(groupValues, key.Variable)
	}
	identityVariable := previewIdentityVariable
	if identityVariable == "" {
		identity, identityErr := r.renderGroupedPivotIdentity(pivot, groupValues)
		if identityErr != nil {
			return nil, identityErr
		}
		identityVariable = r.newInternalVariable("reshape_identity")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", identityVariable, identity))
	}
	outputProjections = append(outputProjections, ir.PhysicalProjection{
		Name: "__loom_row_id", Value: ir.PhysicalValue{Variable: identityVariable},
	})
	baseObject, err := r.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = MERGE(%s, %s)", pivot.OutputRowVariable, baseObject, categoryCellsVariable))
	return lines, nil
}

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
