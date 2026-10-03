package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionRowLineage(sourceQuery string, stages []ir.PhysicalConstructionStage, terminal ir.PhysicalRowLineageReturn) (RenderedPhysicalPlan, error) {
	if len(stages) == 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("row lineage requires a construction stage")
	}
	if terminal.Trace != nil {
		return r.renderComposedRelatedRowLineage(sourceQuery, stages, terminal)
	}
	stage := stages[0]
	if stage.Kind == ir.PhysicalStageCodedGroupOp {
		return r.renderCodedGroupRowLineage(sourceQuery, stage, terminal)
	}
	group := stage.Group
	if group == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("row lineage Group payload is required")
	}
	input := r.newInternalVariable("row_lineage_group_input")
	selected := r.newInternalVariable("row_lineage_selected_group")
	identity := r.newInternalVariable("row_lineage_identity")
	keyVars := make([]string, len(group.Keys))
	collectedVars := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collect := make([]string, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	selectedFields := make([]string, 0, len(group.Keys))
	for index, key := range group.Keys {
		keyVar := r.newInternalVariable(fmt.Sprintf("row_lineage_group_key_%d", index))
		collectedVar := r.newInternalVariable(fmt.Sprintf("row_lineage_collected_group_key_%d", index))
		keyVars[index] = keyVar
		collectedVars[index] = collectedVar
		keyAliases[index] = fmt.Sprintf("key%d", index)
		sortKeys = append(sortKeys, collectedVar+" ASC")
		outputColumnID := ""
		for _, column := range stage.OutputColumns {
			if column.Name == key.OutputColumn {
				outputColumnID = column.ID
				break
			}
		}
		if outputColumnID == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key output %q has no stable column ID", key.OutputColumn)
		}
		nameBind := r.newInternalBindKey("row_lineage_group_identity_column")
		r.bindVars[nameBind] = outputColumnID
		identityParts = append(identityParts, "[@"+nameBind+", "+collectedVar+"]")
		selectedFields = append(selectedFields, keyAliases[index]+": "+collectedVar)
	}
	requestedRowID := "@" + terminal.RowIDBindKey
	var matchLines []string
	if len(group.Keys) == 0 {
		count := r.newInternalVariable("row_lineage_empty_group_count")
		matchLines = append(matchLines,
			"LET "+selected+" = FIRST(",
			"  FOR "+input+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
			"  COLLECT WITH COUNT INTO "+count,
			"  LET "+identity+" = TO_STRING(["+strings.Join(identityParts, ", ")+"])",
			"  FILTER "+identity+" == "+requestedRowID,
			"  RETURN {}",
			")",
		)
	} else {
		filters := make([]string, 0, len(group.Keys)*2)
		for index, key := range group.Keys {
			if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
				filters = append(filters, keyVars[index]+" != null")
			} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
				filters = append(filters, fmt.Sprintf("ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", keyVars[index]))
			} else if group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup {
				return RenderedPhysicalPlan{}, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
			}
			typeName, err := aqlTableScalarType(key.Kind)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
			}
			typeBind := r.newInternalBindKey("row_lineage_group_key_type")
			r.bindVars[typeBind] = typeName
			filters = append(filters, fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", keyVars[index], keyVars[index], typeBind))
		}
		for index := range group.Keys {
			collect[index] = collectedVars[index] + " = " + keyVars[index]
		}
		matchLines = append(matchLines, "LET "+selected+" = FIRST(", "  FOR "+input+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )")
		for index, key := range group.Keys {
			columnBind := r.newInternalBindKey("row_lineage_group_input_column")
			r.bindVars[columnBind] = key.InputColumn
			matchLines = append(matchLines, fmt.Sprintf("  LET %s = %s[@%s]", keyVars[index], input, columnBind))
		}
		for _, filter := range filters {
			matchLines = append(matchLines, "  FILTER "+filter)
		}
		matchLines = append(matchLines, "  COLLECT "+strings.Join(collect, ", "), "  SORT "+strings.Join(sortKeys, ", "),
			"  LET "+identity+" = TO_STRING(["+strings.Join(identityParts, ", ")+"])",
			"  FILTER "+identity+" == "+requestedRowID,
			"  RETURN {"+strings.Join(selectedFields, ", ")+"}", ")")
	}
	filteredSelection := ""
	if len(stages) > 1 {
		passed, filterLines, renderErr := r.renderGroupFilterLineageCandidate(sourceQuery, stage, stages[1:], selected, keyAliases)
		if renderErr != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render Group filter lineage candidate: %w", renderErr)
		}
		filteredSelection = passed
		matchLines = append(matchLines, filterLines...)
	}
	page := r.newInternalVariable("row_lineage_page")
	contributor := r.newInternalVariable("row_lineage_contributor")
	filters := make([]string, 0, len(group.Keys)*2+2)
	filters = append(filters, selected+" != null")
	if filteredSelection != "" {
		filters = append(filters, filteredSelection+" != null")
	}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKey("row_lineage_contributor_column")
		r.bindVars[columnBind] = key.InputColumn
		value := fmt.Sprintf("%s[@%s]", contributor, columnBind)
		if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
			filters = append(filters, value+" != null")
		} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
			filters = append(filters, fmt.Sprintf("ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKey("row_lineage_contributor_type")
		r.bindVars[typeBind] = typeName
		filters = append(filters, fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind))
		filters = append(filters, value+" == "+selected+"."+keyAliases[index])
	}
	resourceIDBind := r.newInternalBindKey("row_lineage_resource_id_column")
	r.bindVars[resourceIDBind] = terminal.ResourceIDColumn
	occurrenceKeyBind := r.newInternalBindKey("row_lineage_occurrence_key_column")
	r.bindVars[occurrenceKeyBind] = terminal.OccurrenceKeyColumn
	resourceTypeBind := r.newInternalBindKey("row_lineage_resource_type")
	r.bindVars[resourceTypeBind] = terminal.ResourceType
	lines := append([]string(nil), matchLines...)
	lines = append(lines,
		"LET "+page+" = (",
		"  FOR "+contributor+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
		"  "+strings.Join(mapStrings(filters, "FILTER "), "\n  "),
		"  SORT "+contributor+"[@"+occurrenceKeyBind+"] ASC",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+contributor+"[@"+resourceIDBind+"], occurrenceKey: "+contributor+"[@"+occurrenceKeyBind+"]}",
		")",
		"RETURN {found: "+selected+" != null"+lineagePassedCondition(filteredSelection)+", contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func lineagePassedCondition(variable string) string {
	if variable == "" {
		return ""
	}
	return " AND " + variable + " != null"
}

