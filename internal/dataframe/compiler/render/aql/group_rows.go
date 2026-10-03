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
  LET member_records = (FOR member IN all_memberships
    FILTER member.groupId == definition.groupId
    SORT member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC
    LET source = FIRST(FOR resource IN @@%s
      FILTER resource.id == member.ref.id AND resource.project == member.ref.project AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType
      RETURN resource)
    FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
    RETURN {source_identity: {project: member.ref.project, generation: member.ref.generation, resource_type: member.ref.resourceType, id: member.ref.id}, payload: source == null ? null : source.payload, __loom_storage_key: source == null ? null : source._key})
  LET contributor_keys = UNIQUE((FOR member_record IN member_records FILTER member_record.__loom_storage_key != null RETURN member_record.__loom_storage_key))
  RETURN {group_revision_id: revision._key, group_id: definition.groupId, group_label: definition.label, group_ordinal: definition.ordinal, __loom_row_id: {group_revision_id: revision._key, group_id: definition.groupId}, members: (FOR member_record IN member_records RETURN KEEP(member_record, "source_identity", "payload")), __loom_root_contributor_keys: contributor_keys})
LET unassigned_records = (FOR selected IN unassigned
	LET source = FIRST(FOR resource IN @@%s
	    FILTER resource.id == selected.id AND resource.project == selected.project AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType
	    RETURN resource)
	  FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
	  RETURN {source_identity: {project: selected.project, generation: selected.generation, resource_type: selected.resourceType, id: selected.id}, payload: source == null ? null : source.payload, __loom_storage_key: source == null ? null : source._key})
LET unassigned_contributor_keys = UNIQUE((FOR member_record IN unassigned_records FILTER member_record.__loom_storage_key != null RETURN member_record.__loom_storage_key))
LET unassigned_row = {group_revision_id: revision._key, group_id: "__loom_unassigned__", group_label: "Unassigned", group_ordinal: MAX(definitions[*].ordinal) + 1, __loom_row_id: {group_revision_id: revision._key, group_id: "__loom_unassigned__"}, members: (FOR member_record IN unassigned_records RETURN KEEP(member_record, "source_identity", "payload")), __loom_root_contributor_keys: unassigned_contributor_keys}
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
	renderer := physicalPlanRenderer{bindVars: rendered, collectionKeys: collectionKeys, setVariables: map[string]string{}, reservedVars: map[string]struct{}{"row": {}, "cohort_member": {}, "cohort_value_rows": {}}}
	traceMemberValue, traceMemberValueFound := groupRowsCellTraceMemberValue(rows)
	if len(rows.MemberValues) == 0 {
		if rows.CellTrace == nil {
			query += "RETURN row\n"
		} else {
			traceLines, traceErr := renderGroupRowsCellTraceReturn(renderer, *rows.CellTrace, "row", nil, ir.PhysicalGroupMemberValue{})
			if traceErr != nil {
				return RenderedPhysicalPlan{}, traceErr
			}
			query += strings.Join(traceLines, "\n") + "\n"
		}
	} else {
		projections := make([]string, 0, len(rows.MemberValues))
		if rows.CellTrace != nil && traceMemberValueFound {
			projections = append(projections, `"__loom_trace_source_identity": cohort_member.source_identity`)
		}
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
		query += strings.Join(lines, "\n") + "\n"
		if rows.CellTrace == nil {
			query += "RETURN MERGE(row, {" + strings.Join(outputs, ", ") + "})\n"
		} else {
			finalRow := renderer.newInternalVariable("cohort_group_final_row")
			query += "LET " + finalRow + " = MERGE(row, {" + strings.Join(outputs, ", ") + "})\n"
			var contributorRows *string
			if traceMemberValueFound {
				valueRows := "cohort_value_rows"
				contributorRows = &valueRows
			}
			traceLines, traceErr := renderGroupRowsCellTraceReturn(renderer, *rows.CellTrace, finalRow, contributorRows, traceMemberValue)
			if traceErr != nil {
				return RenderedPhysicalPlan{}, traceErr
			}
			query += strings.Join(traceLines, "\n") + "\n"
		}
	}
	if err := validateGroupRowsBindReferences(rows, rendered, query); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(rendered, query)}, nil
}

func groupRowsCellTraceMemberValue(rows ir.PhysicalGroupRows) (ir.PhysicalGroupMemberValue, bool) {
	if rows.CellTrace == nil {
		return ir.PhysicalGroupMemberValue{}, false
	}
	for _, value := range rows.MemberValues {
		if value.Output == rows.CellTrace.OutputColumn {
			return value, true
		}
	}
	return ir.PhysicalGroupMemberValue{}, false
}

