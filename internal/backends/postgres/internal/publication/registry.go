package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/calypr/loom/internal/dataframe/publication"
)

const (
	BundleExecutionsTable = "publication_bundle_executions"
	BundlePointersTable   = "publication_bundle_pointers"
	BundleLeasesTable     = "publication_bundle_leases"
)

var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

var ErrInvalidSchema = errors.New("publication postgres: invalid schema name")

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type DB interface {
	execer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Registry struct {
	db         DB
	executions string
	pointers   string
	leases     string
}

var (
	_ publication.BundleCatalog         = (*Registry)(nil)
	_ publication.ExactExecutionCatalog = (*Registry)(nil)
	_ publication.PagedBundleCatalog    = (*Registry)(nil)
)

func New(pool DB, schema string) (*Registry, error) {
	if pool == nil {
		return nil, errors.New("publication postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchema, schema)
	}
	return &Registry{
		db:         pool,
		executions: pgx.Identifier{schema, BundleExecutionsTable}.Sanitize(),
		pointers:   pgx.Identifier{schema, BundlePointersTable}.Sanitize(),
		leases:     pgx.Identifier{schema, BundleLeasesTable}.Sanitize(),
	}, nil
}

func pointerKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

func executionStates(state publication.BundleState) []string {
	states := []string{string(state.Canonical())}
	switch state.Canonical() {
	case publication.BundleQueued:
		states = append(states, string(publication.BundlePending))
	case publication.BundleRunning:
		states = append(states, string(publication.BundlePreflight), string(publication.BundleLoading))
	case publication.BundlePublished:
		states = append(states, string(publication.BundleReady))
	}
	return states
}

func executionParams(execution publication.BundleExecution) ([]any, error) {
	payload, err := json.Marshal(execution)
	if err != nil {
		return nil, fmt.Errorf("publication postgres: marshal execution payload: %w", err)
	}
	return []any{
		execution.ID, execution.Key, execution.Project, execution.DatasetGeneration,
		execution.Name, execution.TranslationVersion, execution.State,
		execution.CreatedAt, execution.UpdatedAt, execution.ReadyAt, execution.PublishedAt,
		string(payload),
	}, nil
}

const executionColumnList = "id, execution_key, project, dataset_generation, recipe_name, " +
	"translation_version, state, created_at, updated_at, ready_at, published_at, payload"

func scanExecution(row pgx.Row) (publication.BundleExecution, error) {
	var payload []byte
	if err := row.Scan(&payload); err != nil {
		return publication.BundleExecution{}, err
	}
	var execution publication.BundleExecution
	if err := json.Unmarshal(payload, &execution); err != nil {
		return publication.BundleExecution{}, fmt.Errorf("publication postgres: decode execution payload: %w", err)
	}
	return execution, nil
}

func (r *Registry) SaveExecution(ctx context.Context, execution publication.BundleExecution, fenceOwner string) error {
	if execution.ID == "" || execution.Key == "" || fenceOwner == "" {
		return publication.ErrBundleLeaseLost
	}
	params, err := executionParams(execution)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("publication postgres: begin save execution: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var held bool
	if err := tx.QueryRow(ctx,
		"SELECT true FROM "+r.leases+
			" WHERE key = $1 AND owner_id = $2 AND expires_at >= $3 FOR UPDATE",
		execution.Key, fenceOwner, time.Now().UTC()).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundleLeaseLost
		}
		return fmt.Errorf("publication postgres: check save fence: %w", err)
	}
	if !held {
		return publication.ErrBundleLeaseLost
	}

	set := "execution_key = EXCLUDED.execution_key, project = EXCLUDED.project, " +
		"dataset_generation = EXCLUDED.dataset_generation, recipe_name = EXCLUDED.recipe_name, " +
		"translation_version = EXCLUDED.translation_version, state = EXCLUDED.state, " +
		"created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at, " +
		"ready_at = EXCLUDED.ready_at, published_at = EXCLUDED.published_at, " +
		"payload = EXCLUDED.payload"
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+r.executions+" ("+executionColumnList+") "+
			"VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) "+
			"ON CONFLICT (id) DO UPDATE SET "+set,
		params...); err != nil {
		return fmt.Errorf("publication postgres: save execution: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: commit save execution: %w", err)
	}
	return nil
}

