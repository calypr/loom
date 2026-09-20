// Package arango persists Loom Explorer aggregate records.
package arango

import store "github.com/calypr/loom/internal/store/arango"

const (
	ExplorersCollection                = "loom_explorers"
	RevisionsCollection                = "loom_explorer_revisions"
	CompilationReceiptsCollection      = "loom_explorer_compilation_receipts"
	SelectionsCollection               = "loom_explorer_selections"
	SelectionMembersCollection         = "loom_explorer_selection_members"
	ExplicitGroupRevisionsCollection   = "loom_explorer_explicit_group_revisions"
	ExplicitGroupDefinitionsCollection = "loom_explorer_explicit_group_definitions"
	ExplicitGroupMembershipsCollection = "loom_explorer_explicit_group_memberships"
	InterpretationLibrariesCollection  = "loom_explorer_interpretation_libraries"
	InterpretationRevisionsCollection  = "loom_explorer_interpretation_revisions"
	// LegacyRepositoryConfigsCollection is read only. It remains in bootstrap
	// for one compatibility window so startup can migrate old default-owner
	// pointers into loom_explorers; no current workflow writes it.
	LegacyRepositoryConfigsCollection = "loom_repository_explorer_configs"
)

// CollectionSpecs is deliberately non-truncating: both draft history pointers
// and immutable revision/share URLs must survive normal server bootstrap.
func CollectionSpecs() []store.CollectionSpec {
	return []store.CollectionSpec{
		{Name: ExplorersCollection, Indexes: [][]string{{"project", "explorerId"}}},
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
		{Name: LegacyRepositoryConfigsCollection, Indexes: [][]string{{"project"}}},
		CapabilitySnapshotCollectionSpec(),
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
