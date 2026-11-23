package aql

import (
	"fmt"

	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r tableShapeRenderer) renderStreamingPivotDirectScalarInputs(
	pivot ir.PhysicalGroupedPivot,
) ([]string, map[string]string, error) {
	if r.rootVariable == "" || len(pivot.GroupKeys) == 0 || len(pivot.Categories) == 0 ||
		len(pivot.RowValues) != 0 || pivot.CodedCorrelation != nil ||
		pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresence != nil ||
		pivot.CategoryColumn == "" || pivot.ValueColumn == "" || pivot.CategoryPresenceColumn == "" {
		return nil, nil, nil
	}
	hasMissingCategory := false
	for _, category := range pivot.Categories {
		if category.MatchKind == ir.PhysicalPivotCategoryMissingMatch {
			hasMissingCategory = true
			break
		}
	}
	if !hasMissingCategory || len(pivot.InputProjections) == 0 {
		return nil, nil, nil
	}

	projectionNames := make(map[string]struct{}, len(pivot.InputProjections))
	presenceOutputFound := false
	for _, projection := range pivot.InputProjections {
		if projection.Name == "" {
			return nil, nil, nil
		}
		if _, duplicate := projectionNames[projection.Name]; duplicate {
			return nil, nil, nil
		}
		projectionNames[projection.Name] = struct{}{}
		if projection.PresenceOutput {
			if projection.Expression != nil || projection.Name != pivot.CategoryPresenceColumn ||
				projection.Presence == nil || projection.Presence.Source.Variable != r.rootVariable ||
				projection.Presence.Source.BindKey != "" {
				return nil, nil, nil
			}
			presenceOutputFound = true
			continue
		}
		if !streamingPivotDirectScalarProjection(projection, r.rootVariable) {
			return nil, nil, nil
		}
	}
	if !presenceOutputFound {
		return nil, nil, nil
	}
	for _, required := range []string{pivot.CategoryColumn, pivot.ValueColumn, pivot.CategoryPresenceColumn} {
		if _, exists := projectionNames[required]; !exists {
			return nil, nil, nil
		}
	}
	for _, key := range pivot.GroupKeys {
		if _, exists := projectionNames[key.Column]; !exists {
			return nil, nil, nil
		}
	}

	lines := make([]string, 0, len(pivot.InputProjections)+1)
	columns := make(map[string]string, len(pivot.InputProjections))
	parentAlias := ""
	if len(r.storedPresenceParentPath) != 0 {
		for _, projection := range pivot.InputProjections {
			if _, usesParent := rebaseStreamingPivotProjectionToStoredParent(
				projection, r.rootVariable, r.storedPresenceParentPath, "parent",
			); usesParent {
				parentValue, err := r.core.renderValue(ir.PhysicalValue{
					Variable: r.rootVariable, Path: r.storedPresenceParentPath,
				})
				if err != nil {
					return nil, nil, fmt.Errorf("streaming Pivot stored parent: %w", err)
				}
				parentAlias = r.core.navigation().newInternalVariable("reshape_stream_source_parent")
				lines = append(lines, fmt.Sprintf("  LET %s = %s", parentAlias, parentValue))
				break
			}
		}
	}
	for index, projection := range pivot.InputProjections {
		renderProjection := projection
		rebasedToParent := false
		if parentAlias != "" {
			renderProjection, rebasedToParent = rebaseStreamingPivotProjectionToStoredParent(
				projection, r.rootVariable, r.storedPresenceParentPath, parentAlias,
			)
		}
		var value string
		var err error
		if renderProjection.PresenceOutput {
			if renderProjection.Name == r.storedPresenceOutputName && !rebasedToParent {
				value, err = r.renderProjectionPresenceOnStoredParent(*renderProjection.Presence)
			} else {
				value, err = r.renderProjectionPresence(*renderProjection.Presence)
			}
		} else if renderProjection.Expression != nil {
			value, err = r.core.renderExpression(*renderProjection.Expression)
		} else {
			value, err = r.core.renderValue(renderProjection.Value)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("streaming Pivot input projection %q: %w", projection.Name, err)
		}
		variable := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_input_%d", index))
		lines = append(lines, fmt.Sprintf("  LET %s = %s", variable, value))
		columns[projection.Name] = variable
	}
	return lines, columns, nil
}

