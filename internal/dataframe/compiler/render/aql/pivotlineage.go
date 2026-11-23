package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

type constructionPivotLineageVariables struct {
	Source string
}

func (r *physicalPlanRenderer) renderConstructionCountRowsGroupPivotRowLineage(
	sourceQuery string,
	groupStage ir.PhysicalConstructionStage,
	pivotStage ir.PhysicalConstructionStage,
	match ir.PhysicalRowLineageStageMatch,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	group, pivot := groupStage.Group, pivotStage.GroupedPivot
	if group == nil || pivot == nil || match.StageID != pivotStage.ID || match.Kind != ir.PhysicalStagePivotOp ||
		match.StageRowIDBindKey != terminal.RowIDBindKey || len(match.IdentityKeyBindKeys) != len(pivot.GroupKeys) {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed COUNT_ROWS Group/Pivot lineage requires its exact typed terminal owner")
	}

	groupedSource, err := r.renderConstructionCountRowsGroupedSource(sourceQuery, groupStage)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("render Group stage for Pivot lineage validation: %w", err)
	}

	resourceIDBind := r.newInternalBindKey("row_lineage_group_pivot_resource_id_column")
	r.bindVars[resourceIDBind] = terminal.ResourceIDColumn
	occurrenceKeyBind := r.newInternalBindKey("row_lineage_group_pivot_occurrence_key_column")
	r.bindVars[occurrenceKeyBind] = terminal.OccurrenceKeyColumn
	resourceTypeBind := r.newInternalBindKey("row_lineage_group_pivot_resource_type")
	r.bindVars[resourceTypeBind] = terminal.ResourceType
	categoryColumnBind := r.newInternalBindKey("row_lineage_group_pivot_category_column")
	r.bindVars[categoryColumnBind] = pivot.CategoryColumn
	valueColumnBind := r.newInternalBindKey("row_lineage_group_pivot_value_column")
	r.bindVars[valueColumnBind] = pivot.ValueColumn
	categoryType, err := aqlTableScalarType(pivot.CategoryType)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("Group/Pivot lineage category type: %w", err)
	}
	categoryTypeBind := r.newInternalBindKey("row_lineage_group_pivot_category_type")
	r.bindVars[categoryTypeBind] = categoryType
	valueType, err := aqlTableScalarType(pivot.ValueType)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("Group/Pivot lineage value type: %w", err)
	}
	valueTypeBind := r.newInternalBindKey("row_lineage_group_pivot_value_type")
	r.bindVars[valueTypeBind] = valueType
	keyColumnBinds := make([]string, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		keyColumnBinds[index] = r.newInternalBindKey(fmt.Sprintf("row_lineage_group_pivot_group_column_%d", index))
		r.bindVars[keyColumnBinds[index]] = key.Column
	}

	lineagePivot := *pivot
	for _, contributorColumn := range []string{pivot.RootContributorInputColumn, pivot.RootContributorOutputColumn} {
		lineagePivot.InputProjections = withoutStageProjection(lineagePivot.InputProjections, contributorColumn)
	}
	lineagePivot.RootContributorInputColumn = ""
	lineagePivot.RootContributorInputMany = false
	lineagePivot.RootContributorOutputColumn = ""
	lineagePivot.RootContributorVariable = ""
	validationLines, err := r.renderConstructionPivotLineageValidation(
		groupedSource, pivotStage, &lineagePivot, match, keyColumnBinds,
		categoryColumnBind, categoryTypeBind, "", valueColumnBind, valueTypeBind,
	)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	validationLines = append(validationLines, "RETURN true")
	selected := r.newInternalVariable("row_lineage_group_pivot_selected")

	pageSource, page, err := r.renderConstructionGroupPivotRootPreimage(
		sourceQuery, groupStage, pivot, match, selected, categoryColumnBind, categoryTypeBind,
	)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	pageVariable := r.newInternalVariable("row_lineage_group_pivot_page")
	page = append(page,
		"SORT "+pageSource+"[@"+occurrenceKeyBind+"] ASC",
		"LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+pageSource+"[@"+resourceIDBind+"], occurrenceKey: "+pageSource+"[@"+occurrenceKeyBind+"]}",
	)

	lines := []string{
		"LET " + selected + " = FIRST(",
		indentQuery(strings.Join(validationLines, "\n"), "  "),
		")",
		"LET " + pageVariable + " = (",
		indentQuery(strings.Join(page, "\n"), "  "),
		")",
		"RETURN {found: " + selected + " != null, contributors: SLICE(" + pageVariable + ", 0, @" + terminal.LimitBindKey + "), hasMore: LENGTH(" + pageVariable + ") > @" + terminal.LimitBindKey + "}",
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderConstructionCountRowsGroupedSource(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
) (string, error) {
	group := stage.Group
	if group == nil || len(group.Keys) == 0 || len(group.Aggregates) != 1 ||
		group.Aggregates[0].Operation != "COUNT_ROWS" || len(group.RowValues) != 0 {
		return "", fmt.Errorf("only keyed COUNT_ROWS Groups without row values are supported")
	}
	source := r.newInternalVariable("row_lineage_group_pivot_group_input")
	input := r.newInternalVariable("row_lineage_group_pivot_group_row")
	count := r.newInternalVariable("row_lineage_group_pivot_group_count")
	lines := []string{"FOR " + source + " IN (", indentQuery(sourceQuery, "  "), ")", "LET " + input + " = " + source}
	collect := make([]string, 0, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	returnFields := make([]string, 0, len(group.Keys)+1)
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("row_lineage_group_pivot_group_input_column_%d", index))
		r.bindVars[columnBind] = key.InputColumn
		value := r.newInternalVariable(fmt.Sprintf("row_lineage_group_pivot_group_value_%d", index))
		lines = append(lines, "LET "+value+" = "+input+"[@"+columnBind+"]")
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, "FILTER "+value+" != null")
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, "FILTER ASSERT("+value+" != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")")
		default:
			return "", fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return "", fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKey(fmt.Sprintf("row_lineage_group_pivot_group_key_type_%d", index))
		r.bindVars[typeBind] = typeName
		lines = append(lines, fmt.Sprintf("FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind))
		groupedValue := r.newInternalVariable(fmt.Sprintf("row_lineage_group_pivot_group_key_%d", index))
		collect = append(collect, groupedValue+" = "+value)
		sortKeys = append(sortKeys, groupedValue+" ASC")
		returnFields = append(returnFields, key.OutputColumn+": "+groupedValue)
	}
	lines = append(lines, "COLLECT "+strings.Join(collect, ", ")+" WITH COUNT INTO "+count)
	lines = append(lines, "SORT "+strings.Join(sortKeys, ", "))
	returnFields = append(returnFields, group.Aggregates[0].Output+": "+count)
	lines = append(lines, "RETURN {"+strings.Join(returnFields, ", ")+"}")
	return strings.Join(lines, "\n"), nil
}

