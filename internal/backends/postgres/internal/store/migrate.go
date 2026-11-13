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
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var embeddedMigrationsFS embed.FS

const (
	migrationsDir         = "migrations"
	LedgerTable           = "schema_migrations"
	advisoryLockNamespace = "loom:pgstore02:migrate:"
	unlockTimeout         = 5 * time.Second
)

var (
	migrationFilePattern = regexp.MustCompile(`^([0-9]+)_([a-z][a-z0-9_]*)\.sql$`)
	migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	schemaNamePattern    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

var (
	ErrInvalidMigration          = errors.New("postgres: invalid migration")
	ErrInvalidSchemaName         = errors.New("postgres: invalid schema name")
	ErrMigrationChecksumMismatch = errors.New("postgres: migration checksum mismatch")
	ErrFutureSchemaVersion       = errors.New("postgres: future schema version in ledger")
	ErrMigrationMissing          = errors.New("postgres: applied migration missing from history")
	ErrMigrationOutOfOrder       = errors.New("postgres: out-of-order migration")
	ErrMigrationApply            = errors.New("postgres: migration apply failed")
	ErrMigrationLockAcquire      = errors.New("postgres: migration lock acquisition failed")
	ErrLedgerRecord              = errors.New("postgres: ledger record failed")
	ErrMigrationPending          = errors.New("postgres: schema is behind the embedded migration history")
)

type Migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

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

func EmbeddedMigrations() ([]Migration, error) {
	return LoadMigrations(embeddedMigrationsFS, migrationsDir)
}

func migrationChecksum(sql string) string {
	sum := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(sum[:])
}

func migrationAdvisoryLockKey(schema string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(advisoryLockNamespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(schema))
	return int64(h.Sum64())
}

type ConnSource interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

var (
	_ ConnSource = (*Client)(nil)
	_ ConnSource = (*pgxpool.Pool)(nil)
)

type LedgerRecord struct {
	Version   int64
	Name      string
	Checksum  string
	AppliedAt time.Time
}

type MigrateResult struct {
	LatestVersion int64
	Applied       []LedgerRecord
}

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

type MissingEmbeddedMigrationError struct {
	Version    int64
	LedgerName string
}

func (e *MissingEmbeddedMigrationError) Error() string {
	return fmt.Sprintf("postgres: ledger records version %d (%s), which is missing from the embedded history",
		e.Version, e.LedgerName)
}

func (e *MissingEmbeddedMigrationError) Unwrap() error { return ErrMigrationMissing }

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

type MigrationApplyError struct {
	Op      string
	Version int64
	Name    string
	Err     error
}

func (e *MigrationApplyError) Error() string {
	return fmt.Sprintf("postgres: migration %d (%s) failed during %s: %v",
		e.Version, e.Name, e.Op, e.Err)
}

func (e *MigrationApplyError) Unwrap() []error { return []error{ErrMigrationApply, e.Err} }

type MigrationRunner struct {
	src        ConnSource
	schema     string
	ledgerFQN  string
	migrations []Migration
	panicHook  func()
}

func NewMigrationRunner(src ConnSource, schema string, migrations []Migration) (*MigrationRunner, error) {
	if src == nil {
		return nil, errors.New("postgres: nil migration connection source")
	}
	if !ValidSchemaName(schema) {
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

func (r *MigrationRunner) Migrate(ctx context.Context) (result MigrateResult, err error) {
	conn, err := r.src.Acquire(ctx)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("postgres: acquire migration connection: %w", err)
	}

	key := migrationAdvisoryLockKey(r.schema)
	if _, lockErr := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); lockErr != nil {
		discardPoolConn(conn)
		return MigrateResult{}, fmt.Errorf("%w for schema %q: %w", ErrMigrationLockAcquire, r.schema, lockErr)
	}

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

func verifyStatementError(op string, err error) error {
	if _, ok := pgErrorFrom(err); ok {
		return err
	}
	return &ConnectError{Op: op, Err: err}
}

func discardPoolConn(conn *pgxpool.Conn) {
	_ = conn.Hijack().Close(context.Background())
}

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

func (r *MigrationRunner) readLedger(ctx context.Context, conn *pgxpool.Conn) ([]LedgerRecord, error) {
	rows, err := conn.Query(ctx,
		"SELECT version, name, checksum, applied_at FROM "+r.ledgerFQN+" ORDER BY version")
	if err != nil {
		if pgErr, ok := pgErrorFrom(err); ok && pgErr.Code == sqlStateUndefinedTable {
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

func ValidSchemaName(schema string) bool {
	return schemaNamePattern.MatchString(schema)
}

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
