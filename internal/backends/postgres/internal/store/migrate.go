// Migration runner for the PostgreSQL 18 source store (pg-store-02).
//
// The runner owns the migration protocol, not the schema content: ordered
// .sql files under migrations/ form the authoritative history, and feature
// tickets (resource, edge, generation, ...) add their own migrations through
// this runner. It deliberately contains no resource/edge/generation table
// definitions.
//
// Guarantees:
//   - Serialized application: every Migrate call runs under a session-level
//     PostgreSQL advisory lock keyed by the target schema, so simultaneous
//     callers apply one migration at a time and each migration is recorded
//     exactly once.
//   - Transactional application: each pending migration runs inside its own
//     transaction together with the target search_path. A failure rolls the
//     migration's DDL back and leaves the prior ledger intact.
//   - Ledger after commit: the version/name/checksum row is inserted only
//     after the migration transaction commits. A crash in the narrow window
//     between commit and record leaves the DDL applied but unrecorded; the
//     next run retries that migration and must converge (make retryable DDL
//     idempotent, e.g. CREATE TABLE IF NOT EXISTS, or record the row by
//     hand). There is no cross-database atomicity and no backend fallback:
//     every failure is returned as a named error for the caller to surface.
//   - Historical integrity: an edited checksum for an already-applied
//     migration, a ledger row ahead of the embedded history (future schema
//     version), a ledger row whose file vanished from the embedded history,
//     and an out-of-order pending migration all fail with named errors
//     before any statement is applied.
//   - Idempotent: re-applying an already-applied history records nothing
//     new and returns the same applied versions and checksums.
//
// The caller owns the schema lifecycle: the runner requires the target
// schema to exist and never creates or drops schemas.
package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// embeddedMigrationsFS holds the authoritative migration history. Files are
// named <version>_<name>.sql with a positive integer version and a
// [a-z][a-z0-9_]* name.
//
//go:embed migrations/*.sql
var embeddedMigrationsFS embed.FS

const (
	// migrationsDir is the directory inside embeddedMigrationsFS.
	migrationsDir = "migrations"
	// LedgerTable is the table that records applied migrations inside the
	// target schema. It is created by migration 000001_ledger.
	LedgerTable = "schema_migrations"
	// advisoryLockNamespace namespaces the advisory-lock key so it cannot
	// collide with unrelated advisory locks in the same database.
	advisoryLockNamespace = "loom:pgstore02:migrate:"
	// unlockTimeout bounds the advisory-lock release, which must succeed
	// even when the caller's context is already done.
	unlockTimeout = 5 * time.Second
	// undefinedTableSQLState is PostgreSQL's 42P01 (undefined_table): the
	// ledger does not exist yet, i.e. nothing has ever been applied.
	undefinedTableSQLState = "42P01"
)

