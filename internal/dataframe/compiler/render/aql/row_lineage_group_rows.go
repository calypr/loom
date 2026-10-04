package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// RenderPhysicalGroupRowsLineage returns a bounded page of members for one
// exact row from the standalone explicit-group source. Requested identity
// fields are supplied separately after compiler-side canonical JSON parsing.
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

// RenderPhysicalCohortGroupRowsLineage traces a composed cohort row through its
// identity-preserving scalar FILTER suffix, then pages pinned membership
// occurrences only when that exact final row still exists.
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
		renderer := physicalPlanRenderer{
			bindVars: binds, collectionKeys: collectionKeys, setVariables: map[string]string{},
			reservedVars: stageSequenceVariableNames(plan), internalPrefix: "cohort_lineage_",
		}
		candidate := renderer.newInternalVariable("candidate")
		unassignedMaximum := renderer.newInternalVariable("unassigned_max_ordinal")
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
			filtered := renderer.newInternalVariable("filtered_candidate")
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
