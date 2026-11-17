package control

import (
	"context"

	datasetstore "github.com/calypr/loom/internal/backends/postgres/internal/dataset"
	explorerstore "github.com/calypr/loom/internal/backends/postgres/internal/explorer"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"

	"github.com/calypr/loom/internal/catalog"
)

type ConnSource = pgstore.ConnSource

type FieldProfileScope = catalog.FieldProfileScope

type (
	ResourceBatch       = pgstore.ResourceBatch
	ResourceBatchResult = pgstore.ResourceBatchResult
	EdgeBatch           = pgstore.EdgeBatch
	EdgeBatchResult     = pgstore.EdgeBatchResult
)

const (
	MaxBatchResources         = pgstore.MaxBatchResources
	MaxBatchEdges             = pgstore.MaxBatchEdges
	MaxBatchIDLength          = pgstore.MaxBatchIDLength
	MaxFieldProfileWriteBatch = pgstore.MaxFieldProfileWriteBatch
	MaxInventoryWriteBatch    = pgstore.MaxInventoryWriteBatch
)

func NewResourceWriter(src ConnSource, schema string) (*ResourceWriter, error) {
	return pgstore.NewResourceWriter(src, schema)
}

func NewEdgeWriter(src ConnSource, schema string) (*EdgeWriter, error) {
	return pgstore.NewEdgeWriter(src, schema)
}

type (
	ResourceWriter = pgstore.ResourceWriter
	EdgeWriter     = pgstore.EdgeWriter
)

func GenerationAdvisoryLockKey(parts ...string) int64 {
	return pgstore.GenerationAdvisoryLockKey(parts...)
}

func WithAdvisoryLock(ctx context.Context, src ConnSource, key int64, work func() error) error {
	return pgstore.WithAdvisoryLock(ctx, src, key, work)
}

type MigrateResult = pgstore.MigrateResult

var CommitOutcomeUnknown = pgstore.CommitOutcomeUnknown

type (
	Client       = pgstore.Client
	ClientConfig = pgstore.Config
)

func OpenPool(ctx context.Context, cfg ClientConfig) (*Client, error) {
	return pgstore.Open(ctx, cfg)
}

var OwnerMigrations = explorerstore.OwnerMigrations

var CheckServerVersion = pgstore.CheckServerVersion

func MigrateSchema(ctx context.Context, client *Client, schema string) (MigrateResult, error) {
	return pgstore.MigrateSchema(ctx, client, schema)
}

func MigrateDatasetReleaseSchema(ctx context.Context, client *Client, schema string) (MigrateResult, error) {
	return datasetstore.MigrateSchema(ctx, client, schema)
}

var ErrResourceConflict = pgstore.ErrResourceConflict
