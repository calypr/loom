package catalog

import "context"

type FieldProfileScope struct {
	Project           string
	DatasetGeneration string
	AuthResourcePath  string
	ResourceType      string
}

type FieldProfileWriter interface {
	WriteFieldProfile(ctx context.Context, scope FieldProfileScope, docs []FieldCatalogDocument, batchSize int) error
}

type InventoryRebuilder interface {
	RebuildResourceInventory(ctx context.Context, opts ResourceInventoryRebuildOptions) (ResourceInventoryRebuildSummary, error)
}

type RelationshipDiscoverer interface {
	DiscoverRelationshipObservations(ctx context.Context, opts RelationshipObservationOptions) (RelationshipObservationResult, error)
}

type SemanticContributor interface {
	WriteSemanticInventoryContributions(ctx context.Context, contributions []SemanticInventoryContribution, batchSize int) error
}