func renderGroupRowsCellTraceReturn(renderer physicalPlanRenderer, trace ir.PhysicalGroupRowsCellTrace, rowVariable string, valueRows *string, memberValue ir.PhysicalGroupMemberValue) ([]string, error) {
	value := ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: trace.Cardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Value:        &ir.PhysicalValue{Variable: rowVariable, Path: []string{trace.OutputColumn}},
	}
	identity := ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Value:        &ir.PhysicalValue{Variable: rowVariable, Path: []string{"__loom_row_id"}},
	}
	terminal := ir.PhysicalCellTraceReturn{
		Value: value, ExplicitIdentity: &identity,
		OffsetBindKey: trace.OffsetBindKey, LimitBindKey: trace.LimitBindKey, FetchLimitBindKey: trace.FetchLimitBindKey,
		OmissionCode: "TRACE_CONTRIBUTORS_UNAVAILABLE", RowExists: true,
	}
	prelude := []string(nil)
	if valueRows != nil {
		contributorRows, lines, err := renderGroupRowsCellTraceContributors(renderer, trace, rowVariable, *valueRows, memberValue)
		if err != nil {
			return nil, err
		}
		prelude = append(prelude, lines...)
		terminal.Contribution = &ir.PhysicalCellTraceContribution{SetVariable: contributorRows, ValueField: "value"}
		terminal.OmissionCode = ""
	}
	traceLines, err := renderer.renderCellTraceReturn(terminal)
	if err != nil {
		return nil, err
	}
	return append(prelude, traceLines...), nil
}

