// Package postgres implements the PostgreSQL-side project recipe revision
// chains (pg-state-recipe-registries-v1): the immutable, project-scoped
// RecipeRevision documents and their CAS head tracking for the PostgreSQL
// control plane.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package adds no second driver, pool, fixture,
// or migration runner: it reuses internal/backends/postgres/internal/store (pg-store-01 pool
// surface, pg-store-02 runner) and adds only the recipe-revision relational
// schema and its Register/Get/List behavior. There is no silent backend
// fallback: every failure is returned for the caller to surface, and no
// Arango path is wired in here. There is no cross-database atomicity and no
// existing-deployment migration.
//
// Guarantees, mirroring internal/dataframe/recipe/exec/arango/revisions.go:
//   - Register canonicalizes the Bundle and takes its digest, then compares
//     the requested parent with the current head under a row lock on the
//     chain's head row. A parent that is not the current head -- including
//     an empty parent once a head exists -- fails with the typed
//     STALE_CURSOR user fault; a parent supplied for a chain's first
//     revision fails with the typed INVALID_REQUEST user fault; a store
//     fault stays untyped (5xx semantics), matching the Arango adapter's
//     error classification.
//   - Concurrent children of one parent have exactly one winner: the loser
//     blocks on the head-row lock, re-reads the moved head, and receives
//     the typed stale-parent conflict. Its revision row is rolled back with
//     its transaction, so a loser never leaves an orphan revision behind.
//   - Retrying the current head's exact content (parent == head == child
//     digest) is idempotent: the stored revision is returned untouched,
//     with no head bump. Appending content whose digest already exists
//     deeper in the chain refreshes that row's parent link and stamp to the
//     new edge, the same one-writer-per-(project, name, digest) behavior the
//     Arango adapter gets from its same-_key overwrite; the bundle itself
//     can never change because the digest pins it.
//   - Get and List stay tenant/project scoped by explicit equality on
//     (project, name) -- and (project, name, digest) for Get -- with a
//     deterministic (created_at DESC, digest) total order; the digest
//     tie-break removes the Arango sort's unspecified tie order.
//   - Missing versus JSON-null versus SQL-NULL stay distinct: parent_digest
//     is NOT NULL DEFAULT ” (the Go string's "no parent"), the payload
//     omits parentDigest when empty (missing), and head_digest is SQL NULL
//     only in the epoch-0 writer-serialization placeholder state, which the
//     pair CHECK makes unrepresentable as data and no read path can return.
//
// The schema is created by this package's embedded migrations
// (migrations/000001_ledger.sql and migrations/000003_recipe_revision_chains.
// sql) applied through the pg-store-02 MigrationRunner; the package contains
// no DDL outside those files. Exactly one embedded history may ever migrate a
// given schema: this history owns recipe-revision schemas, and the sibling
// exec-registry history (internal/backends/postgres/internal/recipeexec) is the
// ticket's other history. Both embed the version-1 ledger byte-identically,
// so a combined control-plane schema may only be migrated by the composed
// history {1 ledger, 2 recipe_exec_registries, 3 recipe_revision_chains} in
// version order; a partial mix fails closed with a named runner error
// (ErrMigrationOutOfOrder or ErrMigrationChecksumMismatch) before applying
// any statement. Callers must surface that failure, not route around it.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	// RevisionsTable stores the immutable project recipe revisions.
	RevisionsTable = "recipe_revisions"
	// HeadsTable stores the per-(project, name) CAS head tracking.
	HeadsTable = "recipe_revision_heads"
)

// migrationsFS holds this ticket's recipe-revision contribution to the
// migration history: the per-schema ledger bootstrap and the revision-chain
// tables. Dependent tickets add further <version>_<name>.sql files here
// through the shared pg-store-02 runner.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ChainMigrations returns this package's embedded migration history,
// validated and sorted by version with the pg-store-02 loader.
func ChainMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("recipe postgres: load revision-chain migrations: %w", err)
	}
	return migrations, nil
}

// MigrateSchema applies this package's revision-chain history to one
// recipe-revision schema of the database reachable through src, serialized
// by the pg-store-02 advisory lock. The caller owns schema creation, and this
// history must be the only one ever applied to that schema (see the package
// comment's schema-ownership invariant). Re-applying the applied history is
// a no-op: the DDL is IF NOT EXISTS, so a rerun never truncates chain state.
// Failures are returned, never absorbed: there is no fallback to another
// backend.
func MigrateSchema(ctx context.Context, src pgstore.ConnSource, schema string) (pgstore.MigrateResult, error) {
	if src == nil {
		return pgstore.MigrateResult{}, errors.New("recipe postgres: nil migration connection source")
	}
	migrations, err := ChainMigrations()
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	runner, err := pgstore.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	return runner.Migrate(ctx)
}

