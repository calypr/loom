package postgres

import (
	"context"
	"errors"
	"fmt"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNilConnSource              = errors.New("dataset postgres: generation store connection source is required")
	ErrInvalidSchemaName          = storepostgres.ErrInvalidSchemaName
	ErrManifestAlreadyExists      = errors.New("generation manifest already exists")
	ErrManifestNotFound           = publication.ErrManifestNotFound
	ErrManifestTransitionConflict = errors.New("generation manifest transition conflict")
	ErrActivationConflict         = errors.New("generation activation conflict")
	ErrUnexpectedStoreResult      = errors.New("unexpected generation store result")
)

type ConnSource interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

var (
	_ ConnSource = (*storepostgres.Client)(nil)
	_ ConnSource = (*pgxpool.Pool)(nil)
)

type Store struct {
	src         ConnSource
	manifestFQN string
	pointerFQN  string
	commitFault func(step string) error
}

func New(src ConnSource, schema string) (*Store, error) {
	if src == nil {
		return nil, ErrNilConnSource
	}
	if !storepostgres.ValidSchemaName(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	return &Store{
		src:         src,
		manifestFQN: pgx.Identifier{schema, "dataset_generation_manifest"}.Sanitize(),
		pointerFQN:  pgx.Identifier{schema, "dataset_active_generation"}.Sanitize(),
	}, nil
}

func (s *Store) validate() error {
	if s == nil || s.src == nil {
		return ErrNilConnSource
	}
	return nil
}

func execCommand(ctx context.Context, tx pgx.Tx, c Command) (pgconn.CommandTag, error) {
	sql, args, err := c.PostgreSQL()
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	return tx.Exec(ctx, sql, args...)
}

func queryRowCommand(ctx context.Context, tx pgx.Tx, c Command) pgx.Row {
	sql, args, err := c.PostgreSQL()
	if err != nil {
		return errRow{err: err}
	}
	return tx.QueryRow(ctx, sql, args...)
}

func queryRowConn(ctx context.Context, conn *pgxpool.Conn, c Command) pgx.Row {
	sql, args, err := c.PostgreSQL()
	if err != nil {
		return errRow{err: err}
	}
	return conn.QueryRow(ctx, sql, args...)
}

type errRow struct{ err error }

func (r errRow) Scan(dest ...any) error { return r.err }

func validateProject(project string) error {
	_, err := publication.NewRef(project, "generation-project-validation")
	return err
}
