package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// renderConstructionCountRowsGroupPivotRowLineage reverses a constrained
// Group(COUNT_ROWS) -> Pivot sequence. Validation streams grouped tuples into
// the existing full-scope Pivot policy checker; the requested row's source
// documents are then streamed directly from the original scoped source.
// Neither pass collects source records into arrays.
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

// renderConstructionCountRowsGroupedSource reproduces the authored Group's
// scalar-key and missing-key semantics while returning only its key tuple and
// COUNT_ROWS value. WITH COUNT INTO is a scalar count, not a retained row set.
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
