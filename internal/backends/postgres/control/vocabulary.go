package control

// The public batch vocabulary of the PostgreSQL source backend.
//
// These re-exports carry the batch-write contract that the ingest
// orchestrator and the load CLI drive: batch payloads, results, the
// connection-source contract, the two bounded writer constructors, the
// advisory-lock helpers, and the batch bound constants. The types are
// aliases of the store's own types, so values cross the factory boundary
// unchanged, while the implementation packages stay under
// internal/backends/postgres/internal/ and unreachable from production
// callers. They belong here, at the backend's public edge, because their
// semantics are PostgreSQL-source-specific (batch ledger, resource/edge
// INSERT contracts); the neutral domain ports (manifest lifecycle, catalog
// evidence) are declared in their domain packages instead.

import (
	"context"

	explorerstore "github.com/calypr/loom/internal/backends/postgres/internal/explorer"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"

	"github.com/calypr/loom/internal/catalog"
)

// ConnSource is the single-connection acquisition contract the store's
// writers take. Runtime.ConnSource returns the runtime's own pool behind it.
type ConnSource = pgstore.ConnSource

// FieldProfileScope is the catalog write identity (re-exported from the
// neutral catalog package for callers that construct batches and profiles
// together).
type FieldProfileScope = catalog.FieldProfileScope

// Batch payloads and results.
type (
	ResourceBatch       = pgstore.ResourceBatch
	ResourceBatchResult = pgstore.ResourceBatchResult
	EdgeBatch           = pgstore.EdgeBatch
	EdgeBatchResult     = pgstore.EdgeBatchResult
)

// Batch bounds, duplicated here as constants (not aliases) so consumers
// bind to the public contract, not the implementation package.
const (
	MaxBatchResources         = pgstore.MaxBatchResources
	MaxBatchEdges             = pgstore.MaxBatchEdges
	MaxBatchIDLength          = pgstore.MaxBatchIDLength
	MaxFieldProfileWriteBatch = pgstore.MaxFieldProfileWriteBatch
	MaxInventoryWriteBatch    = pgstore.MaxInventoryWriteBatch
)

// NewResourceWriter constructs the bounded resource batch writer over a
// connection source and schema, keeping every store-side validation.
func NewResourceWriter(src ConnSource, schema string) (*ResourceWriter, error) {
	return pgstore.NewResourceWriter(src, schema)
}

// NewEdgeWriter constructs the bounded edge batch writer over a connection
// source and schema, keeping every store-side validation.
func NewEdgeWriter(src ConnSource, schema string) (*EdgeWriter, error) {
	return pgstore.NewEdgeWriter(src, schema)
}

// ResourceWriter and EdgeWriter are re-exported so consumer-side seams
// (the ingest writers' committer indirection) can name them without
// reaching into the implementation tree.
type (
	ResourceWriter = pgstore.ResourceWriter
	EdgeWriter     = pgstore.EdgeWriter
)

// GenerationAdvisoryLockKey derives the stable advisory-lock key for one
// dataset generation's inventory rebuild.
func GenerationAdvisoryLockKey(parts ...string) int64 {
	return pgstore.GenerationAdvisoryLockKey(parts...)
}

// WithAdvisoryLock runs work while holding the generation-scoped advisory
// lock on the caller's connection source.
func WithAdvisoryLock(ctx context.Context, src ConnSource, key int64, work func() error) error {
	return pgstore.WithAdvisoryLock(ctx, src, key, work)
}

// MigrateResult reports the outcome of one schema setup pass.
type MigrateResult = pgstore.MigrateResult

// CommitOutcomeUnknown is the driver-aware half of the "may still have
// committed" classification: SQLSTATE 08xx/57P01/57P02, the store sentinels,
// and connectivity classes. Callers keep their own domain-specific cases
// (safe-point cancellation) ahead of this check.
var CommitOutcomeUnknown = pgstore.CommitOutcomeUnknown

// ClientConfig and OpenPool expose the bare-pool open for witnesses and
// operator tools that deliberately need a raw readiness-checked pool
// without any control-plane assembly or schema setup. New production
// callers must use Open; this seam exists for test harnesses that witness
// pool-level behavior (pg_stat_activity attribution, backend termination)
// and for the store package's own contract tests.
type (
	Client       = pgstore.Client
	ClientConfig = pgstore.Config
)

func OpenPool(ctx context.Context, cfg ClientConfig) (*Client, error) {
	return pgstore.Open(ctx, cfg)
}

// OwnerMigrations exposes the owned migration set (names and ordered
// SQL) for witnesses that verify ledger contents directly.
var OwnerMigrations = explorerstore.OwnerMigrations

// CheckServerVersion is the store's readiness classifier (major must be 18)
// over a version number and display string, exposed for witnesses that
// gate on the same gate the pool's Ping enforces.
var CheckServerVersion = pgstore.CheckServerVersion

// MigrateSchema applies the owned source-series migrations to one schema,
// idempotently, over a caller-supplied pool. Runtime.MigrateSchema is the
// same pass against the runtime pool.
func MigrateSchema(ctx context.Context, client *Client, schema string) (MigrateResult, error) {
	return pgstore.MigrateSchema(ctx, client, schema)
}

var ErrResourceConflict = pgstore.ErrResourceConflict
