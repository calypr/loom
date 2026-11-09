// Package postgres implements the PostgreSQL-side immutable name/version
// recipe registrations and their fenced execution-occurrence usage markers
// (pg-state-recipe-registries-v1) for the PostgreSQL recipe-execution
// control plane.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package adds no second driver, pool, fixture,
// or migration runner: it reuses internal/store/postgres (pg-store-02 runner)
// and adds only the recipe-registration relational schema and its exec.Store
// behavior. There is no silent backend fallback: every failure is returned
// for the caller to surface, and no Arango path is wired in here. There is
// no cross-database atomicity and no existing-deployment migration.
//
// Guarantees, mirroring internal/dataframe/recipe/exec/arango/registry.go:
//   - One row per (recipeName, translationVersion); the canonical Bundle and
//     digest round-trip through the authoritative jsonb payload while the
//     identity columns are scope equality, and LoadRecipe's name-only "current"
//     read is the highest translationVersion in exact byte order (COLLATE "C",
//     the deterministic order Arango's byte-order SORT gives).
//   - Exact-content retries are idempotent at every entry point: SaveRecipe
//     of the same digest returns success and leaves the row untouched
//     (ON CONFLICT DO NOTHING plus a locked digest comparison), and
//     exec.PersistentRegistry keeps its digest-equality short circuit above
//     this store.
//   - A version cannot change once any execution uses it, and concurrent
//     first use versus replacement is fenced, not checked-then-acted: every
//     ReplaceRecipeVersion takes SELECT ... FOR UPDATE on the recipe row
//     before it probes usage, and every RecordRecipeExecutionOccurrence takes
//     the same row lock before it inserts. One transaction commits first and
//     the other observes the committed state, so the forbidden interleaving
//     (replace after a first durable use, with the use invisible to the
//     replace) cannot occur. A replace blocked behind a committing first use
//     fails with exec.ErrRecipeVersionImmutable.
//   - Usage is occurrence-identified: each durable use is one row keyed by
//     its own (recipeName, translationVersion, occurrenceID), so retrying
//     the recording of one occurrence idempotently reconverges on that row;
//     repeated occurrences from one owner correlate through owner_sequence,
//     assigned under the same row lock so it is strictly increasing per
//     (version, owner).
//   - Divergent concurrent first registrations have one winner: the loser of
//     the INSERT race reports the typed ErrRecipeVersionConflict instead of
//     silently overwriting or silently losing content, and Record on a
//     never-registered version reports exec.ErrRecipeNotFound rather than
//     recording an orphan use.
//   - Two documented sharpenings of the Arango adapter, both inside the
//     store contract: ReplaceRecipeVersion on a never-registered version
//     reports exec.ErrRecipeNotFound (the Arango AQL conflates absent with
//     used by returning ErrRecipeVersionImmutable), and usage is probed with
//     the exact scope equality the domain uses rather than the Arango query's
//     case-variant Name/TranslationVersion matching, which only exists to
//     tolerate legacy document casing.
//
// Deployment wiring: the PostgreSQL execution flow records the durable start
// of each bundle execution through RecordRecipeExecutionOccurrence on this
// registry (same schema, no cross-database atomicity), so "used" always
// means "a fenced occurrence row exists". The publication bundle catalog
// (internal/dataframe/publication/postgres, a separate ticket's history) is
// deliberately not read here: coupling a version's immutability gate to
// another ticket's tables would reintroduce the cross-history dependency this
// program's contract forbids.
//
// The schema is created by this package's embedded migrations
// (migrations/000001_ledger.sql and migrations/000002_recipe_exec_registries.
// sql) applied through the pg-store-02 MigrationRunner; the package contains
// no DDL outside those files. Exactly one embedded history may ever migrate a
// given schema; see the schema-ownership note in
// internal/dataframe/recipe/postgres (same ticket, sibling history,
// byte-identical version-1 ledger, version-ordered composition {1, 2, 3}).
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

	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

