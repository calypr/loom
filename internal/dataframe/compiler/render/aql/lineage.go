package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func composedLineageIdentityPreservingStage(kind ir.PhysicalStageOperationKind) bool {
	switch kind {
	case ir.PhysicalStageDeriveOp, ir.PhysicalStageFilterOp, ir.PhysicalStageRelatedEligibilityOp,
		ir.PhysicalStageRelatedSourceOp, ir.PhysicalStageRelatedFieldOp:
		return true
	default:
		return false
	}
}

func (r rowLineageRenderer) renderComposedRelatedRowLineage(
	sourceQuery string,
	stages []ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	trace := terminal.Trace
	if trace == nil || trace.RootKeyBindKey == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage requires a validated root key trace")
	}
	if _, ok := r.bindVars[trace.RootKeyBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage root key bind %q is missing", trace.RootKeyBindKey)
	}
	if terminal.RowIDBindKey == "" || terminal.ResourceType == "" || terminal.ResourceIDColumn == "" || terminal.OccurrenceKeyColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage is missing terminal or root contributor metadata")
	}
	if _, ok := r.bindVars[terminal.RowIDBindKey]; !ok {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage final identity bind %q is missing", terminal.RowIDBindKey)
	}
	if len(stages) == 0 || stages[len(stages)-1].RowIdentityColumn == "" {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage final stage has no identity column")
	}

	matches := make(map[string]ir.PhysicalRowLineageStageMatch, len(trace.Stages))
	for _, match := range trace.Stages {
		if match.StageID == "" || match.Kind != ir.PhysicalStageRelatedExpandOp || match.StageRowIDBindKey == "" || match.RelatedTerminalIDBindKey == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage has an incomplete related-stage match")
		}
		if _, duplicate := matches[match.StageID]; duplicate {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage repeats stage %q", match.StageID)
		}
		if _, ok := r.bindVars[match.StageRowIDBindKey]; !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q identity bind %q is missing", match.StageID, match.StageRowIDBindKey)
		}
		if _, ok := r.bindVars[match.RelatedTerminalIDBindKey]; !ok {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q terminal bind %q is missing", match.StageID, match.RelatedTerminalIDBindKey)
		}
		if match.RelatedRowKind != "RELATED" && match.RelatedRowKind != "EMPTY" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage stage %q has unsupported row kind %q", match.StageID, match.RelatedRowKind)
		}
		matches[match.StageID] = match
	}

	rootSource := r.core.navigation().newInternalVariable("row_lineage_root_source")
	rootKeyColumnBind := r.core.newInternalBindKey("row_lineage_root_key_column")
	r.bindVars[rootKeyColumnBind] = terminal.OccurrenceKeyColumn
	rootIDColumnBind := r.core.newInternalBindKey("row_lineage_root_resource_id_column")
	r.bindVars[rootIDColumnBind] = terminal.ResourceIDColumn
	rootTypeBind := r.core.newInternalBindKey("row_lineage_root_resource_type")
	r.bindVars[rootTypeBind] = terminal.ResourceType
	states := r.core.navigation().newInternalVariable("row_lineage_composed_source_rows")
	lines := []string{
		"LET " + states + " = (",
		"  FOR " + rootSource + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",

		"  FILTER " + rootSource + "[@" + rootKeyColumnBind + "] == @" + trace.RootKeyBindKey,
		"  RETURN {row: " + rootSource + ", root: {resourceType: @" + rootTypeBind + ", resourceId: " + rootSource + "[@" + rootIDColumnBind + "], occurrenceKey: " + rootSource + "[@" + rootKeyColumnBind + "]}}",
		")",
	}

	witnessFields := make([]string, 0, len(matches))
	traceIndex := 0
	for _, stage := range stages {
		if stage.ID == "" {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage construction stage has no ID")
		}
		if stage.Kind == ir.PhysicalStageRelatedExpandOp {
			match, ok := matches[stage.ID]
			if !ok || match.Kind != stage.Kind || traceIndex >= len(trace.Stages) || trace.Stages[traceIndex].StageID != stage.ID {
				return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage has no ordered validated identity for related stage %q", stage.ID)
			}
			if stage.RelatedExpand == nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage related stage %q has no typed payload", stage.ID)
			}
			traceIndex++
			field := r.core.navigation().newInternalVariable("row_lineage_stage_witness")
			previousFields := append([]string(nil), witnessFields...)
			witnessFields = append(witnessFields, field)
			stageRows, stageLines, err := r.renderComposedRelatedLineageStage(states, stage, match, field, previousFields)
			if err != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render composed related stage %q: %w", stage.ID, err)
			}
			lines = append(lines, stageLines...)
			states = stageRows
			continue
		}
		if !composedLineageIdentityPreservingStage(stage.Kind) {
			return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage does not replay stage %q of kind %q", stage.ID, stage.Kind)
		}
		stageRows, stageLines, err := r.renderComposedLineageProjectionStage(states, stage, witnessFields)
		if err != nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("render composed identity-preserving stage %q: %w", stage.ID, err)
		}
		lines = append(lines, stageLines...)
		states = stageRows
	}
	if traceIndex != len(trace.Stages) {
		return RenderedPhysicalPlan{}, fmt.Errorf("composed row lineage trace contains a stage outside the construction sequence")
	}

	finalState := r.core.navigation().newInternalVariable("row_lineage_composed_final_state")
	finalCandidate := r.core.navigation().newInternalVariable("row_lineage_composed_final_candidate")
	finalIdentityColumnBind := r.core.newInternalBindKey("row_lineage_final_identity_column")
	r.bindVars[finalIdentityColumnBind] = stages[len(stages)-1].RowIdentityColumn
	lines = append(lines,
		"LET "+finalState+" = FIRST(",
		"  FOR "+finalCandidate+" IN "+states,
		"  FILTER "+finalCandidate+".row[@"+finalIdentityColumnBind+"] == @"+terminal.RowIDBindKey,
		"  RETURN "+finalCandidate,
		")",
	)

	page := r.core.navigation().newInternalVariable("row_lineage_composed_page")
	ordinal := r.core.navigation().newInternalVariable("row_lineage_composed_ordinal")
	contributor := r.core.navigation().newInternalVariable("row_lineage_composed_contributor")
	selected := make([]string, 0, len(witnessFields)+1)
	selected = append(selected, finalState+".root")
	for _, field := range witnessFields {
		selected = append(selected, finalState+"."+field)
	}
	contributorExpression := "null"
	for index := len(selected) - 1; index >= 0; index-- {
		contributorExpression = fmt.Sprintf("%s == %d ? %s : (%s)", ordinal, index, selected[index], contributorExpression)
	}
	lines = append(lines,
		"LET "+page+" = (",
		"  FOR "+ordinal+" IN 0.."+fmt.Sprint(len(selected)-1),
		"  LET "+contributor+" = "+contributorExpression,
		"  FILTER "+finalState+" != null && "+contributor+" != null",
		"  LIMIT @"+terminal.OffsetBindKey+", @"+terminal.FetchLimitBindKey,
		"  RETURN "+contributor,
		")",
		"RETURN {found: "+finalState+" != null, contributors: SLICE("+page+", 0, @"+terminal.LimitBindKey+"), hasMore: LENGTH("+page+") > @"+terminal.LimitBindKey+"}",
	)
	query := strings.Join(lines, "\n") + "\n"
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(r.bindVars, query)}, nil
}

