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
		if len(stages) == 1 && stages[0].Kind == ir.PhysicalStagePivotOp &&
			len(terminal.Trace.Stages) == 1 && terminal.Trace.Stages[0].Kind == ir.PhysicalStagePivotOp {
			return r.renderConstructionPivotRowLineage(sourceQuery, stages[0], terminal.Trace.Stages[0], terminal)
		}
		if len(stages) == 2 && stages[0].Kind == ir.PhysicalStageGroupOp && stages[1].Kind == ir.PhysicalStagePivotOp &&
			len(terminal.Trace.Stages) == 1 && terminal.Trace.Stages[0].Kind == ir.PhysicalStagePivotOp {
			return r.renderConstructionCountRowsGroupPivotRowLineage(sourceQuery, stages[0], stages[1], terminal.Trace.Stages[0], terminal)
		}
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
	if group.SemanticKey != nil {
		if len(stages) != 1 {
			return RenderedPhysicalPlan{}, fmt.Errorf("semantic Group row lineage currently requires a terminal direct-source Group")
		}
		return r.renderSemanticGroupRowLineage(sourceQuery, stage, terminal)
	}
	if group.RelatedOccurrence != nil {
		if len(stages) != 1 || group.CodingKey == nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("related Group row lineage requires one terminal CODING_IDENTITY source Group")
		}
		return r.renderRelatedCodingIdentityGroupRowLineage(sourceQuery, stage, terminal)
	}
	if group.CodingKey != nil {
		if len(stages) != 1 {
			return RenderedPhysicalPlan{}, fmt.Errorf("CODING_IDENTITY Group row lineage requires one terminal source Group")
		}
		return r.renderCodingIdentityGroupRowLineage(sourceQuery, stage, terminal)
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

func (r *physicalPlanRenderer) renderCodingIdentityGroupRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	group := stage.Group
	if group == nil || group.CodingKey == nil || group.SemanticKey != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("CODING_IDENTITY Group payload is required")
	}
	coding := group.CodingKey
	input := r.newInternalVariable("row_lineage_coding_group_input")
	selected := r.newInternalVariable("row_lineage_coding_selected_group")
	identity := r.newInternalVariable("row_lineage_coding_identity")
	codingInputTuple := r.newInternalVariable("row_lineage_coding_input_tuple")
	codingCollectedTuple := r.newInternalVariable("row_lineage_coding_collected_tuple")
	codingAlias := "codingTuple"
	keyVars := make([]string, len(group.Keys))
	keyCollected := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collectParts := make([]string, 0, len(group.Keys)+1)
	sortKeys := make([]string, 0, len(group.Keys)+1)
	selectedFields := make([]string, 0, len(group.Keys)+1)
	filters := make([]string, 0, len(group.Keys)*2+4)
	for index, key := range group.Keys {
		keyVars[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_coding_group_key_%d", index))
		keyCollected[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_coding_collected_key_%d", index))
		keyAliases[index] = fmt.Sprintf("key%d", index)
		columnBind := r.newInternalBindKeyWithValue("row_lineage_coding_group_input_column", key.InputColumn)
		filters = append(filters, fmt.Sprintf("LET %s = %s[@%s]", keyVars[index], input, columnBind))
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			filters = append(filters, "FILTER "+keyVars[index]+" != null")
		case ir.PhysicalStageGroupMissingKeyError:
			filters = append(filters, fmt.Sprintf("FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", keyVars[index]))
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKeyWithValue("row_lineage_coding_group_key_type", typeName)
		filters = append(filters, fmt.Sprintf("FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", keyVars[index], keyVars[index], typeBind))
		outputID := ""
		for _, column := range stage.OutputColumns {
			if column.Name == key.OutputColumn {
				outputID = column.ID
				break
			}
		}
		if outputID == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key output %q has no stable column ID", key.OutputColumn)
		}
		outputBind := r.newInternalBindKeyWithValue("row_lineage_coding_group_identity_column", outputID)
		identityParts = append(identityParts, "[@"+outputBind+", "+keyCollected[index]+"]")
		collectParts = append(collectParts, keyCollected[index]+" = "+keyVars[index])
		sortKeys = append(sortKeys, keyCollected[index]+" ASC")
		selectedFields = append(selectedFields, keyAliases[index]+": "+keyCollected[index])
	}
	codingOutputID := ""
	for _, column := range stage.OutputColumns {
		if column.Name == coding.OutputColumn {
			codingOutputID = column.ID
			break
		}
	}
	if codingOutputID == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("CODING_IDENTITY output %q has no stable column ID", coding.OutputColumn)
	}
	codingOutputBind := r.newInternalBindKeyWithValue("row_lineage_coding_identity_column", codingOutputID)
	codingMembershipBind := r.newInternalBindKeyWithValue("row_lineage_coding_membership_column", coding.MembershipColumn)
	codingMembership := r.newInternalVariable("row_lineage_coding_membership")
	codingMissingFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, codingInputTuple)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	identityParts = append(identityParts, "[@"+codingOutputBind+", \"coding_identity\", "+codingCollectedTuple+"]")
	collectParts = append(collectParts, codingCollectedTuple+" = "+codingInputTuple)
	sortKeys = append(sortKeys, codingCollectedTuple+" ASC")
	selectedFields = append(selectedFields, codingAlias+": "+codingCollectedTuple)
	matchLines := []string{
		"LET " + selected + " = FIRST(",
		"  FOR " + input + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
	}
	for _, filter := range filters {
		matchLines = append(matchLines, "  "+filter)
	}
	matchLines = append(matchLines,
		fmt.Sprintf("  LET %s = %s[@%s]", codingMembership, input, codingMembershipBind),
	)
	matchLines = append(matchLines, codingIdentityMembershipTypeAssertions(codingMembership)...)
	matchLines = append(matchLines, fmt.Sprintf("  LET %s = %s", codingInputTuple, codingIdentityTupleExpression(codingMembership)))
	if codingMissingFilter != "" {
		matchLines = append(matchLines, "  "+codingMissingFilter)
	}
	matchLines = append(matchLines,
		"  COLLECT "+strings.Join(collectParts, ", "),
		"  SORT "+strings.Join(sortKeys, ", "),
		"  LET "+identity+" = TO_STRING(["+strings.Join(identityParts, ", ")+"])",
		"  FILTER "+identity+" == @"+terminal.RowIDBindKey,
		"  RETURN {"+strings.Join(selectedFields, ", ")+"}",
		")",
	)

	contributor := r.newInternalVariable("row_lineage_coding_contributor")
	contributorMembership := r.newInternalVariable("row_lineage_coding_contributor_membership")
	contributorFilters := []string{selected + " != null"}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKeyWithValue("row_lineage_coding_contributor_column", key.InputColumn)
		value := fmt.Sprintf("%s[@%s]", contributor, columnBind)
		if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
			contributorFilters = append(contributorFilters, value+" != null")
		} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
			contributorFilters = append(contributorFilters, fmt.Sprintf("ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKeyWithValue("row_lineage_coding_contributor_type", typeName)
		contributorFilters = append(contributorFilters,
			fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind),
			value+" == "+selected+"."+keyAliases[index],
		)
	}
	contributorMembershipBind := r.newInternalBindKeyWithValue("row_lineage_coding_contributor_membership_column", coding.MembershipColumn)
	contributorTupleVariable := r.newInternalVariable("row_lineage_coding_contributor_input_tuple")
	contributorMissingFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, contributorTupleVariable)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	resourceIDBind := r.newInternalBindKeyWithValue("row_lineage_coding_resource_id_column", terminal.ResourceIDColumn)
	occurrenceBind := r.newInternalBindKeyWithValue("row_lineage_coding_occurrence_key_column", terminal.OccurrenceKeyColumn)
	resourceTypeBind := r.newInternalBindKeyWithValue("row_lineage_coding_resource_type", terminal.ResourceType)
	page := r.newInternalVariable("row_lineage_coding_page")
	lines := append([]string(nil), matchLines...)
	lines = append(lines,
		"LET "+page+" = (",
		"  FOR "+contributor+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
		fmt.Sprintf("  LET %s = %s[@%s]", contributorMembership, contributor, contributorMembershipBind),
	)
	lines = append(lines, codingIdentityMembershipTypeAssertions(contributorMembership)...)
	lines = append(lines, fmt.Sprintf("  LET %s = %s", contributorTupleVariable, codingIdentityTupleExpression(contributorMembership)))
	if contributorMissingFilter != "" {
		lines = append(lines, "  "+contributorMissingFilter)
	}
	for _, filter := range contributorFilters {
		lines = append(lines, "  FILTER "+filter)
	}
	lines = append(lines,
		"  FILTER "+contributorTupleVariable+" == "+selected+"."+codingAlias,
		"  SORT "+contributor+"[@"+occurrenceBind+"] ASC",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+contributor+"[@"+resourceIDBind+"], occurrenceKey: "+contributor+"[@"+occurrenceBind+"]}",
		")",
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderSemanticGroupRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	group := stage.Group
	if group == nil || group.SemanticKey == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("semantic Group payload is required")
	}
	semantic := group.SemanticKey
	lineageSemantic := *semantic
	// The direct Group input row carries `_key`, but the row-lineage source
	// projection exposes that same root identity under the hidden occurrence
	// key column. Reuse the projected identity when rehydrating the resource.
	lineageSemantic.SourceIdentityColumn = terminal.OccurrenceKeyColumn
	input := r.newInternalVariable("row_lineage_semantic_group_input")
	selected := r.newInternalVariable("row_lineage_semantic_selected_group")
	identity := r.newInternalVariable("row_lineage_semantic_identity")
	codeTuple := r.newInternalVariable("row_lineage_semantic_tuple")
	keyVars := make([]string, len(group.Keys))
	collectedVars := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collect := make([]string, 0, len(group.Keys)+1)
	sortKeys := make([]string, 0, len(group.Keys)+1)
	selectedFields := make([]string, 0, len(group.Keys)+1)
	filters := make([]string, 0, len(group.Keys)+1)
	for index, key := range group.Keys {
		keyVar := r.newInternalVariable(fmt.Sprintf("row_lineage_semantic_group_key_%d", index))
		collectedVar := r.newInternalVariable(fmt.Sprintf("row_lineage_semantic_collected_key_%d", index))
		keyVars[index], collectedVars[index] = keyVar, collectedVar
		keyAliases[index] = fmt.Sprintf("key%d", index)
		columnBind := r.newInternalBindKeyWithValue("row_lineage_semantic_group_input_column", key.InputColumn)
		filters = append(filters, fmt.Sprintf("LET %s = %s[@%s]", keyVar, input, columnBind))
		if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
			filters = append(filters, "FILTER "+keyVar+" != null")
		} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
			filters = append(filters, fmt.Sprintf("FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", keyVar))
		} else if group.MissingKeyPolicy != ir.PhysicalStageGroupMissingKeyGroup {
			return RenderedPhysicalPlan{}, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKeyWithValue("row_lineage_semantic_group_key_type", typeName)
		filters = append(filters, fmt.Sprintf("FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", keyVar, keyVar, typeBind))
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
		nameBind := r.newInternalBindKeyWithValue("row_lineage_semantic_group_identity_column", outputColumnID)
		identityParts = append(identityParts, "[@"+nameBind+", "+collectedVar+"]")
		collect = append(collect, collectedVar+" = "+keyVar)
		sortKeys = append(sortKeys, collectedVar+" ASC")
		selectedFields = append(selectedFields, keyAliases[index]+": "+collectedVar)
	}
	semanticMembershipLines, memberships, member, err := r.semanticGroupMembershipForLineage(input, &lineageSemantic)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	semanticTupleBind := r.newInternalBindKey("row_lineage_semantic_tuple_column")
	for _, column := range stage.OutputColumns {
		if column.Name == semantic.OutputColumn {
			r.bindVars[semanticTupleBind] = column.ID
			break
		}
	}
	if r.bindVars[semanticTupleBind] == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("semantic Group output %q has no stable column ID", semantic.OutputColumn)
	}
	collect = append(collect, codeTuple+" = ["+member+".system, "+member+".version, "+member+".code]")
	sortKeys = append(sortKeys, codeTuple+" ASC")
	selectedFields = append(selectedFields, "semanticTuple: "+codeTuple)
	familyBinding := r.newInternalBindKeyWithValue("row_lineage_semantic_group_binding", semantic.BindingID)
	familyRule := r.newInternalBindKeyWithValue("row_lineage_semantic_group_rule", semantic.RuleVersion)
	familyID := r.newInternalBindKeyWithValue("row_lineage_semantic_group_family", semantic.FamilyID)
	familySystem := r.newInternalBindKeyWithValue("row_lineage_semantic_group_system", semantic.System)
	familyVersion := r.newInternalBindKeyWithValue("row_lineage_semantic_group_version", semantic.Version)
	familyPath := r.newInternalBindKeyWithValue("row_lineage_semantic_group_path", semantic.CodingPath)
	identityParts = append(identityParts, "[@"+semanticTupleBind+", \"semantic_family\", @"+familyBinding+", @"+familyRule+", @"+familyID+", @"+familySystem+", @"+familyVersion+", @"+familyPath+", "+codeTuple+"]")
	semanticFilter := ""
	switch group.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup:
	case ir.PhysicalStageGroupMissingKeyExclude:
		semanticFilter = "FILTER " + member + ".code != null"
	case ir.PhysicalStageGroupMissingKeyError:
		semanticFilter = "FILTER ASSERT(" + member + ".code != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")"
	default:
		return RenderedPhysicalPlan{}, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
	}
	matchLines := []string{
		"LET " + selected + " = FIRST(",
		"  FOR " + input + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
	}
	matchLines = append(matchLines, filters...)
	matchLines = append(matchLines, semanticMembershipLines...)
	matchLines = append(matchLines, "  FOR "+member+" IN "+memberships)
	if semanticFilter != "" {
		matchLines = append(matchLines, "  "+semanticFilter)
	}
	matchLines = append(matchLines,
		"  COLLECT "+strings.Join(collect, ", "),
		"  SORT "+strings.Join(sortKeys, ", "),
		"  LET "+identity+" = TO_STRING(["+strings.Join(identityParts, ", ")+"])",
		"  FILTER "+identity+" == @"+terminal.RowIDBindKey,
		"  RETURN {"+strings.Join(selectedFields, ", ")+"}",
		")",
	)

	contributor := r.newInternalVariable("row_lineage_semantic_contributor")
	contributorMembershipLines, contributorMemberships, contributorMember, err := r.semanticGroupMembershipForLineage(contributor, &lineageSemantic)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	contributorFilters := []string{selected + " != null"}
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKeyWithValue("row_lineage_semantic_contributor_column", key.InputColumn)
		value := fmt.Sprintf("%s[@%s]", contributor, columnBind)
		if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
			contributorFilters = append(contributorFilters, value+" != null")
		} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
			contributorFilters = append(contributorFilters, fmt.Sprintf("ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", value))
		}
		contributorFilters = append(contributorFilters, value+" == "+selected+"."+keyAliases[index])
	}
	if semanticFilter != "" {
		if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyExclude {
			contributorFilters = append(contributorFilters, contributorMember+".code != null")
		} else if group.MissingKeyPolicy == ir.PhysicalStageGroupMissingKeyError {
			contributorFilters = append(contributorFilters, "ASSERT("+contributorMember+".code != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")")
		}
	}
	contributorFilters = append(contributorFilters,
		"["+contributorMember+".system, "+contributorMember+".version, "+contributorMember+".code] == "+selected+".semanticTuple")
	resourceIDBind := r.newInternalBindKeyWithValue("row_lineage_semantic_resource_id_column", terminal.ResourceIDColumn)
	occurrenceKeyBind := r.newInternalBindKeyWithValue("row_lineage_semantic_occurrence_key_column", terminal.OccurrenceKeyColumn)
	resourceTypeBind := r.newInternalBindKeyWithValue("row_lineage_semantic_resource_type", terminal.ResourceType)
	page := r.newInternalVariable("row_lineage_semantic_page")
	lines := append([]string(nil), matchLines...)
	lines = append(lines,
		"LET "+page+" = (",
		"  FOR "+contributor+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
	)
	lines = append(lines, contributorMembershipLines...)
	lines = append(lines,
		"  FOR "+contributorMember+" IN "+contributorMemberships,
		"  "+strings.Join(mapStrings(contributorFilters, "FILTER "), "\n  "),
		"  SORT "+contributor+"[@"+occurrenceKeyBind+"] ASC",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+contributor+"[@"+resourceIDBind+"], occurrenceKey: "+contributor+"[@"+occurrenceKeyBind+"]}",
		")",
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) semanticGroupMembershipForLineage(
	inputRow string,
	semantic *ir.PhysicalStageGroupSemanticKey,
) ([]string, string, string, error) {
	lines, memberships, err := r.renderConstructionGroupSemanticMembership(semantic, constructionGroupInput{}, inputRow)
	if err != nil {
		return nil, "", "", err
	}
	membership := r.newInternalVariable("row_lineage_semantic_membership")
	return lines, memberships, membership, nil
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