func (r *Registry) GetExecution(ctx context.Context, id string) (publication.BundleExecution, error) {
	execution, err := scanExecution(r.db.QueryRow(ctx,
		"SELECT payload FROM "+r.executions+" WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return publication.BundleExecution{}, publication.ErrBundleNotFound
	}
	if err != nil {
		return publication.BundleExecution{}, err
	}
	return execution.CanonicalizeLegacy(), nil
}

func (r *Registry) FindExecutionByKey(ctx context.Context, key string) (publication.BundleExecution, error) {
	execution, err := scanExecution(r.db.QueryRow(ctx,
		"SELECT payload FROM "+r.executions+
			" WHERE execution_key = $1 ORDER BY created_at DESC, id DESC LIMIT 1", key))
	if errors.Is(err, pgx.ErrNoRows) {
		return publication.BundleExecution{}, publication.ErrBundleNotFound
	}
	if err != nil {
		return publication.BundleExecution{}, err
	}
	return execution.CanonicalizeLegacy(), nil
}

func (r *Registry) scanExecutionRows(ctx context.Context, state publication.BundleState, before time.Time, afterUpdatedAt time.Time, afterID string, limit int) ([]publication.BundleExecution, time.Time, string, error) {
	query := "SELECT payload, updated_at, id FROM " + r.executions +
		" WHERE state = ANY($1) AND updated_at < $2"
	args := []any{executionStates(state), before}
	if limit > 0 {
		query += " AND (updated_at > $3 OR (updated_at = $3 AND id > $4))"
		args = append(args, afterUpdatedAt, afterID)
		args = append(args, limit)
		query += fmt.Sprintf(" ORDER BY updated_at ASC, id ASC LIMIT $%d", len(args))
	} else {
		query += " ORDER BY updated_at ASC, id ASC"
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, time.Time{}, "", fmt.Errorf("publication postgres: list executions: %w", err)
	}
	defer rows.Close()
	out := []publication.BundleExecution{}
	var lastAt time.Time
	var lastID string
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload, &lastAt, &lastID); err != nil {
			return nil, time.Time{}, "", fmt.Errorf("publication postgres: scan execution: %w", err)
		}
		var execution publication.BundleExecution
		if err := json.Unmarshal(payload, &execution); err != nil {
			return nil, time.Time{}, "", fmt.Errorf("publication postgres: decode execution payload: %w", err)
		}
		out = append(out, execution.CanonicalizeLegacy())
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, "", fmt.Errorf("publication postgres: list executions: %w", err)
	}
	return out, lastAt, lastID, nil
}

func (r *Registry) ListExecutions(ctx context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out, _, _, err := r.scanExecutionRows(ctx, state, before, time.Time{}, "", 0)
	return out, err
}

