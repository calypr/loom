// Package postgres implements the PostgreSQL-side publication bundle
// catalog (pg-publication-bundle-catalog-v1): execution/output metadata,
// quality/state metadata, the exact logical pointer registry, and fenced
// bundle leases for the PostgreSQL control plane.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication row store. This registry persists metadata and logical
// pointers only -- it never reads, writes, or migrates ClickHouse data rows,
// and it never falls back to another backend on its own: every failure is
// returned for the caller to surface.
//
// Guarantees, mirroring internal/dataframe/publication/arango:
//   - The domain key and pointer-name semantics are the untouched
//     publication.BundleIdentity functions; the registry stores what the
//     domain computes and adds no name-only fallback of its own.
//   - Exact lookup filters on explicit project, dataset generation, recipe,
//     translation version, and output -- all four scope components must
//     match, and no name-only "latest" path exists.
//   - Legacy payloads canonicalize on read (publication.BundleExecution.
//     CanonicalizeLegacy) without rewriting the stored row; historical
//     records stay byte-identical.
//   - Pointer updates are an atomic compare-and-swap and PublishExecution
//     commits the execution state and the visibility pointer in one
//     PostgreSQL transaction, so no observer sees a PUBLISHED execution the
//     pointer does not yet name. Leases are fenced: SaveExecution and
//     PublishExecution require the current unexpired owner.
//   - Missing, JSON-null, and SQL-NULL stay distinct: timestamptz columns
//     are SQL NULL exactly when the Go pointer is nil, and the payload
//     preserves the original JSON null-versus-absent shape.
//
// The schema is created by tests/migrations/000004_publication_bundle_catalog.
// sql applied through the pg-store-02 MigrationRunner; this package contains
// no DDL.
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
	// BundleExecutionsTable stores execution identity, output schemas,
	// quality/state metadata, and the exact selector payload.
	BundleExecutionsTable = "publication_bundle_executions"
	// BundlePointersTable stores the exact logical pointer registry.
	BundlePointersTable = "publication_bundle_pointers"
	// BundleLeasesTable stores fenced bundle leases.
	BundleLeasesTable = "publication_bundle_leases"
)

// schemaNamePattern matches the migration runner's own schema rule so a
// registry can only ever address a safe lowercase identifier schema.
var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ErrInvalidSchema reports a schema name that is not a safe lowercase
// identifier; the registry refuses to build rather than interpolate it.
var ErrInvalidSchema = errors.New("publication postgres: invalid schema name")

