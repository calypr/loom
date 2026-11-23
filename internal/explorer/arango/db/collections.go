package db

import store "github.com/calypr/loom/internal/store/arango"

const (
	ExplorersCollection                = "loom_explorers"
	DraftRevisionsCollection           = "loom_explorer_draft_revisions"
	RevisionsCollection                = "loom_explorer_revisions"
	CompilationReceiptsCollection      = "loom_explorer_compilation_receipts"
	SelectionsCollection               = "loom_explorer_selections"
	SelectionMembersCollection         = store.SelectionMembersCollection
	ExplicitGroupRevisionsCollection   = "loom_explorer_explicit_group_revisions"
	ExplicitGroupDefinitionsCollection = "loom_explorer_explicit_group_definitions"
	ExplicitGroupMembershipsCollection = "loom_explorer_explicit_group_memberships"
	InterpretationLibrariesCollection  = "loom_explorer_interpretation_libraries"
	InterpretationRevisionsCollection  = "loom_explorer_interpretation_revisions"
	ViewBuilderSettingsCollection      = "loom_explorer_view_builder_settings"

	LegacyRepositoryConfigsCollection = "loom_repository_explorer_configs"
)

const WorkspaceProjectionsCollection = "loom_explorer_workspace_projections"
