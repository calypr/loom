package postgres

import (
	"context"
	"errors"
	"fmt"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

type InventoryStore interface {
	ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]storepostgres.InventorySourceGroup, error)
	InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error
	PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error
	ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error)
	ReadCountSums(ctx context.Context, scope storepostgres.InventoryCountScope) (map[string]int64, error)
}

var _ InventoryStore = (*storepostgres.InventoryRepository)(nil)

type FieldProfileStore interface {
	UpsertFieldProfileBatch(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage storepostgres.FieldProfileCoverage, batchSize int) error
	ReadFieldProfileRows(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error)
	ReadFieldProfileRowsFiltered(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error)
	ReadProfileCoverage(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]storepostgres.FieldProfileCoverage, error)
	ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error)
}

var _ FieldProfileStore = (*storepostgres.FieldProfileRepository)(nil)

type InventoryAdapter struct {
	inventory InventoryStore
}

func NewInventoryAdapter(inventory InventoryStore) (*InventoryAdapter, error) {
	if inventory == nil {
		return nil, errors.New("catalog PostgreSQL inventory store is required")
	}
	return &InventoryAdapter{inventory: inventory}, nil
}

type FieldProfileAdapter struct {
	profiles FieldProfileStore
}

func NewFieldProfileAdapter(profiles FieldProfileStore) (*FieldProfileAdapter, error) {
	if profiles == nil {
		return nil, errors.New("catalog PostgreSQL field profile store is required")
	}
	return &FieldProfileAdapter{profiles: profiles}, nil
}

type RelationshipAdapter struct {
	relationships RelationshipStore
}

func NewRelationshipAdapter(relationships RelationshipStore) (*RelationshipAdapter, error) {
	if relationships == nil {
		return nil, errors.New("catalog PostgreSQL relationship evidence store is required")
	}
	return &RelationshipAdapter{relationships: relationships}, nil
}

type Store struct {
	InventoryAdapter
	FieldProfileAdapter
	RelationshipAdapter
}

var ErrStoreLegNotAssembled = errors.New("postgres catalog: store leg is not assembled")

func requireLeg(leg string, present bool, method string) error {
	if !present {
		return fmt.Errorf("%w: %s was called without the %s repository", ErrStoreLegNotAssembled, method, leg)
	}
	return nil
}

func NewFullStore(inventory InventoryStore, profiles FieldProfileStore, relationships RelationshipStore) (*Store, error) {
	inventoryAdapter, err := NewInventoryAdapter(inventory)
	if err != nil {
		return nil, err
	}
	profileAdapter, err := NewFieldProfileAdapter(profiles)
	if err != nil {
		return nil, err
	}
	relationshipAdapter, err := NewRelationshipAdapter(relationships)
	if err != nil {
		return nil, err
	}
	return &Store{
		InventoryAdapter:    *inventoryAdapter,
		FieldProfileAdapter: *profileAdapter,
		RelationshipAdapter: *relationshipAdapter,
	}, nil
}