var (
	migrationFilePattern = regexp.MustCompile(`^([0-9]+)_([a-z][a-z0-9_]*)\.sql$`)
	migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	schemaNamePattern    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// Sentinel errors for classifying migration failures without string matching.
var (
	// ErrInvalidMigration reports a malformed migration file or Migration
	// value: bad file name, duplicate version or name, non-positive
	// version, or empty SQL.
	ErrInvalidMigration = errors.New("postgres: invalid migration")
	// ErrInvalidSchemaName reports a target schema that is not a safe
	// lowercase identifier or does not exist.
	ErrInvalidSchemaName = errors.New("postgres: invalid schema name")
	// ErrMigrationChecksumMismatch reports an embedded migration whose
	// computed checksum differs from the checksum the ledger recorded for
	// the same version: history was edited after it was applied.
	ErrMigrationChecksumMismatch = errors.New("postgres: migration checksum mismatch")
	// ErrFutureSchemaVersion reports a ledger version above every embedded
	// migration: the ledger was written by a newer binary than this one.
	ErrFutureSchemaVersion = errors.New("postgres: future schema version in ledger")
	// ErrMigrationMissing reports a ledger version at or below the latest
	// embedded version whose file no longer exists in the embedded history.
	ErrMigrationMissing = errors.New("postgres: applied migration missing from history")
	// ErrMigrationOutOfOrder reports a pending migration whose version is
	// below the highest version already recorded; the history was amended
	// with an old file instead of a new version.
	ErrMigrationOutOfOrder = errors.New("postgres: out-of-order migration")
	// ErrMigrationApply reports a migration whose transaction failed; the
	// migration's DDL was rolled back and the ledger untouched.
	ErrMigrationApply = errors.New("postgres: migration apply failed")
	// ErrMigrationLockAcquire reports failure to take the migration
	// advisory lock, e.g. the caller context expiring while a competing
	// migration holds the lock. The underlying cause stays inspectable
	// (context.DeadlineExceeded, context.Canceled, or a cancelled-query
	// *pgconn.PgError).
	ErrMigrationLockAcquire = errors.New("postgres: migration lock acquisition failed")
	// ErrLedgerRecord reports a migration that committed but whose ledger
	// row could not be written. This is an operator-visible split-brain
	// window; see the package comment.
	ErrLedgerRecord = errors.New("postgres: ledger record failed")
	// ErrMigrationPending reports a ledger that is behind the embedded
	// history: at least one embedded migration has no ledger row. Verify
	// (the read-only probe) returns it; readiness uses it, together with
	// the schema-integrity sentinels, to distinguish a never-migrated or
	// partially-migrated schema from an edited/future ledger and from a
	// connectivity failure. Migrate never returns it (it applies pending
	// migrations instead of reporting them).
	ErrMigrationPending = errors.New("postgres: schema is behind the embedded migration history")
)

// Migration is one embedded SQL migration. Checksum is the lowercase hex
// SHA-256 of the exact SQL bytes; the ledger compares against it.
type Migration struct {
	// Version is the positive, strictly ordered version from the file name.
	Version int64
	// Name is the [a-z][a-z0-9_]* name from the file name.
	Name string
	// SQL is the complete migration body, executed as one simple-protocol
	// command inside the migration transaction. It must not manage its own
	// transaction blocks; PostgreSQL rejects those inside a transaction.
	// The transaction pins search_path to the target schema alone -- no
	// public -- so migrations must schema-qualify references to objects that
	// live outside the target schema (e.g. public.gen_random_uuid()).
	SQL string
	// Checksum is the lowercase hex SHA-256 of SQL.
	Checksum string
}

// LoadMigrations reads and validates every <version>_<name>.sql file in dir
// of fsys, sorted by version. Every regular file must match the pattern: an
// unexpected file name is an error, not something silently skipped.
func LoadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidMigration, dir, err)
	}
	out := make([]Migration, 0, len(entries))
	seenVersion := make(map[int64]string, len(entries))
	seenName := make(map[string]int64, len(entries))
	for _, entry := range entries {
		fileName := entry.Name()
		if entry.IsDir() {
			return nil, fmt.Errorf("%w: %s/%s: nested directories are not supported",
				ErrInvalidMigration, dir, fileName)
		}
		matches := migrationFilePattern.FindStringSubmatch(fileName)
		if matches == nil {
			return nil, fmt.Errorf("%w: %s/%s: file name must match <version>_<name>.sql "+
				"with a positive integer version and an [a-z][a-z0-9_]* name",
				ErrInvalidMigration, dir, fileName)
		}
		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("%w: %s/%s: version must be a positive integer",
				ErrInvalidMigration, dir, fileName)
		}
		name := matches[2]
		if prev, dup := seenVersion[version]; dup {
			return nil, fmt.Errorf("%w: %s/%s: version %d already used by %s",
				ErrInvalidMigration, dir, fileName, version, prev)
		}
		if prev, dup := seenName[name]; dup {
			return nil, fmt.Errorf("%w: %s/%s: name %q already used by version %d",
				ErrInvalidMigration, dir, fileName, name, prev)
		}
		sql, err := fs.ReadFile(fsys, path.Join(dir, fileName))
		if err != nil {
			return nil, fmt.Errorf("%w: %s/%s: read: %v", ErrInvalidMigration, dir, fileName, err)
		}
		if strings.TrimSpace(string(sql)) == "" {
			return nil, fmt.Errorf("%w: %s/%s: empty SQL body", ErrInvalidMigration, dir, fileName)
		}
		seenVersion[version] = fileName
		seenName[name] = version
		out = append(out, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(sql),
			Checksum: migrationChecksum(string(sql)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// EmbeddedMigrations returns the authoritative embedded migration history,
// validated and sorted by version.
func EmbeddedMigrations() ([]Migration, error) {
	return LoadMigrations(embeddedMigrationsFS, migrationsDir)
}

// migrationChecksum is the lowercase hex SHA-256 of the exact SQL bytes.
func migrationChecksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

// migrationAdvisoryLockKey derives a stable PostgreSQL bigint advisory-lock
// key from the target schema, so callers migrating the same schema serialize
// while unrelated advisory locks are untouched. 64-bit FNV-1a collisions
// across schema names are theoretically possible; schema names are
// developer-controlled lowercase identifiers, so a collision cannot be
// produced accidentally.
func migrationAdvisoryLockKey(schema string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(advisoryLockNamespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(schema))
	return int64(h.Sum64())
}

// ConnSource supplies the connection that holds the advisory lock and runs
// migrations. Both *Client (the pg-store-01 pool) and *pgxpool.Pool satisfy
// it; tests pass two clients to prove separate connections serialize.
type ConnSource interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

var (
	_ ConnSource = (*Client)(nil)
	_ ConnSource = (*pgxpool.Pool)(nil)
)

// LedgerRecord is one row of the migration ledger: version, name, checksum,
// and the time the runner recorded the committed migration.
type LedgerRecord struct {
	Version   int64
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// MigrateResult is the outcome of a Migrate call: the full applied history
// re-read from the ledger, so an idempotent re-apply returns the same
// versions and checksums as the run that applied them.
type MigrateResult struct {
	// LatestVersion is the highest recorded version, or 0 for an empty
	// ledger.
	LatestVersion int64
	// Applied is every ledger row, ascending by version.
	Applied []LedgerRecord
}

// MigrationChecksumMismatchError reports an embedded migration whose checksum
// no longer matches the ledger: an already-applied historical migration was
// edited. It matches errors.Is(err, ErrMigrationChecksumMismatch).
type MigrationChecksumMismatchError struct {
	Version          int64
	Name             string
	LedgerChecksum   string
	ComputedChecksum string
}

func (e *MigrationChecksumMismatchError) Error() string {
	return fmt.Sprintf("postgres: migration %d (%s) was edited after it was applied: "+
		"ledger checksum %s, embedded checksum %s",
		e.Version, e.Name, e.LedgerChecksum, e.ComputedChecksum)
}

func (e *MigrationChecksumMismatchError) Unwrap() error { return ErrMigrationChecksumMismatch }

// FutureSchemaVersionError reports a ledger version above every embedded
// migration. The ledger was written by a future schema version; this binary
// must not apply anything. It matches errors.Is(err, ErrFutureSchemaVersion).
type FutureSchemaVersionError struct {
	LedgerVersion  int64
	LedgerName     string
	LatestEmbedded int64
}

func (e *FutureSchemaVersionError) Error() string {
	return fmt.Sprintf("postgres: ledger contains version %d (%s), above the latest embedded version %d; "+
		"the ledger is ahead of this binary",
		e.LedgerVersion, e.LedgerName, e.LatestEmbedded)
}

func (e *FutureSchemaVersionError) Unwrap() error { return ErrFutureSchemaVersion }

// MissingEmbeddedMigrationError reports a ledger version at or below the
// latest embedded version whose file no longer exists in the embedded
// history. It matches errors.Is(err, ErrMigrationMissing).
type MissingEmbeddedMigrationError struct {
	Version    int64
	LedgerName string
}

func (e *MissingEmbeddedMigrationError) Error() string {
	return fmt.Sprintf("postgres: ledger records version %d (%s), which is missing from the embedded history",
		e.Version, e.LedgerName)
}

func (e *MissingEmbeddedMigrationError) Unwrap() error { return ErrMigrationMissing }

// OutOfOrderMigrationError reports a pending migration below the highest
// already-recorded version. It matches errors.Is(err, ErrMigrationOutOfOrder).
type OutOfOrderMigrationError struct {
	Version      int64
	Name         string
	LatestRecord int64
}

func (e *OutOfOrderMigrationError) Error() string {
	return fmt.Sprintf("postgres: migration %d (%s) is pending although version %d is already recorded; "+
		"add a new version instead of editing history",
		e.Version, e.Name, e.LatestRecord)
}

func (e *OutOfOrderMigrationError) Unwrap() error { return ErrMigrationOutOfOrder }

// MigrationApplyError reports a migration whose own transaction failed. The
// migration's DDL was rolled back and no ledger row was written. It matches
// errors.Is(err, ErrMigrationApply) and chains the driver error for
// inspection, e.g. errors.As for *pgconn.PgError.
type MigrationApplyError struct {
	// Op is the failing step: "begin", "search_path", "apply", or "commit".
	Op      string
	Version int64
	Name    string
	Err     error
}

func (e *MigrationApplyError) Error() string {
	return fmt.Sprintf("postgres: migration %d (%s) failed during %s: %v",
		e.Version, e.Name, e.Op, e.Err)
}

// Unwrap exposes both the class sentinel and the cause for errors.Is/As.
func (e *MigrationApplyError) Unwrap() []error { return []error{ErrMigrationApply, e.Err} }

// MigrationRunner applies an embedded migration history to one schema of one
// PostgreSQL database, serialized by a session-level advisory lock. Create
// one with NewMigrationRunner.
type MigrationRunner struct {
	src        ConnSource
	schema     string
	ledgerFQN  string
	migrations []Migration
	// panicHook, when non-nil, is invoked at the top of the locked
	// section. It is a test seam (set directly by in-package tests) for
	// exercising the panic-safety of the advisory-lock release path;
	// production construction always leaves it nil.
	panicHook func()
}

// NewMigrationRunner validates the inputs and normalizes the history. It
// copies migrations, recomputes every checksum from the SQL (so hand-built
// values cannot disagree with content), and rejects non-positive versions,
// duplicate versions or names, invalid names, and empty SQL bodies. The
// connection source must be live only when Migrate runs, not here.
func NewMigrationRunner(src ConnSource, schema string, migrations []Migration) (*MigrationRunner, error) {
	if src == nil {
		return nil, errors.New("postgres: nil migration connection source")
	}
	if !schemaNamePattern.MatchString(schema) {
		return nil, fmt.Errorf("%w: %q must match [a-z_][a-z0-9_]{0,62}", ErrInvalidSchemaName, schema)
	}
	normalized := append([]Migration(nil), migrations...)
	for i := range normalized {
		m := &normalized[i]
		if m.Version < 1 {
			return nil, fmt.Errorf("%w: version %d is below 1", ErrInvalidMigration, m.Version)
		}
		if !migrationNamePattern.MatchString(m.Name) {
			return nil, fmt.Errorf("%w: version %d has invalid name %q", ErrInvalidMigration, m.Version, m.Name)
		}
		if strings.TrimSpace(m.SQL) == "" {
			return nil, fmt.Errorf("%w: version %d (%s) has an empty SQL body", ErrInvalidMigration, m.Version, m.Name)
		}
		m.Checksum = migrationChecksum(m.SQL)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Version < normalized[j].Version })
	seenVersion := make(map[int64]struct{}, len(normalized))
	seenName := make(map[string]struct{}, len(normalized))
	for _, m := range normalized {
		if _, dup := seenVersion[m.Version]; dup {
			return nil, fmt.Errorf("%w: duplicate version %d", ErrInvalidMigration, m.Version)
		}
		if _, dup := seenName[m.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrInvalidMigration, m.Name)
		}
		seenVersion[m.Version] = struct{}{}
		seenName[m.Name] = struct{}{}
	}
	return &MigrationRunner{
		src:        src,
		schema:     schema,
		ledgerFQN:  pgx.Identifier{schema, LedgerTable}.Sanitize(),
		migrations: normalized,
	}, nil
}

// Migrate applies every pending migration and returns the full applied
// history. Before applying anything it refuses, with named errors, an edited
// historical checksum (ErrMigrationChecksumMismatch), a ledger version above
// the embedded history (ErrFutureSchemaVersion), a vanished applied
// migration (ErrMigrationMissing), and an out-of-order pending file
// (ErrMigrationOutOfOrder). A failing migration rolls back its own DDL and
// returns a *MigrationApplyError with the ledger intact. Concurrent callers
// on the same schema serialize on an advisory lock and each migration is
// recorded exactly once; re-applying an applied history is a no-op that
// returns the same result.
func (r *MigrationRunner) Migrate(ctx context.Context) (result MigrateResult, err error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("postgres: acquire migration connection: %w", err)
	}

	key := migrationAdvisoryLockKey(r.schema)
	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); lockErr != nil {
		// pg_advisory_lock is session-scoped and a failed statement tells
		// the client nothing about whether the grant raced the failure
		// (e.g. a cancel landing after the backend granted the lock), and
		// statement abort never releases a session-level lock. Symmetric
		// with the unlock path: a connection of unknown lock state is
		// discarded, never pooled.
		discardPoolConn(conn)
		return MigrateResult{}, fmt.Errorf("%w for schema %q: %w", ErrMigrationLockAcquire, r.schema, lockErr)
	}

	// From here the session holds the lock. Every exit path, panic included,
	// must confirm the unlock and release the connection, or discard it.
	// The release must also work on a cancelled context: an advisory lock is
	// session-scoped, so a pooled connection still holding it would keep
	// every later caller waiting.
	defer func() {
		unlockCtx, cancelUnlock := context.WithTimeout(context.WithoutCancel(ctx), unlockTimeout)
		defer cancelUnlock()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1)", key).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			discardPoolConn(conn)
			if err == nil {
				if unlockErr != nil {
					err = fmt.Errorf("postgres: release migration lock for schema %q: %w", r.schema, unlockErr)
					return
				}
				err = fmt.Errorf("postgres: release migration lock for schema %q: reported already unlocked", r.schema)
			}
			return
		}
		conn.Release()
	}()

	return r.migrateLocked(ctx, conn)
}

