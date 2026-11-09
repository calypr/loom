// Package postgres contains the PostgreSQL 18 persistence adapter for catalog
// facts.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This slice owns the generation-scoped resource
// inventory: exact zero-inclusive counts per resource type and authorization
// path, and a publish-last completeness marker so a partial count build is
// never readable as complete. There is no cross-database atomicity and no
// backend fallback: every failure is a named error the caller must surface.
package postgres

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/catalog"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
)

// InventoryStore is the persistence boundary this adapter needs from
// internal/store/postgres. *storepostgres.InventoryRepository implements it;
// tests may wrap the real repository to inject failures, never to fake
// results.
type InventoryStore interface {
	// ScanSourceCounts groups retained source rows for one exact
	// project/generation/resource type by auth path, ascending.
	ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]storepostgres.InventorySourceGroup, error)
	// InsertCounts commits one build's count rows in bounded chunks.
	InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error
	// PublishBuild swaps the published marker only after the store's commit
	// guard confirms the build's own rows are durable and complete.
	PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error
	// ReadBuild reads the one published marker for a project/generation.
	ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error)
	// ReadCountSums sums committed counts for one build inside one scope.
	ReadCountSums(ctx context.Context, scope storepostgres.InventoryCountScope) (map[string]int64, error)
}

var _ InventoryStore = (*storepostgres.InventoryRepository)(nil)

// Store is the PostgreSQL catalog adapter for this slice. Create one with
// New.
type Store struct {
	inventory InventoryStore
}

// New validates the persistence boundary. The store holds no state beyond
// the injected repository; the caller owns the pool and schema lifecycle.
func New(inventory InventoryStore) (*Store, error) {
	if inventory == nil {
		return nil, errors.New("catalog PostgreSQL inventory store is required")
	}
	return &Store{inventory: inventory}, nil
}