func (r tableShapeRenderer) renderGroupedTablePivotStreamingMissingPreview(
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
	scalarInputs map[string]string,
) ([]string, error) {
	if len(pivot.Categories) == 0 || len(pivot.GroupKeys) == 0 || len(pivot.RowValues) != 0 ||
		pivot.CodedCorrelation != nil || pivot.RootContributorInputColumn != "" || pivot.RootContributorOutputColumn != "" ||
		!pivot.CategoryPresenceFromInput || pivot.CategoryPresenceColumn == "" || categoryTypeBind == "" || valueTypeBind == "" ||
		(scalarInputs == nil && (categoryPresenceBind == "" || categoryColumnBind == "" || valueColumnBind == "")) {
		return nil, fmt.Errorf("streaming Pivot preview requires direct presence-aware category and value projections")
	}
	categoryValue, value := "", ""
	if scalarInputs == nil {
		categoryValue = fmt.Sprintf("%s[@%s]", pivot.InputRowVariable, categoryColumnBind)
		value = fmt.Sprintf("%s[@%s]", pivot.InputRowVariable, valueColumnBind)
	} else {
		var ok bool
		categoryValue, ok = scalarInputs[pivot.CategoryColumn]
		if !ok {
			return nil, fmt.Errorf("streaming Pivot scalar category projection %q is missing", pivot.CategoryColumn)
		}
		value, ok = scalarInputs[pivot.ValueColumn]
		if !ok {
			return nil, fmt.Errorf("streaming Pivot scalar value projection %q is missing", pivot.ValueColumn)
		}
		if _, ok := scalarInputs[pivot.CategoryPresenceColumn]; !ok {
			return nil, fmt.Errorf("streaming Pivot scalar category presence projection %q is missing", pivot.CategoryPresenceColumn)
		}
	}

	accumulators := make([]streamingPivotCategoryAccumulator, 0, len(pivot.Categories))
	aggregateTerms := make([]string, 0, len(pivot.Categories)*3+1)
	matches := make([]string, 0, len(pivot.Categories))
	for index, category := range pivot.Categories {
		var match string
		var err error
		if scalarInputs == nil {
			match, err = groupedPivotCategoryMatchPredicate(
				category, pivot.InputRowVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind,
			)
		} else {
			match, err = groupedPivotDirectCategoryMatchPredicate(
				category, categoryValue, scalarInputs[pivot.CategoryPresenceColumn], categoryTypeBind,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("category %q: %w", category.Output, err)
		}
		matchVariable := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_match_%d", index))
		valueVariable := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_value_%d", index))
		matchedCount := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_count_%d", index))
		nonNullCount := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_value_count_%d", index))
		reducedValue := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_reduced_%d", index))
		outputValue := r.core.navigation().newInternalVariable(fmt.Sprintf("reshape_stream_category_output_%d", index))
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
		rowUnlisted := r.core.navigation().newInternalVariable("reshape_stream_row_unlisted")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", rowUnlisted, notListed))
		unlistedCount = r.core.navigation().newInternalVariable("reshape_stream_unlisted_count")
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
	identityVariable := r.core.navigation().newInternalVariable("reshape_stream_identity")
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
	baseObject, err := r.core.renderReturn(ir.PhysicalReturn{Projections: baseProjections})
	if err != nil {
		return nil, err
	}
	categoryObject, err := r.core.renderReturn(ir.PhysicalReturn{Projections: categoryProjections})
	if err != nil {
		return nil, err
	}
	categoryCells := r.core.navigation().newInternalVariable("reshape_stream_category_cells")
	lines = append(lines, fmt.Sprintf("  LET %s = %s", categoryCells, categoryObject))
	output := r.core.navigation().newInternalVariable("reshape_stream_pivot_output")
	lines = append(lines, fmt.Sprintf("  LET %s = MERGE(%s, %s)", output, baseObject, categoryCells))
	lines = append(lines, fmt.Sprintf("  LET %s = %s", pivot.OutputRowVariable, output))
	return lines, nil
}
