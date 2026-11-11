package postgres

import (
	"context"
	"fmt"
	"hash/fnv"
)

// Session-scoped advisory lock helper for serializing short critical
// sections across sessions of the same schema, with the same discipline as
// the migration runner's lock: every exit path confirms the unlock, and a
// connection of unknown lock state is discarded rather than returned to the
// pool.

// generationAdvisoryLockNamespace namespaces generation-load helper locks
// away from the migration lock domain.
const generationAdvisoryLockNamespace = "loom/pg-ingest-03/inventory-rebuild/v1"

// GenerationAdvisoryLockKey derives a stable int64 advisory-lock key for a
// generation-scoped critical section. The same parts always map to the same
// key; callers never supply raw SQL.
func GenerationAdvisoryLockKey(parts ...string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(generationAdvisoryLockNamespace))
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return int64(h.Sum64())
}

// WithAdvisoryLock runs work while holding a session-level advisory lock on
// one acquired connection, so concurrent callers of the same key serialize.
// The unlock is confirmed on every exit path; a connection whose lock state
// is unknown (failed unlock or reported-already-unlocked) is discarded,
// never pooled, because a session still holding the lock would starve every
// later caller.
func WithAdvisoryLock(ctx context.Context, src ConnSource, key int64, work func() error) (err error) {
	conn, err := src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire advisory lock connection: %w", err)
	}
	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); lockErr != nil {
		// A failed grant tells us nothing about whether the backend granted
		// the lock before aborting; a statement abort never releases a
		// session-level lock, so the connection is discarded.
		discardPoolConn(conn)
		return fmt.Errorf("postgres: acquire advisory lock %d: %w", key, lockErr)
	}
	defer func() {
		unlockCtx, cancelUnlock := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancelUnlock()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			discardPoolConn(conn)
			if err == nil {
				if unlockErr != nil {
					err = fmt.Errorf("postgres: release advisory lock %d: %w", key, unlockErr)
					return
				}
				err = fmt.Errorf("postgres: release advisory lock %d: reported already unlocked", key)
			}
			return
		}
		conn.Release()
	}()
	return work()
}
