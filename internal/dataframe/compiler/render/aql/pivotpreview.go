package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql/internal/aqlsyntax"
	"github.com/calypr/loom/internal/dataframe/spec"
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

func (r tableShapeRenderer) renderGroupedTablePivotDynamicPreview(
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

	specsBind := r.core.newInternalBindKey("reshape_category_specs")
	r.bindVars[specsBind] = specs
	categorySpecVariable := r.core.navigation().newInternalVariable("reshape_category_spec")
	cellVariable := r.core.navigation().newInternalVariable("reshape_dynamic_cell")
	valueVariable := r.core.navigation().newInternalVariable("reshape_dynamic_value")
	valuesVariable := r.core.navigation().newInternalVariable("reshape_dynamic_values")
	typedValuesVariable := r.core.navigation().newInternalVariable("reshape_dynamic_typed_values")
	cellResultVariable := r.core.navigation().newInternalVariable("reshape_dynamic_cell_result")
	categoryMatch, err := groupedPivotDynamicCategoryMatchPredicate(
		categorySpecVariable, cellVariable, categoryColumnBind, categoryPresenceBind, categoryTypeBind,
	)
	if err != nil {
		return nil, err
	}

	categoryCellsVariable := r.core.navigation().newInternalVariable("reshape_dynamic_cells")
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

	outputProjections := make([]ir.PhysicalProjection, 0, len(pivot.GroupKeys)+len(pivot.RowValues)+3)
	for _, key := range pivot.GroupKeys {
		name := key.Output
		if name == "" {
			name = key.Column
		}
		outputProjections = append(outputProjections, ir.PhysicalProjection{
			Name: name, Hidden: key.Hidden, Value: ir.PhysicalValue{Variable: key.Variable},
		})
	}
	for _, rowValue := range pivot.RowValues {
		outputProjections = append(outputProjections, ir.PhysicalProjection{
			Name: rowValue.Output, Value: ir.PhysicalValue{Variable: rowValue.Variable},
		})
	}
	if pivot.RootContributorOutputColumn != "" {
		outputProjections = append(outputProjections, ir.PhysicalProjection{
			Name: pivot.RootContributorOutputColumn, Hidden: true, Value: ir.PhysicalValue{Variable: pivot.RootContributorVariable},
		})
	}

	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" || pivot.UnlistedCategoryPolicy == "ERROR" {
		unlistedVariable := r.core.navigation().newInternalVariable("reshape_unlisted_count")
		itemVariable := r.core.navigation().newInternalVariable("reshape_unlisted_item")
		listedSpecVariable := r.core.navigation().newInternalVariable("reshape_listed_category_spec")
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
		identityVariable = r.core.navigation().newInternalVariable("reshape_identity")
		lines = append(lines, fmt.Sprintf("  LET %s = %s", identityVariable, identity))
	}
	outputProjections = append(outputProjections, ir.PhysicalProjection{
		Name: "__loom_row_id", Value: ir.PhysicalValue{Variable: identityVariable},
	})
	baseObject, err := r.core.renderReturn(ir.PhysicalReturn{Projections: outputProjections})
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("  LET %s = MERGE(%s, %s)", pivot.OutputRowVariable, baseObject, categoryCellsVariable))
	return lines, nil
}

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

type streamingPivotCategoryAccumulator struct {
	matchVariable string
	valueVariable string
	matchedCount  string
	nonNullCount  string
	reducedValue  string
	outputValue   string
	category      ir.PhysicalGroupedPivotCategory
}

func rebaseStreamingPivotProjectionToStoredParent(
	projection ir.PhysicalProjection,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) (ir.PhysicalProjection, bool) {
	rebased := false
	if projection.PresenceOutput && projection.Presence != nil {
		presence := *projection.Presence
		if rebaseStreamingPivotPresenceToStoredParent(&presence, rootVariable, parentPath, parentVariable) {
			projection.Presence = &presence
			rebased = true
		}
	} else if projection.Expression != nil && projection.Expression.Kind == ir.PhysicalExtractExpression && projection.Expression.Extract != nil {
		expression := *projection.Expression
		extract := *expression.Extract
		if streamingPivotDirectScalarProjection(projection, rootVariable) {
			if rebaseStreamingPivotExtractToStoredParent(&extract, rootVariable, parentPath, parentVariable) {
				expression.Extract = &extract
				projection.Expression = &expression
				rebased = true
			}
		}
	} else if projection.Expression == nil {
		if value, ok := rebaseStreamingPivotValueToStoredParent(projection.Value, rootVariable, parentPath, parentVariable); ok {
			projection.Value = value
			rebased = true
		}
	}
	return projection, rebased
}