// schemaNamePattern matches a safe lowercase PostgreSQL identifier, the same
// shape the pg-store-02 runner accepts, so a registry and its runner can
// never disagree about the target schema.
var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// DB is the statement surface the registry needs. *pgxpool.Pool satisfies
// it; the registry owns no connection beyond what a call acquires.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// RevisionRegistry is the PostgreSQL recipe.RevisionStore. All statements
// use ordered $n parameters and schema-qualified table names; nothing
// user-supplied is interpolated into SQL.
type RevisionRegistry struct {
	db        DB
	revisions string
	heads     string
}

var _ recipe.RevisionStore = (*RevisionRegistry)(nil)

// New binds a registry to one schema of a PostgreSQL 18 database served by
// pool. The schema must already contain the revision-chain tables (this
// package's MigrateSchema); New never runs DDL.
func New(pool DB, schema string) (*RevisionRegistry, error) {
	if pool == nil {
		return nil, errors.New("recipe postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("recipe postgres: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &RevisionRegistry{
		db:        pool,
		revisions: pgx.Identifier{schema, RevisionsTable}.Sanitize(),
		heads:     pgx.Identifier{schema, HeadsTable}.Sanitize(),
	}, nil
}

// Register canonicalizes the Bundle, then extends the (project, name) chain
// under the head-row lock: the requested parent must name the current head,
// and the head move is a guarded compare-and-swap on that same digest, so
// concurrent children of one parent converge to exactly one winner while the
// loser re-reads the moved head and receives the typed STALE_CURSOR user
// fault. First registrations require the empty parent. A head retry of the
// stored content (parent == head == child digest) is idempotent and writes
// nothing. Every store fault stays untyped for 5xx semantics.
func (r *RevisionRegistry) Register(ctx context.Context, project string, bundle recipe.Bundle, parent string) (recipe.RecipeRevision, error) {
	if strings.TrimSpace(project) == "" || strings.TrimSpace(bundle.Name) == "" {
		return recipe.RecipeRevision{}, dataframeInvalidRequest("project and recipe name are required")
	}
	canonical, err := bundle.CanonicalBundle()
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	bundle = canonical
	digest, err := bundle.Digest()
	if err != nil {
		return recipe.RecipeRevision{}, err
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: begin register: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Ensure the chain's head row exists so writers serialize on it, then
	// take the row lock. A concurrent first registration blocks here (its
	// placeholder INSERT waits on the unique index), and every writer after
	// the lock holder re-reads the committed head under a fresh snapshot.
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+r.heads+" (project, name, head_digest, epoch, updated_at)"+
			" VALUES ($1, $2, NULL, 0, $3)"+
			" ON CONFLICT (project, name) DO NOTHING",
		project, bundle.Name, time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: ensure revision head: %w", err)
	}
	var (
		head  *string
		epoch int64
	)
	if err := tx.QueryRow(ctx,
		"SELECT head_digest, epoch FROM "+r.heads+
			" WHERE project = $1 AND name = $2 FOR UPDATE",
		project, bundle.Name).Scan(&head, &epoch); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The placeholder INSERT is in this same transaction, so the
			// row always exists; absence is a store fault, not a no-op.
			return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: revision head row vanished: %w", err)
		}
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: lock revision head: %w", err)
	}

	if head == nil {
		// Placeholder epoch-0 state: no revision of this chain has ever
		// committed, so a first registration is legal and requires no
		// parent. A supplied parent cannot name anything: typed user fault,
		// and the rolled-back transaction leaves the placeholder behind
		// only until this transaction's outcome -- it never becomes data.
		if parent != "" {
			return recipe.RecipeRevision{}, dataframeInvalidRequest(fmt.Sprintf(
				"recipe revision parent %q provided for first revision", parent))
		}
	} else if *head == digest && parent == *head {
		// Idempotent retry of the current head's exact content: return the
		// stored revision untouched, no head bump, no new row.
		revision, err := r.scanRevision(ctx, tx, project, bundle.Name, digest)
		if err != nil {
			return recipe.RecipeRevision{}, err
		}
		return revision, nil
	} else if parent != *head {
		return recipe.RecipeRevision{}, dataframeStaleCursor(fmt.Errorf(
			"recipe revision parent %q is not current (current %q)", parent, *head))
	}

	// CAS move of the head. The guard is re-evaluated against the newest
	// committed row, so even a hypothetical lock slip lands as RowsAffected
	// 0 -- the typed stale-parent conflict -- rather than a lost update.
	expected := any(nil)
	if parent != "" {
		expected = parent
	}
	tag, err := tx.Exec(ctx,
		"UPDATE "+r.heads+" SET head_digest = $3, epoch = epoch + 1, updated_at = $4"+
			" WHERE project = $1 AND name = $2"+
			" AND head_digest IS NOT DISTINCT FROM $5",
		project, bundle.Name, digest, time.Now().UTC().Truncate(time.Microsecond), expected)
	if err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: advance revision head: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return recipe.RecipeRevision{}, dataframeStaleCursor(fmt.Errorf(
			"recipe revision parent %q raced the current head", parent))
	}

	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	revision := recipe.RecipeRevision{
		Project:   project,
		Name:      bundle.Name,
		Digest:    digest,
		Parent:    parent,
		Bundle:    bundle,
		CreatedAt: createdAt,
	}
	payload, err := json.Marshal(revision)
	if err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: marshal revision payload: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+r.revisions+
			" (project, name, digest, parent_digest, payload, created_at)"+
			" VALUES ($1, $2, $3, $4, $5, $6)"+
			" ON CONFLICT (project, name, digest) DO UPDATE SET"+
			" parent_digest = EXCLUDED.parent_digest, payload = EXCLUDED.payload,"+
			" created_at = EXCLUDED.created_at",
		project, bundle.Name, digest, parent, string(payload), createdAt); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: insert revision: %w", err)
	}

	// Re-read the authoritative stored row: appending a digest that already
	// exists deeper in the chain refreshes that row's parent link and stamp
	// to the new edge -- the one-writer-per-(project, name, digest) rule the
	// Arango adapter enforces through same-_key overwrite (the bundle itself
	// cannot change, because the digest pins it). The read confirms the
	// committed shape.
	stored, err := r.scanRevision(ctx, tx, project, bundle.Name, digest)
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: commit register: %w", err)
	}
	return stored, nil
}

