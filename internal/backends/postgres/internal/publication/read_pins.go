package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
)

const (
	ExecutionRetentionTable     = "publication_execution_retention"
	ExecutionReadPinsTable      = "publication_execution_read_pins"
	ExecutionCleanupClaimsTable = "publication_execution_cleanup_claims"

	executionCleanupTTL = 2 * time.Minute
)

var ErrInvalidCleanupIdentity = errors.New("publication postgres: cleanup execution id and owner are required")

type readPinAttempt struct {
	Op          string
	ExecutionID string
	OwnerID     string
	BackendPID  uint32
	Statements  []string
	Claimed     bool
}

var readPinGate atomic.Pointer[func(readPinAttempt) error]

type ReadPins struct {
	db        DB
	retention string
	pins      string
	cleanup   string

	claimTTL time.Duration
}

var _ publication.ExecutionReadPinCatalog = (*ReadPins)(nil)

type BundleCatalog struct {
	*Registry
	pins *ReadPins
}

var (
	_ publication.BundleCatalog           = (*BundleCatalog)(nil)
	_ publication.ExactExecutionCatalog   = (*BundleCatalog)(nil)
	_ publication.PagedBundleCatalog      = (*BundleCatalog)(nil)
	_ publication.ExecutionReadPinCatalog = (*BundleCatalog)(nil)
)

func NewBundleCatalog(pool DB, schema string) (*BundleCatalog, error) {
	registry, err := New(pool, schema)
	if err != nil {
		return nil, err
	}
	pins, err := NewReadPins(pool, schema)
	if err != nil {
		return nil, err
	}
	return &BundleCatalog{Registry: registry, pins: pins}, nil
}

func (c *BundleCatalog) Pins() *ReadPins { return c.pins }

func (c *BundleCatalog) AcquireExecutionReadPin(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.AcquireExecutionReadPin(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) RenewExecutionReadPin(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.RenewExecutionReadPin(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) ReleaseExecutionReadPin(ctx context.Context, executionID, ownerID string) error {
	return c.pins.ReleaseExecutionReadPin(ctx, executionID, ownerID)
}

func (c *BundleCatalog) ClaimExecutionCleanup(ctx context.Context, executionID, ownerID string) (bool, error) {
	return c.pins.ClaimExecutionCleanup(ctx, executionID, ownerID)
}

func (c *BundleCatalog) RenewExecutionCleanup(ctx context.Context, executionID, ownerID string, expiresAt time.Time) (bool, error) {
	return c.pins.RenewExecutionCleanup(ctx, executionID, ownerID, expiresAt)
}

func (c *BundleCatalog) ReleaseExecutionCleanup(ctx context.Context, executionID, ownerID string) error {
	return c.pins.ReleaseExecutionCleanup(ctx, executionID, ownerID)
}

func NewReadPins(pool DB, schema string) (*ReadPins, error) {
	if pool == nil {
		return nil, errors.New("publication postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &ReadPins{
		db:        pool,
		retention: pgx.Identifier{schema, ExecutionRetentionTable}.Sanitize(),
		pins:      pgx.Identifier{schema, ExecutionReadPinsTable}.Sanitize(),
		cleanup:   pgx.Identifier{schema, ExecutionCleanupClaimsTable}.Sanitize(),
		claimTTL:  executionCleanupTTL,
	}, nil
}

type readPinTx struct {
	p        *ReadPins
	tx       pgx.Tx
	attempt  readPinAttempt
	recorded []string
}

func (p *ReadPins) beginTransition(ctx context.Context, op, executionID, owner string) (*readPinTx, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("publication postgres: begin %s: %w", op, err)
	}
	t := &readPinTx{p: p, tx: tx, attempt: readPinAttempt{Op: op, ExecutionID: executionID, OwnerID: owner}}
	if conn := tx.Conn(); conn != nil {
		t.attempt.BackendPID = conn.PgConn().PID()
	}
	return t, nil
}

func (t *readPinTx) record(sql string) {
	t.recorded = append(t.recorded, sql)
	t.attempt.Statements = t.recorded
}

func (t *readPinTx) exec(ctx context.Context, sql string, args ...any) (int64, error) {
	t.record(sql)
	tag, err := t.tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, fmt.Errorf("publication postgres: %s: %w", t.attempt.Op, err)
	}
	return tag.RowsAffected(), nil
}

func (t *readPinTx) queryBool(ctx context.Context, sql string, args ...any) (bool, error) {
	t.record(sql)
	var value bool
	if err := t.tx.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
		return false, fmt.Errorf("publication postgres: %s guard: %w", t.attempt.Op, err)
	}
	return value, nil
}

