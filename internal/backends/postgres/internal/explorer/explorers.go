package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const OwnerTable = "explorers"

//go:embed migrations/*.sql
var migrationsFS embed.FS

func OwnerMigrations() ([]pgstore.Migration, error) {
	migrations, err := pgstore.LoadMigrations(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: load owner migrations: %w", err)
	}
	return migrations, nil
}

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

func VerifySchema(ctx context.Context, client *pgstore.Client, schema string) error {
	if client == nil {
		return errors.New("postgres explorer store: nil client")
	}
	migrations, err := OwnerMigrations()
	if err != nil {
		return err
	}
	runner, err := pgstore.NewMigrationRunner(client, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}

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

type Store struct {
	client               *pgstore.Client
	insert               string
	get                  string
	list                 string
	receipt              receiptStatements
	revision             revisionStatements
	drafts               draftSQL
	selections           selectionSQL
	selectionMembers     selectionMemberSQL
	afterHistoryInsert   func(context.Context, pgx.Tx) error
	afterMemberInsert    func(context.Context, pgx.Tx) error
	beforeCleanupCommit  func(context.Context, pgx.Tx) error
	beforeCompleteCommit func(context.Context, pgx.Tx) error
	memberPageStreamed   func(rows int)
}

func New(client *pgstore.Client, schema string) (*Store, error) {
	if client == nil {
		return nil, errors.New("postgres explorer store: client is required")
	}
	if !pgstore.ValidSchemaName(schema) {
		return nil, fmt.Errorf("postgres explorer store: invalid schema %q: must match [a-z_][a-z0-9_]{0,62}", schema)
	}
	table := sanitizeTableRef(schema, OwnerTable)
	cols := strings.Join(ownerColumns, ", ")
	return &Store{
		client:           client,
		drafts:           newDraftSQL(schema),
		selections:       newSelectionSQL(schema),
		selectionMembers: newSelectionMemberSQL(schema),
		insert: "INSERT INTO " + table + " (" + cols + ") VALUES (" + ownerPlaceholders() + ") " +
			"ON CONFLICT (project, explorer_id) DO NOTHING RETURNING " + cols,
		get:      "SELECT " + cols + " FROM " + table + " WHERE project = $1 AND explorer_id = $2",
		list:     "SELECT " + cols + " FROM " + table + " WHERE project = $1 ORDER BY explorer_id",
		receipt:  newReceiptStatements(schema),
		revision: newRevisionStatements(schema),
	}, nil
}

func sanitizeTableRef(schema, table string) string {
	return pgx.Identifier{schema, table}.Sanitize()
}

func orderedPlaceholders(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(parts, ", ")
}

func ownerPlaceholders() string {
	return orderedPlaceholders(len(ownerColumns))
}

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

func jsonParam(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

type rowScanner interface {
	Scan(dest ...any) error
}

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
			return nil, explorer.ErrDraftConflict
		}
		return nil, fmt.Errorf("postgres explorer store: create explorer owner %q/%q: %w",
			e.Project, e.ExplorerID, err)
	}
	return &out, nil
}

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
