package arango

import (
	"github.com/calypr/loom/internal/explorer/arango/capabilitydb"
	"github.com/calypr/loom/internal/explorer/arango/db"
	store "github.com/calypr/loom/internal/store/arango"
)

func CollectionSpecs() []store.CollectionSpec {
	return []store.CollectionSpec{
		{Name: db.ExplorersCollection, Indexes: [][]string{{"project", "explorerId"}}},
		{Name: db.DraftRevisionsCollection, Indexes: [][]string{{"project", "explorerId", "draftVersion"}}},
		{Name: db.RevisionsCollection, Indexes: [][]string{{"project", "explorerId", "createdAt"}, {"project", "status"}}},
		{Name: db.CompilationReceiptsCollection, Indexes: [][]string{
			{"project", "explorerId", "intentDigest"},
			{"project", "explorerId", "createdAt"},
			{"project", "explorerId", "compilationKey", "receiptFormatVersion", "compilerContractVersion"},
		}},
		{Name: db.SelectionsCollection, Indexes: [][]string{
			{"project", "id"},
			{"project", "idempotencyKey"},
			{"project", "generation", "resourceType", "createdAt"},
			{"state", "createdAt"},
		}},
		{Name: db.SelectionMembersCollection, Indexes: [][]string{
			{"selectionId", "project", "generation", "resourceType", "id"},
			{"selectionId", "memberKey"},
		}},
		{Name: db.ExplicitGroupRevisionsCollection, Indexes: [][]string{
			{"project", "id"},
			{"project", "idempotencyKey"},
			{"project", "generation", "resourceType", "createdAt"},
			{"state", "createdAt"},
		}},
		{Name: db.ExplicitGroupDefinitionsCollection, Indexes: [][]string{
			{"revisionId", "ordinal"},
			{"revisionId", "groupId"},
		}},
		{Name: db.ExplicitGroupMembershipsCollection, Indexes: [][]string{
			{"revisionId", "groupId", "id"},
			{"revisionId", "project", "generation", "resourceType", "id"},
		}},
		{Name: db.InterpretationLibrariesCollection, Indexes: [][]string{{"project", "id"}, {"project", "headRevisionId"}}},
		{Name: db.InterpretationRevisionsCollection, Indexes: [][]string{{"project", "id"}, {"project", "libraryId", "createdAt"}, {"project", "contentDigest"}}},
		{Name: db.ViewBuilderSettingsCollection, Indexes: [][]string{{"project"}}},
		{Name: db.LegacyRepositoryConfigsCollection, Indexes: [][]string{{"project"}}},
		capabilitydb.CapabilitySnapshotCollectionSpec(),
		workspaceProjectionCollectionSpec(),
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