// Verify reports the schema's ledger state against the embedded history
// without mutating anything: no advisory lock is taken, no DDL is applied,
// and no ledger row is written. It is the read-only counterpart of the
// refuse-before-apply law Migrate runs under the lock, answering the same
// questions with the same named errors:
//
//   - absent target schema → ErrInvalidSchemaName (the same precondition
//     migrateLocked refuses to run past; the caller owns schema lifecycle);
//   - edited applied checksum → *MigrationChecksumMismatchError;
//   - ledger ahead of the embedded history → *FutureSchemaVersionError;
//   - ledger row whose file vanished from the history → *MissingEmbeddedMigrationError;
//   - out-of-order pending file → *OutOfOrderMigrationError;
//   - pending migrations with no ledger row (including a never-created
//     ledger table, which readLedger already reads as an empty history) →
//     ErrMigrationPending.
//
// Fault classification is deliberate and exact: a failure with no server
// answer — pool acquire, a dead connection, or a transport/protocol fault
// while running or reading either statement — returns the pool's own
// *ConnectError chain (errors.Is(err, ErrConnect)), the same classification
// Ping gives a dead server, so a caller can tell connectivity down from
// schema state. A fault the server itself answered (a *pgconn.PgError)
// proves the connection was alive, so it is reported verbatim without
// ErrConnect: it is neither connectivity nor a named schema state, and a
// readiness classifier must not report a live connection as
// connectivity_down. (The shared readLedger wrap stays untouched for
// Migrate's use; Verify owns this boundary.)
//
// Verify deliberately takes no advisory lock: a readiness probe must never
// queue behind a long migration. Each statement reads the ledger as of its
// own instant, which is the honest answer for a readiness verdict.
func (r *MigrationRunner) Verify(ctx context.Context) error {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	var schemaExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regnamespace($1) IS NOT NULL", r.schema).Scan(&schemaExists); err != nil {
		return verifyStatementError("verify", err)
	}
	if !schemaExists {
		return fmt.Errorf("%w: schema %q does not exist in this database; the caller owns schema lifecycle",
			ErrInvalidSchemaName, r.schema)
	}
	applied, err := r.readLedger(ctx, conn)
	if err != nil {
		return verifyStatementError("verify ledger", err)
	}
	if err := r.validateHistory(applied); err != nil {
		return err
	}
	pending, err := r.pending(applied)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		first := pending[0]
		return fmt.Errorf("%w: %d pending migration(s), first version %d (%s); schema %q",
			ErrMigrationPending, len(pending), first.Version, first.Name, r.schema)
	}
	return nil
}

