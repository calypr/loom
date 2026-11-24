package db

import (
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/selection"
)

const (
	ExplorersCollection                = "loom_explorers"
	DraftRevisionsCollection           = "loom_explorer_draft_revisions"
	RevisionsCollection                = "loom_explorer_revisions"
	CompilationReceiptsCollection      = "loom_explorer_compilation_receipts"
	SelectionsCollection               = "loom_explorer_selections"
	SelectionMembersCollection         = selection.MembersCollection
	ExplicitGroupRevisionsCollection   = "loom_explorer_explicit_group_revisions"
	ExplicitGroupDefinitionsCollection = "loom_explorer_explicit_group_definitions"
	ExplicitGroupMembershipsCollection = "loom_explorer_explicit_group_memberships"
	InterpretationLibrariesCollection  = "loom_explorer_interpretation_libraries"
	InterpretationRevisionsCollection  = "loom_explorer_interpretation_revisions"
	ViewBuilderSettingsCollection      = "loom_explorer_view_builder_settings"

	LegacyRepositoryConfigsCollection = "loom_repository_explorer_configs"
)

const WorkspaceProjectionsCollection = "loom_explorer_workspace_projections"

func GroupRowsCollections() recipe.GroupRowsCollections {
	return recipe.GroupRowsCollections{
		Revisions:        ExplicitGroupRevisionsCollection,
		Selections:       SelectionsCollection,
		Definitions:      ExplicitGroupDefinitionsCollection,
		Memberships:      ExplicitGroupMembershipsCollection,
		SelectionMembers: SelectionMembersCollection,
	}
}