func (t *readPinTx) finish(ctx context.Context, claimed bool) (bool, error) {
	t.attempt.Claimed = claimed
	if gate := readPinGate.Load(); gate != nil {
		if err := (*gate)(t.attempt); err != nil {
			_ = t.tx.Rollback(ctx)
			return false, err
		}
	}
	if !claimed {
		if err := t.tx.Rollback(ctx); err != nil {
			return false, fmt.Errorf("publication postgres: %s rollback refused transition: %w", t.attempt.Op, err)
		}
		return false, nil
	}
	if err := t.tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("publication postgres: %s commit: %w", t.attempt.Op, err)
	}
	return true, nil
}

func (t *readPinTx) close() {
	_ = t.tx.Rollback(context.WithoutCancel(context.Background()))
}

func (t *readPinTx) ensureAnchor(ctx context.Context, now time.Time) error {
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.retention+" (execution_id, updated_at) VALUES ($1, $2)"+
			" ON CONFLICT (execution_id) DO NOTHING",
		t.attempt.ExecutionID, now); err != nil {
		return err
	}
	held, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !held {
		return fmt.Errorf("publication postgres: %s: anchor row vanished for execution %s", t.attempt.Op, t.attempt.ExecutionID)
	}
	return nil
}

func (t *readPinTx) lockAnchor(ctx context.Context) (bool, error) {
	t.record("SELECT true FROM " + t.p.retention + " WHERE execution_id = $1 FOR UPDATE")
	var held bool
	err := t.tx.QueryRow(ctx,
		"SELECT true FROM "+t.p.retention+" WHERE execution_id = $1 FOR UPDATE",
		t.attempt.ExecutionID).Scan(&held)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("publication postgres: %s lock anchor: %w", t.attempt.Op, err)
	}
	return held, nil
}

func (t *readPinTx) touchAnchor(ctx context.Context, now time.Time) error {
	_, err := t.exec(ctx,
		"UPDATE "+t.p.retention+" SET updated_at = $2 WHERE execution_id = $1",
		t.attempt.ExecutionID, now)
	return err
}

func (t *readPinTx) guardCleanupFree(ctx context.Context, now time.Time) (bool, error) {
	return t.queryBool(ctx,
		"SELECT NOT EXISTS (SELECT 1 FROM "+t.p.cleanup+" WHERE execution_id = $1 AND expires_at >= $2)",
		t.attempt.ExecutionID, now)
}

func (t *readPinTx) guardNoActivePins(ctx context.Context, now time.Time) (bool, error) {
	return t.queryBool(ctx,
		"SELECT NOT EXISTS (SELECT 1 FROM "+t.p.pins+" WHERE execution_id = $1 AND expires_at >= $2)",
		t.attempt.ExecutionID, now)
}

func (t *readPinTx) gcExpiredPins(ctx context.Context, now time.Time) error {
	_, err := t.exec(ctx,
		"DELETE FROM "+t.p.pins+" WHERE execution_id = $1 AND expires_at < $2",
		t.attempt.ExecutionID, now)
	return err
}