const (
	// RecipesTable stores one row per (recipe_name, translation_version).
	RecipesTable = "recipe_exec_recipes"
	// OccurrencesTable stores the fenced execution-occurrence usage markers.
	OccurrencesTable = "recipe_exec_occurrences"
)

// migrationsFS holds this ticket's exec-registry contribution to the
// migration history: the per-schema ledger bootstrap and the registration
// and occurrence tables. Dependent tickets add further <version>_<name>.sql
// files here through the shared pg-store-02 runner.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// RegistryMigrations returns this package's embedded migration history,
// validated and sorted by version with the pg-store-02 loader.
func RegistryMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("recipe exec postgres: load registry migrations: %w", err)
	}
	return migrations, nil
}

// MigrateSchema applies this package's exec-registry history to one schema
// of the database reachable through src, serialized by the pg-store-02
// advisory lock. The caller owns schema creation, and this history must be
// the only one ever applied to that schema (see the package comment's
// schema-ownership invariant). Re-applying the applied history is a no-op:
// the DDL is IF NOT EXISTS, so a rerun never truncates registrations.
// Failures are returned, never absorbed: there is no fallback to another
// backend.
func MigrateSchema(ctx context.Context, src pgstore.ConnSource, schema string) (pgstore.MigrateResult, error) {
	if src == nil {
		return pgstore.MigrateResult{}, errors.New("recipe exec postgres: nil migration connection source")
	}
	migrations, err := RegistryMigrations()
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

// ErrRecipeVersionConflict reports that a divergent registration won the
// concurrent first-registration race for this (name, translationVersion).
// The row holds the winner's canonical digest; the caller's content was not
// stored. exec.PersistentRegistry has no sentinel for this race; a retried
// RegisterVersion reloads the winner and routes through the replace-or-
// immutable path.
var ErrRecipeVersionConflict = errors.New("recipe version already registered with a different digest")

// DB is the statement surface the registry needs. *pgxpool.Pool satisfies
// it; the registry owns no connection beyond what a call acquires.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Registry is the PostgreSQL exec.Store. All statements use ordered $n
// parameters and schema-qualified table names; nothing user-supplied is
// interpolated into SQL.
type Registry struct {
	db          DB
	recipes     string
	occurrences string
}

var _ exec.Store = (*Registry)(nil)

// New binds a registry to one schema of a PostgreSQL 18 database served by
// pool. The schema must already contain the recipe-registration tables
// (this package's MigrateSchema); New never runs DDL.
func New(pool DB, schema string) (*Registry, error) {
	if pool == nil {
		return nil, errors.New("recipe exec postgres: nil pool")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("recipe exec postgres: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &Registry{
		db:          pool,
		recipes:     pgx.Identifier{schema, RecipesTable}.Sanitize(),
		occurrences: pgx.Identifier{schema, OccurrencesTable}.Sanitize(),
	}, nil
}

// entryParams are the ordered $1..$6 bind values for a full registration
// row: the identity scope, the digest, the marshaled exec.Entry payload, and
// the two timestamps. The payload is authoritative on read, so the canonical
// Bundle round-trips byte-for-byte through the write.
func entryParams(entry exec.Entry, now time.Time) ([]any, error) {
	payload, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("recipe exec postgres: marshal entry payload: %w", err)
	}
	return []any{entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest, string(payload), now, now}, nil
}

// SaveRecipe inserts one registration. The retry of identical content is
// idempotent (conflict with the stored digest is success), and a divergent
// concurrent first registration loses the INSERT race and reports
// ErrRecipeVersionConflict with the winner's row untouched.
func (r *Registry) SaveRecipe(ctx context.Context, entry exec.Entry) error {
	now := time.Now().UTC().Truncate(time.Microsecond)
	params, err := entryParams(entry, now)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx,
		"INSERT INTO "+r.recipes+
			" (recipe_name, translation_version, digest, payload, created_at, updated_at)"+
			" VALUES ($1, $2, $3, $4, $5, $6)"+
			" ON CONFLICT (recipe_name, translation_version) DO NOTHING",
		params...)
	if err != nil {
		return fmt.Errorf("recipe exec postgres: save recipe: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	// The row already exists. Read the winner's digest under its lock:
	// identical content is the idempotent success the contract demands;
	// divergent content is the concurrent-registration loser signal, never a
	// silent overwrite.
	var stored string
	err = r.db.QueryRow(ctx,
		"SELECT digest FROM "+r.recipes+
			" WHERE recipe_name = $1 AND translation_version = $2 FOR UPDATE",
		entry.Bundle.Name, entry.Bundle.TranslationVersion).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s@%s vanished during save", exec.ErrRecipeNotFound,
			entry.Bundle.Name, entry.Bundle.TranslationVersion)
	}
	if err != nil {
		return fmt.Errorf("recipe exec postgres: inspect saved recipe: %w", err)
	}
	if stored == entry.Digest {
		return nil
	}
	return fmt.Errorf("%w: %s@%s", ErrRecipeVersionConflict, entry.Bundle.Name, entry.Bundle.TranslationVersion)
}

