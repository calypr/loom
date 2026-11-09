// Package postgres implements the dataset generation lifecycle store over
// PostgreSQL 18: generation manifests, manifest state transitions, and the
// active-generation compare-and-swap pointer.
//
// PostgreSQL 18 is an alternative to Arango for the source graph/catalog and
// control persistence; Arango remains supported and ClickHouse remains the
// publication data plane. This package never falls back to another backend:
// every failure is returned as a named error for the caller to surface, and
// no method ever consults Arango or ClickHouse.
//
// Ownership follows the architecture contract: this store owns only dataset
// generation manifests and the active-generation pointer. Project
// DatasetRelease/active-project-release and Explorer/ClickHouse publication
// pointers belong to other owners.
//
// Guarantees:
//   - Atomic compare-and-swap: manifest transitions compare state plus
//     schema identity in the UPDATE predicate, and activation compares the
//     active-generation pointer's current value inside one transaction.
//     There is no check-then-act window between reading the pointer and
//     swapping it.
//   - No cross-database atomicity: the manifest, pointer, and any other
//     backend's state are committed independently.
//   - Backend-tagged commands with ordered positional SQL parameters; AQL
//     or an untagged command fails closed before execution.
//   - Missing data is a missing row, never SQL NULL; a JSON null inside the
//     schema identity document stays a JSON null. Public manifest state
//     names (LOADING, STAGED, READY, FAILED) and schema identity document
//     bytes are persisted unchanged.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	publication "github.com/calypr/loom/internal/dataset"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors for classifying store failures without string matching.
var (
	// ErrNilConnSource reports construction without a connection source.
	ErrNilConnSource = errors.New("dataset postgres: generation store connection source is required")
	// ErrInvalidSchemaName reports a target schema that is not a safe
	// lowercase identifier.
	ErrInvalidSchemaName = errors.New("dataset postgres: invalid schema name")
	// ErrManifestAlreadyExists reports a second CreateManifest for the same
	// (project, generation).
	ErrManifestAlreadyExists = errors.New("generation manifest already exists")
	// ErrManifestNotFound is the public domain sentinel for a missing
	// manifest, aliased so callers classify it without importing this
	// package's vocabulary.
	ErrManifestNotFound = publication.ErrManifestNotFound
	// ErrManifestTransitionConflict reports a transition whose stored row
	// no longer carries the expected state and immutable schema identity.
	ErrManifestTransitionConflict = errors.New("generation manifest transition conflict")
	// ErrActivationConflict reports an activation whose candidate is not a
	// persisted staged manifest matching the request, or whose
	// compare-and-swap against the active-generation pointer failed because
	// the pointer does not hold the expected value.
	ErrActivationConflict = errors.New("generation activation conflict")
	// ErrUnexpectedStoreResult reports rows that disagree with the request
	// or with the store's own invariants.
	ErrUnexpectedStoreResult = errors.New("unexpected generation store result")
)

// uniqueViolationSQLState is PostgreSQL's 23505: the manifest primary key
// (or pointer primary key) already holds the row an INSERT was attempting.
const uniqueViolationSQLState = "23505"

var schemaNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ConnSource supplies pooled connections. *store/postgres.Client (the
// pg-store-01 readiness-checked pool) and *pgxpool.Pool satisfy it. The
// caller owns the schema lifecycle; this store requires the schema and its
// migrations to exist and never creates, drops, or migrates anything.
type ConnSource interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

var (
	_ ConnSource = (*storepostgres.Client)(nil)
	_ ConnSource = (*pgxpool.Pool)(nil)
)

// Store is one schema-bound view of the dataset generation lifecycle tables.
// Create one with New; it is safe for concurrent use.
type Store struct {
	src         ConnSource
	manifestFQN string
	pointerFQN  string
	// commitFault, when non-nil, is invoked immediately before COMMIT in
	// every transactional write path; returning an error from it aborts
	// the transaction. It is a test seam (set directly by in-package
	// tests, mirroring the pg-store-02 runner's panicHook) for exercising
	// failure-before-commit; production construction always leaves it
	// nil.
	commitFault func(step string) error
}

// New validates the connection source and target schema and binds the store
// to <schema>.dataset_generation_manifest and <schema>.dataset_active_generation.
func New(src ConnSource, schema string) (*Store, error) {
	if src == nil {
		return nil, ErrNilConnSource
	}
	if !schemaNamePattern.MatchString(schema) {
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

// execCommand dispatches one backend-tagged command through its PostgreSQL
// accessor and executes it. A mismatched or untagged command fails closed
// before pgx sees a statement.
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

// queryRowConn dispatches a tagged command through its PostgreSQL accessor to
// a connection-level QueryRow. A mismatched or untagged command fails closed
// before pgx sees a statement.
func queryRowConn(ctx context.Context, conn *pgxpool.Conn, c Command) pgx.Row {
	sql, args, err := c.PostgreSQL()
	if err != nil {
		return errRow{err: err}
	}
	return conn.QueryRow(ctx, sql, args...)
}

// errRow lets a dispatch failure surface through the normal Scan error path.
type errRow struct{ err error }

func (r errRow) Scan(dest ...any) error { return r.err }

// isUniqueViolation reports a 23505 unique_violation from the server.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationSQLState
}

// validateProject rejects project names the domain refuses to address.
func validateProject(project string) error {
	_, err := publication.NewRef(project, "generation-project-validation")
	return err
}