func renderGroupRowsCellTraceContributors(
	renderer physicalPlanRenderer,
	trace ir.PhysicalGroupRowsCellTrace,
	rowVariable, valueRows string,
	memberValue ir.PhysicalGroupMemberValue,
) (string, []string, error) {
	columnBind := renderer.newInternalBindKeyWithValue("group_rows_trace_column", memberValue.Output)
	input := renderer.newInternalVariable("group_rows_trace_input")
	item := renderer.newInternalVariable("group_rows_trace_value")
	contributors := renderer.newInternalVariable("group_rows_trace_contributors")
	finalValue, err := renderer.renderExpression(ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: trace.Cardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Value:        &ir.PhysicalValue{Variable: rowVariable, Path: []string{trace.OutputColumn}},
	})
	if err != nil {
		return "", nil, fmt.Errorf("render group-row trace output value: %w", err)
	}
	match := item + " == " + finalValue
	if memberValue.Policy == "ALL" {
		match = item + " IN " + finalValue
	} else if memberValue.Policy != "ONE" {
		return "", nil, fmt.Errorf("group-row cell trace has unsupported row-value policy %q", memberValue.Policy)
	}
	project := func(value string) string {
		return fmt.Sprintf("{resourceType: %s.__loom_trace_source_identity.resource_type, id: %s.__loom_trace_source_identity.id, value: %s}", input, input, value)
	}
	var source string
	if memberValue.Expression.Cardinality == ir.PhysicalArrayCardinality {
		arrayValue := renderer.newInternalVariable("group_rows_trace_array")
		source = fmt.Sprintf(
			"(FOR %s IN %s LET %s = %s[@%s] FILTER %s != null FOR %s IN (ASSERT(IS_ARRAY(%s), \"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH\") ? %s : []) FILTER %s != null FILTER %s RETURN %s)",
			input, valueRows, arrayValue, input, columnBind, arrayValue,
			item, arrayValue, arrayValue, item, match, project(item),
		)
	} else {
		source = fmt.Sprintf(
			"(FOR %s IN %s LET %s = %s[@%s] FILTER %s != null FILTER %s RETURN %s)",
			input, valueRows, item, input, columnBind, item, match, project(item),
		)
	}
	uniqueRows := renderer.newInternalVariable("group_rows_trace_unique")
	lines := []string{
		"LET " + contributors + " = " + source,
		fmt.Sprintf("LET %s = (FOR %s IN UNIQUE(%s) SORT %s.resourceType ASC, %s.id ASC, %s.value ASC RETURN %s)", uniqueRows, item, contributors, item, item, item, item),
	}
	return uniqueRows, lines, nil
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
	groupContributorKeys := r.newInternalVariable("cohort_group_contributor_keys")
	unassignedContributorKeys := r.newInternalVariable("cohort_unassigned_contributors")
	lines = append(lines,
		fmt.Sprintf("  LET %s = (FOR %s IN definitions", groupRows, definition),
		fmt.Sprintf("    LET %s = (FOR member IN all_memberships", memberRecords),
		fmt.Sprintf("      FILTER member.groupId == %s.groupId", definition),
		fmt.Sprintf("      LET %s = FIRST(FOR resource IN @@%s", source, rows.ResourceCollectionBindKey),
		"        FILTER resource.id == member.ref.id AND resource.project == member.ref.project AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType",
		"        RETURN resource)",
		fmt.Sprintf("      FILTER %s", cohortMemberInputPredicate(source, contributors, cohort.PreserveMissingMembers)),
		fmt.Sprintf("      FILTER %s", cohortMemberAuthorizationPredicate(source, rows, cohort.PreserveMissingMembers)),
		fmt.Sprintf("      RETURN {source_identity: {project: member.ref.project, generation: member.ref.generation, resource_type: member.ref.resourceType, id: member.ref.id}, payload: %s == null ? null : %s.payload, __loom_storage_key: %s == null ? null : %s._key})", source, source, source, source),
		fmt.Sprintf("    LET members = (FOR %s IN %s RETURN KEEP(%s, \"source_identity\", \"payload\"))", memberRow, memberRecords, memberRow),
		fmt.Sprintf("    LET %s = UNIQUE((FOR member IN %s FILTER member.__loom_storage_key != null RETURN member.__loom_storage_key))", groupContributorKeys, memberRecords),
		fmt.Sprintf("    RETURN {group_revision_id: revision._key, group_id: %s.groupId, group_label: %s.label, group_ordinal: %s.ordinal, __loom_row_id: {group_revision_id: revision._key, group_id: %s.groupId}, members: members, %q: %s})", definition, definition, definition, definition, cohort.RootContributorOutputColumn, groupContributorKeys),
	)
	lines = append(lines,
		"  LET unassigned_records = (FOR selected IN unassigned",
		"    LET source = FIRST(FOR resource IN @@"+rows.ResourceCollectionBindKey,
		"      FILTER resource.id == selected.id AND resource.project == selected.project AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType",
		"      RETURN resource)",
		fmt.Sprintf("    FILTER %s", cohortMemberInputPredicate("source", contributors, cohort.PreserveMissingMembers)),
		fmt.Sprintf("    FILTER %s", cohortMemberAuthorizationPredicate("source", rows, cohort.PreserveMissingMembers)),
		"    RETURN {source_identity: {project: selected.project, generation: selected.generation, resource_type: selected.resourceType, id: selected.id}, payload: source == null ? null : source.payload, __loom_storage_key: source == null ? null : source._key})",
		"  LET unassigned_members = (FOR member IN unassigned_records RETURN KEEP(member, \"source_identity\", \"payload\"))",
		fmt.Sprintf("  LET %s = UNIQUE((FOR member IN unassigned_records FILTER member.__loom_storage_key != null RETURN member.__loom_storage_key))", unassignedContributorKeys),
		fmt.Sprintf("  LET unassigned_row = {group_revision_id: revision._key, group_id: \"__loom_unassigned__\", group_label: \"Unassigned\", group_ordinal: MAX(definitions[*].ordinal) + 1, __loom_row_id: {group_revision_id: revision._key, group_id: \"__loom_unassigned__\"}, members: unassigned_members, %q: %s}", cohort.RootContributorOutputColumn, unassignedContributorKeys),
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

func cohortMemberInputPredicate(source, contributors string, preserveMissing bool) string {
	if preserveMissing {
		return fmt.Sprintf("%s == null OR %s._key IN %s", source, source, contributors)
	}
	return fmt.Sprintf("%s != null AND %s._key IN %s", source, source, contributors)
}

func cohortMemberAuthorizationPredicate(source string, rows ir.PhysicalGroupRows, preserveMissing bool) string {
	if preserveMissing {
		return fmt.Sprintf("%s == null OR @%s == true OR %s.auth_resource_path IN @%s", source, rows.AuthUnrestrictedBindKey, source, rows.AuthResourcePathsBindKey)
	}
	return fmt.Sprintf("@%s == true OR %s.auth_resource_path IN @%s", rows.AuthUnrestrictedBindKey, source, rows.AuthResourcePathsBindKey)
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