func (r rowLineageRenderer) renderComposedLineageProjectionStage(
	priorRows string,
	stage ir.PhysicalConstructionStage,
	witnessFields []string,
) (string, []string, error) {
	states := r.core.navigation().newInternalVariable("row_lineage_composed_projection_rows")
	state := r.core.navigation().newInternalVariable("row_lineage_composed_projection_state")
	lines := []string{"LET " + states + " = (", "  FOR " + state + " IN " + priorRows,
		"  LET " + stage.InputRowVariable + " = " + state + ".row"}
	for index, operation := range stage.DerivedLets {
		if operation.Kind != ir.PhysicalExpressionLetOp {
			return "", nil, fmt.Errorf("derived operation %d has kind %q", index, operation.Kind)
		}
		rendered, err := r.core.renderExpressionLet(operation, "  ")
		if err != nil {
			return "", nil, fmt.Errorf("render derived operation %d: %w", index, err)
		}
		lines = append(lines, rendered...)
	}
	if stage.Filter != nil {
		rendered, err := r.core.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}, "  ")
		if err != nil {
			return "", nil, fmt.Errorf("render filter: %w", err)
		}
		lines = append(lines, rendered...)
	}
	output, err := r.core.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return "", nil, fmt.Errorf("render output row: %w", err)
	}
	lines = append(lines, "  LET "+stage.OutputRowVariable+" = "+output)
	fields := []string{"row: " + stage.OutputRowVariable, "root: " + state + ".root"}
	for _, field := range witnessFields {
		fields = append(fields, field+": "+state+"."+field)
	}
	lines = append(lines, "  RETURN {"+strings.Join(fields, ", ")+"}", ")")
	return states, lines, nil
}

