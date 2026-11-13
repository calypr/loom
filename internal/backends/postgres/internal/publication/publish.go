package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/dataframe/publication"
)

type publishAttempt struct {
	PointerName       string
	PointerKey        string
	ExpectedExecution string
	ExecutionID       string
	OwnerID           string
	BackendPID        uint32
	Statements        []string
	Converged         bool
}

var publishGate atomic.Pointer[func(publishAttempt) error]

const publishExecutionSet = "execution_key = $2, project = $3, dataset_generation = $4, recipe_name = $5, " +
	"translation_version = $6, state = $7, created_at = $8, updated_at = $9, " +
	"ready_at = $10, published_at = $11, payload = $12"

func (r *Registry) PublishExecution(ctx context.Context, name, expected string, execution publication.BundleExecution) error {
	params, err := executionParams(execution)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publication postgres: begin publish: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	attempt := publishAttempt{
		PointerName:       name,
		PointerKey:        pointerKey(name),
		ExpectedExecution: expected,
		ExecutionID:       execution.ID,
		OwnerID:           execution.OwnerID,
	}
	if conn := tx.Conn(); conn != nil {
		attempt.BackendPID = conn.PgConn().PID()
	}
	record := func(sql string) { attempt.Statements = append(attempt.Statements, sql) }

	fenceSQL := "SELECT true FROM " + r.leases +
		" WHERE key = $1 AND owner_id = $2 AND expires_at >= $3 FOR UPDATE"
	record(fenceSQL)
	var held bool
	if err := tx.QueryRow(ctx, fenceSQL, execution.Key, execution.OwnerID, time.Now().UTC()).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish lease: %w", err)
	}
	if !held {
		return publication.ErrBundlePointerConflict
	}

	lockSQL := "SELECT true FROM " + r.executions + " WHERE id = $1 FOR UPDATE"
	record(lockSQL)
	var exists bool
	if err := tx.QueryRow(ctx, lockSQL, execution.ID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish execution: %w", err)
	}
	if !exists {
		return publication.ErrBundlePointerConflict
	}

	swapped, casSQL, err := compareAndSwapPointer(ctx, tx, r.pointers, name, expected, execution.ID, execution.UpdatedAt)
	if err != nil {
		return err
	}
	record(casSQL)
	if !swapped {
		var current string
		pointerReadSQL := "SELECT execution_id FROM " + r.pointers + " WHERE pointer_key = $1"
		record(pointerReadSQL)
		if err := tx.QueryRow(ctx, pointerReadSQL, attempt.PointerKey).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return publication.ErrBundlePointerConflict
			}
			return fmt.Errorf("publication postgres: read contested pointer: %w", err)
		}
		if current != execution.ID {
			return publication.ErrBundlePointerConflict
		}
		var matches bool
		payloadReadSQL := "SELECT payload = $2::jsonb FROM " + r.executions + " WHERE id = $1"
		record(payloadReadSQL)
		if err := tx.QueryRow(ctx, payloadReadSQL, execution.ID, params[len(params)-1]).Scan(&matches); err != nil {
			return fmt.Errorf("publication postgres: compare publish payload: %w", err)
		}
		if !matches {
			return publication.ErrBundlePointerConflict
		}
		attempt.Converged = true
	} else {
		updateSQL := "UPDATE " + r.executions + " SET " + publishExecutionSet + " WHERE id = $1"
		record(updateSQL)
		if _, err := tx.Exec(ctx, updateSQL, params...); err != nil {
			return fmt.Errorf("publication postgres: publish execution: %w", err)
		}
	}

	if gate := publishGate.Load(); gate != nil {
		if err := (*gate)(attempt); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: commit publish: %w", err)
	}
	return nil
}
