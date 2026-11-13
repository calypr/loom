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
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
)

const (
	RecipesTable     = "recipe_exec_recipes"
	OccurrencesTable = "recipe_exec_occurrences"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func RegistryMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("recipe exec postgres: load registry migrations: %w", err)
	}
	return migrations, nil
}

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

func VerifySchema(ctx context.Context, src pgstore.ConnSource, schema string) error {
	if src == nil {
		return errors.New("recipe exec postgres: nil migration connection source")
	}
	migrations, err := RegistryMigrations()
	if err != nil {
		return err
	}
	runner, err := pgstore.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}

var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

var ErrRecipeVersionConflict = errors.New("recipe version already registered with a different digest")

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Registry struct {
	db          DB
	recipes     string
	occurrences string
}

var _ exec.Store = (*Registry)(nil)

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

func entryParams(entry exec.Entry, now time.Time) ([]any, error) {
	payload, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("recipe exec postgres: marshal entry payload: %w", err)
	}
	return []any{entry.Bundle.Name, entry.Bundle.TranslationVersion, entry.Digest, string(payload), now, now}, nil
}

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

func (r *Registry) LoadRecipe(ctx context.Context, name string) (exec.Entry, error) {
	return r.loadEntry(ctx,
		"SELECT payload FROM "+r.recipes+
			" WHERE recipe_name = $1"+
			" ORDER BY translation_version DESC LIMIT 1",
		name)
}

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
		return exec.Entry{}, exec.ErrRecipeNotFound
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
