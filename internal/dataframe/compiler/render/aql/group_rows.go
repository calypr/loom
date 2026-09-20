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
      FILTER resource._key == member.ref.id AND resource.project == member.ref.project AND resource.dataset_generation == member.ref.generation AND resource.resourceType == member.ref.resourceType
      RETURN resource)
    FILTER source == null OR @%s == true OR source.auth_resource_path IN @%s
    RETURN {source_identity: {project: member.ref.project, generation: member.ref.generation, resource_type: member.ref.resourceType, id: member.ref.id}, payload: source == null ? null : source.payload})
  RETURN {group_revision_id: revision._key, group_id: definition.groupId, group_label: definition.label, group_ordinal: definition.ordinal, __loom_row_id: {group_revision_id: revision._key, group_id: definition.groupId}, members: members})
LET unassigned_row = {group_revision_id: revision._key, group_id: "__loom_unassigned__", group_label: "Unassigned", group_ordinal: MAX(definitions[*].ordinal) + 1, __loom_row_id: {group_revision_id: revision._key, group_id: "__loom_unassigned__"}, members: (FOR selected IN unassigned
	LET source = FIRST(FOR resource IN @@%s
	    FILTER resource._key == selected.id AND resource.project == selected.project AND resource.dataset_generation == selected.generation AND resource.resourceType == selected.resourceType
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
	query += "RETURN row\n"
	rendered := runtimePhysicalBindVars(plan.BindVars, collectionKeys)
	if err := validateGroupRowsBindReferences(rows, rendered, query); err != nil {
		return RenderedPhysicalPlan{}, err
	}
	return RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(rendered, query)}, nil
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