func rebaseStreamingPivotValueToStoredParent(
	value ir.PhysicalValue,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) (ir.PhysicalValue, bool) {
	if value.Variable != rootVariable || value.BindKey != "" || !aqlsyntax.PathHasPrefix(value.Path, parentPath) {
		return value, false
	}
	value.Variable = parentVariable
	value.Path = append([]string(nil), value.Path[len(parentPath):]...)
	return value, true
}

func rebaseStreamingPivotPresenceToStoredParent(
	presence *ir.PhysicalProjectionPresence,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) bool {
	if presence == nil {
		return false
	}
	if source, ok := rebaseStreamingPivotValueToStoredParent(presence.Source, rootVariable, parentPath, parentVariable); ok {
		presence.Source = source
		return true
	}
	source := presence.Source
	if source.Variable != rootVariable || source.BindKey != "" || len(source.Path) >= len(parentPath) ||
		len(presence.Paths) == 0 || !aqlsyntax.PathHasPrefix(parentPath, source.Path) {
		return false
	}
	parentRemainder := parentPath[len(source.Path):]
	for _, path := range presence.Paths {
		if len(path) <= len(parentRemainder) {
			return false
		}
		for index, segment := range parentRemainder {
			if path[index] != segment {
				return false
			}
		}
	}
	source.Variable = parentVariable
	source.Path = nil
	presence.Source = source
	paths := make([][]string, 0, len(presence.Paths))
	for _, path := range presence.Paths {
		paths = append(paths, append([]string(nil), path[len(parentRemainder):]...))
	}
	presence.Paths = paths
	return true
}

func rebaseStreamingPivotExtractToStoredParent(
	extract *ir.PhysicalExtract,
	rootVariable string,
	parentPath []string,
	parentVariable string,
) bool {
	if !streamingPivotDirectScalarExtract(extract, rootVariable) {
		return false
	}
	stepOffset := 0
	if len(extract.Source.Path) >= len(parentPath) {
		if !aqlsyntax.PathHasPrefix(extract.Source.Path, parentPath) {
			return false
		}
		extract.Source.Path = append([]string(nil), extract.Source.Path[len(parentPath):]...)
	} else {
		if !aqlsyntax.PathHasPrefix(parentPath, extract.Source.Path) {
			return false
		}
		stepOffset = len(parentPath) - len(extract.Source.Path)
		if stepOffset > len(extract.Selector.Steps) {
			return false
		}
		for index := 0; index < stepOffset; index++ {
			if extract.Selector.Steps[index].Field != parentPath[len(extract.Source.Path)+index] {
				return false
			}
		}
		extract.Source.Path = nil
		extract.Selector.Steps = append([]spec.SelectorStep(nil), extract.Selector.Steps[stepOffset:]...)
	}
	extract.Source.Variable = parentVariable
	return true
}

func streamingPivotDirectScalarProjection(projection ir.PhysicalProjection, rootVariable string) bool {
	if projection.Presence != nil && (projection.Presence.Source.Variable != rootVariable || projection.Presence.Source.BindKey != "") {
		return false
	}
	if projection.Expression == nil {
		return projection.Value.Variable == rootVariable && projection.Value.BindKey == "" && len(projection.Value.Path) > 0
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality || expression.Extract == nil {
		return false
	}
	return streamingPivotDirectScalarExtract(expression.Extract, rootVariable)
}

func streamingPivotDirectScalarExtract(extract *ir.PhysicalExtract, rootVariable string) bool {
	if extract == nil {
		return false
	}
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		extract.Source.BindKey != "" || len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 ||
		extract.Prepared != nil || extract.Distinct || extract.UnitNormalization != nil ||
		extract.Selector.Filter != nil || len(extract.Selector.Steps) == 0 {
		return false
	}
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func groupedPivotDirectCategoryMatchPredicate(
	category ir.PhysicalGroupedPivotCategory,
	categoryValue string,
	presenceValue string,
	categoryTypeBind string,
) (string, error) {
	if categoryValue == "" || presenceValue == "" {
		return "", fmt.Errorf("category scalar projection is required")
	}
	return groupedPivotCategoryMatchExpression(category, categoryValue, presenceValue+" == true", categoryTypeBind, true)
}
