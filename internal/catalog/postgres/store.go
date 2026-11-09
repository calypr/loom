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

// FieldProfileStore is the persistence boundary this adapter needs from
// internal/store/postgres for bounded field-profile records.
// *storepostgres.FieldProfileRepository implements it; tests may wrap the
// real repository to inject failures, never to fake results.
type FieldProfileStore interface {
	// UpsertFieldProfileBatch replaces exactly the identities named by the
	// documents and refreshes the scope's coverage marker in one
	// transaction. An empty document list records a known-empty profile by
	// writing only the marker.
	UpsertFieldProfileBatch(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage storepostgres.FieldProfileCoverage, batchSize int) error
	// ReadFieldProfileRows returns every persisted document (field records
	// and any profile_status marker) for one exact scope, ordered by path.
	ReadFieldProfileRows(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error)
	// ReadFieldProfileRowsFiltered returns every persisted document inside
	// one project/generation/type/auth filter, ordered by resource type and
	// path.
	ReadFieldProfileRowsFiltered(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error)
	// ReadProfileCoverage returns the scope-level persistence markers inside
	// one filter. Their presence is what makes a zero-row profile read a
	// known-empty profile rather than an unavailable one.
	ReadProfileCoverage(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]storepostgres.FieldProfileCoverage, error)
}

var _ FieldProfileStore = (*storepostgres.FieldProfileRepository)(nil)

// Store is the PostgreSQL catalog adapter for this slice. Create one with
// New or NewFieldProfileStore.
type Store struct {
	inventory InventoryStore
	profiles  FieldProfileStore
}

// New validates the persistence boundary. The store holds no state beyond
// the injected repository; the caller owns the pool and schema lifecycle.
func New(inventory InventoryStore) (*Store, error) {
	if inventory == nil {
		return nil, errors.New("catalog PostgreSQL inventory store is required")
	}
	return &Store{inventory: inventory}, nil
}

// NewFieldProfileStore validates the field-profile persistence boundary and
// returns a Store exposing only the field-profile methods (WriteFieldProfile,
// ReadFieldProfile, DiscoverFieldEnrichment). The store holds no state
// beyond the injected repository; the caller owns the pool and schema
// lifecycle.
func NewFieldProfileStore(profiles FieldProfileStore) (*Store, error) {
	if profiles == nil {
		return nil, errors.New("catalog PostgreSQL field profile store is required")
	}
	return &Store{profiles: profiles}, nil
}
