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
	ExecutionCheckpointsTable = "publication_execution_checkpoints"
	LeaseGrantsTable          = "publication_lease_grants"
)

var ErrInvalidLeaseKey = errors.New("publication postgres: lease key and owner are required")

type CheckpointRecord struct {
	Key            string
	Sequence       int64
	OwnerID        string
	Generation     int64
	CheckpointedAt time.Time
	Payload        []byte
}

type FencedLeases struct {
	db          DB
	leases      string
	grants      string
	checkpoints string
}

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
		string(encoded)).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, publication.ErrBundleLeaseLost
	}
	if err != nil {
		return 0, fmt.Errorf("publication postgres: checkpoint: %w", err)
	}
	return sequence, nil
}

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
