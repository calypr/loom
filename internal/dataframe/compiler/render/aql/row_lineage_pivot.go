package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderConstructionPivotRowLineage validates Pivot policies over the full
// scoped input, then pages the exact typed-key preimage without a contributor
// array.
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

type constructionPivotLineageVariables struct {
	Source string
}

// renderConstructionPivotLineageValidation matches grouped-Pivot execution
// semantics across every source group. Group aggregates retain counts only;
// the final scalar aggregate keeps selected-row discovery from constructing a
// result array proportional to the number of Pivot groups.
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
