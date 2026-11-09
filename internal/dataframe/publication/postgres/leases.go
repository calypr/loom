package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
)

const (
	// ExecutionCheckpointsTable stores owner-conditional checkpoint records.
	// The lease rows they fence live in BundleLeasesTable (owned by
	// 000004_publication_bundle_catalog.sql); the records themselves are
	// created by
	// tests/migrations-leases/000006_publication_lease_checkpoints.sql
	// applied through the pg-store-02 MigrationRunner. This file contains no DDL.
	ExecutionCheckpointsTable = "publication_execution_checkpoints"
	// LeaseGrantsTable stores the per-key generation high-water every
	// committed Acquire bumps, so retired fencing tokens are never reissued
	// across lease-row deletion. Created by the same 000006 migration; only
	// AcquireLease writes it, and it is never deleted.
	LeaseGrantsTable = "publication_lease_grants"
)

// ErrInvalidLeaseKey reports an Acquire with an empty lease key or owner.
// Unlike a lost fence (publication.ErrBundleLeaseLost), it marks a caller
// bug that must never reach the shared lease table as a stored row.
var ErrInvalidLeaseKey = errors.New("publication postgres: lease key and owner are required")

// CheckpointRecord is one durable checkpoint written under a fenced lease.
// OwnerID and Generation pin the exact ownership grant that wrote it (the
// lease epoch at write time), so a witness can reconstruct which
// owner-episode produced every checkpoint after takeovers and same-owner
// reclaims. Payload holds the jsonb-normalized stored bytes; the distinction
// between a missing member and a JSON null is preserved inside it.
type CheckpointRecord struct {
	Key            string
	Sequence       int64
	OwnerID        string
	Generation     int64
	CheckpointedAt time.Time
	Payload        []byte
}

// FencedLeases fences execution-owner and reconciler checkpoints with
// owner-conditional PostgreSQL leases over the shared publication bundle
// lease table. It is the hardened checkpoint path of the PostgreSQL control
// plane, deliberately stricter than the Arango-parity lease methods on
// Registry:
//
//   - Acquire claims a free or expired key, or re-grants a key already held
//     by the same owner. Every committed grant bumps a per-key high-water in
//     LeaseGrantsTable and writes that value as the lease epoch, so the
//     returned generation is a strictly monotonic fencing token per key
//     (repeated-owner correlation) across the whole lifetime of the key --
//     even after the lease row itself is deleted by ReleaseLease,
//     Registry.ReleaseBundleLease, or anything else: a retired token can
//     never be reissued while its previous holder may still be alive. A
//     claim of an expired key is a takeover that retires the previous grant.
//     (Registry's own Arango-parity lease methods keep their baseline epoch
//     behaviour and do not participate in this high-water protocol; epochs
//     written by Registry.AcquireBundleLease are not keeper fencing tokens.)
//   - Renew and Expire require the exact owner AND generation AND an
//     unexpired lease: an expired lease can never be renewed or resurrected
//     by its old owner. (Registry.RenewBundleLease keeps the Arango
//     baseline's clock-race leniency for its own callers; this keeper does
//     not.)
//   - Checkpoint applies its record only if the write SQL itself finds the
//     lease row owned by the claimed owner at the claimed generation and
//     unexpired at the call instant. Owner and expiry are compared inside
//     the single INSERT, never in a separate read an attacker of stale state
//     -- a paused writer -- could have raced.
//   - Release is owner-and-generation-conditional, so a stale token cannot
//     clear the row a takeover installed; the same owner's stale generation
//     cannot clear its own newer grant either.
//
// Like the Registry, the keeper persists metadata only: ClickHouse remains
// the publication data plane, there is no cross-database atomicity, and
// there is no silent backend fallback -- every failure is returned for the
// caller to surface. All statements use ordered $n parameters and
// schema-qualified table names; nothing user-supplied is interpolated.
type FencedLeases struct {
	db          DB
	leases      string
	grants      string
	checkpoints string
}

