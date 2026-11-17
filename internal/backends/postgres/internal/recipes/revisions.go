package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const (
	RevisionsTable = "recipe_revisions"
	HeadsTable     = "recipe_revision_heads"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func ChainMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("recipe postgres: load revision-chain migrations: %w", err)
	}
	return migrations, nil
}

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

func VerifySchema(ctx context.Context, src pgstore.ConnSource, schema string) error {
	if src == nil {
		return errors.New("recipe postgres: nil migration connection source")
	}
	migrations, err := ChainMigrations()
	if err != nil {
		return err
	}
	runner, err := pgstore.NewMigrationRunner(src, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

type RevisionRegistry struct {
	db        DB
	revisions string
	heads     string
}

var _ recipe.RevisionStore = (*RevisionRegistry)(nil)

func New(pool DB, schema string) (*RevisionRegistry, error) {
	if pool == nil {
		return nil, errors.New("recipe postgres: nil pool")
	}
	if !pgstore.ValidSchemaName(schema) {
		return nil, fmt.Errorf("recipe postgres: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	return &RevisionRegistry{
		db:        pool,
		revisions: pgx.Identifier{schema, RevisionsTable}.Sanitize(),
		heads:     pgx.Identifier{schema, HeadsTable}.Sanitize(),
	}, nil
}

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
			return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: revision head row vanished: %w", err)
		}
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: lock revision head: %w", err)
	}

	if head == nil {
		if parent != "" {
			return recipe.RecipeRevision{}, dataframeInvalidRequest(fmt.Sprintf(
				"recipe revision parent %q provided for first revision", parent))
		}
	} else if *head == digest && parent == *head {
		revision, err := r.scanRevision(ctx, tx, project, bundle.Name, digest)
		if err != nil {
			return recipe.RecipeRevision{}, err
		}
		return revision, nil
	} else if parent != *head {
		return recipe.RecipeRevision{}, dataframeStaleCursor(fmt.Errorf(
			"recipe revision parent %q is not current (current %q)", parent, *head))
	}

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

	stored, err := r.scanRevision(ctx, tx, project, bundle.Name, digest)
	if err != nil {
		return recipe.RecipeRevision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: commit register: %w", err)
	}
	return stored, nil
}

func (r *RevisionRegistry) Get(ctx context.Context, project, name, digest string) (recipe.RecipeRevision, error) {
	return r.scanRevision(ctx, r.db, project, name, digest)
}

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

type revisionReader interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

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

func decodeRevisionPayload(payload []byte) (recipe.RecipeRevision, error) {
	var revision recipe.RecipeRevision
	if err := json.Unmarshal(payload, &revision); err != nil {
		return recipe.RecipeRevision{}, fmt.Errorf("recipe postgres: decode revision payload: %w", err)
	}
	return revision, nil
}

func dataframeInvalidRequest(message string) error {
	return dataframeerrors.Wrap(errors.New(message), dataframeerrors.CodeInvalidRequest, "")
}

func dataframeStaleCursor(cause error) error {
	return dataframeerrors.Wrap(cause, dataframeerrors.CodeStaleCursor, "")
}
