package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func renderPhysicalGroupRows(plan ir.PhysicalPlan, rows ir.PhysicalGroupRows) (RenderedPhysicalPlan, error) {
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		return RenderedPhysicalPlan{}, err
	}
	if err := validateRenderablePhysicalPlan(plan, collectionKeys); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	query := fmt.Sprintf(`LET revision = DOCUMENT(@@%s, @%s)
FOR revision_guard IN [ASSERT(revision != null AND revision.state == "COMPLETE" AND revision.project == @%s AND revision.generation == @%s AND revision.resourceType == @%s, "EXPLICIT_GROUP_REVISION_INVALID")]
  FILTER revision_guard
LET selection = DOCUMENT(@@%s, revision.sourceSelectionRevisionId)
FOR selection_guard IN [ASSERT(selection != null AND selection.complete == true AND selection.project == @%s AND selection.generation == @%s AND selection.resourceType == @%s AND revision.scopeDigest == selection.scopeDigest AND revision.sourceMembershipDigest == selection.membershipDigest, "EXPLICIT_GROUP_SOURCE_STALE")]
  FILTER selection_guard
LET all_memberships = (FOR member IN @@%s
  FILTER member.revisionId == revision._key AND member.project == @%s AND member.generation == @%s AND member.resourceType == @%s
  FILTER member.ref.project == member.project AND member.ref.generation == member.generation AND member.ref.resourceType == member.resourceType AND member.ref.id == member.id
  SORT member.groupId ASC, member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC
  RETURN member)
LET unassigned = (FOR selected IN @@%s
  FILTER selected.selectionId == selection._key AND selected.project == @%s AND selected.generation == @%s AND selected.resourceType == @%s
  FILTER LENGTH(FOR member IN all_memberships
    FILTER member.project == selected.project AND member.generation == selected.generation AND member.resourceType == selected.resourceType AND member.id == selected.id
    RETURN 1) == 0
  SORT selected.project ASC, selected.generation ASC, selected.resourceType ASC, selected.id ASC
  RETURN selected)
FOR policy_guard IN [ASSERT(@%s != "ERROR" OR LENGTH(unassigned) == 0, "EXPLICIT_GROUP_UNASSIGNED_MEMBER")]
  FILTER policy_guard
LET definitions = (FOR definition IN @@%s
  FILTER definition.revisionId == revision._key AND definition.project == @%s
  SORT definition.ordinal ASC, definition.groupId ASC
  RETURN definition)
LET explicit_rows = (FOR definition IN definitions
  LET members = (FOR member IN all_memberships
    FILTER member.groupId == definition.groupId
    SORT member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC
    LET source = FIRST(FOR resource IN @@%s
      FILTER resource.id == member.ref.id AND resource.project == member.ref.project AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType
      RETURN resource)
    FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
    RETURN {source_identity: {project: member.ref.project, generation: member.ref.generation, resource_type: member.ref.resourceType, id: member.ref.id}, payload: source == null ? null : source.payload})
  RETURN {group_revision_id: revision._key, group_id: definition.groupId, group_label: definition.label, group_ordinal: definition.ordinal, __loom_row_id: {group_revision_id: revision._key, group_id: definition.groupId}, members: members})
LET unassigned_row = {group_revision_id: revision._key, group_id: "__loom_unassigned__", group_label: "Unassigned", group_ordinal: MAX(definitions[*].ordinal) + 1, __loom_row_id: {group_revision_id: revision._key, group_id: "__loom_unassigned__"}, members: (FOR selected IN unassigned
	LET source = FIRST(FOR resource IN @@%s
	    FILTER resource.id == selected.id AND resource.project == selected.project AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType
	    RETURN resource)
	  FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
	  RETURN {source_identity: {project: selected.project, generation: selected.generation, resource_type: selected.resourceType, id: selected.id}, payload: source == null ? null : source.payload})}
LET rows = @%s == "GROUP_AS_UNASSIGNED" AND LENGTH(unassigned) > 0 ? APPEND(explicit_rows, [unassigned_row]) : explicit_rows
FOR row IN rows
SORT row.group_ordinal ASC, row.group_id ASC
`, rows.RevisionCollectionBindKey, rows.RevisionIDBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.SelectionCollectionBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.MembershipsCollectionBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.SelectionMembersCollectionBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey,
		rows.PolicyBindKey, rows.DefinitionsCollectionBindKey, rows.ProjectBindKey, rows.ResourceCollectionBindKey, rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey,
		rows.ResourceCollectionBindKey, rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey, rows.PolicyBindKey)
	if rows.LimitBindKey != "" {
		query += "LIMIT @" + rows.LimitBindKey + "\n"
	}
	rendered := runtimePhysicalBindVars(plan.BindVars, collectionKeys)
	if len(rows.MemberValues) == 0 {
		query += "RETURN row\n"
	} else {
		renderer := physicalPlanRenderer{bindVars: rendered, collectionKeys: collectionKeys, setVariables: map[string]string{}, reservedVars: map[string]struct{}{"row": {}, "cohort_member": {}, "cohort_value_rows": {}}}
		projections := make([]string, 0, len(rows.MemberValues))
		outputs := make([]string, 0, len(rows.MemberValues))
		values := make([]ir.PhysicalStageRowValue, 0, len(rows.MemberValues))
		for index, value := range rows.MemberValues {
			expression, err := renderer.renderExpression(value.Expression)
			if err != nil {
				return RenderedPhysicalPlan{}, err
			}
			variable := renderer.newInternalVariable(fmt.Sprintf("cohort_member_value_%d", index))
			projections = append(projections, fmt.Sprintf("%q: %s", value.Output, expression))
			outputs = append(outputs, fmt.Sprintf("%q: %s", value.Output, variable))
			values = append(values, ir.PhysicalStageRowValue{InputColumn: value.Output, InputKind: value.Kind, InputMany: value.Expression.Cardinality == ir.PhysicalArrayCardinality, Output: value.Output, Policy: value.Policy, Variable: variable})
		}
		query += "LET cohort_value_rows = (FOR cohort_member IN row.members RETURN {" + strings.Join(projections, ", ") + "})\n"
		lines, err := renderer.renderConstructionRowValueLets(values, "cohort_value_rows", "")
		if err != nil {
			return RenderedPhysicalPlan{}, err
		}
		query += strings.Join(lines, "\n") + "\nRETURN MERGE(row, {" + strings.Join(outputs, ", ") + "})\n"
	}
	if err := validateGroupRowsBindReferences(rows, rendered, query); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(rendered, query)}, nil
}