func (p *ReadPins) AcquireExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "acquire-pin", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	if err := t.ensureAnchor(ctx, now); err != nil {
		return false, err
	}
	free, err := t.guardCleanupFree(ctx, now)
	if err != nil {
		return false, err
	}
	if !free {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.pins+" (execution_id, owner_id, expires_at, updated_at)"+
			" VALUES ($1, $2, $3, $4)"+
			" ON CONFLICT (execution_id, owner_id) DO UPDATE SET expires_at = $3, updated_at = $4",
		executionID, owner, expires, now); err != nil {
		return false, err
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

func (p *ReadPins) RenewExecutionReadPin(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "renew-pin", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return t.finish(ctx, false)
	}
	free, err := t.guardCleanupFree(ctx, now)
	if err != nil {
		return false, err
	}
	if !free {
		return t.finish(ctx, false)
	}
	owned, err := t.queryBool(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+t.p.pins+" WHERE execution_id = $1 AND owner_id = $2 AND expires_at >= $3)",
		executionID, owner, now)
	if err != nil {
		return false, err
	}
	if !owned {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	rows, err := t.exec(ctx,
		"UPDATE "+t.p.pins+" SET expires_at = $3, updated_at = $4"+
			" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner, expires, now)
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, fmt.Errorf("publication postgres: renew-pin lost pin row under lock: %s/%s", executionID, owner)
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

func (p *ReadPins) ReleaseExecutionReadPin(ctx context.Context, executionID, owner string) error {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return publication.ErrExecutionReadPinLost
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "release-pin", executionID, owner)
	if err != nil {
		return err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return t.commitTransition(ctx)
	}
	if _, err := t.exec(ctx,
		"DELETE FROM "+t.p.pins+" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner); err != nil {
		return err
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return err
	}
	return t.commitTransition(ctx)
}

func (p *ReadPins) ClaimExecutionCleanup(ctx context.Context, executionID, owner string) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "claim-cleanup", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	if err := t.ensureAnchor(ctx, now); err != nil {
		return false, err
	}
	noPins, err := t.guardNoActivePins(ctx, now)
	if err != nil {
		return false, err
	}
	if !noPins {
		return t.finish(ctx, false)
	}
	var (
		claimOwner   string
		claimExpires time.Time
		claimExists  bool
	)
	t.record("SELECT owner_id, expires_at FROM " + t.p.cleanup + " WHERE execution_id = $1")
	err = t.tx.QueryRow(ctx,
		"SELECT owner_id, expires_at FROM "+t.p.cleanup+" WHERE execution_id = $1",
		executionID).Scan(&claimOwner, &claimExpires)
	if err == nil {
		claimExists = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("publication postgres: claim-cleanup read claim: %w", err)
	}
	if claimExists && !claimExpires.Before(now) && claimOwner != owner {
		return t.finish(ctx, false)
	}
	if err := t.gcExpiredPins(ctx, now); err != nil {
		return false, err
	}
	if _, err := t.exec(ctx,
		"INSERT INTO "+t.p.cleanup+" (execution_id, owner_id, expires_at, updated_at)"+
			" VALUES ($1, $2, $3, $4)"+
			" ON CONFLICT (execution_id) DO UPDATE SET owner_id = $2, expires_at = $3, updated_at = $4",
		executionID, owner, now.Add(p.claimTTL), now); err != nil {
		return false, err
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

func (p *ReadPins) RenewExecutionCleanup(ctx context.Context, executionID, owner string, expires time.Time) (bool, error) {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return false, ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "renew-cleanup", executionID, owner)
	if err != nil {
		return false, err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return false, err
	}
	if !exists {
		return t.finish(ctx, false)
	}
	var (
		claimOwner   string
		claimExpires time.Time
		claimFound   bool
	)
	t.record("SELECT owner_id, expires_at FROM " + t.p.cleanup + " WHERE execution_id = $1")
	err = t.tx.QueryRow(ctx,
		"SELECT owner_id, expires_at FROM "+t.p.cleanup+" WHERE execution_id = $1",
		executionID).Scan(&claimOwner, &claimExpires)
	if err == nil {
		claimFound = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("publication postgres: renew-cleanup read claim: %w", err)
	}
	if !claimFound || claimOwner != owner || claimExpires.Before(now) {
		return t.finish(ctx, false)
	}
	noPins, err := t.guardNoActivePins(ctx, now)
	if err != nil {
		return false, err
	}
	if !noPins {
		return t.finish(ctx, false)
	}
	rows, err := t.exec(ctx,
		"UPDATE "+t.p.cleanup+" SET expires_at = $3, updated_at = $4"+
			" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner, expires, now)
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, fmt.Errorf("publication postgres: renew-cleanup lost claim row under lock: %s/%s", executionID, owner)
	}
	if err := t.touchAnchor(ctx, now); err != nil {
		return false, err
	}
	return t.finish(ctx, true)
}

func (p *ReadPins) ReleaseExecutionCleanup(ctx context.Context, executionID, owner string) error {
	executionID, owner = strings.TrimSpace(executionID), strings.TrimSpace(owner)
	if executionID == "" || owner == "" {
		return ErrInvalidCleanupIdentity
	}
	now := time.Now().UTC()
	t, err := p.beginTransition(ctx, "release-cleanup", executionID, owner)
	if err != nil {
		return err
	}
	defer t.close()
	exists, err := t.lockAnchor(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return t.commitTransition(ctx)
	}
	rows, err := t.exec(ctx,
		"DELETE FROM "+t.p.cleanup+" WHERE execution_id = $1 AND owner_id = $2",
		executionID, owner)
	if err != nil {
		return err
	}
	if rows == 1 {
		if err := t.touchAnchor(ctx, now); err != nil {
			return err
		}
	}
	return t.commitTransition(ctx)
}

func (t *readPinTx) commitTransition(ctx context.Context) error {
	t.attempt.Claimed = true
	if gate := readPinGate.Load(); gate != nil {
		if err := (*gate)(t.attempt); err != nil {
			_ = t.tx.Rollback(ctx)
			return err
		}
	}
	if err := t.tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: %s commit: %w", t.attempt.Op, err)
	}
	return nil
}