func (r rowLineageRenderer) renderComposedRelatedLineageStage(
	priorRows string,
	stage ir.PhysicalConstructionStage,
	match ir.PhysicalRowLineageStageMatch,
	witnessField string,
	previousWitnessFields []string,
) (string, []string, error) {
	related := stage.RelatedExpand
	if match.RelatedRowKind == "EMPTY" && related.EmptyPolicy != ir.PhysicalUnnestPreserveParent {
		return "", nil, fmt.Errorf("empty identity requires PRESERVE_PARENT")
	}
	input := stage.InputRowVariable
	state := r.core.navigation().newInternalVariable("row_lineage_composed_related_state")
	rows := r.core.navigation().newInternalVariable("row_lineage_composed_related_rows")
	item := related.ItemVariable
	if item == "" || related.IdentityVariable == "" || related.ParentIdentityColumn == "" {
		return "", nil, fmt.Errorf("related stage is missing identity variables")
	}

	subplan := related.RelatedRecords
	subplan.Sort, subplan.Unique, subplan.DistinctBy = nil, false, nil
	if match.RelatedRowKind == "RELATED" {
		terminalVariable := ""
		for _, operation := range subplan.Operations {
			if operation.Kind == ir.PhysicalTraversalOp && operation.Traversal != nil {
				terminalVariable = operation.Traversal.TargetVariable
			}
		}
		if terminalVariable == "" {
			return "", nil, fmt.Errorf("related route has no terminal traversal")
		}
		operations := append([]ir.PhysicalOperation(nil), subplan.Operations...)
		operations = append(operations, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: terminalVariable, Path: []string{"_id"}},
				Right: &ir.PhysicalValue{BindKey: match.RelatedTerminalIDBindKey},
			}},
		})
		subplan.Operations = operations
	}
	route, err := r.core.renderSubplan(subplan, "  ", true)
	if err != nil {
		return "", nil, fmt.Errorf("render exact authorized route: %w", err)
	}

	lines := []string{"LET " + rows + " = (", "  FOR " + state + " IN " + priorRows,
		"  LET " + input + " = " + state + ".row",
		"  LET " + item + " = FIRST(" + route + ")"}
	if match.RelatedRowKind == "EMPTY" {
		lines = append(lines, "  FILTER "+item+" == null")
	} else {
		lines = append(lines, "  FILTER "+item+" != null")
	}
	parentIdentityColumnBind := r.core.newInternalBindKey("row_lineage_related_parent_identity_column")
	r.bindVars[parentIdentityColumnBind] = related.ParentIdentityColumn
	identity := constructionRelatedExpandIdentityExpression(input, parentIdentityColumnBind, related.ConstructionIDBindKey, item)
	output, err := r.core.renderReturn(ir.PhysicalReturn{Projections: stage.OutputProjections})
	if err != nil {
		return "", nil, fmt.Errorf("render related output row: %w", err)
	}
	lines = append(lines,
		"  LET "+related.IdentityVariable+" = "+identity,
		"  LET "+stage.OutputRowVariable+" = "+output,
	)
	rowIdentityColumnBind := r.core.newInternalBindKey("row_lineage_related_stage_identity_column")
	r.bindVars[rowIdentityColumnBind] = stage.RowIdentityColumn
	lines = append(lines, "  FILTER "+stage.OutputRowVariable+"[@"+rowIdentityColumnBind+"] == @"+match.StageRowIDBindKey)
	witness := "null"
	if match.RelatedRowKind == "RELATED" {
		targetTypeBind := r.core.newInternalBindKey("row_lineage_related_resource_type")
		r.bindVars[targetTypeBind] = related.TargetResourceType
		witness = "{resourceType: @" + targetTypeBind + ", resourceId: " + item + ".resource_id, occurrenceKey: PARSE_IDENTIFIER(" + item + ".terminal_id).key}"
	}
	fields := []string{"row: " + stage.OutputRowVariable, "root: " + state + ".root"}
	for _, field := range previousWitnessFields {
		fields = append(fields, field+": "+state+"."+field)
	}
	fields = append(fields, witnessField+": "+witness)
	lines = append(lines, "  RETURN {"+strings.Join(fields, ", ")+"}", ")")
	return rows, lines, nil
}

func RenderPhysicalGroupRowsLineage(
	plan ir.PhysicalPlan,
	requestedRevisionID, requestedGroupID string,
	offset, limit int,
) (RenderedPhysicalPlan, error) {
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate physical group rows plan: %w", err)
	}
	if plan.StageSequence != nil || len(plan.Operations) != 1 ||
		plan.Operations[0].Kind != ir.PhysicalGroupRowsOp || plan.Operations[0].GroupRows == nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("group row lineage requires one standalone GROUP_ROWS operation")
	}
	if offset < 0 || limit < 1 {
		return RenderedPhysicalPlan{}, fmt.Errorf("group row lineage page bounds are invalid")
	}
	return renderPhysicalGroupRowsLineage(plan, *plan.Operations[0].GroupRows, nil, requestedRevisionID, requestedGroupID, offset, limit)
}