// execer is the statement surface shared by pgxpool pools and transactions.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB is the pool surface the registry needs. *pgxpool.Pool satisfies it; the
// registry owns no connection beyond what a call acquires.
type DB interface {
	execer
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Registry is the PostgreSQL BundleCatalog. All statements use ordered $n
// parameters and schema-qualified table names; nothing user-supplied is
// interpolated into SQL.
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

// New binds a registry to one schema of a PostgreSQL 18 database served by
// pool. The schema must already contain the publication bundle catalog
// tables (tests/migrations/000004_publication_bundle_catalog.sql via the
// pg-store-02 MigrationRunner); New never runs DDL.
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

// pointerKey is the hex SHA-256 of the logical pointer name -- the same
// digest discipline the Arango registry uses -- so a PointerName carrying
// embedded NUL bytes (legal for the length-prefixed domain encoding) can key
// a text primary key. The exact name still round-trips losslessly through
// the bytea column.
func pointerKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// executionStates expands a canonical state query to the compatibility-only
// stored values, exactly as the Arango registry does, so legacy rows remain
// visible without ever being rewritten. The result is plain strings so pgx
// binds it as text[] for state = ANY($n).
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

// executionParams are the ordered $1..$12 bind values for a full row write:
// identity scope, scan columns, nullable stamps (SQL NULL exactly when the
// Go pointer is nil), and the marshaled payload. The execution is stored
// exactly as given -- legacy states and missing fields included -- because
// canonicalization belongs to reads, not writes.
func executionParams(execution publication.BundleExecution) ([]any, error) {
	payload, err := json.Marshal(execution)
	if err != nil {
		return nil, fmt.Errorf("publication postgres: marshal execution payload: %w", err)
	}
	return []any{
		execution.ID, execution.Key, execution.Project, execution.DatasetGeneration,
		execution.Name, execution.TranslationVersion, execution.State,
		execution.CreatedAt, execution.UpdatedAt, execution.ReadyAt, execution.PublishedAt,
		// The payload binds as text with an unspecified parameter OID so
		// PostgreSQL infers jsonb from the column context; []byte would be
		// typed bytea and rejected.
		string(payload),
	}, nil
}

const executionColumnList = "id, execution_key, project, dataset_generation, recipe_name, " +
	"translation_version, state, created_at, updated_at, ready_at, published_at, payload"

// scanExecution decodes one payload-bearing row into a BundleExecution. The
// payload is authoritative, so nanosecond timestamps and the JSON
// null-versus-absent shape survive a save/read round trip untouched.
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

// SaveExecution checkpoints execution metadata under the lease fence on the
// execution's key: the write is applied in one transaction only while
// fenceOwner holds an unexpired lease, so a checkpoint from a worker that
// lost ownership fails with ErrBundleLeaseLost instead of clobbering the
// new owner's record. An empty ID, key, or owner is a lost fence, matching
// the Arango registry.
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

// GetExecution loads one execution by id. Legacy shapes are canonicalized on
// the returned copy only; the stored row is never rewritten.
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

// FindExecutionByKey returns the newest-created execution sharing an
// identity key. The id tie-break makes the answer deterministic when
// created_at ties, where the Arango query's sort was unspecified.
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

// scanExecutionRows reads one bounded (state, updated_at, id)-ordered slice.
// A positive limit applies the keyset continuation after (afterUpdatedAt,
// afterID); a zero limit is the unbounded scan. It returns the canonicalized
// page plus the raw scan columns of its last row for continuation.
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

// ListExecutions returns every execution in the requested state
// (compatibility-only stored values included) updated strictly before
// before, ordered stably by (updated_at, id). An empty result is a non-nil
// empty slice, matching the Arango registry.
func (r *Registry) ListExecutions(ctx context.Context, state publication.BundleState, before time.Time) ([]publication.BundleExecution, error) {
	out, _, _, err := r.scanExecutionRows(ctx, state, before, time.Time{}, "", 0)
	return out, err
}

// VisitExecutionPages walks a state scan in stable (updated_at, id) keyset
// pages, calling visit sequentially and stopping on its first error, with
// the same contract and continuation discipline as the Arango registry.
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

// publishedSelectorStates are the states an exact lookup may return, the
// same published/ready pair the Arango exact query accepts. Plain strings
// bind as text[] for state = ANY($n).
var publishedSelectorStates = []string{string(publication.BundlePublished), string(publication.BundleReady)}

// FindExecutionBySelector is the exact release-verification lookup. All
// four scope components (project, generation, recipe, translation version)
// are required equality filters and the output must exist among the
// execution's outputs; there is no name-only latest fallback. Among equal
// matches the Arango tie order (published, ready, updated) is preserved,
// with a deterministic id tie-break.
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

// GetPointer reads one logical pointer. The pointer name round-trips
// byte-exact through its bytea column, including embedded NUL bytes.
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

// compareAndSwapPointer moves a pointer to next only while the current row
// still names expected, or when the row is absent and the insert wins. The
// ON CONFLICT guard is re-evaluated against the newest committed row, so
// racing publishers converge to exactly one winner. The conflict clause
// references the target by its unqualified name, as PostgreSQL requires, and
// the exact name binds as bytea so NUL-bearing PointerNames survive, which
// both text and jsonb would reject.
func compareAndSwapPointer(ctx context.Context, e execer, qualifiedPointers, name, expected, next string, updatedAt time.Time) (bool, error) {
	tag, err := e.Exec(ctx,
		"INSERT INTO "+qualifiedPointers+" (pointer_key, name_bytes, execution_id, updated_at)"+
			" VALUES ($1, $2, $3, $4)"+
			" ON CONFLICT (pointer_key) DO UPDATE SET execution_id = $3, updated_at = $4"+
			" WHERE "+BundlePointersTable+".execution_id = $5",
		pointerKey(name), []byte(name), next, updatedAt, expected)
	if err != nil {
		return false, fmt.Errorf("publication postgres: compare-and-swap pointer: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CompareAndSwapPointer is the visibility update: it succeeds only when the
// pointer is absent or still names expected; otherwise it reports
// ErrBundlePointerConflict with the row untouched.
func (r *Registry) CompareAndSwapPointer(ctx context.Context, name, expected, next string) error {
	swapped, err := compareAndSwapPointer(ctx, r.db, r.pointers, name, expected, next, time.Now().UTC())
	if err != nil {
		return err
	}
	if !swapped {
		return publication.ErrBundlePointerConflict
	}
	return nil
}

// PublishExecution atomically commits the PUBLISHED execution metadata and
// its visibility pointer in one PostgreSQL transaction under the lease
// fence. A missing execution, an unheld or expired lease, or a failed
// pointer compare-and-swap rolls the whole thing back and reports
// ErrBundlePointerConflict, so no observer -- above all the reconcile
// sweep -- ever sees a PUBLISHED execution the pointer does not yet name.
// Lock order is stable across writers: lease, execution, pointer.
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

	var held bool
	if err := tx.QueryRow(ctx,
		"SELECT true FROM "+r.leases+
			" WHERE key = $1 AND owner_id = $2 AND expires_at >= $3 FOR UPDATE",
		execution.Key, execution.OwnerID, time.Now().UTC()).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish lease: %w", err)
	}
	if !held {
		return publication.ErrBundlePointerConflict
	}

	var exists bool
	if err := tx.QueryRow(ctx,
		"SELECT true FROM "+r.executions+" WHERE id = $1 FOR UPDATE", execution.ID).
		Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return publication.ErrBundlePointerConflict
		}
		return fmt.Errorf("publication postgres: check publish execution: %w", err)
	}
	if !exists {
		return publication.ErrBundlePointerConflict
	}

	swapped, err := compareAndSwapPointer(ctx, tx, r.pointers, name, expected, execution.ID, execution.UpdatedAt)
	if err != nil {
		return err
	}
	if !swapped {
		return publication.ErrBundlePointerConflict
	}

	set := "execution_key = $2, project = $3, dataset_generation = $4, recipe_name = $5, " +
		"translation_version = $6, state = $7, created_at = $8, updated_at = $9, " +
		"ready_at = $10, published_at = $11, payload = $12"
	if _, err := tx.Exec(ctx,
		"UPDATE "+r.executions+" SET "+set+" WHERE id = $1", params...); err != nil {
		return fmt.Errorf("publication postgres: publish execution: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("publication postgres: commit publish: %w", err)
	}
	return nil
}

// AcquireBundleLease claims a lease key when it is free, expired, or already
// held by owner. Every grant bumps epoch, correlating repeated ownership of
// one key (including a same-owner reclaim after expiry). The conflict clause
// references the target by its unqualified name, as PostgreSQL requires.
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

// RenewBundleLease extends the lease only for its current owner. Matching
// the Arango registry, renewal does not re-check expiry: an owner whose
// renewal raced the clock keeps its fence, while a different owner can
// never renew.
func (r *Registry) RenewBundleLease(ctx context.Context, key, owner string, expires time.Time) (bool, error) {
	tag, err := r.db.Exec(ctx,
		"UPDATE "+r.leases+" SET expires_at = $3 WHERE key = $1 AND owner_id = $2",
		key, owner, expires)
	if err != nil {
		return false, fmt.Errorf("publication postgres: renew bundle lease: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReleaseBundleLease removes the lease only for its current owner; a
// non-owner release is a no-op that leaves the fence intact.
func (r *Registry) ReleaseBundleLease(ctx context.Context, key, owner string) error {
	if _, err := r.db.Exec(ctx,
		"DELETE FROM "+r.leases+" WHERE key = $1 AND owner_id = $2", key, owner); err != nil {
		return fmt.Errorf("publication postgres: release bundle lease: %w", err)
	}
	return nil
}