// ReplaceRecipeVersion swaps the stored canonical entry only while no
// execution occurrence exists, fenced through the recipe row lock: the
// usage probe runs inside the same transaction that holds FOR UPDATE on the
// row, and RecordRecipeExecutionOccurrence takes the same lock before it
// inserts, so a first durable use committing mid-replace forces this call to
// fail with exec.ErrRecipeVersionImmutable. A replace of a never-registered
// version reports exec.ErrRecipeNotFound (see the package comment for the
// documented divergence from the Arango adapter).
func (r *Registry) ReplaceRecipeVersion(ctx context.Context, entry exec.Entry) error {
	now := time.Now().UTC().Truncate(time.Microsecond)
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("recipe exec postgres: marshal entry payload: %w", err)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("recipe exec postgres: begin replace: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var held bool
	if err := tx.QueryRow(ctx,
		"SELECT true FROM "+r.recipes+
			" WHERE recipe_name = $1 AND translation_version = $2 FOR UPDATE",
		entry.Bundle.Name, entry.Bundle.TranslationVersion).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s@%s", exec.ErrRecipeNotFound,
				entry.Bundle.Name, entry.Bundle.TranslationVersion)
		}
		return fmt.Errorf("recipe exec postgres: lock recipe row: %w", err)
	}

	var used bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+r.occurrences+
			" WHERE recipe_name = $1 AND translation_version = $2)",
		entry.Bundle.Name, entry.Bundle.TranslationVersion).Scan(&used); err != nil {
		return fmt.Errorf("recipe exec postgres: probe usage: %w", err)
	}
	if used {
		return fmt.Errorf("%w: %s@%s", exec.ErrRecipeVersionImmutable,
			entry.Bundle.Name, entry.Bundle.TranslationVersion)
	}

	// updated_at moves; created_at survives the replacement.
	set := "digest = $3, payload = $4, updated_at = $5"
	if _, err := tx.Exec(ctx,
		"UPDATE "+r.recipes+" SET "+set+
			" WHERE recipe_name = $1 AND translation_version = $2",
		entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest,
		string(payload), now); err != nil {
		return fmt.Errorf("recipe exec postgres: replace recipe: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("recipe exec postgres: commit replace: %w", err)
	}
	return nil
}

// LoadRecipe returns the current registration for one name: the highest
// translationVersion in exact byte order. The primary key makes that answer
// unique, so no additional tie-break is needed.
func (r *Registry) LoadRecipe(ctx context.Context, name string) (exec.Entry, error) {
	return r.loadEntry(ctx,
		"SELECT payload FROM "+r.recipes+
			" WHERE recipe_name = $1"+
			" ORDER BY translation_version DESC LIMIT 1",
		name)
}

