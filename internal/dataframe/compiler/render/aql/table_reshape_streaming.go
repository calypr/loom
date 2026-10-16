package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type streamingPivotCategoryAccumulator struct {
	matchVariable string
	valueVariable string
	matchedCount  string
	nonNullCount  string
	reducedValue  string
	outputValue   string
	category      ir.PhysicalGroupedPivotCategory
}

func (r *physicalPlanRenderer) renderGroupedTablePivotStreamingMissingPreview(
	pivot ir.PhysicalGroupedPivot,
	lines []string,
	groupCollect []string,
	categoryColumnBind string,
	valueColumnBind string,
	categoryPresenceBind string,
	categoryTypeBind string,
	valueTypeBind string,
	previewLimitBindKey string,
	sortGroups bool,
) ([]string, error) {
	if len(pivot.Categories) == 0 || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.CodedCorrelation != nil || pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresenceColumn == "" || categoryPresenceBind == "" ||
		categoryColumnBind == "" || valueColumnBind == "" || categoryTypeBind == "" || valueTypeBind == "" {
		return nil, fmt.Errorf("streaming Pivot preview requires direct presence-aware category and value projections")
	}

	accumulators := make([]streamingPivotCategoryAccumulator, 0, len(pivot.Categories))
	aggregateTerms := make([]string, 0, len(pivot.Categories)*3+1)
	matches := make([]string, 0, len(pivot.Categories))
	value := fmt.Sprintf("%s[@%s]", pivot.InputRowVariable, valueColumnBind)
	for index, category := range pivot.Categories {
		match, err := groupedPivotCategoryMatchPredicate(
			category, pivot.InputRowVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind,
		)
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category.Output, err)
		}
		matchVariable := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_match_%d", index))
		valueVariable := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_value_%d", index))
		matchedCount := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_count_%d", index))
		nonNullCount := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_value_count_%d", index))
		reducedValue := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_reduced_%d", index))
		outputValue := r.newInternalVariable(fmt.Sprintf("reshape_stream_category_output_%d", index))
		lines = append(lines,
			fmt.Sprintf("  LET %s = %s", matchVariable, match),
			fmt.Sprintf("  LET %s = (%s AND %s != null ? (ASSERT(TYPENAME(%s) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\") ? %s : null) : null)",
				valueVariable, matchVariable, value, value, valueTypeBind, value),
		)
		aggregateTerms = append(aggregateTerms,
			fmt.Sprintf("%s = SUM(%s ? 1 : 0)", matchedCount, matchVariable),
			fmt.Sprintf("%s = SUM(%s != null ? 1 : 0)", nonNullCount, valueVariable),
		)
		reducer := pivot.DuplicatePolicy
		if reducer == "ERROR" {
			// Once the full-group cardinality assertion passes, MAX returns the
			// sole value (or NULL) without retaining a per-group value array.
			reducer = "MAX"
		}
		if reducer != "SUM" && reducer != "MIN" && reducer != "MAX" {
			return nil, fmt.Errorf("unsupported streaming Pivot duplicate policy %q", pivot.DuplicatePolicy)
		}
		aggregateTerms = append(aggregateTerms, fmt.Sprintf("%s = %s(%s)", reducedValue, reducer, valueVariable))
		matches = append(matches, matchVariable)
		accumulators = append(accumulators, streamingPivotCategoryAccumulator{
			matchVariable: matchVariable, valueVariable: valueVariable, matchedCount: matchedCount,
			nonNullCount: nonNullCount, reducedValue: reducedValue, outputValue: outputValue, category: category,
		})
	}

	unlistedCount := ""
	if pivot.UnlistedCategoryPolicy == "ERROR" || pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
		notListed := "NOT (" + strings.Join(matches, " OR ") + ")"
		rowUnlisted := r.newInternalVariable("reshape_stream_row_unlisted")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", rowUnlisted, notListed))
		unlistedCount = r.newInternalVariable("reshape_stream_unlisted_count")
		aggregateTerms = append(aggregateTerms, fmt.Sprintf("%s = SUM(%s ? 1 : 0)", unlistedCount, rowUnlisted))
	} else {
		return nil, fmt.Errorf("unsupported streaming Pivot unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	if len(groupCollect) == 0 {
		return nil, fmt.Errorf("streaming Pivot preview has no group-key expressions")
	}
	lines = append(lines, "  COLLECT "+strings.Join(groupCollect, ", ")+" AGGREGATE "+strings.Join(aggregateTerms, ", "))

	for _, category := range accumulators {
		if pivot.MissingCellPolicy == "ERROR" {
			count := category.matchedCount
			if pivot.DuplicatePolicy != "ERROR" {
				count = category.nonNullCount
			}
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s > 0, \"TABLE_PIVOT_CELL_MISSING\")", count))
		}
		if pivot.DuplicatePolicy == "ERROR" {
			lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s <= 1, \"TABLE_PIVOT_CELL_CARDINALITY\")", category.matchedCount))
		}
	}
	if pivot.UnlistedCategoryPolicy == "ERROR" {
		lines = append(lines, fmt.Sprintf("  FILTER ASSERT(%s == 0, \"TABLE_PIVOT_UNLISTED_CATEGORY\")", unlistedCount))
	}

	groupValues := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		groupValues = append(groupValues, key.Variable)
	}
	identity, err := r.renderGroupedPivotIdentity(pivot, groupValues)
	if err != nil {
		return nil, err
	}
	identityVariable := r.newInternalVariable("reshape_stream_identity")
	lines = append(lines, fmt.Sprintf("  LET %s = %s", identityVariable, identity))
	if previewLimitBindKey != "" {
		lines = append(lines, fmt.Sprintf("  SORT %s ASC", identityVariable), "  LIMIT @"+previewLimitBindKey)
	} else if sortGroups {
		sort := make([]string, 0, len(pivot.GroupKeys))
		for _, key := range pivot.GroupKeys {
			sort = append(sort, key.Variable+" ASC")
		}
		lines = append(lines, "  SORT "+strings.Join(sort, ", "))
	}

	categoryProjections := make([]ir.PhysicalProjection, 0, len(accumulators))
	for _, category := range accumulators {
		valueExpression := category.reducedValue
		if pivot.DuplicatePolicy != "ERROR" {
			valueExpression = fmt.Sprintf("(%s == 0 ? null : %s)", category.nonNullCount, category.reducedValue)
		}
		lines = append(lines, fmt.Sprintf("  LET %s = %s", category.outputValue, valueExpression))
		categoryProjections = append(categoryProjections, ir.PhysicalProjection{
			Name: category.category.Output, Value: ir.PhysicalValue{Variable: category.outputValue},
		})
	}

	baseProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+2)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		baseProjections = append(baseProjections, ir.PhysicalProjection{
			Name: name, Hidden: key.Hidden, Value: ir.PhysicalValue{Variable: key.Variable},
		})
	}
	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" && pivot.UnlistedEvidenceColumn != "" {
		baseProjections = append(baseProjections, ir.PhysicalProjection{
			Name: pivot.UnlistedEvidenceColumn, Value: ir.PhysicalValue{Variable: unlistedCount},
		})
	}
	baseProjections = append(baseProjections, ir.PhysicalProjection{
		Name: "__loom_row_id", Value: ir.PhysicalValue{Variable: identityVariable},
	})
	baseObject, err := r.renderReturn(ir.PhysicalReturn{Projections: baseProjections})
	if err != nil {
		return nil, err
	}
	categoryObject, err := r.renderReturn(ir.PhysicalReturn{Projections: categoryProjections})
	if err != nil {
		return nil, err
	}
	categoryCells := r.newInternalVariable("reshape_stream_category_cells")
	lines = append(lines, fmt.Sprintf("  LET %s = %s", categoryCells, categoryObject))
	output := r.newInternalVariable("reshape_stream_pivot_output")
	lines = append(lines, fmt.Sprintf("  LET %s = MERGE(%s, %s)", output, baseObject, categoryCells))
	lines = append(lines, fmt.Sprintf("  LET %s = %s", pivot.OutputRowVariable, output))
	return lines, nil
}
