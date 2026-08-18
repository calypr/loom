// Package arango persists Loom Explorer aggregate records.
package arango

import store "github.com/calypr/loom/internal/store/arango"

const (
	ExplorersCollection         = "loom_explorers"
	RevisionsCollection         = "loom_explorer_revisions"
	RepositoryConfigsCollection = "loom_repository_explorer_configs"
)

// CollectionSpecs is deliberately non-truncating: both draft history pointers
// and immutable revision/share URLs must survive normal server bootstrap.
func CollectionSpecs() []store.CollectionSpec {
	return []store.CollectionSpec{
		{Name: ExplorersCollection, Indexes: [][]string{{"project", "explorerId"}}},
		{Name: RevisionsCollection, Indexes: [][]string{{"project", "explorerId", "createdAt"}, {"project", "status"}, {"project", "explorerId", "sourceCommit", "definitionDigest", "sourceGeneration"}}},
		{Name: RepositoryConfigsCollection, Indexes: [][]string{{"project"}}},
	}
}
func BootstrapSpec() store.BootstrapSpec { return store.BootstrapSpec{Collections: CollectionSpecs()} }
