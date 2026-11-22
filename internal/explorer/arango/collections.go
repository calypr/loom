package arango

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

func CollectionSpecs() []store.CollectionSpec {
	return []store.CollectionSpec{
		{Name: ExplorersCollection, Indexes: [][]string{{"project", "explorerId"}}},
		{Name: DraftRevisionsCollection, Indexes: [][]string{{"project", "explorerId", "draftVersion"}}},
		{Name: RevisionsCollection, Indexes: [][]string{{"project", "explorerId", "createdAt"}, {"project", "status"}}},
		{Name: CompilationReceiptsCollection, Indexes: [][]string{
			{"project", "explorerId", "intentDigest"},
			{"project", "explorerId", "createdAt"},
			{"project", "explorerId", "compilationKey", "receiptFormatVersion", "compilerContractVersion"},
		}},
		{Name: SelectionsCollection, Indexes: [][]string{
			{"project", "id"},
			{"project", "idempotencyKey"},
			{"project", "generation", "resourceType", "createdAt"},
			{"state", "createdAt"},
		}},
		{Name: SelectionMembersCollection, Indexes: [][]string{
			{"selectionId", "project", "generation", "resourceType", "id"},
			{"selectionId", "memberKey"},
		}},
		{Name: ExplicitGroupRevisionsCollection, Indexes: [][]string{
			{"project", "id"},
			{"project", "idempotencyKey"},
			{"project", "generation", "resourceType", "createdAt"},
			{"state", "createdAt"},
		}},
		{Name: ExplicitGroupDefinitionsCollection, Indexes: [][]string{
			{"revisionId", "ordinal"},
			{"revisionId", "groupId"},
		}},
		{Name: ExplicitGroupMembershipsCollection, Indexes: [][]string{
			{"revisionId", "groupId", "id"},
			{"revisionId", "project", "generation", "resourceType", "id"},
		}},
		{Name: InterpretationLibrariesCollection, Indexes: [][]string{{"project", "id"}, {"project", "headRevisionId"}}},
		{Name: InterpretationRevisionsCollection, Indexes: [][]string{{"project", "id"}, {"project", "libraryId", "createdAt"}, {"project", "contentDigest"}}},
		{Name: ViewBuilderSettingsCollection, Indexes: [][]string{{"project"}}},
		{Name: LegacyRepositoryConfigsCollection, Indexes: [][]string{{"project"}}},
		CapabilitySnapshotCollectionSpec(),
		workspaceProjectionCollectionSpec(),
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
