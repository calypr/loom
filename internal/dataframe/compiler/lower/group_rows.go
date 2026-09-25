package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func buildGroupRowsPhysicalPlan(output semantic.OutputPlan, context semantic.ExecutionContext) (ir.PhysicalPlan, error) {
	groups := output.GroupRows
	if groups == nil || output.RowGrain != "groups" {
		return ir.PhysicalPlan{}, fmt.Errorf("grouped physical plan requires groups row grain and a pinned revision")
	}
	if strings.TrimSpace(groups.RevisionID) == "" || strings.TrimSpace(context.DatasetGeneration) == "" {
		return ir.PhysicalPlan{}, fmt.Errorf("grouped physical plan requires revision and dataset generation identities")
	}
	selectionProject := strings.TrimSpace(context.SelectionProject)
	if selectionProject == "" {
		return ir.PhysicalPlan{}, fmt.Errorf("grouped physical plan requires the canonical selection project identity")
	}
	policy := groups.UnassignedMemberPolicy
	if policy != "ERROR" && policy != "EXCLUDE" && policy != "GROUP_AS_UNASSIGNED" {
		return ir.PhysicalPlan{}, fmt.Errorf("unsupported unassigned member policy %q", policy)
	}
	const (
		revisionCollection   = "loom_explorer_explicit_group_revisions"
		selectionCollection  = "loom_explorer_selections"
		definitionCollection = "loom_explorer_explicit_group_definitions"
		membershipCollection = "loom_explorer_explicit_group_memberships"
		selectionMembers     = "loom_explorer_selection_members"
	)
	binds := map[string]any{
		"group_rows_revision_collection":          revisionCollection,
		"group_rows_selection_collection":         selectionCollection,
		"group_rows_definition_collection":        definitionCollection,
		"group_rows_membership_collection":        membershipCollection,
		"group_rows_selection_members_collection": selectionMembers,
		"group_rows_resource_collection":          output.RootResourceType,
		"group_rows_revision_id":                  groups.RevisionID,
		"project":                                 selectionProject,
		"dataset_generation":                      context.DatasetGeneration,
		"resource_type":                           output.RootResourceType,
		"group_rows_unassigned_policy":            policy,
		"auth_resource_paths":                     append([]string(nil), context.AuthResourcePaths...),
		"auth_resource_paths_unrestricted":        semanticAuthScopeUnrestricted(context),
	}
	rows := &ir.PhysicalGroupRows{
		RevisionCollectionBindKey: "group_rows_revision_collection", SelectionCollectionBindKey: "group_rows_selection_collection",
		DefinitionsCollectionBindKey: "group_rows_definition_collection", MembershipsCollectionBindKey: "group_rows_membership_collection",
		SelectionMembersCollectionBindKey: "group_rows_selection_members_collection", ResourceCollectionBindKey: "group_rows_resource_collection",
		RevisionIDBindKey: "group_rows_revision_id", ProjectBindKey: "project", DatasetGenerationBindKey: "dataset_generation",
		ResourceTypeBindKey: "resource_type", PolicyBindKey: "group_rows_unassigned_policy",
		AuthResourcePathsBindKey: "auth_resource_paths", AuthUnrestrictedBindKey: "auth_resource_paths_unrestricted",
	}
	plan := ir.PhysicalPlan{
		Version:    1,
		Engine:     ir.PhysicalEngineAQL,
		Source:     ir.PhysicalSource{SemanticNode: "group_rows", SemanticField: "group_revision", ResourceType: output.RootResourceType},
		BindVars:   binds,
		Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalGroupRowsOp, Source: ir.PhysicalSource{SemanticNode: "group_rows", ResourceType: output.RootResourceType}, GroupRows: rows}},
	}
	if err := plan.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate grouped physical plan: %w", err)
	}
	return plan, nil
}