// LoadRecipeVersion returns the exact registration for one
// (name, translationVersion).
func (r *Registry) LoadRecipeVersion(ctx context.Context, name, translationVersion string) (exec.Entry, error) {
	return r.loadEntry(ctx,
		"SELECT payload FROM "+r.recipes+
			" WHERE recipe_name = $1 AND translation_version = $2",
		name, translationVersion)
}

func (r *Registry) loadEntry(ctx context.Context, query string, args ...any) (exec.Entry, error) {
	var payload []byte
	err := r.db.QueryRow(ctx, query, args...).Scan(&payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return exec.Entry{}, fmt.Errorf("%w", exec.ErrRecipeNotFound)
	}
	if err != nil {
		return exec.Entry{}, fmt.Errorf("recipe exec postgres: load recipe: %w", err)
	}
	var entry exec.Entry
	if err := json.Unmarshal(payload, &entry); err != nil {
		return exec.Entry{}, fmt.Errorf("recipe exec postgres: decode entry payload: %w", err)
	}
	return entry, nil
}

// RecipeVersionUsed reports whether any execution occurrence exists for the
// exact (name, translationVersion).
func (r *Registry) RecipeVersionUsed(ctx context.Context, name, translationVersion string) (bool, error) {
	var used bool
	if err := r.db.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+r.occurrences+
			" WHERE recipe_name = $1 AND translation_version = $2)",
		name, translationVersion).Scan(&used); err != nil {
		return false, fmt.Errorf("recipe exec postgres: probe usage: %w", err)
	}
	return used, nil
}

// RecordRecipeExecutionOccurrence marks one durable execution of a recipe
// version. The occurrence is identified by (name, translationVersion,
// occurrenceID): re-recording the same occurrence is an idempotent no-op, so
// a worker that retries after a crash cannot manufacture phantom uses. The
// occurrence row is inserted inside a transaction that first takes the
// recipe row FOR UPDATE -- the same lock ReplaceRecipeVersion holds across
// its usage probe -- so first use and replacement are serialized and one of
// them provably commits first. Recording a use for a never-registered
// version reports exec.ErrRecipeNotFound; an orphan use is always a wiring
// fault, never a silent state.
//
// ownerSequence is assigned under the recipe-row lock as the next ordinal
// for (version, owner), correlating repeated occurrences from one owner:
// a witness can order a repeated owner's occurrences even when recorded_at
// values tie.
func (r *Registry) RecordRecipeExecutionOccurrence(ctx context.Context, name, translationVersion, occurrenceID, ownerID string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(translationVersion) == "" || strings.TrimSpace(occurrenceID) == "" {
		return errors.New("recipe exec postgres: recipe name, translation version, and occurrence id are required")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("recipe exec postgres: begin record occurrence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var held bool
	if err := tx.QueryRow(ctx,
		"SELECT true FROM "+r.recipes+
			" WHERE recipe_name = $1 AND translation_version = $2 FOR UPDATE",
		name, translationVersion).Scan(&held); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s@%s", exec.ErrRecipeNotFound, name, translationVersion)
		}
		return fmt.Errorf("recipe exec postgres: lock recipe row for occurrence: %w", err)
	}

	var next int64
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(MAX(owner_sequence), 0) + 1 FROM "+r.occurrences+
			" WHERE recipe_name = $1 AND translation_version = $2 AND owner_id = $3",
		name, translationVersion, ownerID).Scan(&next); err != nil {
		return fmt.Errorf("recipe exec postgres: next owner sequence: %w", err)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO "+r.occurrences+
			" (recipe_name, translation_version, occurrence_id, owner_id, owner_sequence, recorded_at)"+
			" VALUES ($1, $2, $3, $4, $5, $6)"+
			" ON CONFLICT (recipe_name, translation_version, occurrence_id) DO NOTHING",
		name, translationVersion, occurrenceID, ownerID, next,
		time.Now().UTC().Truncate(time.Microsecond)); err != nil {
		return fmt.Errorf("recipe exec postgres: record occurrence: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("recipe exec postgres: commit occurrence: %w", err)
	}
	return nil
}