func RenderPhysicalCohortGroupRowsLineage(
	plan ir.PhysicalPlan,
	groupStage ir.PhysicalConstructionStage,
	filterStages []ir.PhysicalConstructionStage,
	requestedRevisionID, requestedGroupID string,
	offset, limit int,
) (RenderedPhysicalPlan, error) {
	if err := plan.Validate(); err != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("validate cohort group row lineage plan: %w", err)
	}
	if plan.StageSequence == nil || groupStage.Kind != ir.PhysicalStageCohortGroupOp || groupStage.CohortGroup == nil ||
		len(filterStages) == 0 || offset < 0 || limit < 1 || len(plan.StageSequence.Stages) != len(filterStages)+1 ||
		plan.StageSequence.Stages[0].ID != groupStage.ID || plan.StageSequence.Stages[0].Kind != groupStage.Kind {
		return RenderedPhysicalPlan{}, fmt.Errorf("cohort group row lineage requires a cohort owner, FILTER suffix, and valid page bounds")
	}
	for index, stage := range filterStages {
		if plan.StageSequence.Stages[index+1].ID != stage.ID || plan.StageSequence.Stages[index+1].Kind != ir.PhysicalStageFilterOp || stage.Filter == nil {
			return RenderedPhysicalPlan{}, fmt.Errorf("cohort group row lineage FILTER suffix does not match the validated stage sequence")
		}
	}
	rows := groupStage.CohortGroup.Rows
	return renderPhysicalGroupRowsLineage(plan, rows, filterStages, requestedRevisionID, requestedGroupID, offset, limit)
}

