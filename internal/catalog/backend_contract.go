package catalog

import "context"

// FieldProfileScope is the project/generation/resource-type identity every
// field-profile write is bound to. It moved here from the PostgreSQL store
// package under arch-backend-06: it is a domain identity, appears in the
// consumer-facing writer contract, and must not force consumers to name a
// backend implementation type. The store package keeps a type alias so its
// own surface is unchanged.
type FieldProfileScope struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	ResourceType      string
}

// The four catalog-evidence writer contracts a PostgreSQL generation load
// drives after the resource and edge passes. They are declared in the
// neutral catalog package (not on the consumer or implementation side) so
// internal/ingest composes the load without importing any backend
// implementation, and the factory's Runtime returns them as typed ports.
// The PostgreSQL adapters in internal/backends/postgres/internal/catalog
// satisfy them (compile-time assertions live in the factory assembly).

// FieldProfileWriter persists profiled field catalogs for one scope.
type FieldProfileWriter interface {
	WriteFieldProfile(ctx context.Context, scope FieldProfileScope, docs []FieldCatalogDocument, batchSize int) error
}

// InventoryRebuilder rebuilds the resource inventory for a dataset
// generation from already-persisted resources.
type InventoryRebuilder interface {
	RebuildResourceInventory(ctx context.Context, opts ResourceInventoryRebuildOptions) (ResourceInventoryRebuildSummary, error)
}

// RelationshipDiscoverer discovers relationship observations for a dataset
// generation from persisted resources and edges.
type RelationshipDiscoverer interface {
	DiscoverRelationshipObservations(ctx context.Context, opts RelationshipObservationOptions) (RelationshipObservationResult, error)
}

// SemanticContributor buffers semantic inventory contributions for a
// dataset generation.
type SemanticContributor interface {
	WriteSemanticInventoryContributions(ctx context.Context, contributions []SemanticInventoryContribution, batchSize int) error
}
