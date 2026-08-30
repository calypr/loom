// Package arango persists Loom Explorer aggregate records.
package arango

import store "github.com/calypr/loom/internal/store/arango"

const (
	ExplorersCollection           = "loom_explorers"
	RevisionsCollection           = "loom_explorer_revisions"
	CompilationReceiptsCollection = "loom_explorer_compilation_receipts"
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
		{Name: LegacyRepositoryConfigsCollection, Indexes: [][]string{{"project"}}},
		CapabilitySnapshotCollectionSpec(),
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