func renderPhysicalGroupRowsLineage(
	plan ir.PhysicalPlan,
	rows ir.PhysicalGroupRows,
	filterStages []ir.PhysicalConstructionStage,
	requestedRevisionID, requestedGroupID string,
	offset, limit int,
) (RenderedPhysicalPlan, error) {
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := validateRenderablePhysicalPlan(plan, collectionKeys); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	binds := runtimePhysicalBindVars(plan.BindVars, collectionKeys)
	requestedRevisionBind := uniqueBindKey(binds, "row_lineage_group_revision_id")
	requestedGroupBind := uniqueBindKey(binds, "row_lineage_group_id")
	offsetBind := uniqueBindKey(binds, "row_lineage_offset")
	limitBind := uniqueBindKey(binds, "row_lineage_limit")
	fetchLimitBind := uniqueBindKey(binds, "row_lineage_fetch_limit")
	binds[requestedRevisionBind] = requestedRevisionID
	binds[requestedGroupBind] = requestedGroupID
	binds[offsetBind] = offset
	binds[limitBind] = limit
	binds[fetchLimitBind] = limit + 1

	candidateLines := ""
	foundExpression := fmt.Sprintf("@%s == revision._key AND (definition != null OR (@%s == \"__loom_unassigned__\" AND @%s == \"GROUP_AS_UNASSIGNED\" AND unassigned_count > 0))", requestedRevisionBind, requestedGroupBind, rows.PolicyBindKey)
	if len(filterStages) != 0 {
		renderer := physicalPlanRenderer{renderContext: renderContext{
			bindVars: binds, collectionKeys: collectionKeys, setVariables: map[string]string{},
			reservedVars: stageSequenceVariableNames(plan), internalPrefix: "cohort_lineage_",
		}}
		candidate := renderer.navigation().newInternalVariable("candidate")
		unassignedMaximum := renderer.navigation().newInternalVariable("unassigned_max_ordinal")
		candidateLines = fmt.Sprintf(`LET %s = @%s == "__loom_unassigned__" ? FIRST(
  FOR candidate_definition IN @@%s
    FILTER candidate_definition.revisionId == revision._key AND candidate_definition.project == @%s
    COLLECT AGGREGATE maximum = MAX(candidate_definition.ordinal)
    RETURN maximum) : null
LET %s = @%s == revision._key ? (definition != null ? {group_id: definition.groupId, group_label: definition.label, group_ordinal: definition.ordinal} : (@%s == "__loom_unassigned__" AND @%s == "GROUP_AS_UNASSIGNED" AND unassigned_count > 0 ? {group_id: "__loom_unassigned__", group_label: "Unassigned", group_ordinal: (%s == null ? 1 : %s + 1)} : null)) : null`,
			unassignedMaximum, requestedGroupBind, rows.DefinitionsCollectionBindKey, rows.ProjectBindKey,
			candidate, requestedRevisionBind, requestedGroupBind, rows.PolicyBindKey, unassignedMaximum, unassignedMaximum)
		current := candidate
		var filterLines []string
		for _, stage := range filterStages {
			filtered := renderer.navigation().newInternalVariable("filtered_candidate")
			operations, renderErr := renderer.renderScopeOperation(
				ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: stage.Filter}, "      ",
			)
			if renderErr != nil {
				return RenderedPhysicalPlan{}, fmt.Errorf("render cohort row filter %q: %w", stage.ID, renderErr)
			}
			filterLines = append(filterLines,
				"LET "+filtered+" = FIRST(",
				"  FOR "+stage.InputRowVariable+" IN ("+current+" == null ? [] : ["+current+"])",
			)
			filterLines = append(filterLines, operations...)
			filterLines = append(filterLines, "      RETURN "+stage.InputRowVariable, "  )")
			current = filtered
		}
		candidateLines += "\n" + strings.Join(filterLines, "\n")
		foundExpression = current + " != null"
	}

	query := fmt.Sprintf(`LET revision = DOCUMENT(@@%s, @%s)
FOR revision_guard IN [ASSERT(revision != null AND revision.state == "COMPLETE" AND revision.project == @%s AND revision.generation == @%s AND revision.resourceType == @%s, "EXPLICIT_GROUP_REVISION_INVALID")]
  FILTER revision_guard
LET selection = DOCUMENT(@@%s, revision.sourceSelectionRevisionId)
FOR selection_guard IN [ASSERT(selection != null AND selection.complete == true AND selection.project == @%s AND selection.generation == @%s AND selection.resourceType == @%s AND revision.scopeDigest == selection.scopeDigest AND revision.sourceMembershipDigest == selection.membershipDigest, "EXPLICIT_GROUP_SOURCE_STALE")]
  FILTER selection_guard
LET unassigned_count = FIRST(FOR selected IN @@%s
  FILTER selected.selectionId == selection._key AND selected.project == @%s AND selected.generation == @%s AND selected.resourceType == @%s
  FILTER FIRST(FOR member IN @@%s
    FILTER member.revisionId == revision._key AND member.project == selected.project AND member.generation == selected.generation AND member.resourceType == selected.resourceType AND member.id == selected.id
    FILTER member.ref.project == member.project AND member.ref.generation == member.generation AND member.ref.resourceType == member.resourceType AND member.ref.id == member.id
    RETURN true) == null
  COLLECT WITH COUNT INTO count
  RETURN count)
FOR policy_guard IN [ASSERT(@%s != "ERROR" OR unassigned_count == 0, "EXPLICIT_GROUP_UNASSIGNED_MEMBER")]
  FILTER policy_guard
LET definition = FIRST(FOR candidate IN @@%s
  FILTER candidate.revisionId == revision._key AND candidate.project == @%s AND candidate.groupId == @%s
  RETURN candidate)
%s
LET found = %s
LET assigned_page = (FOR member IN @@%s
  FILTER found AND @%s == revision._key AND definition != null
  FILTER member.revisionId == revision._key AND member.project == @%s AND member.generation == @%s AND member.resourceType == @%s
  FILTER member.ref.project == member.project AND member.ref.generation == member.generation AND member.ref.resourceType == member.resourceType AND member.ref.id == member.id
  FILTER member.groupId == definition.groupId
  SORT member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC
  LET source = FIRST(FOR resource IN @@%s
    FILTER resource.id == member.ref.id AND resource.project == @%s AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType
    RETURN resource)
  FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
  LIMIT @%s, @%s
  RETURN {resourceType: @%s, resourceId: member.ref.id, occurrenceKey: member._key})
LET unassigned_page = (FOR selected IN @@%s
  FILTER found AND @%s == revision._key AND @%s == "__loom_unassigned__" AND @%s == "GROUP_AS_UNASSIGNED" AND unassigned_count > 0
  FILTER selected.selectionId == selection._key AND selected.project == @%s AND selected.generation == @%s AND selected.resourceType == @%s
  FILTER FIRST(FOR member IN @@%s
    FILTER member.revisionId == revision._key AND member.project == selected.project AND member.generation == selected.generation AND member.resourceType == selected.resourceType AND member.id == selected.id
    FILTER member.ref.project == member.project AND member.ref.generation == member.generation AND member.ref.resourceType == member.resourceType AND member.ref.id == member.id
    RETURN true) == null
  SORT selected.project ASC, selected.generation ASC, selected.resourceType ASC, selected.id ASC
  LET source = FIRST(FOR resource IN @@%s
    FILTER resource.id == selected.id AND resource.project == @%s AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType
    RETURN resource)
  FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
  LIMIT @%s, @%s
  RETURN {resourceType: @%s, resourceId: selected.id, occurrenceKey: selected._key})
LET page = definition != null ? assigned_page : unassigned_page
RETURN {found: found, contributors: SLICE(page, 0, @%s), hasMore: LENGTH(page) > @%s}
`, rows.RevisionCollectionBindKey, rows.RevisionIDBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.SelectionCollectionBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.SelectionMembersCollectionBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.MembershipsCollectionBindKey, rows.PolicyBindKey, rows.DefinitionsCollectionBindKey, rows.ProjectBindKey, requestedGroupBind,
		candidateLines, foundExpression,
		rows.MembershipsCollectionBindKey, requestedRevisionBind, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.ResourceCollectionBindKey, rows.ResourceProjectBindKey, rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey, offsetBind, fetchLimitBind, rows.ResourceTypeBindKey,
		rows.SelectionMembersCollectionBindKey, requestedRevisionBind, requestedGroupBind, rows.PolicyBindKey,
		rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey, rows.MembershipsCollectionBindKey,
		rows.ResourceCollectionBindKey, rows.ResourceProjectBindKey, rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey, offsetBind, fetchLimitBind, rows.ResourceTypeBindKey,
		limitBind, limitBind)
	if err := validateGroupRowsBindReferences(rows, binds, query); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(binds, query)}, nil
}

