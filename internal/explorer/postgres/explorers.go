// Package postgres implements the PostgreSQL 18 Explorer control-plane
// stores on top of the shared pg-store pool and migration runner.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package adds no second driver, pool, fixture,
// or migration runner: it reuses internal/store/postgres (pg-store-01 pool,
// pg-store-02 runner) and adds only the Explorer-owner relational schema and
// its Create/Get/List behavior. There is no silent backend fallback: every
// failure is returned for the caller to surface, and no Arango path is wired
// in here.
//
// This slice implements the owner record and the draft CAS with exact
// preimage history (drafts.go). The remaining explorer.Store methods
// (receipts, revisions, selections, ...) arrive with the state tickets that
// depend on this foundation, adding their own migrations under migrations/
// and methods beside these.
//
// Schema ownership (pinned by TestPostgresExplorerOwnerMixedHistoryIsRefused):
// every MigrationRunner keys its ledger -- and therefore its authoritative
// applied history -- by schema, so exactly one embedded history may ever
// migrate a given schema. The pg-store-02 history
// (internal/store/postgres.MigrateSchema) owns source-store schemas; this
// package's history owns Explorer control-plane schemas, and dependent state
// tickets extend this same history rather than starting a competing one.
// Pointing two histories at one schema is a deployment misconfiguration: the
// runner fails closed with a named error (for these two histories,
// pgstore.ErrMigrationChecksumMismatch on version 1, whose bodies differ
// deliberately: IF NOT EXISTS for this feature history versus the plain
// CREATE TABLE of the source-store history) before applying any statement,
// so a mixed schema never converges silently or loses state. Callers must
// surface that failure, not route around it.
package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/calypr/loom/internal/explorer"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// OwnerTable is the table holding Explorer owner records, created by
// migration 000002_explorer_owner.sql inside the store's schema.
const OwnerTable = "explorers"

// migrationsFS holds this package's contribution to the migration history:
// the per-schema ledger bootstrap and the Explorer-owner table. Feature
// tickets add further <version>_<name>.sql files here through the shared
// pg-store-02 runner.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// OwnerMigrations returns this package's embedded migration history,
// validated and sorted by version with the pg-store-02 loader.
func OwnerMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: load owner migrations: %w", err)
	}
	return migrations, nil
}

