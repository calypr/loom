package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderRelatedCodingIdentityGroupRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	group := stage.Group
	if group == nil || group.CodingKey == nil || group.RelatedOccurrence == nil || group.SemanticKey != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("related CODING_IDENTITY Group payload is required")
	}
	coding, adapter := group.CodingKey, group.RelatedOccurrence
	if err := validateConstructionGroupRelatedOccurrenceRendererShape(adapter); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if terminal.Trace != nil || terminal.RowIDBindKey == "" || terminal.ResourceType == "" ||
		terminal.ResourceIDColumn == "" || terminal.OccurrenceKeyColumn == "" ||
		terminal.OccurrenceKeyColumn != adapter.OccurrenceKeyColumn || coding.RelatedSource == nil ||
		coding.RelatedSource.RouteID != adapter.RouteID || coding.SourceUnnest != nil ||
		coding.MembershipColumn == "" || coding.MembershipColumn != adapter.CodingMembershipColumn ||
		coding.SourceOccurrenceKeyColumn != adapter.OccurrenceKeyColumn || coding.CodingPath == "" ||
		coding.CodingPath != adapter.CodingSelector || group.RootContributorInputColumn != "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("related CODING_IDENTITY Group lineage is not bound to its exact terminal occurrence")
	}
	for _, aggregate := range group.Aggregates {
		if aggregate.Operation != "COUNT_ROWS" || aggregate.InputColumn != "" || aggregate.InputKind != "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("related Group row lineage supports COUNT_ROWS aggregates only")
		}
	}
	if len(group.Aggregates) == 0 {
		return RenderedPhysicalPlan{}, fmt.Errorf("related Group row lineage requires COUNT_ROWS")
	}
	if _, ok := r.bindVars[terminal.RowIDBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("related Group lineage row identity bind %q is missing", terminal.RowIDBindKey)
	}

	input := r.newInternalVariable("row_lineage_related_input")
	selected, selectedIdentity, selectedTuple := r.newInternalVariable("row_lineage_related_selected"),
		r.newInternalVariable("row_lineage_related_identity"), "codingTuple"
	keyVars := make([]string, len(group.Keys))
	keyCollected := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collectParts := make([]string, 0, len(group.Keys)+1)
	sortKeys := make([]string, 0, len(group.Keys)+1)
	selectedFields := make([]string, 0, len(group.Keys)+1)
	keyLines := make([]string, 0, len(group.Keys)*3)
	for index, key := range group.Keys {
		if key.InputColumn == "" || key.OutputColumn == "" || key.Kind == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("related Group key %d is incomplete", index)
		}
		keyVars[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_related_group_key_%d", index))
		keyCollected[index] = r.newInternalVariable(fmt.Sprintf("row_lineage_related_collected_key_%d", index))
		keyAliases[index] = fmt.Sprintf("key%d", index)
		columnBind := r.newInternalBindKeyWithValue("row_lineage_related_group_input_column", key.InputColumn)
		keyLines = append(keyLines, fmt.Sprintf("LET %s = %s[@%s]", keyVars[index], input, columnBind))
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyGroup:
		case ir.PhysicalStageGroupMissingKeyExclude:
			keyLines = append(keyLines, "FILTER "+keyVars[index]+" != null")
		case ir.PhysicalStageGroupMissingKeyError:
			keyLines = append(keyLines, fmt.Sprintf("FILTER ASSERT(%s != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")", keyVars[index]))
		default:
			return RenderedPhysicalPlan{}, fmt.Errorf("unsupported Group missing-key policy %q", group.MissingKeyPolicy)
		}
		typeName, err := aqlTableScalarType(key.Kind)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key %q: %w", key.InputColumn, err)
		}
		typeBind := r.newInternalBindKeyWithValue("row_lineage_related_group_key_type", typeName)
		keyLines = append(keyLines, fmt.Sprintf("FILTER ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", keyVars[index], keyVars[index], typeBind))
		outputID := stageOutputColumnID(stage.OutputColumns, key.OutputColumn)
		if outputID == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("Group key output %q has no stable column ID", key.OutputColumn)
		}
		outputBind := r.newInternalBindKeyWithValue("row_lineage_related_group_identity_column", outputID)
		identityParts = append(identityParts, "[@"+outputBind+", "+keyCollected[index]+"]")
		collectParts = append(collectParts, keyCollected[index]+" = "+keyVars[index])
		sortKeys = append(sortKeys, keyCollected[index]+" ASC")
		selectedFields = append(selectedFields, keyAliases[index]+": "+keyCollected[index])
	}

	codingOutputID := stageOutputColumnID(stage.OutputColumns, coding.OutputColumn)
	if codingOutputID == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("CODING_IDENTITY output %q has no stable column ID", coding.OutputColumn)
	}
	codingOutputBind := r.newInternalBindKeyWithValue("row_lineage_related_coding_identity_column", codingOutputID)
	codingMembershipBind := r.newInternalBindKeyWithValue("row_lineage_related_coding_membership_column", coding.MembershipColumn)
	codingMembership := r.newInternalVariable("row_lineage_related_coding_membership")
	codingInputTuple := r.newInternalVariable("row_lineage_related_coding_input_tuple")
	identityParts = append(identityParts, "[@"+codingOutputBind+", \"coding_identity\", "+selectedTuple+"]")
	collectParts = append(collectParts, selectedTuple+" = "+codingInputTuple)
	sortKeys = append(sortKeys, selectedTuple+" ASC")
	selectedFields = append(selectedFields, selectedTuple+": "+selectedTuple)
	codingMissingFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, codingInputTuple)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	identityColumnBinds := strings.Join(identityParts, ", ")
	collect := "COLLECT " + strings.Join(collectParts, ", ") + " AGGREGATE " + r.newInternalVariable("row_lineage_related_count") + " = SUM(1)"

	selectedInput, err := r.renderRelatedCodingGroupInputSubquery(sourceQuery, stage, adapter, "selection")
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	selectedLines := []string{
		"LET " + selected + " = FIRST(",
		"  FOR " + input + " IN (\n" + indentQuery(selectedInput, "    ") + "\n  )",
	}
	selectedLines = append(selectedLines, indentLines(keyLines, "  ")...)
	selectedLines = append(selectedLines,
		fmt.Sprintf("  LET %s = %s[@%s]", codingMembership, input, codingMembershipBind),
	)
	selectedLines = append(selectedLines, indentLines(codingIdentityMembershipTypeAssertions(codingMembership), "  ")...)
	selectedLines = append(selectedLines,
		fmt.Sprintf("  LET %s = %s", codingInputTuple, codingIdentityTupleExpression(codingMembership)),
	)
	if codingMissingFilter != "" {
		selectedLines = append(selectedLines, "  "+codingMissingFilter)
	}
	selectedLines = append(selectedLines,
		"  "+collect,
		"  SORT "+strings.Join(sortKeys, ", "),
		"  LET "+selectedIdentity+" = TO_STRING(["+identityColumnBinds+"])",
		"  FILTER "+selectedIdentity+" == @"+terminal.RowIDBindKey,
		"  RETURN {"+strings.Join(selectedFields, ", ")+"}",
		")",
	)

	contributor := r.newInternalVariable("row_lineage_related_contributor")
	contributorMembership := r.newInternalVariable("row_lineage_related_contributor_membership")
	contributorTuple := r.newInternalVariable("row_lineage_related_contributor_tuple")
	contributorFilters := make([]string, 0, len(group.Keys)*3+3)
	for index, key := range group.Keys {
		columnBind := r.newInternalBindKeyWithValue("row_lineage_related_contributor_column", key.InputColumn)
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
		typeBind := r.newInternalBindKeyWithValue("row_lineage_related_contributor_key_type", typeName)
		contributorFilters = append(contributorFilters,
			fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind),
			value+" == "+selected+"."+keyAliases[index],
		)
	}
	contributorMembershipBind := r.newInternalBindKeyWithValue("row_lineage_related_contributor_membership_column", coding.MembershipColumn)
	contributorMissingFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, contributorTuple)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	resourceIDBind := r.newInternalBindKeyWithValue("row_lineage_related_resource_id_column", terminal.ResourceIDColumn)
	occurrenceBind := r.newInternalBindKeyWithValue("row_lineage_related_occurrence_key_column", terminal.OccurrenceKeyColumn)
	resourceTypeBind := r.newInternalBindKeyWithValue("row_lineage_related_resource_type", terminal.ResourceType)
	page := r.newInternalVariable("row_lineage_related_page")
	pageInput, err := r.renderRelatedCodingGroupInputSubquery(sourceQuery, stage, adapter, "contributors")
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	pageLines := []string{
		"LET " + page + " = (",
		"  FOR " + contributor + " IN (\n" + indentQuery(pageInput, "    ") + "\n  )",
		"  FILTER " + selected + " != null",
		fmt.Sprintf("  LET %s = %s[@%s]", contributorMembership, contributor, contributorMembershipBind),
	}
	pageLines = append(pageLines, indentLines(codingIdentityMembershipTypeAssertions(contributorMembership), "  ")...)
	pageLines = append(pageLines, fmt.Sprintf("  LET %s = %s", contributorTuple, codingIdentityTupleExpression(contributorMembership)))
	if contributorMissingFilter != "" {
		pageLines = append(pageLines, "  "+contributorMissingFilter)
	}
	pageLines = append(pageLines, indentLines(mapStrings(contributorFilters, "FILTER "), "  ")...)
	pageLines = append(pageLines,
		"  FILTER "+contributorTuple+" == "+selected+"."+selectedTuple,
		"  SORT "+contributor+"[@"+occurrenceBind+"] ASC",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+contributor+"[@"+resourceIDBind+"], occurrenceKey: "+contributor+"[@"+occurrenceBind+"]}",
		")",
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	lines := append(selectedLines, pageLines...)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderRelatedCodingGroupInputSubquery(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	adapter *ir.PhysicalStageGroupRelatedOccurrenceAdapter,
	name string,
) (string, error) {
	parent := stage.InputRowVariable
	if parent == "" {
		return "", fmt.Errorf("related Group %s input has no typed parent-row variable", name)
	}
	lines := []string{"FOR " + parent + " IN (\n" + indentQuery(sourceQuery, "  ") + "\n)"}
	prepared, merged, err := r.renderConstructionGroupRelatedOccurrenceInput(stage, parent, adapter, false)
	if err != nil {
		return "", fmt.Errorf("prepare related Group %s input: %w", name, err)
	}
	lines = append(lines, prepared...)
	lines = append(lines, "RETURN "+merged)
	return strings.Join(lines, "\n"), nil
}

func stageOutputColumnID(columns []ir.PhysicalStageColumn, name string) string {
	for _, column := range columns {
		if column.Name == name {
			return column.ID
		}
	}
	return ""
}

func indentLines(lines []string, prefix string) []string {
	indented := make([]string, len(lines))
	for index, line := range lines {
		indented[index] = prefix + strings.TrimLeft(line, " \t")
	}
	return indented
}
