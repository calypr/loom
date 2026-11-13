package postgres

import (
	"context"
	"fmt"
	"hash/fnv"
)

const generationAdvisoryLockNamespace = "loom/pg-ingest-03/inventory-rebuild/v1"

func GenerationAdvisoryLockKey(parts ...string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(generationAdvisoryLockNamespace))
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return int64(h.Sum64())
}

func WithAdvisoryLock(ctx context.Context, src ConnSource, key int64, work func() error) (err error) {
	conn, err := src.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire advisory lock connection: %w", err)
	}
	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); lockErr != nil {
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