func uniqueBindKey(binds map[string]any, suffix string) string {
	base := "__loom_physical_" + suffix
	key := base
	for counter := 1; ; counter++ {
		if _, exists := binds[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, counter)
	}
}

type rowLineageRenderer struct {
	*renderContext
	core *physicalPlanRenderer
}

func (r rowLineageRenderer) renderConstructionRowLineage(sourceQuery string, stages []ir.PhysicalConstructionStage, terminal ir.PhysicalRowLineageReturn) (RenderedPhysicalPlan, error) {
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
	input := r.core.navigation().newInternalVariable("row_lineage_group_input")
	selected := r.core.navigation().newInternalVariable("row_lineage_selected_group")
	identity := r.core.navigation().newInternalVariable("row_lineage_identity")
	keyVars := make([]string, len(group.Keys))
	collectedVars := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collect := make([]string, len(group.Keys))
	sortKeys := make([]string, 0, len(group.Keys))
	selectedFields := make([]string, 0, len(group.Keys))
	for index, key := range group.Keys {
		keyVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_group_key_%d", index))
		collectedVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_collected_group_key_%d", index))
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
		nameBind := r.core.newInternalBindKey("row_lineage_group_identity_column")
		r.bindVars[nameBind] = outputColumnID
		identityParts = append(identityParts, "[@"+nameBind+", "+collectedVar+"]")
		selectedFields = append(selectedFields, keyAliases[index]+": "+collectedVar)
	}
	requestedRowID := "@" + terminal.RowIDBindKey
	var matchLines []string
	if len(group.Keys) == 0 {
		count := r.core.navigation().newInternalVariable("row_lineage_empty_group_count")
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
			typeBind := r.core.newInternalBindKey("row_lineage_group_key_type")
			r.bindVars[typeBind] = typeName
			filters = append(filters, fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", keyVars[index], keyVars[index], typeBind))
		}
		for index := range group.Keys {
			collect[index] = collectedVars[index] + " = " + keyVars[index]
		}
		matchLines = append(matchLines, "LET "+selected+" = FIRST(", "  FOR "+input+" IN (\n"+indentQuery(sourceQuery, "    ")+"\n  )")
		for index, key := range group.Keys {
			columnBind := r.core.newInternalBindKey("row_lineage_group_input_column")
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
	page := r.core.navigation().newInternalVariable("row_lineage_page")
	contributor := r.core.navigation().newInternalVariable("row_lineage_contributor")
	filters := make([]string, 0, len(group.Keys)*2+2)
	filters = append(filters, selected+" != null")
	if filteredSelection != "" {
		filters = append(filters, filteredSelection+" != null")
	}
	for index, key := range group.Keys {
		columnBind := r.core.newInternalBindKey("row_lineage_contributor_column")
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
		typeBind := r.core.newInternalBindKey("row_lineage_contributor_type")
		r.bindVars[typeBind] = typeName
		filters = append(filters, fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind))
		filters = append(filters, value+" == "+selected+"."+keyAliases[index])
	}
	resourceIDBind := r.core.newInternalBindKey("row_lineage_resource_id_column")
	r.bindVars[resourceIDBind] = terminal.ResourceIDColumn
	occurrenceKeyBind := r.core.newInternalBindKey("row_lineage_occurrence_key_column")
	r.bindVars[occurrenceKeyBind] = terminal.OccurrenceKeyColumn
	resourceTypeBind := r.core.newInternalBindKey("row_lineage_resource_type")
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

func (r rowLineageRenderer) renderCodingIdentityGroupRowLineage(
	sourceQuery string,
	stage ir.PhysicalConstructionStage,
	terminal ir.PhysicalRowLineageReturn,
) (RenderedPhysicalPlan, error) {
	group := stage.Group
	if group == nil || group.CodingKey == nil || group.SemanticKey != nil {
		return RenderedPhysicalPlan{}, fmt.Errorf("CODING_IDENTITY Group payload is required")
	}
	coding := group.CodingKey
	input := r.core.navigation().newInternalVariable("row_lineage_coding_group_input")
	selected := r.core.navigation().newInternalVariable("row_lineage_coding_selected_group")
	identity := r.core.navigation().newInternalVariable("row_lineage_coding_identity")
	codingInputTuple := r.core.navigation().newInternalVariable("row_lineage_coding_input_tuple")
	codingCollectedTuple := r.core.navigation().newInternalVariable("row_lineage_coding_collected_tuple")
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
		keyVars[index] = r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_coding_group_key_%d", index))
		keyCollected[index] = r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_coding_collected_key_%d", index))
		keyAliases[index] = fmt.Sprintf("key%d", index)
		columnBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_group_input_column", key.InputColumn)
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
		typeBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_group_key_type", typeName)
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
		outputBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_group_identity_column", outputID)
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
	codingOutputBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_identity_column", codingOutputID)
	codingMembershipBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_membership_column", coding.MembershipColumn)
	codingMembership := r.core.navigation().newInternalVariable("row_lineage_coding_membership")
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

	contributor := r.core.navigation().newInternalVariable("row_lineage_coding_contributor")
	contributorMembership := r.core.navigation().newInternalVariable("row_lineage_coding_contributor_membership")
	contributorFilters := []string{selected + " != null"}
	for index, key := range group.Keys {
		columnBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_contributor_column", key.InputColumn)
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
		typeBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_contributor_type", typeName)
		contributorFilters = append(contributorFilters,
			fmt.Sprintf("ASSERT(%s == null OR TYPENAME(%s) == @%s, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")", value, value, typeBind),
			value+" == "+selected+"."+keyAliases[index],
		)
	}
	contributorMembershipBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_contributor_membership_column", coding.MembershipColumn)
	contributorTupleVariable := r.core.navigation().newInternalVariable("row_lineage_coding_contributor_input_tuple")
	contributorMissingFilter, err := codingIdentityMissingCodeFilter(group.MissingKeyPolicy, contributorTupleVariable)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	resourceIDBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_resource_id_column", terminal.ResourceIDColumn)
	occurrenceBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_occurrence_key_column", terminal.OccurrenceKeyColumn)
	resourceTypeBind := r.core.newInternalBindKeyWithValue("row_lineage_coding_resource_type", terminal.ResourceType)
	page := r.core.navigation().newInternalVariable("row_lineage_coding_page")
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

func (r rowLineageRenderer) renderSemanticGroupRowLineage(
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

	lineageSemantic.SourceIdentityColumn = terminal.OccurrenceKeyColumn
	input := r.core.navigation().newInternalVariable("row_lineage_semantic_group_input")
	selected := r.core.navigation().newInternalVariable("row_lineage_semantic_selected_group")
	identity := r.core.navigation().newInternalVariable("row_lineage_semantic_identity")
	codeTuple := r.core.navigation().newInternalVariable("row_lineage_semantic_tuple")
	keyVars := make([]string, len(group.Keys))
	collectedVars := make([]string, len(group.Keys))
	keyAliases := make([]string, len(group.Keys))
	identityParts := []string{"[\"construction\", @" + group.ConstructionIDBindKey + "]", "[\"operation\", \"GROUP\"]"}
	collect := make([]string, 0, len(group.Keys)+1)
	sortKeys := make([]string, 0, len(group.Keys)+1)
	selectedFields := make([]string, 0, len(group.Keys)+1)
	filters := make([]string, 0, len(group.Keys)+1)
	for index, key := range group.Keys {
		keyVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_semantic_group_key_%d", index))
		collectedVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_semantic_collected_key_%d", index))
		keyVars[index], collectedVars[index] = keyVar, collectedVar
		keyAliases[index] = fmt.Sprintf("key%d", index)
		columnBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_input_column", key.InputColumn)
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
		typeBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_key_type", typeName)
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
		nameBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_identity_column", outputColumnID)
		identityParts = append(identityParts, "[@"+nameBind+", "+collectedVar+"]")
		collect = append(collect, collectedVar+" = "+keyVar)
		sortKeys = append(sortKeys, collectedVar+" ASC")
		selectedFields = append(selectedFields, keyAliases[index]+": "+collectedVar)
	}
	semanticMembershipLines, memberships, member, err := r.semanticGroupMembershipForLineage(input, &lineageSemantic)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	semanticTupleBind := r.core.newInternalBindKey("row_lineage_semantic_tuple_column")
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
	familyBinding := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_binding", semantic.BindingID)
	familyRule := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_rule", semantic.RuleVersion)
	familyID := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_family", semantic.FamilyID)
	familySystem := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_system", semantic.System)
	familyVersion := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_version", semantic.Version)
	familyPath := r.core.newInternalBindKeyWithValue("row_lineage_semantic_group_path", semantic.CodingPath)
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

	contributor := r.core.navigation().newInternalVariable("row_lineage_semantic_contributor")
	contributorMembershipLines, contributorMemberships, contributorMember, err := r.semanticGroupMembershipForLineage(contributor, &lineageSemantic)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	contributorFilters := []string{selected + " != null"}
	for index, key := range group.Keys {
		columnBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_contributor_column", key.InputColumn)
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
	resourceIDBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_resource_id_column", terminal.ResourceIDColumn)
	occurrenceKeyBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_occurrence_key_column", terminal.OccurrenceKeyColumn)
	resourceTypeBind := r.core.newInternalBindKeyWithValue("row_lineage_semantic_resource_type", terminal.ResourceType)
	page := r.core.navigation().newInternalVariable("row_lineage_semantic_page")
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

func (r rowLineageRenderer) semanticGroupMembershipForLineage(
	inputRow string,
	semantic *ir.PhysicalStageGroupSemanticKey,
) ([]string, string, string, error) {
	lines, memberships, err := r.core.renderConstructionGroupSemanticMembership(semantic, constructionGroupInput{}, inputRow)
	if err != nil {
		return nil, "", "", err
	}
	membership := r.core.navigation().newInternalVariable("row_lineage_semantic_membership")
	return lines, memberships, membership, nil
}

func (r rowLineageRenderer) renderGroupFilterLineageCandidate(
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
	groupRows := r.core.navigation().newInternalVariable("row_lineage_filter_group_rows")
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
			columnBind := r.core.newInternalBindKey("row_lineage_filter_group_input_column")
			r.bindVars[columnBind] = key.InputColumn
			keyVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_filter_group_key_%d", index))
			lines = append(lines,
				"  LET "+keyVar+" = "+input+"[@"+columnBind+"]",
				"  FILTER "+keyVar+" == "+selected+"."+keyAliases[index],
			)
		}
		groupLines, err = r.core.renderConstructionGroupStage(groupOutput, constructionGroupInput{SourceRowInScope: true})
	} else {
		input := r.core.navigation().newInternalVariable("row_lineage_filter_source")
		inputRows := r.core.navigation().newInternalVariable("row_lineage_filter_group_inputs")
		lines = []string{
			"LET " + inputRows + " = (",
			"  FOR " + input + " IN (\n" + indentQuery(sourceQuery, "    ") + "\n  )",
			"  FILTER " + selected + " != null",
		}
		for index, key := range group.Keys {
			columnBind := r.core.newInternalBindKey("row_lineage_filter_group_input_column")
			r.bindVars[columnBind] = key.InputColumn
			keyVar := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_filter_group_key_%d", index))
			lines = append(lines,
				"  LET "+keyVar+" = "+input+"[@"+columnBind+"]",
				"  FILTER "+keyVar+" == "+selected+"."+keyAliases[index],
			)
		}
		lines = append(lines, "  RETURN "+input, ")")
		lines = append(lines, "LET "+groupRows+" = (")
		groupLines, err = r.core.renderConstructionGroupStage(groupOutput, constructionGroupInput{RowsVariable: inputRows})
	}
	if err != nil {
		return "", nil, err
	}
	lines = append(lines, groupLines...)
	lines = append(lines, ")")
	priorRows := groupRows
	for index, filterStage := range candidateFilters {
		stageRows := r.core.navigation().newInternalVariable(fmt.Sprintf("row_lineage_filter_stage_%d", index+1))
		lines = append(lines,
			"LET "+stageRows+" = (",
			"  FOR "+filterStage.InputRowVariable+" IN "+priorRows,
		)
		predicate, renderErr := r.core.renderScopeOperation(ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: filterStage.Filter}, "  ")
		if renderErr != nil {
			return "", nil, fmt.Errorf("filter stage %q: %w", filterStage.ID, renderErr)
		}
		lines = append(lines, predicate...)
		object, renderErr := r.core.renderReturn(ir.PhysicalReturn{Projections: filterStage.OutputProjections})
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
	passed := r.core.navigation().newInternalVariable("row_lineage_filter_selected")
	final := r.core.navigation().newInternalVariable("row_lineage_filter_final")
	lines = append(lines,
		"LET "+passed+" = FIRST(",
		"  FOR "+final+" IN "+priorRows,
		"  RETURN true",
		")",
	)
	return passed, lines, nil
}

func lineagePassedCondition(variable string) string {
	if variable == "" {
		return ""
	}
	return " AND " + variable + " != null"
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

func (r *physicalPlanRenderer) rowLineage() rowLineageRenderer {
	return rowLineageRenderer{renderContext: &r.renderContext, core: r}
}