// verifyStatementError is Verify's fault-classification boundary for a
// statement issued on the acquired connection: a server-answered fault
// (*pgconn.PgError) means the connection was alive and is returned
// verbatim, carrying no ErrConnect; anything else (no server answer: dead
// connection, transport/protocol fault) is a connectivity fault and joins
// the pool's ErrConnect chain. It is Verify-owned on purpose: the shared
// readLedger wrap stays exactly as Migrate depends on it.
func verifyStatementError(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return err
	}
	return &ConnectError{Op: op, Err: err}
}

// discardPoolConn removes a connection from its pool without returning it
// and closes it. A session-level advisory lock survives the end of a
// transaction, so a connection whose release could not be confirmed must
// never go back to the pool: another caller could be kept waiting by a lock
// nobody intends to unlock.
func discardPoolConn(conn *pgxpool.Conn) {
	_ = conn.Hijack().Close(context.Background())
}

// migrateLocked runs under the held advisory lock on conn.
func (r *MigrationRunner) migrateLocked(ctx context.Context, conn *pgxpool.Conn) (MigrateResult, error) {
	if r.panicHook != nil {
		r.panicHook()
	}
	var schemaExists bool
	if err := conn.QueryRow(ctx, "SELECT to_regnamespace($1) IS NOT NULL", r.schema).Scan(&schemaExists); err != nil {
		return MigrateResult{}, fmt.Errorf("postgres: check schema %q: %w", r.schema, err)
	}
	if !schemaExists {
		return MigrateResult{}, fmt.Errorf("%w: schema %q does not exist in this database; the caller owns schema lifecycle",
			ErrInvalidSchemaName, r.schema)
	}

	applied, err := r.readLedger(ctx, conn)
	if err != nil {
		return MigrateResult{}, err
	}
	if err := r.validateHistory(applied); err != nil {
		return MigrateResult{}, err
	}

	pending, err := r.pending(applied)
	if err != nil {
		return MigrateResult{}, err
	}
	for _, m := range pending {
		if err := r.applyOne(ctx, conn, m); err != nil {
			return MigrateResult{}, err
		}
	}

	final, err := r.readLedger(ctx, conn)
	if err != nil {
		return MigrateResult{}, err
	}
	result := MigrateResult{Applied: final}
	if len(final) > 0 {
		result.LatestVersion = final[len(final)-1].Version
	}
	return result, nil
}