// NewLeaseKeeper binds a fenced-lease keeper to one schema of a PostgreSQL
// 18 database served by pool. The schema must already contain the
// publication bundle lease table (000004, via Registry's history) and the
// grant high-water and checkpoint records tables (000006); NewLeaseKeeper
// never runs DDL and never falls back when any is missing -- the first
// statement surfaces the failure.
func NewLeaseKeeper(pool DB, schema string) (*FencedLeases, error) {
	if pool == nil {
		return nil, errors.New("publication postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &FencedLeases{
		db:          pool,
		leases:      pgx.Identifier{schema, BundleLeasesTable}.Sanitize(),
		grants:      pgx.Identifier{schema, LeaseGrantsTable}.Sanitize(),
		checkpoints: pgx.Identifier{schema, ExecutionCheckpointsTable}.Sanitize(),
	}, nil
}

// AcquireLease claims a lease key when it is free, expired (a takeover), or
// already held by owner (a regrant, e.g. after the owner let its own lease
// lapse). Every committed grant bumps the per-key high-water in
// LeaseGrantsTable inside one transaction and writes that value as the
// lease epoch, then returns it as the generation fencing token the caller
// must present to Renew, Expire, Release, and Checkpoint. Because the
// high-water row is never deleted, generations are strictly monotonic per
// key across lease-row deletion: after a release (by ReleaseLease or
// Registry.ReleaseBundleLease alike), the same owner id re-acquiring the
// key receives a strictly newer generation, so a straggler carrying a
// retired token can never match the live grant. held=false means another
// owner currently holds an unexpired lease on the key; the high-water bump
// is rolled back with the failed transaction, so no generation is burned.
// Concurrent acquires serialize on the high-water row, then the lease row --
// a fixed order that cannot cycle against other keepers.
func (k *FencedLeases) AcquireLease(ctx context.Context, key, owner string, expires time.Time) (int64, bool, error) {
	if key == "" || owner == "" {
		return 0, false, fmt.Errorf("%w: got key %q owner %q", ErrInvalidLeaseKey, key, owner)
	}
	now := time.Now().UTC()
	tx, err := k.db.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("publication postgres: begin acquire fenced lease: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var highWater int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO "+k.grants+" (key, last_generation) VALUES ($1, 1)"+
			" ON CONFLICT (key) DO UPDATE SET last_generation = "+LeaseGrantsTable+".last_generation + 1"+
			" RETURNING last_generation",
		key).Scan(&highWater); err != nil {
		return 0, false, fmt.Errorf("publication postgres: bump fenced lease high-water: %w", err)
	}

	var generation int64
	err = tx.QueryRow(ctx,
		"INSERT INTO "+k.leases+" (key, owner_id, expires_at, acquired_at, epoch)"+
			" VALUES ($1, $2, $3, $4, $5)"+
			" ON CONFLICT (key) DO UPDATE SET owner_id = $2, expires_at = $3, acquired_at = $4,"+
			" epoch = $5"+
			" WHERE "+BundleLeasesTable+".expires_at < $4 OR "+BundleLeasesTable+".owner_id = $2"+
			" RETURNING epoch",
		key, owner, expires, now, highWater).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("publication postgres: acquire fenced lease: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("publication postgres: commit acquire fenced lease: %w", err)
	}
	return generation, true, nil
}

// RenewLease extends the lease only for its exact current owner and
// generation, and only while the lease is unexpired at the call instant. An
// expired lease can never be renewed: the old owner must go back through
// Acquire, which grants a new generation and retires this token.
func (k *FencedLeases) RenewLease(ctx context.Context, key, owner string, generation int64, expires time.Time) (bool, error) {
	if key == "" || owner == "" || generation < 1 {
		return false, nil
	}
	tag, err := k.db.Exec(ctx,
		"UPDATE "+k.leases+" SET expires_at = $4"+
			" WHERE key = $1 AND owner_id = $2 AND epoch = $3 AND expires_at >= $5",
		key, owner, generation, expires, time.Now().UTC())
	if err != nil {
		return false, fmt.Errorf("publication postgres: renew fenced lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ExpireLease retires the lease immediately for its exact current owner and
// generation -- the graceful yield of an execution owner that knows it must
// stop, so a reconciler or successor can take over without waiting for the
// TTL. The stored expiry is set one microsecond behind the call instant so
// every later owner-conditional guard, which compares against its own later
// call time, sees the lease as strictly expired. A stale token can expire
// nothing.
func (k *FencedLeases) ExpireLease(ctx context.Context, key, owner string, generation int64) (bool, error) {
	if key == "" || owner == "" || generation < 1 {
		return false, nil
	}
	retireAt := time.Now().UTC().Add(-time.Microsecond)
	tag, err := k.db.Exec(ctx,
		"UPDATE "+k.leases+" SET expires_at = $4"+
			" WHERE key = $1 AND owner_id = $2 AND epoch = $3 AND expires_at >= $4",
		key, owner, generation, retireAt)
	if err != nil {
		return false, fmt.Errorf("publication postgres: expire fenced lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseLease removes the lease only for its exact current owner and
// generation. A non-owner, a stale generation, or a stale owner token after
// a takeover is a no-op that leaves the new owner's fence intact.
func (k *FencedLeases) ReleaseLease(ctx context.Context, key, owner string, generation int64) (bool, error) {
	if key == "" || owner == "" || generation < 1 {
		return false, nil
	}
	tag, err := k.db.Exec(ctx,
		"DELETE FROM "+k.leases+" WHERE key = $1 AND owner_id = $2 AND epoch = $3",
		key, owner, generation)
	if err != nil {
		return false, fmt.Errorf("publication postgres: release fenced lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// jsonKind names the leading JSON token of a rejected encoding for the
// refusal message, without echoing payload bytes into the error.
func jsonKind(encoded []byte) string {
	switch encoded[0] {
	case '[':
		return "a JSON array"
	case '"':
		return "a JSON string"
	case 't', 'f':
		return "a JSON bool"
	case 'n':
		return "JSON null"
	default:
		return "a JSON number"
	}
}

// Checkpoint writes one progress record for the execution-owner or
// reconciler identified by (key, owner, generation). The owner, generation,
// and unexpired comparisons run inside the INSERT itself, evaluated at the
// call instant, so a writer that lost ownership -- above all a paused
// writer resuming after a takeover -- cannot interleave a read-then-write
// race past the fence: the statement matches zero rows and the checkpoint
// is refused with publication.ErrBundleLeaseLost. The per-key sequence is
// assigned by the same statement, so it is gap-free across ownership
// changes and a stale token cannot pre-fill it.
func (k *FencedLeases) Checkpoint(ctx context.Context, key, owner string, generation int64, payload any) (int64, error) {
	if key == "" || owner == "" || generation < 1 {
		return 0, publication.ErrBundleLeaseLost
	}
	if payload == nil {
		return 0, errors.New("publication postgres: checkpoint payload is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("publication postgres: marshal checkpoint payload: %w", err)
	}
	// The stored row must be a JSON object (000006 CHECK). json.Marshal of a
	// scalar, array, or null succeeds, so the object shape is enforced here
	// at the boundary -- a caller bug is refused without touching the server,
	// the same discipline as ErrInvalidLeaseKey, and never as a wrapped 23514.
	if len(encoded) == 0 || encoded[0] != '{' {
		return 0, fmt.Errorf("publication postgres: checkpoint payload must be a JSON object, got %s", jsonKind(encoded))
	}
	now := time.Now().UTC()
	var sequence int64
	err = k.db.QueryRow(ctx,
		"WITH next AS (SELECT COALESCE(MAX(sequence), 0) + 1 AS seq FROM "+k.checkpoints+" WHERE key = $1)"+
			" INSERT INTO "+k.checkpoints+" (key, sequence, owner_id, generation, checkpointed_at, payload)"+
			" SELECT $1, next.seq, $2, $3, $4, $5::jsonb FROM next"+
			" WHERE EXISTS (SELECT 1 FROM "+k.leases+" l"+
			"  WHERE l.key = $1 AND l.owner_id = $2 AND l.epoch = $3 AND l.expires_at >= $4)"+
			" RETURNING sequence",
		key, owner, generation, now,
		// Bound as text with an explicit ::jsonb cast: INSERT ... SELECT
		// does not pin the parameter type from the column context the way
		// VALUES does, and []byte would be typed bytea and rejected.
		string(encoded)).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, publication.ErrBundleLeaseLost
	}
	if err != nil {
		return 0, fmt.Errorf("publication postgres: checkpoint: %w", err)
	}
	return sequence, nil
}

// LatestCheckpoint returns the highest-sequence checkpoint for a key, the
// resume point a new owner reads after a takeover. ok=false means the key
// has never been checkpointed.
func (k *FencedLeases) LatestCheckpoint(ctx context.Context, key string) (CheckpointRecord, bool, error) {
	var record CheckpointRecord
	err := k.db.QueryRow(ctx,
		"SELECT key, sequence, owner_id, generation, checkpointed_at, payload"+
			" FROM "+k.checkpoints+" WHERE key = $1 ORDER BY sequence DESC LIMIT 1",
		key).Scan(&record.Key, &record.Sequence, &record.OwnerID, &record.Generation,
		&record.CheckpointedAt, &record.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckpointRecord{}, false, nil
	}
	if err != nil {
		return CheckpointRecord{}, false, fmt.Errorf("publication postgres: latest checkpoint: %w", err)
	}
	return record, true, nil
}