func (r *Registry) VisitExecutionPages(ctx context.Context, state publication.BundleState, before time.Time, pageSize int, visit publication.BundleExecutionPageFunc) error {
	if pageSize <= 0 {
		return fmt.Errorf("execution page size must be positive")
	}
	if visit == nil {
		return fmt.Errorf("execution page visitor is required")
	}
	var afterUpdatedAt time.Time
	afterID := ""
	for {
		page, lastAt, lastID, err := r.scanExecutionRows(ctx, state, before, afterUpdatedAt, afterID, pageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		if err := visit(page); err != nil {
			return err
		}
		if len(page) < pageSize {
			return nil
		}
		if lastID == "" || lastAt.IsZero() {
			return fmt.Errorf("execution page has no stable continuation key")
		}
		afterUpdatedAt, afterID = lastAt, lastID
	}
}

var publishedSelectorStates = []string{string(publication.BundlePublished), string(publication.BundleReady)}

func (r *Registry) FindExecutionBySelector(ctx context.Context, project, generation string, selector publication.DataframeSelector) (publication.BundleExecution, publication.BundleOutputRecord, error) {
	if err := selector.Validate(); err != nil {
		return publication.BundleExecution{}, publication.BundleOutputRecord{}, err
	}
	execution, err := scanExecution(r.db.QueryRow(ctx,
		"SELECT payload FROM "+r.executions+
			" WHERE project = $1 AND dataset_generation = $2 AND recipe_name = $3"+
			" AND translation_version = $4 AND state = ANY($5)"+
			" AND EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(payload->'outputs', '[]'::jsonb)) AS o"+
			" WHERE o->>'name' = $6 OR o->>'Name' = $6)"+
			" ORDER BY published_at DESC NULLS LAST, ready_at DESC NULLS LAST, updated_at DESC, id DESC"+
			" LIMIT 1",
		project, generation, selector.Recipe, selector.TranslationVersion,
		publishedSelectorStates, selector.Output))
	if errors.Is(err, pgx.ErrNoRows) {
		return publication.BundleExecution{}, publication.BundleOutputRecord{}, publication.ErrBundleNotFound
	}
	if err != nil {
		return publication.BundleExecution{}, publication.BundleOutputRecord{}, err
	}
	execution = execution.CanonicalizeLegacy()
	for _, output := range execution.Outputs {
		if output.Name == selector.Output {
			return execution, output, nil
		}
	}
	return publication.BundleExecution{}, publication.BundleOutputRecord{}, publication.ErrBundleNotFound
}

func (r *Registry) GetPointer(ctx context.Context, name string) (publication.BundlePointer, error) {
	var (
		nameBytes   []byte
		executionID string
		updatedAt   time.Time
	)
	err := r.db.QueryRow(ctx,
		"SELECT name_bytes, execution_id, updated_at FROM "+r.pointers+" WHERE pointer_key = $1",
		pointerKey(name)).Scan(&nameBytes, &executionID, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return publication.BundlePointer{}, publication.ErrBundleNotFound
	}
	if err != nil {
		return publication.BundlePointer{}, err
	}
	return publication.BundlePointer{Name: string(nameBytes), ExecutionID: executionID, UpdatedAt: updatedAt}, nil
}

func compareAndSwapPointer(ctx context.Context, e execer, qualifiedPointers, name, expected, next string, updatedAt time.Time) (bool, string, error) {
	sql := "INSERT INTO " + qualifiedPointers + " (pointer_key, name_bytes, execution_id, updated_at)" +
		" VALUES ($1, $2, $3, $4)" +
		" ON CONFLICT (pointer_key) DO UPDATE SET execution_id = $3, updated_at = $4" +
		" WHERE " + BundlePointersTable + ".execution_id = $5"
	tag, err := e.Exec(ctx, sql, pointerKey(name), []byte(name), next, updatedAt, expected)
	if err != nil {
		return false, sql, fmt.Errorf("publication postgres: compare-and-swap pointer: %w", err)
	}
	return tag.RowsAffected() == 1, sql, nil
}

func (r *Registry) CompareAndSwapPointer(ctx context.Context, name, expected, next string) error {
	swapped, _, err := compareAndSwapPointer(ctx, r.db, r.pointers, name, expected, next, time.Now().UTC())
	if err != nil {
		return err
	}
	if !swapped {
		return publication.ErrBundlePointerConflict
	}
	return nil
}

func (r *Registry) AcquireBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	now := time.Now().UTC()
	tag, err := r.db.Exec(ctx,
		"INSERT INTO "+r.leases+" (key, owner_id, expires_at, acquired_at, epoch)"+
			" VALUES ($1, $2, $3, $4, 1)"+
			" ON CONFLICT (key) DO UPDATE SET owner_id = $2, expires_at = $3, acquired_at = $4,"+
			" epoch = "+BundleLeasesTable+".epoch + 1"+
			" WHERE "+BundleLeasesTable+".expires_at < $4 OR "+BundleLeasesTable+".owner_id = $2",
		key, owner, expires, now)
	if err != nil {
		return false, fmt.Errorf("publication postgres: acquire bundle lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Registry) RenewBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx,
		"UPDATE "+r.leases+" SET expires_at = $3 WHERE key = $1 AND owner_id = $2",
		key, owner, expires)
	if err != nil {
		return false, fmt.Errorf("publication postgres: renew bundle lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Registry) ReleaseBundleLease(ctx context.Context, key, owner string) error {
	if _, err := r.db.Exec(ctx,
		"DELETE FROM "+r.leases+" WHERE key = $1 AND owner_id = $2", key, owner); err != nil {
		return fmt.Errorf("publication postgres: release bundle lease: %w", err)
	}
	return nil
}