// readLedger returns every ledger row ascending by version. A missing ledger
// table means nothing has ever been applied (bootstrap migration 000001_ledger
// has not run yet) and reads as an empty history.
func (r *MigrationRunner) readLedger(ctx context.Context, conn *pgxpool.Conn) ([]LedgerRecord, error) {
	rows, err := conn.Query(ctx,
		"SELECT version, name, checksum, applied_at FROM "+r.ledgerFQN+" ORDER BY version")
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == undefinedTableSQLState {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres: read migration ledger: %w", err)
	}
	defer rows.Close()
	var out []LedgerRecord
	for rows.Next() {
		var rec LedgerRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.AppliedAt); err != nil {
			return nil, fmt.Errorf("postgres: scan migration ledger: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: read migration ledger: %w", err)
	}
	return out, nil
}

// validateHistory refuses to apply anything when the ledger disagrees with
// the embedded history. Ledger rows are checked ascending by version, so a
// deterministic first problem is reported.
func (r *MigrationRunner) validateHistory(applied []LedgerRecord) error {
	byVersion := make(map[int64]Migration, len(r.migrations))
	latestEmbedded := int64(0)
	for _, m := range r.migrations {
		byVersion[m.Version] = m
		if m.Version > latestEmbedded {
			latestEmbedded = m.Version
		}
	}
	for _, rec := range applied {
		m, known := byVersion[rec.Version]
		if !known {
			if rec.Version > latestEmbedded {
				return &FutureSchemaVersionError{
					LedgerVersion:  rec.Version,
					LedgerName:     rec.Name,
					LatestEmbedded: latestEmbedded,
				}
			}
			return &MissingEmbeddedMigrationError{Version: rec.Version, LedgerName: rec.Name}
		}
		if rec.Checksum != m.Checksum {
			return &MigrationChecksumMismatchError{
				Version:          rec.Version,
				Name:             m.Name,
				LedgerChecksum:   rec.Checksum,
				ComputedChecksum: m.Checksum,
			}
		}
	}
	return nil
}

