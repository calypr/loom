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
	"fmt"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

// InventoryStore is the persistence boundary this adapter needs from
// internal/backends/postgres/internal/store. *storepostgres.InventoryRepository implements it;
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
// internal/backends/postgres/internal/store for bounded field-profile records.
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
	// ListDistinctAuthResourcePaths returns the distinct non-empty auth
	// paths recorded for one project across every dataset generation: the
	// cross-generation union the project-level write authorization (F-B27)
	// needs, with no dataset_generation predicate (an empty generation is
	// the legacy unscoped namespace, never "any generation"). A project
	// with no recorded paths returns an honest empty, never a refusal.
	ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error)
}

var _ FieldProfileStore = (*storepostgres.FieldProfileRepository)(nil)

// Store is the PostgreSQL catalog adapter for this slice. Create one with
// NewFullStore (every repository, the shape Runtime.Catalog must carry), or
// with the single-leg New / NewFieldProfileStore / NewRelationshipEvidenceStore
// for a port that consumes only its own method family. A method whose
// repository is absent refuses through requireLeg as a typed unavailability —
// it never dereferences nil (PG-PG-CAPABILITY-EVIDENCE-COMPOSITION-PANIC:
// the capability resolver consumes all three reads from one value).
type Store struct {
	inventory     InventoryStore
	profiles      FieldProfileStore
	relationships RelationshipStore
}

// ErrStoreLegNotAssembled reports that a Store method ran on a Store built
// without its repository. Like every other unassigned PostgreSQL port, the
// consumer must treat it as a configuration refusal; it is returned instead
// of dereferencing the nil field.
var ErrStoreLegNotAssembled = errors.New("postgres catalog: store leg is not assembled")

// requireLeg names the missing repository before any query runs. The
// method-name argument keeps the refusal attributable to the call site.
func requireLeg(leg string, present bool, method string) error {
	if !present {
		return fmt.Errorf("%w: %s was called on a Store without the %s repository", ErrStoreLegNotAssembled, method, leg)
	}
	return nil
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

// NewRelationshipEvidenceStore validates the relationship-evidence
// persistence boundary and returns a Store exposing only the relationship
// discovery methods (DiscoverRelationshipObservations,
// ReadRelationshipObservations). The store holds no state beyond the
// injected boundary; the caller owns the pool and schema lifecycle, and the
// occurrence table must be created by MigrateSchema before use.
func NewRelationshipEvidenceStore(relationships RelationshipStore) (*Store, error) {
	if relationships == nil {
		return nil, errors.New("catalog PostgreSQL relationship evidence store is required")
	}
	return &Store{relationships: relationships}, nil
}

// NewFullStore assembles the complete catalog adapter over all three
// persistence boundaries: inventory, field profiles, and relationship
// evidence. This is the shape that satisfies the CapabilityEvidenceReader
// contract (DiscoverResourceInventory, DiscoverRelationshipObservations,
// DiscoverFieldEnrichment all read from one value), mirroring the Arango
// lane's single full store. Every repository is required; a genuinely
// unassigned series must stay a nil port at the runtime level, where
// consumers treat it as a configuration refusal. The store holds no state
// beyond the injected repositories; the caller owns the pool and schema
// lifecycle.
func NewFullStore(inventory InventoryStore, profiles FieldProfileStore, relationships RelationshipStore) (*Store, error) {
	if inventory == nil {
		return nil, errors.New("catalog PostgreSQL inventory store is required")
	}
	if profiles == nil {
		return nil, errors.New("catalog PostgreSQL field profile store is required")
	}
	if relationships == nil {
		return nil, errors.New("catalog PostgreSQL relationship evidence store is required")
	}
	return &Store{inventory: inventory, profiles: profiles, relationships: relationships}, nil
}