func (r *physicalPlanRenderer) renderCohortRootScan(root ir.PhysicalRootScan) ([]string, error) {
	source := root.CohortSource
	if source == nil {
		return nil, fmt.Errorf("cohort root source is required")
	}
	revision := r.newInternalVariable("cohort_source_revision")
	revisionGuard := r.newInternalVariable("cohort_source_revision_guard")
	selection := r.newInternalVariable("cohort_source_selection")
	selectionGuard := r.newInternalVariable("cohort_source_selection_guard")
	selectionMember := r.newInternalVariable("cohort_source_selection_member")
	selectionCandidate := r.newInternalVariable("cohort_source_selection_candidate")
	memberships := r.newInternalVariable("cohort_source_membership")
	membershipRow := r.newInternalVariable("cohort_source_membership_row")
	membershipCandidate := r.newInternalVariable("cohort_source_membership_candidate")
	membershipCheck := r.newInternalVariable("cohort_source_membership_check")
	unassigned := r.newInternalVariable("cohort_source_unassigned")
	policyGuard := r.newInternalVariable("cohort_source_policy_guard")
	candidateIDs := r.newInternalVariable("cohort_source_candidate_ids")
	candidateID := r.newInternalVariable("cohort_source_candidate_id")
	lines := []string{
		fmt.Sprintf("LET %s = DOCUMENT(@@%s, @%s)", revision, source.RevisionCollectionBindKey, source.RevisionIDBindKey),
		fmt.Sprintf("FOR %s IN [ASSERT(%s != null AND %s.state == \"COMPLETE\" AND %s.project == @%s AND %s.generation == @%s AND %s.resourceType == @%s, \"EXPLICIT_GROUP_REVISION_INVALID\")]", revisionGuard, revision, revision, revision, source.ProjectBindKey, revision, source.DatasetGenerationBindKey, revision, source.ResourceTypeBindKey),
		fmt.Sprintf("  FILTER %s", revisionGuard),
		fmt.Sprintf("LET %s = DOCUMENT(@@%s, %s.sourceSelectionRevisionId)", selection, source.SelectionCollectionBindKey, revision),
		fmt.Sprintf("FOR %s IN [ASSERT(%s != null AND %s.complete == true AND %s.project == @%s AND %s.generation == @%s AND %s.resourceType == @%s AND %s.scopeDigest == %s.scopeDigest AND %s.sourceMembershipDigest == %s.membershipDigest, \"EXPLICIT_GROUP_SOURCE_STALE\")]", selectionGuard, selection, selection, selection, source.ProjectBindKey, selection, source.DatasetGenerationBindKey, selection, source.ResourceTypeBindKey, revision, selection, revision, selection),
		fmt.Sprintf("  FILTER %s", selectionGuard),
		fmt.Sprintf("LET %s = (FOR %s IN @@%s", memberships, membershipRow, source.MembershipsCollectionBindKey),
		fmt.Sprintf("  FILTER %s.revisionId == %s._key AND %s.project == @%s AND %s.generation == @%s AND %s.resourceType == @%s", membershipRow, revision, membershipRow, source.ProjectBindKey, membershipRow, source.DatasetGenerationBindKey, membershipRow, source.ResourceTypeBindKey),
		fmt.Sprintf("  FILTER %s.ref.project == %s.project AND %s.ref.generation == %s.generation AND %s.ref.resourceType == %s.resourceType AND %s.ref.id == %s.id", membershipRow, membershipRow, membershipRow, membershipRow, membershipRow, membershipRow, membershipRow, membershipRow),
		fmt.Sprintf("  RETURN %s)", membershipRow),
		fmt.Sprintf("LET %s = (FOR %s IN @@%s", unassigned, selectionMember, source.SelectionMembersCollectionBindKey),
		fmt.Sprintf("  FILTER %s.selectionId == %s._key AND %s.project == @%s AND %s.generation == @%s AND %s.resourceType == @%s", selectionMember, selection, selectionMember, source.ProjectBindKey, selectionMember, source.DatasetGenerationBindKey, selectionMember, source.ResourceTypeBindKey),
		fmt.Sprintf("  FILTER LENGTH(FOR %s IN %s FILTER %s.project == %s.project AND %s.generation == %s.generation AND %s.resourceType == %s.resourceType AND %s.id == %s.id RETURN 1) == 0", membershipCheck, memberships, membershipCheck, selectionMember, membershipCheck, selectionMember, membershipCheck, selectionMember, membershipCheck, selectionMember),
		fmt.Sprintf("  RETURN %s)", selectionMember),
		fmt.Sprintf("FOR %s IN [ASSERT(@%s != \"ERROR\" OR LENGTH(%s) == 0, \"EXPLICIT_GROUP_UNASSIGNED_MEMBER\")]", policyGuard, source.PolicyBindKey, unassigned),
		fmt.Sprintf("  FILTER %s", policyGuard),
		fmt.Sprintf("LET %s = UNION_DISTINCT(", candidateIDs),
		fmt.Sprintf("  (FOR %s IN @@%s FILTER %s.selectionId == %s._key AND %s.project == @%s AND %s.generation == @%s AND %s.resourceType == @%s RETURN %s.id),", selectionCandidate, source.SelectionMembersCollectionBindKey, selectionCandidate, selection, selectionCandidate, source.ProjectBindKey, selectionCandidate, source.DatasetGenerationBindKey, selectionCandidate, source.ResourceTypeBindKey, selectionCandidate),
		fmt.Sprintf("  (FOR %s IN %s RETURN %s.id)", membershipCandidate, memberships, membershipCandidate),
		")",
		fmt.Sprintf("FOR %s IN %s", candidateID, candidateIDs),
		fmt.Sprintf("  FOR %s IN @@%s", root.Variable, root.CollectionBindKey),
		fmt.Sprintf("    FILTER %s.id == %s AND %s.project == @%s AND %s.dataset_generation == @%s AND %s.resourceType == @%s", root.Variable, candidateID, root.Variable, source.ProjectBindKey, root.Variable, source.DatasetGenerationBindKey, root.Variable, source.ResourceTypeBindKey),
	}
	return lines, nil
}