// pending returns the embedded migrations with no ledger row, ascending, and
// rejects out-of-order application (a pending version below the highest
// recorded one).
func (r *MigrationRunner) pending(applied []LedgerRecord) ([]Migration, error) {
	recorded := make(map[int64]struct{}, len(applied))
	latestRecord := int64(0)
	for _, rec := range applied {
		recorded[rec.Version] = struct{}{}
		if rec.Version > latestRecord {
			latestRecord = rec.Version
		}
	}
	var pending []Migration
	for _, m := range r.migrations {
		if _, done := recorded[m.Version]; done {
			continue
		}
		if m.Version < latestRecord {
			return nil, &OutOfOrderMigrationError{
				Version:      m.Version,
				Name:         m.Name,
				LatestRecord: latestRecord,
			}
		}
		pending = append(pending, m)
	}
	return pending, nil
}

// applyOne applies one migration transactionally, then records it. The DDL
// and the search_path run in one transaction; the ledger INSERT runs only
// after that transaction commits.
func (r *MigrationRunner) applyOne(ctx context.Context, conn *pgxpool.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return &MigrationApplyError{Op: "begin", Version: m.Version, Name: m.Name, Err: err}
	}
	abandoned := true
	defer func() {
		if abandoned {
			_ = tx.Rollback(ctx)
		}
	}()

	setSearchPath := "SET LOCAL search_path TO " + pgx.Identifier{r.schema}.Sanitize()
	if _, err := tx.Exec(ctx, setSearchPath); err != nil {
		return &MigrationApplyError{Op: "search_path", Version: m.Version, Name: m.Name, Err: err}
	}
	// No arguments, so pgx sends the body as one simple-protocol command,
	// which supports multiple semicolon-separated statements.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return &MigrationApplyError{Op: "apply", Version: m.Version, Name: m.Name, Err: err}
	}
	if err := tx.Commit(ctx); err != nil {
		return &MigrationApplyError{Op: "commit", Version: m.Version, Name: m.Name, Err: err}
	}
	abandoned = false

	if _, err := conn.Exec(ctx,
		"INSERT INTO "+r.ledgerFQN+" (version, name, checksum) VALUES ($1, $2, $3)",
		m.Version, m.Name, m.Checksum); err != nil {
		return fmt.Errorf("%w: migration %d (%s) committed but was not recorded; resolve the ledger before retrying: %w",
			ErrLedgerRecord, m.Version, m.Name, err)
	}
	return nil
}