// MigrateSchema applies this package's owner history to one Explorer
// control-plane schema of the database reachable through client, serialized
// by the pg-store-02 advisory lock. The caller owns schema creation, and this
// history must be the only one ever applied to that schema: a schema already
// migrated by another history (e.g. internal/store/postgres.MigrateSchema)
// fails closed with pgstore.ErrMigrationChecksumMismatch before any statement
// runs (see the package comment; pinned by
// TestPostgresExplorerOwnerMixedHistoryIsRefused). Re-applying the applied
// history is a no-op: the DDL is IF NOT EXISTS, so a rerun never truncates
// existing owner state. Failures are returned, never absorbed: there is no
// fallback to another backend.
func MigrateSchema(ctx context.Context, client *pgstore.Client, schema string) (pgstore.MigrateResult, error) {
	if client == nil {
		return pgstore.MigrateResult{}, errors.New("postgres explorer store: nil client")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	runner, err := pgstore.NewMigrationRunner(client, schema, migrations)
	if err != nil {
		return pgstore.MigrateResult{}, err
	}
	return runner.Migrate(ctx)
}

// schemaNamePattern matches a safe lowercase PostgreSQL identifier, the same
// shape the pg-store-02 runner accepts, so a Store and its runner can never
// disagree about the target schema.
var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ownerColumns is the single source of truth for column order: the INSERT
// column list, the RETURNING/SELECT column list, the Create argument order,
// and the scan order all derive from it. Ordered SQL parameters ($1..$N)
// therefore match the Go argument slice by construction.
var ownerColumns = []string{
	"project",
	"explorer_id",
	"title",
	"management_mode",
	"draft_config",
	"draft_version",
	"draft_digest",
	"draft_snapshot_token",
	"draft_source_generation",
	"draft_authorization_scope_digest",
	"previous_draft_revision_id",
	"last_authoring_command_id",
	"last_authoring_command_digest",
	"last_authoring_command_results",
	"active_revision_id",
	"updated_by",
	"updated_at",
}

// Store persists Explorer owner records in one schema of a PostgreSQL 18
// database reachable through the shared pg-store-01 pool. Create, Get, and
// List are the methods implemented by this foundation slice; they mirror the
// Arango adapter's owner semantics (insert-once, scoped reads, byte-order
// listing) with relational typed columns and lossless JSONB payloads.
type Store struct {
	client *pgstore.Client
	insert string // backend-tagged INSERT ... ON CONFLICT DO NOTHING RETURNING
	get    string // backend-tagged SELECT ... WHERE project = $1 AND explorer_id = $2
	list   string // backend-tagged SELECT ... WHERE project = $1 ORDER BY explorer_id
	// drafts holds the draft CAS/preimage statement set (drafts.go),
	// built once for the same schema the owner statements target.
	drafts draftSQL
	// afterHistoryInsert is the test-only fault/interleaving seam of
	// SaveDraft (see drafts.go). Production never assigns it.
	afterHistoryInsert func(context.Context, pgx.Tx) error
}

// New builds a Store for one Explorer control-plane schema of the database
// behind client -- a schema created by the caller and migrated exclusively
// by this package's MigrateSchema (see the package comment's schema-ownership
// invariant). It opens no connection: the pool stays shared with other
// control-plane users. The schema name must be a safe lowercase identifier
// (the pg-store-02 runner accepts exactly the same shape); every statement
// the Store runs binds the pre-qualified, sanitized table reference and uses
// ordered SQL parameters.
func New(client *pgstore.Client, schema string) (*Store, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: client is required")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	table := pgx.Identifier{schema, OwnerTable}.Sanitize()
	cols := strings.Join(ownerColumns, ", ")
	return &Store{
		client: client,
		drafts: newDraftSQL(schema),
		insert: "INSERT INTO " + table + " (" + cols + ") VALUES (" + ownerPlaceholders() + ") " +
			"ON CONFLICT (project, explorer_id) DO NOTHING RETURNING " + cols,
		get:  "SELECT " + cols + " FROM " + table + " WHERE project = $1 AND explorer_id = $2",
		list: "SELECT " + cols + " FROM " + table + " WHERE project = $1 ORDER BY explorer_id",
	}, nil
}

// ownerPlaceholders renders $1..$N for len(ownerColumns) ordered parameters.
func ownerPlaceholders() string {
	parts := make([]string, len(ownerColumns))
	for i := range ownerColumns {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

// ownerArgs maps an Explorer to its column values in ownerColumns order.
// json.RawMessage payloads cross the boundary explicitly: nil becomes SQL
// NULL, while the literal bytes (including JSON null) go to the jsonb
// parameter unchanged, preserving missing versus JSON-null.
func ownerArgs(e explorer.Explorer) []any {
	return []any{
		e.Project,
		e.ExplorerID,
		e.Title,
		string(e.ManagementMode),
		jsonParam(e.DraftConfig),
		e.DraftVersion,
		e.DraftDigest,
		e.DraftSnapshotToken,
		e.DraftSourceGeneration,
		e.DraftAuthorizationScopeDigest,
		e.PreviousDraftRevisionID,
		e.LastAuthoringCommandID,
		e.LastAuthoringCommandDigest,
		jsonParam(e.LastAuthoringCommandResults),
		e.ActiveRevisionID,
		e.UpdatedBy,
		e.UpdatedAt,
	}
}

// jsonParam encodes a json.RawMessage for a jsonb parameter: nil and the
// empty payload are "missing" (SQL NULL, matching encoding/json omitempty);
// anything else, including the four bytes "null", is passed verbatim so
// jsonb stores it as a JSON value distinct from SQL NULL.
func jsonParam(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// rowScanner is the Scan surface of both pgx.Row (QueryRow) and the current
// row of pgx.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanOwner decodes one row into an Explorer. Targets follow ownerColumns
// order. The jsonb columns scan into json.RawMessage: SQL NULL yields a nil
// (missing) payload and the jsonb value 'null' yields RawMessage("null").
// management_mode decodes into the typed ManagementMode.
func scanOwner(row rowScanner, e *explorer.Explorer) error {
	var managementMode string
	targets := []any{
		&e.Project,
		&e.ExplorerID,
		&e.Title,
		&managementMode,
		&e.DraftConfig,
		&e.DraftVersion,
		&e.DraftDigest,
		&e.DraftSnapshotToken,
		&e.DraftSourceGeneration,
		&e.DraftAuthorizationScopeDigest,
		&e.PreviousDraftRevisionID,
		&e.LastAuthoringCommandID,
		&e.LastAuthoringCommandDigest,
		&e.LastAuthoringCommandResults,
		&e.ActiveRevisionID,
		&e.UpdatedBy,
		&e.UpdatedAt,
	}
	if err := row.Scan(targets...); err != nil {
		return err
	}
	e.ManagementMode = explorer.ManagementMode(managementMode)
	return nil
}

// Create inserts the Explorer owner exactly once. A second Create for an
// existing (project, explorer_id) identity inserts nothing and maps to
// explorer.ErrDraftConflict (the same duplicate-owner classification the
// Arango adapter returns); the stored row is never overwritten. The RETURNING
// row -- what PostgreSQL actually persisted -- is decoded and returned.
func (s *Store) Create(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire create connection: %w", err)
	}
	defer conn.Release()

	var out explorer.Explorer
	err = scanOwner(conn.QueryRow(ctx, s.insert, ownerArgs(e)...), &out)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT DO NOTHING returned no row: the owner identity
			// already exists. Insert-once semantics, never an overwrite.
			return nil, explorer.ErrDraftConflict
		}
		return nil, fmt.Errorf("postgres explorer store: create explorer owner %q/%q: %w",
			e.Project, e.ExplorerID, err)
	}
	return &out, nil
}

// Get returns the owner record for one Explorer, scoped to its project: a
// missing Explorer and an Explorer that exists in another project both
// return explorer.ErrNotFound with no information leak.
func (s *Store) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire get connection: %w", err)
	}
	defer conn.Release()

	var out explorer.Explorer
	err = scanOwner(conn.QueryRow(ctx, s.get, project, id), &out)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, explorer.ErrNotFound
		}
		return nil, fmt.Errorf("postgres explorer store: get explorer owner %q/%q: %w", project, id, err)
	}
	return &out, nil
}

// List returns every owner record in one project, sorted by ExplorerID in
// exact byte order (the identity columns are COLLATE "C", matching Arango's
// byte-order listing). The result is never nil: an empty project lists as an
// empty slice.
func (s *Store) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire list connection: %w", err)
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, s.list, project)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explorer owners %q: %w", project, err)
	}
	defer rows.Close()

	out := []explorer.Explorer{}
	for rows.Next() {
		var e explorer.Explorer
		if err := scanOwner(rows, &e); err != nil {
			return nil, fmt.Errorf("postgres explorer store: scan explorer owner %q: %w", project, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres explorer store: list explorer owners %q: %w", project, err)
	}
	return out, nil
}