func (r *physicalPlanRenderer) renderGroupFilterLineageCandidate(
	sourceQuery string,
	groupStage ir.PhysicalConstructionStage,
	filterStages []ir.PhysicalConstructionStage,
	selected string,
	keyAliases []string,
) (string, []string, error) {
	group := groupStage.Group
	if group == nil {
		return "", nil, fmt.Errorf("GROUP payload is required")
	}
	groupRows := r.newInternalVariable("row_lineage_filter_group_rows")
	groupOutput := groupStage
	candidateFilters := append([]ir.PhysicalConstructionStage(nil), filterStages...)
	contributorColumn := ""
	if group.RootContributorInputColumn != "" {
		contributorColumn = group.RootContributorOutputColumn
		groupCopy := *group
		groupCopy.RootContributorInputColumn = ""
		groupCopy.RootContributorInputMany = false
		groupCopy.RootContributorOutputColumn = ""
		groupCopy.RootContributorVariable = ""
		groupOutput.Group = &groupCopy
		groupOutput.OutputColumns = withoutStageColumn(groupOutput.OutputColumns, contributorColumn)
		groupOutput.OutputProjections = withoutStageProjection(groupOutput.OutputProjections, contributorColumn)
		for index := range candidateFilters {
			candidateFilters[index].InputColumns = withoutStageColumn(candidateFilters[index].InputColumns, contributorColumn)
			candidateFilters[index].OutputColumns = withoutStageColumn(candidateFilters[index].OutputColumns, contributorColumn)
			candidateFilters[index].OutputProjections = withoutStageProjection(candidateFilters[index].OutputProjections, contributorColumn)
		}
	}
	var lines, groupLines []string
	var err error
	streamGroup := len(group.Keys) != 0
	if streamGroup {
		input := groupStage.InputRowVariable
		lines = []string{
			"LET " + groupRows + " = (",
			"  FOR " + input + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
			"  FILTER " + selected + " != null",
		}
		for index, key := range group.Keys {
			columnBind := r.newInternalBindKey("row_lineage_filter_group_input_column")
			r.bindVars[columnBind] = key.InputColumn
			keyVar := r.newInternalVariable(fmt.Sprintf("row_lineage_filter_group_key_%d", index))
			lines = append(lines,
				"  LET "+keyVar+" = "+input+"[@"+columnBind+"]",
				"  FILTER "+keyVar+" == "+selected+"."+keyAliases[index],
			)
		}
		groupLines, err = r.renderConstructionGroupStage(groupOutput, constructionGroupInput{SourceRowInScope: true})
	} else {
		input := r.newInternalVariable("row_lineage_filter_source")
		inputRows := r.newInternalVariable("row_lineage_filter_group_inputs")
		lines = []string{
			"LET " + inputRows + " = (",
			"  FOR " + input + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
			"  FILTER " + selected + " != null",
		}
		for index, key := range group.Keys {
			columnBind := r.newInternalBindKey("row_lineage_filter_group_input_column")
			r.bindVars[columnBind] = key.InputColumn
			keyVar := r.newInternalVariable(fmt.Sprintf("row_lineage_filter_group_key_%d", index))
			lines = append(lines,
				"  LET "+keyVar+" = "+input+"[@"+columnBind+"]",
				"  FILTER "+keyVar+" == "+selected+"."+keyAliases[index],
			)
		}
		lines = append(lines, "  RETURN "+input, ")")
		lines = append(lines, "LET "+groupRows+" = (")
		groupLines, err = r.renderConstructionGroupStage(groupOutput, constructionGroupInput{RowsVariable: inputRows})
	}
	if err != nil {
		return "", nil, err
	}
	lines = append(lines, groupLines...)
	lines = append(lines, ")")
	priorRows := groupRows
	for index, filterStage := range candidateFilters {
		stageRows := r.newInternalVariable(fmt.Sprintf("row_lineage_filter_stage_%d", index+1))
		lines = append(lines,
			"LET "+stageRows+" = (",
			"  FOR "+filterStage.InputRowVariable+" IN "+priorRows,
		)
		predicate, renderErr := r.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: filterStage.Filter}, "  ")
		if renderErr != nil {
			return "", nil, fmt.Errorf("filter stage %q: %w", filterStage.ID, renderErr)
		}
		lines = append(lines, predicate...)
		object, renderErr := r.renderReturn(ir.PhysicalReturn{Projections: filterStage.OutputProjections})
		if renderErr != nil {
			return "", nil, fmt.Errorf("filter stage %q output row: %w", filterStage.ID, renderErr)
		}
		lines = append(lines,
			"  LET "+filterStage.OutputRowVariable+" = "+object,
			"  RETURN "+filterStage.OutputRowVariable,
			")",
		)
		priorRows = stageRows
	}
	passed := r.newInternalVariable("row_lineage_filter_selected")
	final := r.newInternalVariable("row_lineage_filter_final")
	lines = append(lines,
		"LET "+passed+" = FIRST(",
		"  FOR "+final+" IN "+priorRows,
		"  RETURN true",
		")",
	)
	return passed, lines, nil
}

func withoutStageColumn(columns []ir.PhysicalStageColumn, name string) []ir.PhysicalStageColumn {
	if name == "" {
		return columns
	}
	result := make([]ir.PhysicalStageColumn, 0, len(columns))
	for _, column := range columns {
		if column.Name != name {
			result = append(result, column)
		}
	}
	return result
}

func withoutStageProjection(projections []ir.PhysicalProjection, name string) []ir.PhysicalProjection {
	if name == "" {
		return projections
	}
	result := make([]ir.PhysicalProjection, 0, len(projections))
	for _, projection := range projections {
		if projection.Name != name {
			result = append(result, projection)
		}
	}
	return result
}

func indentQuery(query, prefix string) string {
	lines := strings.Split(strings.TrimSuffix(query, "\n"), "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n")
}

func mapStrings(values []string, prefix string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = prefix + value
	}
	return result
}