// MigrateSchema is the production entry point: it applies the embedded
// migration history to schema using the readiness-checked pg-store-01 pool.
// It never falls back to another backend; failures are returned for the
// caller to surface.
func MigrateSchema(ctx context.Context, client *Client, schema string) (MigrateResult, error) {
	if client == nil {
		return MigrateResult{}, errors.New("postgres: nil client")
	}
	migrations, err := EmbeddedMigrations()
	if err != nil {
		return MigrateResult{}, err
	}
	runner, err := NewMigrationRunner(client, schema, migrations)
	if err != nil {
		return MigrateResult{}, err
	}
	return runner.Migrate(ctx)
}

// VerifySchema is the read-only production counterpart of MigrateSchema: it
// reports the schema's ledger state against the embedded history (see
// MigrationRunner.Verify) and never acquires the migration lock, applies
// DDL, or records a ledger row. It is the probe behind the server runtime's
// readiness classification: schema state must be visible to /readyz without
// any possibility of a probe mutating the store.
func VerifySchema(ctx context.Context, client *Client, schema string) error {
	if client == nil {
		return errors.New("postgres: nil client")
	}
	migrations, err := EmbeddedMigrations()
	if err != nil {
		return err
	}
	runner, err := NewMigrationRunner(client, schema, migrations)
	if err != nil {
		return err
	}
	return runner.Verify(ctx)
}