// Get reads one revision by exact project, name, and digest. Cross-project
// reads report recipe.ErrRecipeRevisionNotFound; there is no fallback.
func (r *RevisionRegistry) Get(ctx context.Context, project, name, digest string) (recipe.RecipeRevision, error) {
	return r.scanRevision(ctx, r.db, project, name, digest)
}

// List returns every revision of one (project, name) chain in the
// deterministic (created_at DESC, digest) total order. An empty result is a
// non-nil empty slice, matching the Arango adapter.
func (r *RevisionRegistry) List(ctx context.Context, project, name string) ([]recipe.RecipeRevision, error) {
	rows, err := r.db.Query(ctx,
		"SELECT payload FROM "+r.revisions+
			" WHERE project = $1 AND name = $2"+
			" ORDER BY created_at DESC, digest",
		project, name)
	if err != nil {
		return nil, fmt.Errorf("recipe postgres: list revisions: %w", err)
	}
	defer rows.Close()
	out := []recipe.RecipeRevision{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("recipe postgres: scan revision: %w", err)
		}
		revision, err := decodeRevisionPayload(payload)
		if err != nil {
			return nil, err
		}
		out = append(out, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recipe postgres: list revisions: %w", err)
	}
	return out, nil
}

// revisionReader is the read surface of both pgx.Tx and the pool.
type revisionReader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// scanRevision decodes the one payload-bearing row for (project, name,
// digest), reporting recipe.ErrRecipeRevisionNotFound when absent. The
// payload is authoritative, so the canonical Bundle and the microsecond
// created_at round-trip untouched.
func (r *RevisionRegistry) scanRevision(ctx context.Context, reader revisionReader, project, name, digest string) (recipe.RecipeRevision, error) {
	var payload []byte
	err := reader.QueryRow(ctx,
		"SELECT payload FROM "+r.revisions+
			" WHERE project = $1 AND name = $2 AND digest = $3",
		project, name, digest).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return recipe.RecipeRevision{}, recipe.ErrRecipeRevisionNotFound
	}
	if err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: load revision: %w", err)
	}
	return decodeRevisionPayload(payload)
}

// decodeRevisionPayload decodes one stored RecipeRevision payload. A payload
// PostgreSQL kept is one this adapter marshaled from a canonical Bundle, so
// a decode failure is a store-integrity fault and stays untyped.
func decodeRevisionPayload(payload []byte) (recipe.RecipeRevision, error) {
	var revision recipe.RecipeRevision
	if err := json.Unmarshal(payload, &revision); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: decode revision payload: %w", err)
	}
	return revision, nil
}

// dataframeInvalidRequest and dataframeStaleCursor classify the two caller
// faults the Arango adapter distinguishes. The stale-parent conflict is not
// retryable (same STALE_CURSOR defaults as the Arango adapter); store faults
// never pass through these helpers and stay untyped 5xx errors.
func dataframeInvalidRequest(message string) error {
	return dataframeerrors.Wrap(errors.New(message), dataframeerrors.CodeInvalidRequest, "")
}

func dataframeStaleCursor(cause error) error {
	return dataframeerrors.Wrap(cause, dataframeerrors.CodeStaleCursor, "")
}