func (r *physicalPlanRenderer) renderConstructionCohortGroupStage(stage ir.PhysicalConstructionStage, inputRows string) ([]string, error) {
	cohort := stage.CohortGroup
	if cohort == nil || strings.TrimSpace(inputRows) == "" {
		return nil, fmt.Errorf("cohort group stage and input rows are required")
	}
	rows := cohort.Rows
	contributorInput := fmt.Sprintf("%s.%s", stage.InputRowVariable, cohort.ContributorInputColumn)
	contributors := r.newInternalVariable("cohort_root_contributors")
	lines := []string{
		fmt.Sprintf("  LET %s = UNIQUE(FLATTEN((FOR %s IN %s RETURN (IS_ARRAY(%s) ? %s : [%s])), 1))", contributors,
			stage.InputRowVariable, inputRows, contributorInput, contributorInput, contributorInput),
		fmt.Sprintf("  LET revision = DOCUMENT(@@%s, @%s)", rows.RevisionCollectionBindKey, rows.RevisionIDBindKey),
		fmt.Sprintf("  FOR revision_guard IN [ASSERT(revision != null AND revision.state == \"COMPLETE\" AND revision.project == @%s AND revision.generation == @%s AND revision.resourceType == @%s, \"EXPLICIT_GROUP_REVISION_INVALID\")]", rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey),
		"    FILTER revision_guard",
		fmt.Sprintf("  LET selection = DOCUMENT(@@%s, revision.sourceSelectionRevisionId)", rows.SelectionCollectionBindKey),
		fmt.Sprintf("  FOR selection_guard IN [ASSERT(selection != null AND selection.complete == true AND selection.project == @%s AND selection.generation == @%s AND selection.resourceType == @%s AND revision.scopeDigest == selection.scopeDigest AND revision.sourceMembershipDigest == selection.membershipDigest, \"EXPLICIT_GROUP_SOURCE_STALE\")]", rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey),
		"    FILTER selection_guard",
		fmt.Sprintf("  LET all_memberships = (FOR member IN @@%s", rows.MembershipsCollectionBindKey),
		fmt.Sprintf("    FILTER member.revisionId == revision._key AND member.project == @%s AND member.generation == @%s AND member.resourceType == @%s", rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey),
		"    FILTER member.ref.project == member.project AND member.ref.generation == member.generation AND member.ref.resourceType == member.resourceType AND member.ref.id == member.id",
		"    SORT member.groupId ASC, member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC",
		"    RETURN member)",
		fmt.Sprintf("  LET unassigned = (FOR selected IN @@%s", rows.SelectionMembersCollectionBindKey),
		fmt.Sprintf("    FILTER selected.selectionId == selection._key AND selected.project == @%s AND selected.generation == @%s AND selected.resourceType == @%s", rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey),
		"    FILTER LENGTH(FOR member IN all_memberships FILTER member.project == selected.project AND member.generation == selected.generation AND member.resourceType == selected.resourceType AND member.id == selected.id RETURN 1) == 0",
		"    SORT selected.project ASC, selected.generation ASC, selected.resourceType ASC, selected.id ASC",
		"    RETURN selected)",
		fmt.Sprintf("  FOR policy_guard IN [ASSERT(@%s != \"ERROR\" OR LENGTH(unassigned) == 0, \"EXPLICIT_GROUP_UNASSIGNED_MEMBER\")]", rows.PolicyBindKey),
		"    FILTER policy_guard",
		fmt.Sprintf("  LET definitions = (FOR definition IN @@%s FILTER definition.revisionId == revision._key AND definition.project == @%s SORT definition.ordinal ASC, definition.groupId ASC RETURN definition)", rows.DefinitionsCollectionBindKey, rows.ProjectBindKey),
	}
	memberRecords := r.newInternalVariable("cohort_group_member_records")
	memberRow := r.newInternalVariable("cohort_group_member_record")
	source := r.newInternalVariable("cohort_group_source")
	definition := r.newInternalVariable("cohort_group_definition")
	groupRows := r.newInternalVariable("cohort_group_rows")
	lines = append(lines,
		fmt.Sprintf("  LET %s = (FOR %s IN definitions", groupRows, definition),
		fmt.Sprintf("    LET %s = (FOR member IN all_memberships", memberRecords),
		fmt.Sprintf("      FILTER member.groupId == %s.groupId", definition),
		fmt.Sprintf("      LET %s = FIRST(FOR resource IN @@%s", source, rows.ResourceCollectionBindKey),
		"        FILTER resource.id == member.ref.id AND resource.project == member.ref.project AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType",
		fmt.Sprintf("        FILTER resource._key IN %s", contributors),
		fmt.Sprintf("        FILTER @%s == true OR resource.auth_resource_path IN @%s", rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey),
		"        RETURN resource)",
		// A composed cohort emits only members backed by resources present in its
		// effective input. The source-only cohort renderer separately preserves
		// pinned memberships with missing resources as null-payload members.
		fmt.Sprintf("      FILTER %s != null", source),
		fmt.Sprintf("      RETURN {source_identity: {project: member.ref.project, generation: member.ref.generation, resource_type: member.ref.resourceType, id: member.ref.id}, payload: %s.payload, __loom_storage_key: %s._key})", source, source),
		fmt.Sprintf("    LET members = (FOR %s IN %s RETURN KEEP(%s, \"source_identity\", \"payload\"))", memberRow, memberRecords, memberRow),
		fmt.Sprintf("    RETURN {group_revision_id: revision._key, group_id: %s.groupId, group_label: %s.label, group_ordinal: %s.ordinal, __loom_row_id: {group_revision_id: revision._key, group_id: %s.groupId}, members: members, %q: UNIQUE(%s[*].__loom_storage_key)})", definition, definition, definition, definition, cohort.RootContributorOutputColumn, memberRecords),
	)
	lines = append(lines,
		"  LET unassigned_records = (FOR selected IN unassigned",
		"    LET source = FIRST(FOR resource IN @@"+rows.ResourceCollectionBindKey,
		"      FILTER resource.id == selected.id AND resource.project == selected.project AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType",
		fmt.Sprintf("      FILTER resource._key IN %s", contributors),
		fmt.Sprintf("      FILTER @%s == true OR resource.auth_resource_path IN @%s", rows.AuthUnrestrictedBindKey, rows.AuthResourcePathsBindKey),
		"      RETURN resource)",
		"    FILTER source != null",
		"    RETURN {source_identity: {project: selected.project, generation: selected.generation, resource_type: selected.resourceType, id: selected.id}, payload: source.payload, __loom_storage_key: source._key})",
		"  LET unassigned_members = (FOR member IN unassigned_records RETURN KEEP(member, \"source_identity\", \"payload\"))",
		fmt.Sprintf("  LET unassigned_row = {group_revision_id: revision._key, group_id: \"__loom_unassigned__\", group_label: \"Unassigned\", group_ordinal: MAX(definitions[*].ordinal) + 1, __loom_row_id: {group_revision_id: revision._key, group_id: \"__loom_unassigned__\"}, members: unassigned_members, %q: UNIQUE(unassigned_records[*].__loom_storage_key)}", cohort.RootContributorOutputColumn),
		fmt.Sprintf("  LET rows = @%s == \"GROUP_AS_UNASSIGNED\" AND LENGTH(unassigned) > 0 ? APPEND(%s, [unassigned_row]) : %s", rows.PolicyBindKey, groupRows, groupRows),
		fmt.Sprintf("  FOR %s IN rows", stage.OutputRowVariable),
		fmt.Sprintf("  SORT %s.group_ordinal ASC, %s.group_id ASC", stage.OutputRowVariable, stage.OutputRowVariable),
	)
	if len(rows.MemberValues) == 0 {
		lines = append(lines, "  RETURN "+stage.OutputRowVariable)
		return lines, nil
	}
	valueRows := r.newInternalVariable("cohort_value_rows")
	projections := make([]string, 0, len(rows.MemberValues))
	values := make([]ir.PhysicalStageRowValue, 0, len(rows.MemberValues))
	outputs := make([]string, 0, len(rows.MemberValues)+1)
	for index, value := range rows.MemberValues {
		expression, err := r.renderExpression(value.Expression)
		if err != nil {
			return nil, err
		}
		variable := r.newInternalVariable(fmt.Sprintf("cohort_member_value_%d", index))
		projections = append(projections, fmt.Sprintf("%q: %s", value.Output, expression))
		outputs = append(outputs, fmt.Sprintf("%q: %s", value.Output, variable))
		values = append(values, ir.PhysicalStageRowValue{InputColumn: value.Output, InputKind: value.Kind, InputMany: value.Expression.Cardinality == ir.PhysicalArrayCardinality, Output: value.Output, Policy: value.Policy, Variable: variable})
	}
	lines = append(lines, fmt.Sprintf("  LET %s = (FOR cohort_member IN %s.members RETURN {%s})", valueRows, stage.OutputRowVariable, strings.Join(projections, ", ")))
	reduction, err := r.renderConstructionRowValueLets(values, valueRows, "")
	if err != nil {
		return nil, err
	}
	lines = append(lines, reduction...)
	lines = append(lines, "  RETURN MERGE("+stage.OutputRowVariable+", {"+strings.Join(outputs, ", ")+"})")
	return lines, nil
}

func validateGroupRowsBindReferences(rows ir.PhysicalGroupRows, binds map[string]any, query string) error {
	for _, key := range []string{rows.RevisionIDBindKey, rows.ProjectBindKey, rows.DatasetGenerationBindKey, rows.ResourceTypeBindKey, rows.PolicyBindKey, rows.AuthResourcePathsBindKey, rows.AuthUnrestrictedBindKey} {
		if !strings.Contains(query, "@"+key) {
			return fmt.Errorf("group rows query does not reference bind %q", key)
		}
		if _, ok := binds[key]; !ok {
			return fmt.Errorf("group rows bind %q is missing", key)
		}
	}
	return nil
}
