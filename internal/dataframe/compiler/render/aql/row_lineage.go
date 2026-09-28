package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderConstructionRowLineage(sourceQuery string, stage ir.PhysicalConstructionStage, terminal ir.PhysicalRowLineageReturn) (RenderedPhysicalPlan, error) {
	if stage.Kind == ir.PhysicalStageRelatedExpandOp {
		return r.renderRelatedExpandRowLineage(sourceQuery, stage, terminal)
	}
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
	page := r.newInternalVariable("row_lineage_page")
	contributor := r.newInternalVariable("row_lineage_contributor")
	filters := make([]string, 0, len(group.Keys)*2+1)
	filters = append(filters, selected+" != null")
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
		"  "+strings.Join(append([]string{"FILTER " + filters[0]}, mapStrings(filters[1:], "FILTER ")...), "\n  "),
		"  SORT "+contributor+"[@"+occurrenceKeyBind+"] ASC",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN {resourceType: @"+resourceTypeBind+", resourceId: "+contributor+"[@"+resourceIDBind+"], occurrenceKey: "+contributor+"[@"+occurrenceKeyBind+"]}",
		")",
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r *physicalPlanRenderer) renderRelatedExpandRowLineage(sourceQuery string, stage ir.PhysicalConstructionStage, terminal ir.PhysicalRowLineageReturn) (RenderedPhysicalPlan, error) {
	related := stage.RelatedExpand
	if related == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("row lineage RELATED_EXPAND payload is required")
	}
	input := stage.InputRowVariable
	item := r.newInternalVariable("row_lineage_related_item")
	identity := r.newInternalVariable("row_lineage_related_identity")
	selected := r.newInternalVariable("row_lineage_related_selected")
	page := r.newInternalVariable("row_lineage_related_page")
	resourceIDColumnBind := r.newInternalBindKey("row_lineage_resource_id_column")
	r.bindVars[resourceIDColumnBind] = terminal.ResourceIDColumn
	occurrenceKeyColumnBind := r.newInternalBindKey("row_lineage_occurrence_key_column")
	r.bindVars[occurrenceKeyColumnBind] = terminal.OccurrenceKeyColumn
	parentIdentityColumnBind := r.newInternalBindKey("row_lineage_parent_identity_column")
	r.bindVars[parentIdentityColumnBind] = related.ParentIdentityColumn
	rootResourceTypeBind := r.newInternalBindKey("row_lineage_root_resource_type")
	r.bindVars[rootResourceTypeBind] = terminal.ResourceType
	targetResourceTypeBind := r.newInternalBindKey("row_lineage_target_resource_type")
	r.bindVars[targetResourceTypeBind] = related.TargetResourceType
	requestedRowID := "@" + terminal.RowIDBindKey
	constructionID := "@" + related.ConstructionIDBindKey
	var lines []string
	var contributorExpressions []string

	subplan := related.RelatedRecords
	subplan.Sort, subplan.Unique = nil, false
	if terminal.RelatedRowKind == "EMPTY" {
		if related.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
			return RenderedPhysicalPlan{}, fmt.Errorf("row lineage empty RELATED_EXPAND row requires PRESERVE_PARENT")
		}
		matches, err := r.renderSubplan(subplan, "    ", true)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render bounded exact related-route existence check: %w", err)
		}
		lines = append(lines,
			"LET "+selected+" = FIRST(",
			"  FOR "+input+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
			"  LET "+item+" = FIRST("+matches+")",
			"  FILTER "+item+" == null",
			"  LET "+identity+" = TO_STRING([[\"input\", "+input+"[@"+parentIdentityColumnBind+"]], [\"construction\", "+constructionID+"], [\"empty\"]])",
			"  FILTER "+identity+" == "+requestedRowID,
			"  RETURN {rootID: "+input+"[@"+resourceIDColumnBind+"], rootKey: "+input+"[@"+occurrenceKeyColumnBind+"]}",
			")",
		)
		contributorExpressions = append(contributorExpressions,
			"{resourceType: @"+rootResourceTypeBind+", resourceId: "+selected+".rootID, occurrenceKey: "+selected+".rootKey}",
		)
	} else {
		terminalVariable := ""
		for _, operation := range subplan.Operations {
			if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
				terminalVariable = operation.Traversal.TargetVariable
			}
		}
		if terminalVariable == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("row lineage RELATED_EXPAND route has no terminal traversal")
		}
		subplan.Operations = append(subplan.Operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: terminalVariable, Path: []string{"_id"}},
				Right: &ir.PhysicalValue{BindKey: terminal.RelatedTerminalIDBindKey},
			}},
		})
		match, err := r.renderSubplan(subplan, "    ", true)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render exact authorized related-route lookup: %w", err)
		}
		lines = append(lines,
			"LET "+selected+" = FIRST(",
			"  FOR "+input+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )",
			"  LET "+item+" = FIRST("+match+")",
			"  FILTER "+item+" != null",
			"  LET "+identity+" = TO_STRING([[\"input\", "+input+"[@"+parentIdentityColumnBind+"]], [\"construction\", "+constructionID+"], [\"related\", "+item+".terminal_id]])",
			"  FILTER "+identity+" == "+requestedRowID,
			"  RETURN {rootID: "+input+"[@"+resourceIDColumnBind+"], rootKey: "+input+"[@"+occurrenceKeyColumnBind+"], terminalID: "+item+".terminal_id, relatedID: "+item+".resource_id}",
			")",
		)
		targetOccurrenceKey := "PARSE_IDENTIFIER(" + selected + ".terminalID).key"
		contributorExpressions = append(contributorExpressions,
			"{resourceType: @"+rootResourceTypeBind+", resourceId: "+selected+".rootID, occurrenceKey: "+selected+".rootKey}",
			"{resourceType: @"+targetResourceTypeBind+", resourceId: "+selected+".relatedID, occurrenceKey: "+targetOccurrenceKey+"}",
		)
	}
	allContributors := r.newInternalVariable("row_lineage_related_contributors")
	lines = append(lines,
		"LET "+allContributors+" = ("+selected+" == null ? [] : ["+strings.Join(contributorExpressions, ", ")+"])",
		"LET "+page+" = SLICE("+allContributors+", @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey+")",
		"RETURN {found: "+selected+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
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