func (r *physicalPlanRenderer) renderConstructionGroupPivotRootPreimage(
	sourceQuery string,
	groupStage ir.PhysicalConstructionStage,
	pivot *ir.PhysicalGroupedPivot,
	match ir.PhysicalRowLineageStageMatch,
	selected, categoryColumnBind, categoryTypeBind string,
) (string, []string, error) {
	group := groupStage.Group
	if group == nil || pivot == nil {
		return "", nil, fmt.Errorf("Group and Pivot payloads are required for the preimage")
	}
	source := r.newInternalVariable("row_lineage_group_pivot_source")
	input := r.newInternalVariable("row_lineage_group_pivot_input")
	lines := []string{"FOR " + source + " IN (", indentQuery(sourceQuery, "  "), ")", "LET " + input + " = " + source}
	groupValues := make(map[string]string, len(group.Keys))
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey(fmt.Sprintf("row_lineage_group_pivot_source_column_%d", index))
		r.bindVars[columnBind] = key.InputColumn
		value := r.newInternalVariable(fmt.Sprintf("row_lineage_group_pivot_source_key_%d", index))
		lines = append(lines, "LET "+value+" = "+input+"[@"+columnBind+"]")
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			lines = append(lines, "FILTER "+value+" != null")
		case ir.PhysicalStageGroupMissingKeyError:
			lines = append(lines, "FILTER ASSERT("+value+" != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")")
		default:
			return "", nil, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return "", nil, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKey(fmt.Sprintf("row_lineage_group_pivot_source_key_type_%d", index))
		r.bindVars[typeBind] = typeName
		lines = append(lines, fmt.Sprintf("FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind))
		groupValues[key.OutputColumn] = value
	}

	categoryValue, ok := groupValues[pivot.CategoryColumn]
	if !ok {
		return "", nil, fmt.Errorf("Pivot category %q is not a Group key", pivot.CategoryColumn)
	}
	categoryObject := r.newInternalVariable("row_lineage_group_pivot_category_projection")
	lines = append(lines, "LET "+categoryObject+" = MERGE({}, {[@"+categoryColumnBind+"]: "+categoryValue+"})")
	listed, err := groupedPivotListedPredicate(*pivot, categoryObject, categoryColumnBind, "", categoryTypeBind)
	if err != nil {
		return "", nil, fmt.Errorf("render Pivot category match: %w", err)
	}
	lines = append(lines, "FILTER "+listed, "FILTER "+selected+" != null")

	for index, key := range pivot.GroupKeys {
		value, ok := groupValues[key.Column]
		if !ok {
			return "", nil, fmt.Errorf("Pivot group key %q is not a Group key", key.Column)
		}
		lines = append(lines, "FILTER "+value+" == @"+match.IdentityKeyBindKeys[index])
	}
	return source, lines, nil
}

func (r *physicalPlanRenderer) renderConstructionPivotRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	match ir.PhysicalRowLineageStageMatch,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	pivot := stage.GroupedPivot
	if pivot == nil || pivot.CodedCorrelation != nil || len(match.IdentityKeyBindKeys) != len(pivot.GroupKeys) {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot lineage requires a matching ordinary Pivot owner")
	}
	if pivot.UnlistedCategoryPolicy != "ERROR" || len(pivot.Categories) == 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot lineage does not support unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	if match.StageID != stage.ID || match.Kind != ir.PhysicalStagePivotOp || match.StageRowIDBindKey != terminal.RowIDBindKey {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot lineage owner does not match the terminal row identity")
	}
	if _, ok := r.bindVars[match.StageRowIDBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot lineage row ID bind %q is missing", match.StageRowIDBindKey)
	}
	for _, key := range match.IdentityKeyBindKeys {
		if _, ok := r.bindVars[key]; !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot lineage key bind %q is missing", key)
		}
	}

	resourceIDBind := r.newInternalBindKey("row_lineage_pivot_resource_id_column")
	r.bindVars[resourceIDBind] = terminal.ResourceIDColumn
	occurrenceKeyBind := r.newInternalBindKey("row_lineage_pivot_occurrence_key_column")
	r.bindVars[occurrenceKeyBind] = terminal.OccurrenceKeyColumn
	resourceTypeBind := r.newInternalBindKey("row_lineage_pivot_resource_type")
	r.bindVars[resourceTypeBind] = terminal.ResourceType
	categoryColumnBind := r.newInternalBindKey("row_lineage_pivot_category_column")
	r.bindVars[categoryColumnBind] = pivot.CategoryColumn
	valueColumnBind := r.newInternalBindKey("row_lineage_pivot_value_column")
	r.bindVars[valueColumnBind] = pivot.ValueColumn
	categoryType, err := aqlTableScalarType(pivot.CategoryType)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot category type: %w", err)
	}
	categoryTypeBind := r.newInternalBindKey("row_lineage_pivot_category_type")
	r.bindVars[categoryTypeBind] = categoryType
	valueType, err := aqlTableScalarType(pivot.ValueType)
	if err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("construction Pivot value type: %w", err)
	}
	valueTypeBind := r.newInternalBindKey("row_lineage_pivot_value_type")
	r.bindVars[valueTypeBind] = valueType
	categoryPresenceBind := ""
	if pivot.CategoryPresence != nil {
		categoryPresenceBind = r.newInternalBindKey("row_lineage_pivot_category_presence_column")
		r.bindVars[categoryPresenceBind] = pivot.CategoryPresenceColumn
	}
	keyColumnBinds := make([]string, len(pivot.GroupKeys))
	for index, key := range pivot.GroupKeys {
		keyColumnBinds[index] = r.newInternalBindKey(fmt.Sprintf("row_lineage_pivot_group_column_%d", index))
		r.bindVars[keyColumnBinds[index]] = key.Column
	}

	selected := r.newInternalVariable("row_lineage_pivot_selected")
	validationLines, err := r.renderConstructionPivotLineageValidation(
		sourceQuery, stage, pivot, match, keyColumnBinds,
		categoryColumnBind, categoryTypeBind, categoryPresenceBind, valueColumnBind, valueTypeBind,
	)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	selectedLines := append(validationLines, "RETURN true")

	pageSource, pageVars, err := r.renderConstructionPivotLineagePreimage(
		sourceQuery, stage, pivot, match, keyColumnBinds,
	)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	page := r.newInternalVariable("row_lineage_pivot_page")
	pageLines := append([]string(nil), pageSource...)
	pageLines = append(pageLines,
		"FILTER "+selected+" != null",
		"SORT "+pageVars.Source+"[@"+occurrenceKeyBind+"] ASC",
		"LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+pageVars.Source+"[@"+resourceIDBind+"], occurrenceKey: "+pageVars.Source+"[@"+occurrenceKeyBind+"]}",
	)

	lines := []string{
		"LET " + selected + " = FIRST(",
		indentQuery(strings.Join(selectedLines, "\n"), "  "),
		")",
		"LET " + page + " = (",
		indentQuery(strings.Join(pageLines, "\n"), "  "),
		")",
		"RETURN {found: " + selected + " != null, contributors: SLICE(" + page + ", 0, @" + terminal.LimitBindKey + "), hasMore: LENGTH(" + page + ") > @" + terminal.LimitBindKey + "}",
	}
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderConstructionPivotLineageValidation(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	pivot *ir.PhysicalGroupedPivot,
	match ir.PhysicalRowLineageStageMatch,
	keyColumnBinds []string,
	categoryColumnBind, categoryTypeBind, categoryPresenceBind, valueColumnBind, valueTypeBind string,
) ([]string, error) {
	source := r.newInternalVariable("row_lineage_pivot_validation_source")
	projected := r.newInternalVariable("row_lineage_pivot_validation_input")
	projection, err := r.renderReturn(ir.PhysicalReturn{Projections: pivot.InputProjections})
	if err != nil {
		return nil, fmt.Errorf("render construction Pivot source projection: %w", err)
	}
	lines := []string{"FOR " + source + " IN (", indentQuery(sourceQuery, "  "), ")", "LET " + stage.InputRowVariable + " = " + source}
	if pivot.CategoryPresence == nil {
		lines = append(lines, "LET "+projected+" = "+projection)
	} else {
		presence, presenceErr := r.renderProjectionPresence(*pivot.CategoryPresence)
		if presenceErr != nil {
			return nil, fmt.Errorf("render Pivot category presence: %w", presenceErr)
		}
		lines = append(lines, "LET "+projected+" = MERGE("+projection+", {[@"+categoryPresenceBind+"]: "+presence+"})")
	}

	groupedKeys := make([]string, len(pivot.GroupKeys))
	collectKeys := make([]string, len(pivot.GroupKeys))
	for index := range pivot.GroupKeys {
		inputKey := r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_validation_key_%d", index))
		groupedKeys[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_group_key_%d", index))
		lines = append(lines, "LET "+inputKey+" = "+projected+"[@"+keyColumnBinds[index]+"]")
		collectKeys[index] = groupedKeys[index] + " = " + inputKey
	}

	categoryListed, err := groupedPivotListedPredicate(*pivot, projected, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
	if err != nil {
		return nil, fmt.Errorf("render Pivot category membership: %w", err)
	}
	lines = append(lines, "FILTER ASSERT("+categoryListed+", \"TABLE_PIVOT_UNLISTED_CATEGORY\")")
	value := r.newInternalVariable("row_lineage_pivot_validation_value")
	lines = append(lines, "LET "+value+" = "+projected+"[@"+valueColumnBind+"]")

	aggregates := make([]string, 0, len(pivot.Categories)*2)
	policyFilters := make([]string, 0, len(pivot.Categories)*2)
	for index, category := range pivot.Categories {
		matchVariable := r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_category_match_%d", index))
		categoryMatch, matchErr := groupedPivotCategoryMatchPredicate(category, projected, categoryColumnBind, categoryPresenceBind, categoryTypeBind)
		if matchErr != nil {
			return nil, fmt.Errorf("Pivot category %q: %w", category.Output, matchErr)
		}
		lines = append(lines, "LET "+matchVariable+" = "+categoryMatch)
		if pivot.DuplicatePolicy != "ERROR" {
			lines = append(lines, fmt.Sprintf("FILTER NOT(%s) OR %s == null OR ASSERT(TYPENAME(%s) == @%s, \"TABLE_PIVOT_VALUE_TYPE_MISMATCH\")", matchVariable, value, value, valueTypeBind))
		}

		rawCount := r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_category_count_%d", index))
		nonNullCount := r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_category_nonnull_%d", index))
		aggregates = append(aggregates,
			rawCount+" = SUM("+matchVariable+" ? 1 : 0)",
			nonNullCount+" = SUM(("+matchVariable+" AND "+value+" != null) ? 1 : 0)",
		)
		if pivot.DuplicatePolicy == "ERROR" {
			policyFilters = append(policyFilters, fmt.Sprintf("ASSERT(%s <= 1, \"TABLE_PIVOT_CELL_CARDINALITY\")", rawCount))
		}
		if pivot.MissingCellPolicy == "ERROR" {
			presenceCount := rawCount
			if pivot.DuplicatePolicy != "ERROR" {
				presenceCount = nonNullCount
			}
			policyFilters = append(policyFilters, fmt.Sprintf("ASSERT(%s > 0, \"TABLE_PIVOT_CELL_MISSING\")", presenceCount))
		}
	}

	if len(collectKeys) == 0 {
		return nil, fmt.Errorf("construction Pivot lineage requires at least one group key")
	}
	lines = append(lines, "COLLECT "+strings.Join(collectKeys, ", ")+" AGGREGATE "+strings.Join(aggregates, ", "))
	for _, policyFilter := range policyFilters {
		lines = append(lines, "FILTER "+policyFilter)
	}
	identity, err := r.renderGroupedPivotIdentity(*pivot, groupedKeys)
	if err != nil {
		return nil, err
	}
	identityVariable := r.newInternalVariable("row_lineage_pivot_validation_identity")
	lines = append(lines, "LET "+identityVariable+" = "+identity)
	selectedCount := r.newInternalVariable("row_lineage_pivot_selected_group_count")
	lines = append(lines,
		"COLLECT AGGREGATE "+selectedCount+" = SUM("+identityVariable+" == @"+match.StageRowIDBindKey+" ? 1 : 0)",
		"FILTER "+selectedCount+" > 0",
	)
	return lines, nil
}

func (r *physicalPlanRenderer) renderConstructionPivotLineagePreimage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	pivot *ir.PhysicalGroupedPivot,
	match ir.PhysicalRowLineageStageMatch,
	keyColumnBinds []string,
) ([]string, constructionPivotLineageVariables, error) {
	source := r.newInternalVariable("row_lineage_pivot_source")
	projected := r.newInternalVariable("row_lineage_pivot_input")
	projection, err := r.renderReturn(ir.PhysicalReturn{Projections: pivot.InputProjections})
	if err != nil {
		return nil, constructionPivotLineageVariables{}, fmt.Errorf("render construction Pivot source projection: %w", err)
	}
	lines := []string{"FOR " + source + " IN (", indentQuery(sourceQuery, "  "), ")", "LET " + stage.InputRowVariable + " = " + source, "LET " + projected + " = " + projection}
	keyValues := make([]string, len(pivot.GroupKeys))
	for index := range pivot.GroupKeys {
		keyValues[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_pivot_group_key_%d", index))
		lines = append(lines, "LET "+keyValues[index]+" = "+projected+"[@"+keyColumnBinds[index]+"]")
		lines = append(lines, "FILTER "+keyValues[index]+" == @"+match.IdentityKeyBindKeys[index])
	}
	identity, err := r.renderGroupedPivotIdentity(*pivot, keyValues)
	if err != nil {
		return nil, constructionPivotLineageVariables{}, err
	}
	identityVariable := r.newInternalVariable("row_lineage_pivot_identity")
	lines = append(lines,
		"LET "+identityVariable+" = "+identity,
		"FILTER "+identityVariable+" == @"+match.StageRowIDBindKey,
	)
	return lines, constructionPivotLineageVariables{Source: source}, nil
}
