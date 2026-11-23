package arango

import (
	"github.com/calypr/loom/internal/explorer/arango/capabilitydb"
	"github.com/calypr/loom/internal/explorer/arango/db"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	ExplorersCollection                = db.ExplorersCollection
	DraftRevisionsCollection           = db.DraftRevisionsCollection
	RevisionsCollection                = db.RevisionsCollection
	CompilationReceiptsCollection      = db.CompilationReceiptsCollection
	SelectionsCollection               = db.SelectionsCollection
	SelectionMembersCollection         = db.SelectionMembersCollection
	ExplicitGroupRevisionsCollection   = db.ExplicitGroupRevisionsCollection
	ExplicitGroupDefinitionsCollection = db.ExplicitGroupDefinitionsCollection
	ExplicitGroupMembershipsCollection = db.ExplicitGroupMembershipsCollection
	InterpretationLibrariesCollection  = db.InterpretationLibrariesCollection
	InterpretationRevisionsCollection  = db.InterpretationRevisionsCollection
	ViewBuilderSettingsCollection      = db.ViewBuilderSettingsCollection
	LegacyRepositoryConfigsCollection  = db.LegacyRepositoryConfigsCollection
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
		capabilitydb.CapabilitySnapshotCollectionSpec(),
		workspaceProjectionCollectionSpec(),
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
