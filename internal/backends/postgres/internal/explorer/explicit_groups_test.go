package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const (
	pgstate06MigrationVersion  = int64(9)
	pgstate06MigrationName     = "explorer_explicit_groups"
	pgstate06MigrationChecksum = "c635cb5ef7c3c04655b4f45b01ba0fe834a7f181f70638f300878caf16c9a14f"
	pgstate06CheckViolation    = "23514"
)

var pgstate06NameRe = regexp.MustCompile(`^pgstate06_it_[0-9]+_[0-9a-f]+$`)

func pgstate06TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 Explorer explicit-group integration tests")
	}
	return raw
}

func pgstate06RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type pgState06Fixture struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)

	store     *ExplicitGroupStore
	witness   *pgxpool.Pool
	witnessID int32
}

func newPGState06Fixture(t *testing.T) *pgState06Fixture {
	t.Helper()
	raw := pgstate06TestPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if !pgstate06NameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&namespaceLeftovers); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if namespaceLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate06_it_* databases remain", namespaceLeftovers)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d pgstate06_it_* schemas remain in the service database", schemaLeftovers)
		}
	})

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if !pgstate06NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	f := &pgState06Fixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
	f.witness, f.witnessID = f.newDedicatedPool(t)
	store, err := f.newStore(t)
	if err != nil {
		f.fail("build explicit-group store", err)
	}
	f.store = store
	return f
}

func (f *pgState06Fixture) migrate(t *testing.T) pgstore.MigrateResult {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       2,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open migration pool", err)
	}
	defer client.Close()
	owner, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}
	runner, err := pgstore.NewMigrationRunner(client, f.schema, owner)
	if err != nil {
		f.fail("new migration runner", err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate state schema", err)
	}
	return result
}

func (f *pgState06Fixture) newStore(t *testing.T) (*ExplicitGroupStore, error) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(client.Close)
	return NewExplicitGroupStore(client, f.schema)
}

func (f *pgState06Fixture) newDedicatedStore(t *testing.T) (*ExplicitGroupStore, int32) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       1,
		MinConns:       1,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open dedicated pg-store pool", err)
	}
	t.Cleanup(client.Close)
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire dedicated pg-store connection", err)
	}
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		conn.Release()
		f.fail("witness dedicated pg-store backend pid", err)
	}
	conn.Release()
	if pid == 0 {
		t.Fatal("dedicated pg-store pool reported backend pid 0")
	}
	store, err := NewExplicitGroupStore(client, f.schema)
	if err != nil {
		f.fail("build dedicated explicit-group store", err)
	}
	return store, pid
}

func (f *pgState06Fixture) newDedicatedPool(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(f.dsn)
	if err != nil {
		f.fail("parse dedicated raw pool config", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, poolConfig)
	if err != nil {
		f.fail("open dedicated raw pool", err)
	}
	t.Cleanup(pool.Close)
	var pid int32
	if err := pool.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated raw backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated raw pool reported backend pid 0")
	}
	return pool, pid
}

func requireDistinctBackendsPGState06(t *testing.T, pids map[string]int32) {
	t.Helper()
	seen := map[int32]string{}
	for label, pid := range pids {
		if pid == 0 {
			t.Fatalf("%s reported backend pid 0", label)
		}
		if other, clash := seen[pid]; clash {
			t.Fatalf("%s and %s share backend pid %d, want provably distinct PG connections", label, other, pid)
		}
		seen[pid] = label
	}
}

func (f *pgState06Fixture) revisionTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupRevisionsTable)
}

func (f *pgState06Fixture) definitionsTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupDefinitionsTable)
}

func (f *pgState06Fixture) headerSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	var out string
	err := f.witness.QueryRow(f.ctx, `SELECT concat_ws(E'\x1f',
		id, project, generation, scope_digest, resource_type,
		source_selection_revision_id, source_membership_digest,
		definition_digest, membership_digest,
		group_count::text, member_count::text, state, idempotency_key,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		COALESCE(to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'),
		COALESCE(writer_token, '<sql-null>'),
		COALESCE(to_char(writer_expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'))
	FROM `+f.revisionTable()+` WHERE id = $1`, revisionID).Scan(&out)
	if err != nil {
		f.fail("header snapshot of "+revisionID, err)
	}
	return out
}

func (f *pgState06Fixture) frozenHeaderSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	snapshot := f.headerSnapshot(t, revisionID)
	parts := strings.Split(snapshot, "\x1f")
	if len(parts) != 17 {
		t.Fatalf("header snapshot of %s has %d fields, want 17", revisionID, len(parts))
	}
	return strings.Join(parts[:15], "\x1f")
}

func (f *pgState06Fixture) definitionsSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	rows, err := f.witness.Query(f.ctx, `SELECT group_id, label, ordinal::text FROM `+
		f.definitionsTable()+` WHERE revision_id = $1 ORDER BY ordinal, group_id`, revisionID)
	if err != nil {
		f.fail("definitions snapshot of "+revisionID, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var groupID, label, ordinal string
		if err := rows.Scan(&groupID, &label, &ordinal); err != nil {
			f.fail("scan definitions snapshot of "+revisionID, err)
		}
		parts = append(parts, strings.Join([]string{groupID, label, ordinal}, "|"))
	}
	if err := rows.Err(); err != nil {
		f.fail("definitions snapshot of "+revisionID, err)
	}
	return strings.Join(parts, "\x1e")
}

func (f *pgState06Fixture) countRows(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	var count int
	if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&count); err != nil {
		f.fail("count rows in "+table, err)
	}
	return count
}

func requirePgErrPGState06(t *testing.T, err error, sqlstate, step string) *pgconn.PgError {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
	return pgErr
}

func requirePgErrConstraintPGState06(t *testing.T, err error, sqlstate, constraint, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrPGState06(t, err, sqlstate, step)
	if pgErr.ConstraintName != constraint {
		t.Fatalf("%s: constraint = %q, want the named guard %q (message: %s)", step, pgErr.ConstraintName, constraint, pgErr.Message)
	}
	return pgErr
}

func requirePgErrTriggerPGState06(t *testing.T, err error, sqlstate, trigger, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrPGState06(t, err, sqlstate, step)
	if !strings.Contains(pgErr.Message, trigger) {
		t.Fatalf("%s: message %q does not name the guard %q", step, pgErr.Message, trigger)
	}
	return pgErr
}

func (f *pgState06Fixture) rerunMigration(t *testing.T, version int64) {
	t.Helper()
	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("load migrations for rerun", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			conn, err := f.iso.Acquire(f.ctx)
			if err != nil {
				f.fail("acquire connection for migration rerun", err)
			}
			defer conn.Release()
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				f.fail("begin migration rerun transaction", err)
			}
			if _, err := tx.Exec(f.ctx,
				"SET LOCAL search_path TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
				tx.Rollback(f.ctx)
				f.fail("set search_path for migration rerun", err)
			}
			if _, err := tx.Exec(f.ctx, m.SQL); err != nil {
				tx.Rollback(f.ctx)
				f.fail(fmt.Sprintf("rerun migration %d/%s", m.Version, m.Name), err)
			}
			if err := tx.Commit(f.ctx); err != nil {
				f.fail("commit migration rerun", err)
			}
			return
		}
	}
	t.Fatalf("migration version %d is absent from the embedded history", version)
}

func (f *pgState06Fixture) dropTrigger(t *testing.T, table, trigger string) {
	t.Helper()
	if _, err := f.witness.Exec(f.ctx, `DROP TRIGGER `+trigger+` ON `+table); err != nil {
		f.fail("drop trigger "+trigger+" on "+table, err)
	}
}

func (f *pgState06Fixture) execOn(t *testing.T, pool *pgxpool.Pool, step, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(f.ctx, sql, args...); err != nil {
		f.fail(step+": "+scrubSQLForTest(sql), err)
	}
}

func scrubSQLForTest(sql string) string {
	if len(sql) > 160 {
		return sql[:160] + "..."
	}
	return sql
}

func pgstate06Selection(project string, suffix string) explorer.SelectionRevision {
	completedAt := time.Now().UTC()
	return explorer.SelectionRevision{
		ID:               "pgstate06-selection-" + suffix,
		Project:          project,
		Generation:       "generation-a",
		ResourceType:     "Patient",
		Rule:             explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:           explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest:      "scope-a",
		RuleDigest:       "selection-rule",
		MembershipDigest: "sha256:" + strings.Repeat("a", 64),
		MemberCount:      3,
		MemberBytes:      30,
		Complete:         true,
		CreatedAt:        completedAt,
		CompletedAt:      &completedAt,
	}
}

func pgstate06Revision(source explorer.SelectionRevision, key string) explorer.ExplicitGroupRevision {
	return explorer.ExplicitGroupRevision{
		ID:                        explorer.ExplicitGroupRevisionIDFor(source.Project, key),
		Project:                   source.Project,
		Generation:                source.Generation,
		ScopeDigest:               source.ScopeDigest,
		ResourceType:              source.ResourceType,
		SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest:    source.MembershipDigest,
		State:                     explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:            key,
		CreatedAt:                 time.Now().UTC(),
	}
}

func pgstate06Definitions() []explorer.ExplicitGroupDefinition {
	return []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-empty", Label: "No members", Ordinal: 1},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	}
}

func pgstate06CanonicalDefinitions(t *testing.T) []explorer.ExplicitGroupDefinition {
	t.Helper()
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(pgstate06Definitions())
	if err != nil {
		t.Fatalf("canonical test definitions: %v", err)
	}
	return canonical
}

func (f *pgState06Fixture) rawInsertHeader(t *testing.T, pool *pgxpool.Pool, revision explorer.ExplicitGroupRevision, writerToken string, writerExpiresAt *time.Time) {
	t.Helper()
	f.execOn(t, pool, "raw insert header",
		`INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest, resource_type,`+
			` source_selection_revision_id, source_membership_digest, definition_digest, membership_digest,`+
			` group_count, member_count, state, idempotency_key, created_at, completed_at, writer_token, writer_expires_at)`+
			` VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', 0, 0, 'STAGING', $8, $9, NULL, $10, $11)`,
		string(revision.ID), revision.Project, revision.Generation, revision.ScopeDigest, revision.ResourceType,
		revision.SourceSelectionRevisionID, revision.SourceMembershipDigest, revision.IdempotencyKey,
		revision.CreatedAt, writerToken, writerExpiresAt)
}

func (f *pgState06Fixture) rawInsertDefinition(t *testing.T, pool *pgxpool.Pool, revisionID, groupID, label string, ordinal int64) {
	t.Helper()
	f.execOn(t, pool, "raw insert definition",
		`INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		revisionID, groupID, label, ordinal)
}

func mustErr(t *testing.T, step string, fn func() error, want error) {
	t.Helper()
	err := fn()
	if !errors.Is(err, want) {
		t.Fatalf("%s: error = %v, want %v", step, err, want)
	}
}

func mustNil(t *testing.T, step string, fn func() error) {
	t.Helper()
	if err := fn(); err != nil {
		t.Fatalf("%s: error = %v, want success", step, err)
	}
}

func TestPostgresExplicitGroupDefinitionsStoreGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	if _, err := NewExplicitGroupStore(nil, "ok_schema"); err == nil {
		t.Fatal("NewExplicitGroupStore(nil, ...) = nil error, want failure")
	}
	for _, bad := range []string{"", "BadName", "has-dash", "1leading", strings.Repeat("x", 64)} {
		if _, err := NewExplicitGroupStore(&pgstore.Client{}, bad); err == nil {
			t.Fatalf("NewExplicitGroupStore(client, %q) = nil error, want failure", bad)
		}
	}
	if _, err := NewExplicitGroupStore(&pgstore.Client{}, "ok_schema"); err != nil {
		t.Fatalf("NewExplicitGroupStore(valid) = %v, want success", err)
	}

	bare := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+bare+`"`); err != nil {
		f.fail("create unmigrated schema", err)
	}
	client, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 1, MinConns: 0, AcquireTimeout: 5 * time.Second})
	if err != nil {
		f.fail("open unmigrated pool", err)
	}
	t.Cleanup(client.Close)
	bareStore, err := NewExplicitGroupStore(client, bare)
	if err != nil {
		f.fail("build unmigrated store", err)
	}
	_, err = bareStore.GetExplicitGroupRevision(f.ctx, "p", "pgstate06-missing")
	requirePgErrPGState06(t, err, "42P01", "unmapped schema read fails closed")
}

func TestPostgresExplicitGroupDefinitionsBeginLifecycleAndOwnership(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "begin-"+pgstate06RandomHex(t))
	key := "begin-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)

	started, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("begin staging revision", err)
	}
	if started.ID != revision.ID || started.State != explorer.ExplicitGroupRevisionStaging {
		t.Fatalf("started = %+v, want the canonical staging header", started)
	}
	if started.CreatedAt.IsZero() || started.CompletedAt != nil {
		t.Fatalf("started carries completion values: %+v", started)
	}
	if want := explorer.ExplicitGroupRevisionIDFor(project, key); started.ID != want {
		t.Fatalf("revision ID = %s, want self-recomputed %s", started.ID, want)
	}

	wantSnapshot := strings.Join([]string{
		string(revision.ID), project, "generation-a", "scope-a", "Patient",
		source.ID, source.MembershipDigest,
		"", "", "0", "0", "STAGING", key,
	}, "\x1f")
	snapshot := f.headerSnapshot(t, string(revision.ID))
	if !strings.HasPrefix(snapshot, wantSnapshot) {
		t.Fatalf("stored header = %q, want identity/binding/completion-empty prefix %q", snapshot, wantSnapshot)
	}

	beforeFull := f.headerSnapshot(t, string(revision.ID))
	beforeFrozen := strings.Join(strings.Split(beforeFull, "\x1f")[:15], "\x1f")
	retry, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("same-owner begin retry", err)
	}
	if retry.ID != started.ID {
		t.Fatalf("retry = %+v, want the stored header", retry)
	}
	afterFull := f.headerSnapshot(t, string(revision.ID))
	afterFrozen := strings.Join(strings.Split(afterFull, "\x1f")[:15], "\x1f")
	if beforeFrozen != afterFrozen {
		t.Fatalf("same-owner retry changed frozen columns:\nbefore %q\nafter  %q", beforeFrozen, afterFrozen)
	}
	if strings.Split(afterFull, "\x1f")[16] == strings.Split(beforeFull, "\x1f")[16] {
		t.Fatal("same-owner retry did not advance the writer lease")
	}

	frozenBefore := f.frozenHeaderSnapshot(t, string(revision.ID))
	mustErr(t, "foreign-token begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "not-the-writer")
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.frozenHeaderSnapshot(t, string(revision.ID)); got != frozenBefore {
		t.Fatalf("foreign-token begin mutated the header:\nbefore %q\nafter  %q", frozenBefore, got)
	}
	mustNil(t, "owner begin after foreign refusal", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token)
		return err
	})

	forgeKey := "drift-" + pgstate06RandomHex(t)
	forge := pgstate06Revision(source, forgeKey)
	forgedRow := forge
	forgedRow.Generation = "generation-forged"
	liveLease := time.Now().UTC().Add(time.Hour)
	f.rawInsertHeader(t, f.witness, forgedRow, "forged-writer", &liveLease)
	drifted := pgstate06Revision(source, forgeKey)
	mustErr(t, "drifted-intent begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, drifted, source, token)
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)

	mustErr(t, "blank-token begin", func() error {
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "   ")
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)

	completedInput := revision
	completedInput.ID = explorer.ExplicitGroupRevisionIDFor(project, key+"-complete")
	completedInput.IdempotencyKey = key + "-complete"
	completedInput.State = explorer.ExplicitGroupRevisionComplete
	completedInput.DefinitionDigest = strings.Repeat("d", 64)
	completedInput.MembershipDigest = strings.Repeat("m", 64)
	completedInput.GroupCount = 1
	now := time.Now().UTC()
	completedInput.CompletedAt = &now
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, completedInput, source, token); err == nil ||
		!strings.Contains(err.Error(), "must begin in staging state") {
		t.Fatalf("completed-state begin = %v, want the staging-state refusal", err)
	}

	forged := revision
	forged.ID = explorer.ExplicitGroupRevisionID("grouprev_" + strings.Repeat("0", 64))
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, forged, source, token); err == nil ||
		strings.Contains(err.Error(), "grouprev_0000000000") {
		t.Fatalf("forged-ID begin = %v, want the domain identity refusal", err)
	}

	bindingCases := []struct {
		name   string
		mutate func(*explorer.SelectionRevision)
	}{
		{"selection id", func(s *explorer.SelectionRevision) { s.ID = "other-selection" }},
		{"project", func(s *explorer.SelectionRevision) { s.Project = "other-project" }},
		{"generation", func(s *explorer.SelectionRevision) { s.Generation = "generation-b" }},
		{"resource type", func(s *explorer.SelectionRevision) { s.ResourceType = "Observation" }},
		{"scope digest", func(s *explorer.SelectionRevision) { s.ScopeDigest = "another-scope" }},
		{"membership digest", func(s *explorer.SelectionRevision) { s.MembershipDigest = "sha256:" + strings.Repeat("b", 64) }},
		{"incomplete selection", func(s *explorer.SelectionRevision) { s.Complete = false }},
	}
	for _, bindingCase := range bindingCases {
		mutated := source
		bindingCase.mutate(&mutated)
		altKey := "binding-" + bindingCase.name + "-" + pgstate06RandomHex(t)
		alt := pgstate06Revision(source, altKey)
		_, err := f.store.BeginExplicitGroupRevision(f.ctx, alt, mutated, token)
		if !errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource) {
			t.Fatalf("begin with mismatched %s = %v, want ErrExplicitGroupRevisionStaleSource", bindingCase.name, err)
		}
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(alt.ID)); got != 0 {
			t.Fatalf("refused %s binding left %d rows, want 0", bindingCase.name, got)
		}
	}
	okKey := "binding-ok-" + pgstate06RandomHex(t)
	ok := pgstate06Revision(source, okKey)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, ok, source, token); err != nil {
		f.fail("begin with correct binding after refusals", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(ok.ID)); got != 1 {
		t.Fatalf("correct-binding begin stored %d rows, want 1", got)
	}

	reopened, err := f.newStore(t)
	if err != nil {
		f.fail("reopen store", err)
	}
	reopenedHeader, err := reopened.BeginExplicitGroupRevision(f.ctx, revision, source, token)
	if err != nil {
		f.fail("reopen begin retry", err)
	}
	if reopenedHeader.ID != revision.ID || reopenedHeader.State != explorer.ExplicitGroupRevisionStaging {
		t.Fatalf("reopened header = %+v, want the stored staging header", reopenedHeader)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("retry path stored %d headers, want exactly 1", got)
	}
}

func TestPostgresExplicitGroupDefinitionsEmptyDefinitionsSurviveReopen(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "defs-"+pgstate06RandomHex(t))
	key := "defs-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}

	shuffled := []explorer.ExplicitGroupDefinition{
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
		{ID: "group-empty", Label: "No members", Ordinal: 1},
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
	}
	mustNil(t, "put definitions", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, shuffled)
	})

	want := strings.Join([]string{
		"group-alpha|Alpha cohort|0",
		"group-empty|No members|1",
		"group-beta|Beta cohort|2",
	}, "\x1e")
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("stored definitions = %q, want literal %q", got, want)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 3 {
		t.Fatalf("definition count = %d, want 3", got)
	}

	reopened, err := f.newStore(t)
	if err != nil {
		f.fail("reopen store", err)
	}
	mustNil(t, "replayed put after reopen", func() error {
		return reopened.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("replayed put changed stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	remixed := []explorer.ExplicitGroupDefinition{
		{ID: "group-empty", Label: "  No members  ", Ordinal: 1},
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	}
	mustNil(t, "remixed put", func() error {
		return reopened.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, remixed)
	})
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("remixed put changed stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	soloKey := "solo-" + pgstate06RandomHex(t)
	solo := pgstate06Revision(source, soloKey)
	soloToken := "writer-solo-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, solo, source, soloToken); err != nil {
		f.fail("begin solo", err)
	}
	mustNil(t, "put single empty group", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, solo.ID, soloToken, []explorer.ExplicitGroupDefinition{
			{ID: "group-only-empty", Label: "Empty but real", Ordinal: 7},
		})
	})
	if got := f.definitionsSnapshot(t, string(solo.ID)); got != "group-only-empty|Empty but real|7" {
		t.Fatalf("empty-group-only revision = %q, want the declared row", got)
	}

	emptyKey := "empty-list-" + pgstate06RandomHex(t)
	empty := pgstate06Revision(source, emptyKey)
	emptyToken := "writer-empty-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, empty, source, emptyToken); err != nil {
		f.fail("begin empty-list revision", err)
	}
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, empty.ID, emptyToken, nil); err == nil {
		t.Fatal("put with zero definitions = nil, want the at-least-one-group refusal")
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(empty.ID)); got != 0 {
		t.Fatalf("refused empty-list put left %d rows, want 0", got)
	}
	mustNil(t, "legal declaration after refusal", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, empty.ID, emptyToken, []explorer.ExplicitGroupDefinition{
			{ID: "group-first", Label: "First", Ordinal: 0},
		})
	})

	before := f.definitionsSnapshot(t, string(revision.ID))
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-alpha", Label: "Copycat", Ordinal: 5},
	}); err == nil {
		t.Fatal("put with duplicate group ID = nil, want the domain refusal")
	}
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 3},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 3},
	}); err == nil {
		t.Fatal("put with duplicate ordinal = nil, want the domain refusal")
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != before {
		t.Fatalf("refused puts mutated stored definitions:\nbefore %q\nafter  %q", before, got)
	}
}

func TestPostgresExplicitGroupDefinitionsWriteOnceAndContentDrift(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "write-once-"+pgstate06RandomHex(t))
	key := "write-once-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	want := f.definitionsSnapshot(t, string(revision.ID))

	driftCases := []struct {
		name  string
		patch func([]explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition
	}{
		{"renamed label", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			g[0].Label = "Renamed cohort"
			return g
		}},
		{"added group", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			return append(g, explorer.ExplicitGroupDefinition{ID: "group-extra", Label: "Extra", Ordinal: 9})
		}},
		{"dropped group", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			return g[:2]
		}},
		{"moved order", func(g []explorer.ExplicitGroupDefinition) []explorer.ExplicitGroupDefinition {
			g[2].Ordinal = 3
			return g
		}},
	}
	for _, driftCase := range driftCases {
		drifted, err := explorer.CanonicalExplicitGroupDefinitions(driftCase.patch(pgstate06Definitions()))
		if err != nil {
			t.Fatalf("canonicalize %s drift fixture: %v", driftCase.name, err)
		}
		mustErr(t, "put "+driftCase.name+" drift", func() error {
			return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, drifted)
		}, explorer.ErrExplicitGroupRevisionConflict)
		if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
			t.Fatalf("%s drift mutated stored definitions:\nbefore %q\nafter  %q", driftCase.name, want, got)
		}
	}
	mustNil(t, "identical put after drift refusals", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})

	mustErr(t, "foreign-token put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, "intruder", pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("foreign-token put mutated stored definitions:\nbefore %q\nafter  %q", want, got)
	}

	mustErr(t, "blank-revision put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, "  ", token, pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "blank-token put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, "  ", pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "unknown-revision put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, explorer.ExplicitGroupRevisionID("grouprev_"+strings.Repeat("9", 64)), token, pgstate06Definitions())
	}, explorer.ErrExplicitGroupRevisionNotFound)
}

func TestPostgresExplicitGroupDefinitionsAbortIdempotence(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "abort-"+pgstate06RandomHex(t))

	mustNil(t, "abort blank id", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, "   ", "writer")
	})
	mustNil(t, "abort missing revision", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, explorer.ExplicitGroupRevisionID("grouprev_"+strings.Repeat("9", 64)), "writer")
	})

	key := "abort-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})

	mustErr(t, "abort blank token", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "  ")
	}, explorer.ErrExplicitGroupRevisionConflict)
	beforeHeader := f.headerSnapshot(t, string(revision.ID))
	beforeDefs := f.definitionsSnapshot(t, string(revision.ID))
	mustErr(t, "foreign-token abort", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "intruder")
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.headerSnapshot(t, string(revision.ID)); got != beforeHeader {
		t.Fatalf("foreign abort mutated the header:\nbefore %q\nafter  %q", beforeHeader, got)
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); got != beforeDefs {
		t.Fatalf("foreign abort mutated the definitions:\nbefore %q\nafter  %q", beforeDefs, got)
	}

	mustNil(t, "owner abort", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token)
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted header count = %d, want 0", got)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted definitions count = %d, want 0", got)
	}
	mustNil(t, "owner abort replay", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token)
	})
	mustNil(t, "anyone abort after removal", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, "intruder")
	})

	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, "reclaimer"); err != nil {
		f.fail("re-claim freed key", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("re-claimed header count = %d, want 1", got)
	}
}

func TestPostgresExplicitGroupDefinitionsStagingHiddenFromReads(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "reads-"+pgstate06RandomHex(t))
	key := "reads-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}

	_, err := f.store.GetExplicitGroupRevision(f.ctx, project, revision.ID)
	mustErr(t, "get staging header", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, project, revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionIncomplete)

	mustErr(t, "get foreign project", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, "other-project", revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)
	mustErr(t, "get blank id", func() error {
		_, err := f.store.GetExplicitGroupRevision(f.ctx, project, "  ")
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)

	_, err = f.store.ListExplicitGroupDefinitions(f.ctx, project, revision.ID)
	mustErr(t, "list definitions of staging", func() error {
		_, err := f.store.ListExplicitGroupDefinitions(f.ctx, project, revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionIncomplete)
	mustErr(t, "list definitions blank id", func() error {
		_, err := f.store.ListExplicitGroupDefinitions(f.ctx, project, "   ")
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)

	listed, err := f.store.ListExplicitGroupRevisions(f.ctx, project, "generation-a", "scope-a", "Patient", 0)
	if err != nil {
		f.fail("list revisions", err)
	}
	if listed == nil {
		t.Fatal("listing returned nil, want empty slice")
	}
	if len(listed) != 0 {
		t.Fatalf("staging header leaked into listing: %+v", listed)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("witness header count = %d, want 1 (hidden by state, not absent)", got)
	}
}

func TestPostgresExplicitGroupDefinitionsCleanupReapsExpiredNeverActive(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "cleanup-"+pgstate06RandomHex(t))
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	expiredLease := now.Add(-time.Minute)
	liveLease := now.Add(5 * time.Minute)

	seeds := []struct {
		revision explorer.ExplicitGroupRevision
		lease    *time.Time
	}{
		{pgstate06Revision(source, "cl-expired-"+pgstate06RandomHex(t)), &expiredLease},
		{pgstate06Revision(source, "cl-null-"+pgstate06RandomHex(t)), nil},
		{pgstate06Revision(source, "cl-active-"+pgstate06RandomHex(t)), &liveLease},
		{pgstate06Revision(source, "cl-young-"+pgstate06RandomHex(t)), &expiredLease},
	}
	expired, nullLease, active, young := seeds[0].revision, seeds[1].revision, seeds[2].revision, seeds[3].revision
	for _, seed := range seeds {
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: seed.revision.ID, Project: seed.revision.Project, Generation: seed.revision.Generation,
			ScopeDigest: seed.revision.ScopeDigest, ResourceType: seed.revision.ResourceType,
			SourceSelectionRevisionID: seed.revision.SourceSelectionRevisionID,
			SourceMembershipDigest:    seed.revision.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            seed.revision.IdempotencyKey,
			CreatedAt:                 past,
		}, "seed-writer", seed.lease)
	}
	f.rawInsertDefinition(t, f.witness, string(expired.ID), "group-a", "A", 0)
	f.rawInsertDefinition(t, f.witness, string(expired.ID), "group-b", "B", 1)
	f.rawInsertDefinition(t, f.witness, string(active.ID), "group-a", "A", 0)
	activeDefs := f.definitionsSnapshot(t, string(active.ID))
	activeHeader := f.headerSnapshot(t, string(active.ID))

	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now, 100); err != nil {
		f.fail("cleanup", err)
	}

	for _, gone := range []explorer.ExplicitGroupRevisionID{expired.ID, nullLease.ID, young.ID} {
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(gone)); got != 0 {
			t.Fatalf("expired header %s count = %d, want 0", gone, got)
		}
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(expired.ID)); got != 0 {
		t.Fatalf("expired definitions count = %d, want 0 (cascade)", got)
	}
	if got := f.headerSnapshot(t, string(active.ID)); got != activeHeader {
		t.Fatalf("cleanup touched the active writer:\nbefore %q\nafter  %q", activeHeader, got)
	}
	if got := f.definitionsSnapshot(t, string(active.ID)); got != activeDefs {
		t.Fatalf("cleanup touched the active writer's definitions:\nbefore %q\nafter  %q", activeDefs, got)
	}

	liveKey := "cl-live-" + pgstate06RandomHex(t)
	live := pgstate06Revision(source, liveKey)
	liveToken := "writer-live-" + pgstate06RandomHex(t)
	f.rawInsertHeader(t, f.witness, live, liveToken, &now)
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now, 100); err != nil {
		f.fail("cleanup live", err)
	}
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(live.ID)); got != 1 {
		t.Fatalf("row created at the before boundary was reaped, want kept (before bound)")
	}

	var bounded []explorer.ExplicitGroupRevisionID
	for i := 0; i < 3; i++ {
		seed := pgstate06Revision(source, fmt.Sprintf("cl-bound-%d-%s", i, pgstate06RandomHex(t)))
		created := now.Add(-time.Duration(3-i) * time.Minute)
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: seed.ID, Project: seed.Project, Generation: seed.Generation,
			ScopeDigest: seed.ScopeDigest, ResourceType: seed.ResourceType,
			SourceSelectionRevisionID: seed.SourceSelectionRevisionID,
			SourceMembershipDigest:    seed.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            seed.IdempotencyKey,
			CreatedAt:                 created,
		}, "seed-writer", func() *time.Time { e := created.Add(-time.Second); return &e }())
		bounded = append(bounded, seed.ID)
	}
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now.Add(time.Minute), 2); err != nil {
		f.fail("bounded cleanup", err)
	}
	remaining := 0
	for _, id := range bounded {
		remaining += f.countRows(t, f.revisionTable(), "id = $1", string(id))
	}
	if remaining != 1 {
		t.Fatalf("bounded sweep left %d rows, want 1", remaining)
	}
	if err := f.store.CleanupExplicitGroupStaging(f.ctx, now.Add(time.Minute), 0); err != nil {
		f.fail("default-limit cleanup", err)
	}
	remaining = 0
	for _, id := range bounded {
		remaining += f.countRows(t, f.revisionTable(), "id = $1", string(id))
	}
	if remaining != 0 {
		t.Fatalf("follow-up sweep left %d rows, want 0", remaining)
	}

	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, active, source, "seed-writer"); err != nil {
		f.fail("active writer begin retry after cleanup", err)
	}
	mustNil(t, "active writer put after cleanup", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, active.ID, "seed-writer", []explorer.ExplicitGroupDefinition{
			{ID: "group-a", Label: "A", Ordinal: 0},
		})
	})
}

func TestPostgresExplicitGroupDefinitionsCorruptionDetection(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "corrupt-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	key := "corrupt-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	f.rawInsertDefinition(t, f.witness, string(revision.ID), "group-padded", " padded ", 0)
	err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, []explorer.ExplicitGroupDefinition{
		{ID: "group-padded", Label: "padded", Ordinal: 0},
	})
	if !errors.Is(err, explorer.ErrCorruptExplicitGroupRevision) {
		t.Fatalf("put over non-canonical row = %v, want ErrCorruptExplicitGroupRevision", err)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("corruption refusal left %d rows, want the single forged row untouched", got)
	}

	cleanKey := "corrupt-clean-" + pgstate06RandomHex(t)
	clean := pgstate06Revision(source, cleanKey)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, clean, source, token); err != nil {
		f.fail("begin clean", err)
	}
	mustNil(t, "clean put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, clean.ID, token, []explorer.ExplicitGroupDefinition{
			{ID: "group-padded", Label: "padded", Ordinal: 0},
		})
	})

	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET idempotency_key = 'rewritten' WHERE id = $1`, string(clean.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard", "rewriting the idempotency key is frozen content")
}

func (f *pgState06Fixture) raceSetup(t *testing.T) (storeA, storeB *ExplicitGroupStore) {
	t.Helper()
	storeA, pidA := f.newDedicatedStore(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackendsPGState06(t, map[string]int32{"storeA": pidA, "storeB": pidB, "witness": f.witnessID})
	return storeA, storeB
}

func raceFire(fnA, fnB func() error) (errA, errB error) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errA = fnA()
	}()
	go func() {
		defer wg.Done()
		<-start
		errB = fnB()
	}()
	close(start)
	wg.Wait()
	return errA, errB
}

func exactlyOneConflict(errA, errB error) bool {
	conflictA := errors.Is(errA, explorer.ErrExplicitGroupRevisionConflict)
	conflictB := errors.Is(errB, explorer.ErrExplicitGroupRevisionConflict)
	return (errA == nil && conflictB) || (conflictA && errB == nil)
}

func TestPostgresExplicitGroupDefinitionsConcurrentClaimRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-claim-"+pgstate06RandomHex(t))

	const rounds = 12
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-claim-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		tokenA := "race-writer-a-" + pgstate06RandomHex(t)
		tokenB := "race-writer-b-" + pgstate06RandomHex(t)

		var headerA, headerB *explorer.ExplicitGroupRevision
		errA, errB := raceFire(
			func() error {
				var err error
				headerA, err = storeA.BeginExplicitGroupRevision(f.ctx, revision, source, tokenA)
				return err
			},
			func() error {
				var err error
				headerB, err = storeB.BeginExplicitGroupRevision(f.ctx, revision, source, tokenB)
				return err
			},
		)
		if !exactlyOneConflict(errA, errB) {
			t.Fatalf("round %d: claim results errA=%v errB=%v, want one success and one typed conflict", round, errA, errB)
		}

		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
			t.Fatalf("round %d: header count = %d, want exactly 1", round, got)
		}
		var storedToken string
		if err := f.witness.QueryRow(f.ctx, `SELECT writer_token FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID)).Scan(&storedToken); err != nil {
			f.fail("witness claim token", err)
		}
		if storedToken != tokenA && storedToken != tokenB {
			t.Fatalf("round %d: stored writer token %q belongs to neither racer", round, storedToken)
		}
		winnerToken, loserToken := tokenA, tokenB
		if storedToken == tokenB {
			winnerToken, loserToken = tokenB, tokenA
		}

		before := f.headerSnapshot(t, string(revision.ID))
		mustErr(t, fmt.Sprintf("round %d loser re-claim", round), func() error {
			_, err := storeB.BeginExplicitGroupRevision(f.ctx, revision, source, loserToken)
			return err
		}, explorer.ErrExplicitGroupRevisionConflict)
		if got := f.headerSnapshot(t, string(revision.ID)); got != before {
			t.Fatalf("round %d: loser re-claim mutated the winner's row:\nbefore %q\nafter  %q", round, before, got)
		}

		mustNil(t, fmt.Sprintf("round %d winner retry before abort", round), func() error {
			_, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, winnerToken)
			return err
		})
		mustNil(t, fmt.Sprintf("round %d winner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, winnerToken)
		})
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("round %d: after abort header count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("round %d: after abort definition count = %d, want 0", round, got)
		}
		_ = headerA
		_ = headerB
	}
}

func TestPostgresExplicitGroupDefinitionsConcurrentPutRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-put-"+pgstate06RandomHex(t))
	otherSource := pgstate06Selection(project, "race-put-other-"+pgstate06RandomHex(t))
	_ = otherSource

	const rounds = 11

	wantCanonical := strings.Join([]string{
		"group-alpha|Alpha cohort|0",
		"group-empty|No members|1",
		"group-beta|Beta cohort|2",
	}, "\x1e")
	keyIdentical := "race-put-identical-" + pgstate06RandomHex(t)
	revisionIdentical := pgstate06Revision(source, keyIdentical)
	token := "race-put-owner-" + pgstate06RandomHex(t)
	if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revisionIdentical, source, token); err != nil {
		f.fail("begin identical-race revision", err)
	}
	for round := 0; round < rounds; round++ {
		errA, errB := raceFire(
			func() error {
				return storeA.PutExplicitGroupDefinitions(f.ctx, revisionIdentical.ID, token, pgstate06Definitions())
			},
			func() error {
				return storeB.PutExplicitGroupDefinitions(f.ctx, revisionIdentical.ID, token, pgstate06Definitions())
			},
		)
		if errA != nil || errB != nil {
			t.Fatalf("identical put round %d: errA=%v errB=%v, want both nil (idempotent replay)", round, errA, errB)
		}
		if got := f.definitionsSnapshot(t, string(revisionIdentical.ID)); got != wantCanonical {
			t.Fatalf("identical put round %d: stored definitions = %q, want byte-identical %q", round, got, wantCanonical)
		}
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revisionIdentical.ID)); got != 3 {
		t.Fatalf("identical puts stored %d definition rows, want exactly 3", got)
	}

	shapeA := pgstate06CanonicalDefinitions(t)
	shapeB, err := explorer.CanonicalExplicitGroupDefinitions([]explorer.ExplicitGroupDefinition{
		{ID: "group-alpha", Label: "Alpha cohort", Ordinal: 0},
		{ID: "group-empty", Label: "No members at all", Ordinal: 1},
		{ID: "group-beta", Label: "Beta cohort", Ordinal: 2},
	})
	if err != nil {
		t.Fatalf("canonicalize shape B: %v", err)
	}
	renderShape := func(shape []explorer.ExplicitGroupDefinition) string {
		parts := make([]string, 0, len(shape))
		for _, g := range shape {
			parts = append(parts, fmt.Sprintf("%s|%s|%d", g.ID, g.Label, g.Ordinal))
		}
		return strings.Join(parts, "\x1e")
	}
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-put-conflict-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		raceToken := "race-put-token-" + pgstate06RandomHex(t)
		if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, raceToken); err != nil {
			f.fail("begin conflict-race revision", err)
		}
		errA, errB := raceFire(
			func() error { return storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, raceToken, shapeA) },
			func() error { return storeB.PutExplicitGroupDefinitions(f.ctx, revision.ID, raceToken, shapeB) },
		)
		if !exactlyOneConflict(errA, errB) {
			t.Fatalf("conflicting put round %d: errA=%v errB=%v, want one success and one typed conflict", round, errA, errB)
		}
		stored := f.definitionsSnapshot(t, string(revision.ID))
		if stored != renderShape(shapeA) && stored != renderShape(shapeB) {
			t.Fatalf("conflicting put round %d: stored definitions = %q, want exactly one canonical shape", round, stored)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 3 {
			t.Fatalf("conflicting put round %d: definition count = %d, want 3 (no interleaved halves)", round, got)
		}
		mustNil(t, fmt.Sprintf("conflicting put round %d owner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, raceToken)
		})
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("conflicting put round %d: after abort definition count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("conflicting put round %d: after abort header count = %d, want 0", round, got)
		}
	}
}

func TestPostgresExplicitGroupDefinitionsCleanupVersusActiveWriterRace(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)
	storeA, storeB := f.raceSetup(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "race-cleanup-"+pgstate06RandomHex(t))

	const rounds = 10
	for round := 0; round < rounds; round++ {
		key := fmt.Sprintf("race-cleanup-%d-%s", round, pgstate06RandomHex(t))
		revision := pgstate06Revision(source, key)
		writerToken := "race-cleanup-writer-" + pgstate06RandomHex(t)

		if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, writerToken); err != nil {
			f.fail("begin racing revision", err)
		}
		if err := storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, writerToken, pgstate06Definitions()); err != nil {
			f.fail("seed racing definitions", err)
		}
		headerBefore := f.headerSnapshot(t, string(revision.ID))
		defsBefore := f.definitionsSnapshot(t, string(revision.ID))

		baitKey := fmt.Sprintf("race-cleanup-bait-%d-%s", round, pgstate06RandomHex(t))
		bait := pgstate06Revision(source, baitKey)
		past := time.Now().UTC().Add(-time.Hour)
		baitLease := time.Now().UTC().Add(-time.Minute)
		f.rawInsertHeader(t, f.witness, explorer.ExplicitGroupRevision{
			ID: bait.ID, Project: bait.Project, Generation: bait.Generation,
			ScopeDigest: bait.ScopeDigest, ResourceType: bait.ResourceType,
			SourceSelectionRevisionID: bait.SourceSelectionRevisionID,
			SourceMembershipDigest:    bait.SourceMembershipDigest,
			State:                     explorer.ExplicitGroupRevisionStaging,
			IdempotencyKey:            bait.IdempotencyKey,
			CreatedAt:                 past,
		}, "bait-writer", &baitLease)
		f.rawInsertDefinition(t, f.witness, string(bait.ID), "group-bait", "Bait", 0)

		errA, errB := raceFire(
			func() error {
				if _, err := storeA.BeginExplicitGroupRevision(f.ctx, revision, source, writerToken); err != nil {
					return err
				}
				return storeA.PutExplicitGroupDefinitions(f.ctx, revision.ID, writerToken, pgstate06Definitions())
			},
			func() error {
				return storeB.CleanupExplicitGroupStaging(f.ctx, time.Now().UTC(), 100)
			},
		)
		if errA != nil {
			t.Fatalf("cleanup race round %d: active writer errored: %v", round, errA)
		}
		if errB != nil {
			t.Fatalf("cleanup race round %d: sweeper errored: %v", round, errB)
		}

		if got := f.frozenHeaderSnapshot(t, string(revision.ID)); got != strings.Join(strings.Split(headerBefore, "\x1f")[:15], "\x1f") {
			t.Fatalf("cleanup race round %d: active writer's frozen fields changed:\nbefore %q\nafter  %q",
				round, strings.Join(strings.Split(headerBefore, "\x1f")[:15], "\x1f"), got)
		}
		if got := f.definitionsSnapshot(t, string(revision.ID)); got != defsBefore {
			t.Fatalf("cleanup race round %d: active writer's definitions changed:\nbefore %q\nafter  %q", round, defsBefore, got)
		}
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(bait.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: bait header count = %d, want 0", round, got)
		}
		if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(bait.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: bait definition count = %d, want 0", round, got)
		}
		mustNil(t, fmt.Sprintf("cleanup race round %d owner abort", round), func() error {
			return storeA.AbortExplicitGroupRevision(f.ctx, revision.ID, writerToken)
		})
		if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
			t.Fatalf("cleanup race round %d: after abort header count = %d, want 0", round, got)
		}
	}
}

func TestPostgresExplicitGroupDefinitionsFaultInjectionRollback(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "fault-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	key := "fault-put-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	headerBefore := f.headerSnapshot(t, string(revision.ID))
	fault := errors.New("injected put fault")
	f.store.afterDefinitionsInserted = func(context.Context, pgx.Tx) error { return fault }
	err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	if err == nil || !errors.Is(err, fault) {
		t.Fatalf("put with injected fault = %v, want the fault to surface", err)
	}
	f.store.afterDefinitionsInserted = nil
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("aborted put left %d definition rows, want 0", got)
	}
	if got := f.headerSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("aborted put moved the header row:\nbefore %q\nafter  %q", headerBefore, got)
	}
	mustNil(t, "put after fault", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	defsAfter := f.definitionsSnapshot(t, string(revision.ID))
	if !strings.Contains(defsAfter, "group-alpha|Alpha cohort|0") || !strings.Contains(defsAfter, "group-empty|No members|1") {
		t.Fatalf("post-fault put stored %q, want the canonical set", defsAfter)
	}

	keyAbort := "fault-abort-" + pgstate06RandomHex(t)
	revisionAbort := pgstate06Revision(source, keyAbort)
	tokenAbort := "writer-abort-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revisionAbort, source, tokenAbort); err != nil {
		f.fail("begin abort fixture", err)
	}
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revisionAbort, source, tokenAbort); err != nil {
		f.fail("warm lease", err)
	}
	mustNil(t, "put abort fixture", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revisionAbort.ID, tokenAbort, pgstate06Definitions())
	})
	headerBeforeAbort := f.headerSnapshot(t, string(revisionAbort.ID))
	defsBeforeAbort := f.definitionsSnapshot(t, string(revisionAbort.ID))
	f.store.afterDefinitionsDeleted = func(context.Context, pgx.Tx) error { return fault }
	err = f.store.AbortExplicitGroupRevision(f.ctx, revisionAbort.ID, tokenAbort)
	if err == nil || !errors.Is(err, fault) {
		t.Fatalf("abort with injected fault = %v, want the fault to surface", err)
	}
	f.store.afterDefinitionsDeleted = nil
	if got := f.headerSnapshot(t, string(revisionAbort.ID)); got != headerBeforeAbort {
		t.Fatalf("aborted abort removed the header:\nbefore %q\nafter  %q", headerBeforeAbort, got)
	}
	if got := f.definitionsSnapshot(t, string(revisionAbort.ID)); got != defsBeforeAbort {
		t.Fatalf("aborted abort removed definition rows:\nbefore %q\nafter  %q", defsBeforeAbort, got)
	}
	mustNil(t, "abort after fault", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revisionAbort.ID, tokenAbort)
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revisionAbort.ID)); got != 0 {
		t.Fatalf("post-fault abort left header count = %d, want 0", got)
	}
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1", string(revisionAbort.ID)); got != 0 {
		t.Fatalf("post-fault abort left definition count = %d, want 0", got)
	}
}

func TestPostgresExplicitGroupDefinitionsMigrationLedgerConvergence(t *testing.T) {
	f := newPGState06Fixture(t)
	first := f.migrate(t)

	owner, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}

	embeddedFiles, embeddedMax := pgstate02EnumerateEmbeddedMigrations(t)
	if len(owner) != len(embeddedFiles) {
		t.Fatalf("embedded history has %d migrations, want %d (loader and the migrations directory disagree)", len(owner), len(embeddedFiles))
	}
	for i, file := range embeddedFiles {
		fileVersion, err := pgstate02MigrationFileVersion(file)
		if err != nil {
			f.fail("parse embedded migration file name", err)
		}
		if owner[i].Version != fileVersion {
			t.Fatalf("loader/directory disagreement: migrations/%s carries version %d, loader reports %d at index %d",
				file, fileVersion, owner[i].Version, i)
		}
	}

	type ledgerRow struct {
		version  int64
		name     string
		checksum string
	}
	readLedger := func() []ledgerRow {
		rows, err := f.witness.Query(f.ctx, `SELECT version, name, checksum FROM `+
			sanitizeTableRef(f.schema, "schema_migrations")+` ORDER BY version`)
		if err != nil {
			f.fail("read ledger", err)
		}
		defer rows.Close()
		var out []ledgerRow
		for rows.Next() {
			var rec ledgerRow
			if err := rows.Scan(&rec.version, &rec.name, &rec.checksum); err != nil {
				f.fail("scan ledger", err)
			}
			out = append(out, rec)
		}
		if err := rows.Err(); err != nil {
			f.fail("read ledger", err)
		}
		return out
	}
	ledger := readLedger()
	if len(ledger) != len(embeddedFiles) {
		t.Fatalf("ledger holds %d rows, want %d (one per embedded migration file)", len(ledger), len(embeddedFiles))
	}
	for i, migration := range owner {
		recomputed := sha256.Sum256([]byte(migration.SQL))
		hexRecomputed := hex.EncodeToString(recomputed[:])
		if migration.Checksum != hexRecomputed {
			t.Fatalf("migration %d/%s loader checksum %s != self-recomputed %s",
				migration.Version, migration.Name, migration.Checksum, hexRecomputed)
		}
		if ledger[i] != (ledgerRow{migration.Version, migration.Name, hexRecomputed}) {
			t.Fatalf("ledger[%d] = %+v, want %+v with self-recomputed checksum %s",
				i, ledger[i], migration, hexRecomputed)
		}
		if migration.Version == pgstate06MigrationVersion {
			if migration.Name != pgstate06MigrationName {
				t.Fatalf("explicit-groups migration name = %q, want %q", migration.Name, pgstate06MigrationName)
			}
			if hexRecomputed != pgstate06MigrationChecksum {
				t.Fatalf("explicit-groups migration checksum = %s, want the byte-frozen pin %s: the migration drifted",
					hexRecomputed, pgstate06MigrationChecksum)
			}
		}
	}

	source := pgstate06Selection("pgstate06-project", "ledger-"+pgstate06RandomHex(t))
	key := "ledger-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	token := "writer-" + pgstate06RandomHex(t)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin ledger fixture", err)
	}
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("renew ledger fixture", err)
	}
	headerBefore := f.headerSnapshot(t, string(revision.ID))

	second := f.migrate(t)
	if len(first.Applied) != len(second.Applied) || first.LatestVersion != second.LatestVersion {
		t.Fatalf("rerun result = %+v, first = %+v, want the identical convergence", second, first)
	}
	if second.LatestVersion != embeddedMax {
		t.Fatalf("latest ledger version = %d, want %d (the maximum version parsed from the embedded migrations directory)",
			second.LatestVersion, embeddedMax)
	}
	if got := readLedger(); len(got) != len(ledger) {
		t.Fatalf("ledger after rerun holds %d rows, want %d", len(got), len(ledger))
	}
	if got := f.headerSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("migration rerun mutated staged state:\nbefore %q\nafter  %q", headerBefore, got)
	}

	pgStoreClient, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second})
	if err != nil {
		f.fail("open mixed-history pool", err)
	}
	defer pgStoreClient.Close()
	competing := fmt.Sprintf("pgstate06_it_%d_%s", time.Now().UnixNano(), pgstate06RandomHex(t))
	if _, err := f.iso.Exec(f.ctx, `CREATE SCHEMA "`+competing+`"`); err != nil {
		f.fail("create competing schema", err)
	}
	if _, err := pgstore.MigrateSchema(f.ctx, pgStoreClient, competing); err != nil {
		f.fail("bootstrap competing schema with source-store history", err)
	}
	_, err = MigrateSchema(f.ctx, pgStoreClient, competing)
	if !errors.Is(err, pgstore.ErrMigrationChecksumMismatch) {
		t.Fatalf("mixed history = %v, want ErrMigrationChecksumMismatch fail-closed", err)
	}
	if _, err := f.iso.Exec(f.ctx, `DROP SCHEMA IF EXISTS "`+competing+`" CASCADE`); err != nil {
		f.fail("drop competing schema", err)
	}
}

func TestZZNoLeftoverPgstate06Resources(t *testing.T) {
	raw := pgstate06TestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for zero-leftover audit: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&databases); err != nil {
		t.Fatalf("audit databases: %v", err)
	}
	if databases != 0 {
		t.Errorf("zero-leftover audit: %d pgstate06_it_* databases remain", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate06\_it\_%' ESCAPE '\'`).Scan(&schemas); err != nil {
		t.Fatalf("audit schemas: %v", err)
	}
	if schemas != 0 {
		t.Errorf("zero-leftover audit: %d pgstate06_it_* schemas remain in the service database", schemas)
	}
}

func TestPostgresExplicitGroupDefinitionsHeaderDatabaseGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "guards-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)

	newHeader := func(label string) explorer.ExplicitGroupRevision {
		key := "guard-" + label + "-" + pgstate06RandomHex(t)
		revision := pgstate06Revision(source, key)
		if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
			f.fail("begin guard fixture "+label, err)
		}
		return revision
	}

	honest := pgstate06Revision(source, "check-honest-"+pgstate06RandomHex(t))
	f.rawInsertHeader(t, f.witness, honest, "tok", nil)
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(honest.ID)); got != 1 {
		t.Fatalf("honest raw header count = %d, want 1", got)
	}

	forgedID := pgstate06Revision(source, "check-forged-id-"+pgstate06RandomHex(t))
	forgedID.ID = explorer.ExplicitGroupRevisionID("grouprev_" + strings.Repeat("0", 64))
	_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'STAGING', $8, now())`,
		string(forgedID.ID), forgedID.Project, forgedID.Generation, forgedID.ScopeDigest, forgedID.ResourceType,
		forgedID.SourceSelectionRevisionID, forgedID.SourceMembershipDigest, forgedID.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionIdentityCheck, "forged id refused by the named identity-binding CHECK")

	complete := pgstate06Revision(source, "check-complete-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'COMPLETE', $8, now())`,
		string(complete.ID), complete.Project, complete.Generation, complete.ScopeDigest, complete.ResourceType,
		complete.SourceSelectionRevisionID, complete.SourceMembershipDigest, complete.IdempotencyKey)
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard",
		"COMPLETE header INSERT refused by the guard: headers are born STAGING (000010 INSERT branch)")

	bogusState := pgstate06Revision(source, "check-bogus-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'BOGUS', $8, now())`,
		string(bogusState.ID), bogusState.Project, bogusState.Generation, bogusState.ScopeDigest, bogusState.ResourceType,
		bogusState.SourceSelectionRevisionID, bogusState.SourceMembershipDigest, bogusState.IdempotencyKey)
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard",
		"BOGUS header INSERT refused at the door by the guard (with the trigger in place no UPDATE or INSERT ever reaches the lifecycle CHECK on state; that CHECK's named classification is witnessed in the membership suite's completion-machine test)")

	stagingWithCompletion := pgstate06Revision(source, "check-staging-values-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, definition_digest, state, idempotency_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'not-empty', 'STAGING', $8, now())`,
		string(stagingWithCompletion.ID), stagingWithCompletion.Project, stagingWithCompletion.Generation,
		stagingWithCompletion.ScopeDigest, stagingWithCompletion.ResourceType,
		stagingWithCompletion.SourceSelectionRevisionID, stagingWithCompletion.SourceMembershipDigest,
		stagingWithCompletion.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionLifecycleValuesCheck, "staging header with completion values refused by the named lifecycle CHECK")

	blankField := pgstate06Revision(source, "check-required-"+pgstate06RandomHex(t))
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest,
		resource_type, source_selection_revision_id, source_membership_digest, state, idempotency_key, created_at)
		VALUES ($1, $2::text, $3::text, $4, $5, $6, $7, 'STAGING', $8, now())`,
		string(blankField.ID), blankField.Project, "", blankField.ScopeDigest, blankField.ResourceType,
		blankField.SourceSelectionRevisionID, blankField.SourceMembershipDigest, blankField.IdempotencyKey)
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		explicitGroupRevisionRequiredFieldsCK, "blank required field refused by the named CHECK")

	guarded := newHeader("guarded")
	frozenUpdate := func(sql string, args ...any) error {
		_, err := f.witness.Exec(f.ctx, sql, args...)
		return err
	}
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET project = 'hijacked' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "frozen column UPDATE names its trigger")
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET state = 'COMPLETE' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard",
		"direct completion refused by the guarded transition: no writer token held")
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET completed_at = now() WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "completed_at is frozen content")
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET writer_expires_at = now() + interval '1 hour' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "lease renewal without the writer-token GUC is refused")

	frozenBefore := f.frozenHeaderSnapshot(t, string(guarded.ID))
	beforeLease := strings.Split(f.headerSnapshot(t, string(guarded.ID)), "\x1f")[16]
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', $1, false)`, token); err != nil {
		f.fail("set writer token GUC on witness", err)
	}
	if _, err := f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET writer_expires_at = now() + interval '2 hours' WHERE id = $1`, string(guarded.ID)); err != nil {
		f.fail("authorized lease renewal", err)
	}
	afterLease := strings.Split(f.headerSnapshot(t, string(guarded.ID)), "\x1f")[16]
	if afterLease == beforeLease {
		t.Fatal("authorized lease renewal did not move the deadline")
	}
	if got := f.frozenHeaderSnapshot(t, string(guarded.ID)); got != frozenBefore {
		t.Fatalf("authorized renewal changed frozen columns:\nbefore %q\nafter  %q", frozenBefore, got)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
		f.fail("clear writer token GUC on witness", err)
	}

	shortenProbe := func(sql string) {
		if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', $1, false)`, token); err != nil {
			f.fail("set writer token GUC", err)
		}
		_, err := f.witness.Exec(f.ctx, sql, string(guarded.ID))
		requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
			"explorer_explicit_group_revisions guard", "lease monotonicity guard")
		if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
			f.fail("clear writer token GUC", err)
		}
	}
	shortenProbe(`UPDATE ` + f.revisionTable() + ` SET writer_expires_at = '2020-01-01T00:00:00Z'::timestamptz WHERE id = $1`)
	shortenProbe(`UPDATE ` + f.revisionTable() + ` SET writer_expires_at = NULL WHERE id = $1`)

	requirePgErrTriggerPGState06(t, frozenUpdate(`DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "unauthorized DELETE names its trigger")

	f.dropTrigger(t, f.revisionTable(), "explorer_explicit_group_revisions_guard")
	unguarded := newHeader("unguarded")
	mustNil(t, "unguarded frozen UPDATE succeeds once the guard is dropped", func() error {
		_, err := f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET source_membership_digest = 'hijacked' WHERE id = $1`, string(unguarded.ID))
		return err
	})
	mustNil(t, "unguarded DELETE succeeds once the guard is dropped", func() error {
		_, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(unguarded.ID))
		return err
	})
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(unguarded.ID)); got != 0 {
		t.Fatalf("unguarded DELETE left %d rows, want 0 (probe must actually delete)", got)
	}
	f.rerunMigration(t, pgstate06MigrationVersion)
	requirePgErrTriggerPGState06(t, frozenUpdate(`UPDATE `+f.revisionTable()+` SET source_membership_digest = 'hijacked2' WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "rerun migration re-blocks the frozen UPDATE")
	requirePgErrTriggerPGState06(t, frozenUpdate(`DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(guarded.ID)),
		pgstate06CheckViolation, "explorer_explicit_group_revisions guard", "rerun migration re-blocks the DELETE")
}

func TestPostgresExplicitGroupDefinitionsDefinitionDatabaseGuards(t *testing.T) {
	f := newPGState06Fixture(t)
	f.migrate(t)

	project := "pgstate06-project"
	source := pgstate06Selection(project, "defguards-"+pgstate06RandomHex(t))
	token := "writer-" + pgstate06RandomHex(t)
	key := "defguards-" + pgstate06RandomHex(t)
	revision := pgstate06Revision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin", err)
	}
	mustNil(t, "put", func() error {
		return f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, pgstate06Definitions())
	})
	want := f.definitionsSnapshot(t, string(revision.ID))

	_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-alpha', 'Dup alpha', 99)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23505", explicitGroupDefinitionPKConstraint,
		"duplicate (revision, group) refused by the named PK constraint")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-new', 'Ordinal clash', 2)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23505", explicitGroupDefinitionOrdinalCK,
		"duplicate ordinal refused by the named unique constraint")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-blanklabel', '', 50)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_label_present", "blank label refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, '', 'Blank group id', 51)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_group_id_present", "blank group id refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ($1, 'group-negative', 'Negative ordinal', -1)`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions_ordinal_non_negative", "negative ordinal refused by the named CHECK")
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal)
		VALUES ('grouprev_' || repeat('f', 64), 'group-orphan', 'Orphan', 52)`)
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "refused: parent revision is absent",
		"orphan definition refused by the named guard branch ahead of the FK")
	f.rawInsertDefinition(t, f.witness, string(revision.ID), "group-legal-control", "Legal control", 53)
	if got := f.countRows(t, f.definitionsTable(), "revision_id = $1 AND group_id = 'group-legal-control'", string(revision.ID)); got != 1 {
		t.Fatalf("legal control definition count = %d, want 1", got)
	}

	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten'
		WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "definition UPDATE names its write-once trigger")

	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+`
		WHERE revision_id = $1 AND group_id = 'group-legal-control'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "unauthorized child DELETE names its trigger")

	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucAbortToken+`', $1, false)`, token); err != nil {
		f.fail("set abort token GUC", err)
	}
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID))
	requirePgErrConstraintPGState06(t, err, "23001", explicitGroupRevisionFKConstraint,
		"header DELETE blocked by the named FK until children are gone")
	if _, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+`
		WHERE revision_id = $1 AND group_id = 'group-legal-control'`, string(revision.ID)); err != nil {
		f.fail("authorized child DELETE", err)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucWriterToken+`', NULL, false)`); err != nil {
		f.fail("clear writer GUC", err)
	}
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config('`+gucAbortToken+`', NULL, false)`); err != nil {
		f.fail("clear abort GUC", err)
	}

	f.dropTrigger(t, f.definitionsTable(), "explorer_explicit_group_definitions_guard")
	mustNil(t, "unguarded definition UPDATE succeeds", func() error {
		_, err := f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten'
			WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
		return err
	})
	f.rerunMigration(t, pgstate06MigrationVersion)
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.definitionsTable()+` SET label = 'Rewritten again'
		WHERE revision_id = $1 AND group_id = 'group-alpha'`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_definitions guard", "rerun migration re-blocks the write-once UPDATE")

	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerPGState06(t, err, pgstate06CheckViolation,
		"explorer_explicit_group_revisions guard", "header guard restored by the same rerun")
	if got := f.countRows(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 1 {
		t.Fatalf("header survived guards count = %d, want 1", got)
	}
	if got := f.definitionsSnapshot(t, string(revision.ID)); !strings.Contains(got, "group-alpha|Rewritten|0") {
		t.Fatalf("definitions after probes = %q, want the witnessed rewrite only", got)
	}
	_ = want
}

const (
	grpmMigrationVersion          = int64(10)
	grpmMigrationName             = "explorer_explicit_group_memberships"
	grpmMigrationChecksum         = "02e0c9ab8bf9864e7d9c82471eacc7406fe2301b025e459784044c4a44489ccd"
	grpmDefinitionsGuardVersion   = int64(11)
	grpmDefinitionsGuardName      = "explorer_explicit_group_definition_guard_hardening"
	grpmDefinitionsGuardChecksum  = "fb45d8f8c312f8f6adb119465da5ed89ac0ddd14929a2c26aa4c917db6d704ec"
	grpmSelectionsFixtureName     = "selection_revisions"
	grpmSelectionsFixtureVersion  = int64(8)
	grpmSelectionsFixtureChecksum = "562a61ef33786cf674f1c87552e2dc8e70691b34116b7f415d5f81960ed8d65d"

	grpmCheckViolation = "23514"

	grpmProject      = "verify-grpm-p"
	grpmGeneration   = "generation-a"
	grpmResourceType = "Patient"

	grpmGroupA = explorer.ExplicitGroupID("verify-grpm-group-a")
	grpmGroupB = explorer.ExplicitGroupID("verify-grpm-group-b")

	grpmRaceRounds  = 10
	grpmSeamTimeout = 5 * time.Second
)

func grpmOwnedNameRe(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `_[0-9]+_[0-9a-f]+$`)
}

func grpmLikePattern(prefix string) string {
	return strings.ReplaceAll(prefix, "_", `\`) + `\_%`
}

var (
	grpmRegistryMu sync.Mutex
	grpmOwnedRes   = map[string]string{}
)

func grpmRegisterResource(dbName, schema string) {
	grpmRegistryMu.Lock()
	defer grpmRegistryMu.Unlock()
	grpmOwnedRes[dbName] = schema
}

func grpmDeRegisterResource(dbName string) {
	grpmRegistryMu.Lock()
	defer grpmRegistryMu.Unlock()
	delete(grpmOwnedRes, dbName)
}

func grpmSnapshotRegistry() map[string]string {
	grpmRegistryMu.Lock()
	defer grpmRegistryMu.Unlock()
	owned := make(map[string]string, len(grpmOwnedRes))
	for k, v := range grpmOwnedRes {
		owned[k] = v
	}
	return owned
}

type grpmFixture struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dbName string
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)

	store     *ExplicitGroupStore
	selStore  *Store
	witness   *pgxpool.Pool
	witnessID int32
}

func newGRPMFixture(t *testing.T) *grpmFixture {
	t.Helper()
	return newGRPMFixtureWithPrefix(t, "verify_grpm")
}

func newGRPMFixtureWithPrefix(t *testing.T, prefix string) *grpmFixture {
	t.Helper()
	raw := pgstate06TestPostgresURL(t)
	nameRe := grpmOwnedNameRe(prefix)
	likePattern := grpmLikePattern(prefix)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixNano(), pgstate06RandomHex(t))
	if !nameRe.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}

	t.Cleanup(func() {
		var mine int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, dbName).Scan(&mine); err != nil {
			t.Errorf("leftover audit: query database catalog: %s", scrub(err))
		}
		if mine != 0 {
			t.Errorf("leftover audit: isolated database %s still exists", dbName)
		}
		var namespaceLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE $1 ESCAPE '\'`, likePattern).Scan(&namespaceLeftovers); err != nil {
			t.Errorf("leftover audit: query database namespace: %s", scrub(err))
		}
		if namespaceLeftovers != 0 {
			t.Errorf("leftover audit: %d %s_* databases remain", namespaceLeftovers, prefix)
		}
		var schemaLeftovers int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_namespace WHERE nspname LIKE $1 ESCAPE '\'`, likePattern).Scan(&schemaLeftovers); err != nil {
			t.Errorf("leftover audit: query schema namespace: %s", scrub(err))
		}
		if schemaLeftovers != 0 {
			t.Errorf("leftover audit: %d %s_* schemas remain in the service database", schemaLeftovers, prefix)
		}
		grpmDeRegisterResource(dbName)
	})

	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)
	if probe, err := url.Parse(dsn); err != nil ||
		probe.Query().Get("dbname") != "" || probe.Path != "/"+dbName {
		t.Fatal("isolated test DSN does not provably target the isolated database")
	}

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixNano(), pgstate06RandomHex(t))
	if !nameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	grpmRegisterResource(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	f := &grpmFixture{ctx: ctx, admin: admin, iso: iso, dbName: dbName, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
	f.witness, f.witnessID = f.newDedicatedPool(t)
	f.store, err = f.newStore(t)
	if err != nil {
		f.fail("build explicit-group store", err)
	}
	selClient, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 4, MinConns: 0, AcquireTimeout: 10 * time.Second})
	if err != nil {
		f.fail("open selection pool", err)
	}
	t.Cleanup(selClient.Close)
	f.selStore, err = New(selClient, f.schema)
	if err != nil {
		f.fail("build selection store", err)
	}
	return f
}

func (f *grpmFixture) migrateWithSelections(t *testing.T) pgstore.MigrateResult {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       2,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open migration pool", err)
	}
	defer client.Close()
	history, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations", err)
	}
	fixture, err := requireOwnerMigration(t, history, grpmSelectionsFixtureVersion, grpmSelectionsFixtureName)
	if err != nil {
		f.fail("selections fixture migration", err)
	}
	embeddedSum := sha256.Sum256([]byte(fixture.SQL))
	if hex.EncodeToString(embeddedSum[:]) != fixture.Checksum ||
		fixture.Checksum != grpmSelectionsFixtureChecksum {
		t.Fatalf("selections migration checksum %s != embedded recompute %s or the frozen pin %s: the #85-owned migration drifted",
			fixture.Checksum, hex.EncodeToString(embeddedSum[:]), grpmSelectionsFixtureChecksum)
	}
	runner, err := pgstore.NewMigrationRunner(client, f.schema, history)
	if err != nil {
		f.fail("new migration runner", err)
	}
	result, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("migrate composed schema", err)
	}
	return result
}

func (f *grpmFixture) newStore(t *testing.T) (*ExplicitGroupStore, error) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	t.Cleanup(client.Close)
	return NewExplicitGroupStore(client, f.schema)
}

func (f *grpmFixture) newDedicatedStore(t *testing.T) (*ExplicitGroupStore, int32) {
	t.Helper()
	client, err := pgstore.Open(f.ctx, pgstore.Config{
		DSN:            f.dsn,
		MaxConns:       1,
		MinConns:       1,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		f.fail("open dedicated pg-store pool", err)
	}
	t.Cleanup(client.Close)
	conn, err := client.Acquire(f.ctx)
	if err != nil {
		f.fail("acquire dedicated pg-store connection", err)
	}
	var pid int32
	if err := conn.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		conn.Release()
		f.fail("witness dedicated pg-store backend pid", err)
	}
	conn.Release()
	if pid == 0 {
		t.Fatal("dedicated pg-store pool reported backend pid 0")
	}
	store, err := NewExplicitGroupStore(client, f.schema)
	if err != nil {
		f.fail("build dedicated explicit-group store", err)
	}
	return store, pid
}

func (f *grpmFixture) newDedicatedPool(t *testing.T) (*pgxpool.Pool, int32) {
	t.Helper()
	poolConfig, err := pgxpool.ParseConfig(f.dsn)
	if err != nil {
		f.fail("parse dedicated raw pool config", err)
	}
	poolConfig.MaxConns = 1
	poolConfig.MinConns = 1
	pool, err := pgxpool.NewWithConfig(f.ctx, poolConfig)
	if err != nil {
		f.fail("open dedicated raw pool", err)
	}
	t.Cleanup(pool.Close)
	var pid int32
	if err := pool.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		f.fail("witness dedicated raw backend pid", err)
	}
	if pid == 0 {
		t.Fatal("dedicated raw pool reported backend pid 0")
	}
	return pool, pid
}

func requireDistinctBackendsGRPM(t *testing.T, pids map[string]int32) {
	t.Helper()
	seen := map[int32]string{}
	for label, pid := range pids {
		if pid == 0 {
			t.Fatalf("%s reported backend pid 0", label)
		}
		if other, clash := seen[pid]; clash {
			t.Fatalf("%s and %s share backend pid %d, want provably distinct PG connections", label, other, pid)
		}
		seen[pid] = label
	}
}

func (f *grpmFixture) revisionTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupRevisionsTable)
}

func (f *grpmFixture) definitionsTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupDefinitionsTable)
}

func (f *grpmFixture) membershipsTable() string {
	return sanitizeTableRef(f.schema, ExplicitGroupMembershipsTable)
}

func (f *grpmFixture) grpmHeaderSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	var out string
	err := f.witness.QueryRow(f.ctx, `SELECT concat_ws(E'\x1f',
		id, project, generation, scope_digest, resource_type,
		source_selection_revision_id, source_membership_digest,
		definition_digest, membership_digest,
		group_count::text, member_count::text, state, idempotency_key,
		to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
		COALESCE(to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'),
		COALESCE(writer_token, '<sql-null>'),
		COALESCE(to_char(writer_expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), '<sql-null>'))
	FROM `+f.revisionTable()+` WHERE id = $1`, revisionID).Scan(&out)
	if err != nil {
		f.fail("header snapshot of "+revisionID, err)
	}
	return out
}

func (f *grpmFixture) membershipSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	rows, err := f.witness.Query(f.ctx, `SELECT group_id, project, generation, resource_type, resource_id FROM `+
		f.membershipsTable()+` WHERE revision_id = $1`+
		` ORDER BY group_id, resource_id, project, generation, resource_type`, revisionID)
	if err != nil {
		f.fail("memberships snapshot of "+revisionID, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var groupID, project, generation, resourceType, resourceID string
		if err := rows.Scan(&groupID, &project, &generation, &resourceType, &resourceID); err != nil {
			f.fail("scan memberships snapshot of "+revisionID, err)
		}
		parts = append(parts, strings.Join([]string{groupID, project, generation, resourceType, resourceID}, "|"))
	}
	if err := rows.Err(); err != nil {
		f.fail("memberships snapshot of "+revisionID, err)
	}
	return strings.Join(parts, "\x1e")
}

func (f *grpmFixture) membershipCtidSnapshot(t *testing.T, revisionID string) string {
	t.Helper()
	rows, err := f.witness.Query(f.ctx, `SELECT ctid::text, group_id, resource_id FROM `+
		f.membershipsTable()+` WHERE revision_id = $1 ORDER BY group_id, resource_id`, revisionID)
	if err != nil {
		f.fail("ctid memberships snapshot of "+revisionID, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var ctid, groupID, resourceID string
		if err := rows.Scan(&ctid, &groupID, &resourceID); err != nil {
			f.fail("scan ctid memberships snapshot of "+revisionID, err)
		}
		parts = append(parts, ctid+"@"+groupID+"/"+resourceID)
	}
	if err := rows.Err(); err != nil {
		f.fail("ctid memberships snapshot of "+revisionID, err)
	}
	return strings.Join(parts, "\x1e")
}

func (f *grpmFixture) countMembers(t *testing.T, revisionID string) int {
	t.Helper()
	return f.countRowsOn(t, f.membershipsTable(), "revision_id = $1", revisionID)
}

func (f *grpmFixture) countRowsOn(t *testing.T, table, where string, args ...any) int {
	t.Helper()
	var count int
	if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+table+` WHERE `+where, args...).Scan(&count); err != nil {
		f.fail("count rows in "+table, err)
	}
	return count
}

func requirePgErrGRPM(t *testing.T, err error, sqlstate, step string) *pgconn.PgError {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: error = %v (%T), want *pgconn.PgError", step, err, err)
	}
	if pgErr.Code != sqlstate {
		t.Fatalf("%s: sqlstate = %s, want %s (error: %v)", step, pgErr.Code, sqlstate, err)
	}
	return pgErr
}

func requirePgErrConstraintGRPM(t *testing.T, err error, sqlstate, constraint, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrGRPM(t, err, sqlstate, step)
	if pgErr.ConstraintName != constraint {
		t.Fatalf("%s: constraint = %q, want the named guard %q (message: %s)", step, pgErr.ConstraintName, constraint, pgErr.Message)
	}
	return pgErr
}

func requirePgErrTriggerGRPM(t *testing.T, err error, sqlstate, trigger, step string) *pgconn.PgError {
	t.Helper()
	pgErr := requirePgErrGRPM(t, err, sqlstate, step)
	if !strings.Contains(pgErr.Message, trigger) {
		t.Fatalf("%s: message %q does not name the guard %q", step, pgErr.Message, trigger)
	}
	return pgErr
}

func (f *grpmFixture) rerunMigrationGRPM(t *testing.T, version int64) {
	t.Helper()
	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("load migrations for rerun", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			conn, err := f.iso.Acquire(f.ctx)
			if err != nil {
				f.fail("acquire connection for migration rerun", err)
			}
			defer conn.Release()
			tx, err := conn.Begin(f.ctx)
			if err != nil {
				f.fail("begin migration rerun transaction", err)
			}
			if _, err := tx.Exec(f.ctx,
				"SET LOCAL search_path TO "+pgx.Identifier{f.schema}.Sanitize()); err != nil {
				tx.Rollback(f.ctx)
				f.fail("set search_path for migration rerun", err)
			}
			if _, err := tx.Exec(f.ctx, m.SQL); err != nil {
				tx.Rollback(f.ctx)
				f.fail(fmt.Sprintf("rerun migration %d/%s", m.Version, m.Name), err)
			}
			if err := tx.Commit(f.ctx); err != nil {
				f.fail("commit migration rerun", err)
			}
			return
		}
	}
	t.Fatalf("migration version %d is absent from the embedded history", version)
}

func (f *grpmFixture) dropTriggerGRPM(t *testing.T, table, trigger string) {
	t.Helper()
	if _, err := f.witness.Exec(f.ctx, `DROP TRIGGER `+trigger+` ON `+table); err != nil {
		f.fail("drop trigger "+trigger+" on "+table, err)
	}
}

func (f *grpmFixture) execOnGRPM(t *testing.T, pool *pgxpool.Pool, step, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(f.ctx, sql, args...); err != nil {
		f.fail(step+": "+scrubSQLForTest(sql), err)
	}
}

func (f *grpmFixture) setGUC(t *testing.T, name string, value any) {
	t.Helper()
	if _, err := f.witness.Exec(f.ctx, `SELECT set_config($1, $2, false)`, name, value); err != nil {
		f.fail("set GUC "+name, err)
	}
}

func grpmRef(resourceID string) explorer.ResourceRef {
	return explorer.ResourceRef{Project: grpmProject, Generation: grpmGeneration, ResourceType: grpmResourceType, ID: resourceID}
}

func grpmMembership(group explorer.ExplicitGroupID, resourceID string) explorer.ExplicitGroupMembership {
	return explorer.ExplicitGroupMembership{GroupID: group, Ref: grpmRef(resourceID)}
}

func (f *grpmFixture) stageSelection(t *testing.T, suffix string, resourceIDs ...string) explorer.SelectionRevision {
	t.Helper()
	selectionID := "verify-grpm-sel-" + suffix
	staging := explorer.SelectionRevision{
		ID:           selectionID,
		Project:      grpmProject,
		Generation:   grpmGeneration,
		ResourceType: grpmResourceType,
		Rule:         explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:       explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest:  "verify-grpm-scope",
		RuleDigest:   "verify-grpm-rule",
		CreatedAt:    time.Now().UTC(),
	}
	token := "grpm-sel-writer-" + suffix
	if _, err := f.selStore.BeginSelection(f.ctx, staging, token); err != nil {
		f.fail("begin source selection "+selectionID, err)
	}
	if len(resourceIDs) > 0 {
		members := make([]explorer.SelectionMember, 0, len(resourceIDs))
		for _, id := range resourceIDs {
			members = append(members, explorer.SelectionMember{Ref: grpmRef(id)})
		}
		if _, err := f.selStore.AppendSelectionMembers(f.ctx, selectionID, token, members); err != nil {
			f.fail("stage source members for "+selectionID, err)
		}
	}
	digest, count, bytes, err := f.selStore.DigestSelectionMembers(f.ctx, grpmProject, selectionID)
	if err != nil {
		f.fail("digest source selection "+selectionID, err)
	}
	if _, err := f.selStore.CompleteSelection(f.ctx, selectionID, token, digest, count, bytes,
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)); err != nil {
		f.fail("complete source selection "+selectionID, err)
	}
	stored, err := f.selStore.GetSelection(f.ctx, grpmProject, selectionID)
	if err != nil {
		f.fail("read stored source selection "+selectionID, err)
	}
	if !stored.Complete || stored.MembershipDigest != digest || stored.MemberCount != count {
		t.Fatalf("stored source selection %q = %+v, want COMPLETE with frozen digest %s and count %d",
			selectionID, stored, digest, count)
	}
	return *stored
}

func grpmRevision(source explorer.SelectionRevision, key string) explorer.ExplicitGroupRevision {
	return explorer.ExplicitGroupRevision{
		ID:                        explorer.ExplicitGroupRevisionIDFor(grpmProject, key),
		Project:                   grpmProject,
		Generation:                source.Generation,
		ScopeDigest:               source.ScopeDigest,
		ResourceType:              source.ResourceType,
		SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest:    source.MembershipDigest,
		State:                     explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey:            key,
		CreatedAt:                 time.Now().UTC(),
	}
}

func grpmDefinitions() []explorer.ExplicitGroupDefinition {
	return []explorer.ExplicitGroupDefinition{
		{ID: grpmGroupA, Label: "Group A", Ordinal: 0},
		{ID: grpmGroupB, Label: "Group B", Ordinal: 1},
	}
}

func (f *grpmFixture) beginGroupRevision(t *testing.T, source explorer.SelectionRevision, key, token string) explorer.ExplicitGroupRevision {
	t.Helper()
	revision := grpmRevision(source, key)
	if _, err := f.store.BeginExplicitGroupRevision(f.ctx, revision, source, token); err != nil {
		f.fail("begin group revision "+key, err)
	}
	return revision
}

func (f *grpmFixture) putDefinitions(t *testing.T, revisionID explorer.ExplicitGroupRevisionID, token string) {
	t.Helper()
	if err := f.store.PutExplicitGroupDefinitions(f.ctx, revisionID, token, grpmDefinitions()); err != nil {
		f.fail("put definitions for "+string(revisionID), err)
	}
}

func (f *grpmFixture) stagedGroupFixture(t *testing.T, suffix string, refs ...string) (explorer.ExplicitGroupRevision, string, explorer.SelectionRevision) {
	t.Helper()
	source := f.stageSelection(t, suffix, refs...)
	token := "grpm-writer-" + suffix
	revision := f.beginGroupRevision(t, source, "grpm-key-"+suffix, token)
	f.putDefinitions(t, revision.ID, token)
	return revision, token, source
}

func (f *grpmFixture) rawInsertMembership(t *testing.T, pool *pgxpool.Pool, revisionID, groupID, project, generation, resourceType, resourceID string) {
	t.Helper()
	f.execOnGRPM(t, pool, "raw insert membership",
		`INSERT INTO `+f.membershipsTable()+` (revision_id, group_id, project, generation, resource_type, resource_id)`+
			` VALUES ($1, $2, $3, $4, $5, $6)`,
		revisionID, groupID, project, generation, resourceType, resourceID)
}

func (f *grpmFixture) rawInsertDefinition(t *testing.T, pool *pgxpool.Pool, revisionID, groupID, label string, ordinal int64) {
	t.Helper()
	f.execOnGRPM(t, pool, "raw insert definition",
		`INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		revisionID, groupID, label, ordinal)
}

func grpmFrameDigest(t *testing.T, pairs ...[2]string) string {
	t.Helper()
	hash := sha256.New()
	writeField := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		if _, err := hash.Write(length[:]); err != nil {
			t.Fatalf("frame digest length write: %v", err)
		}
		if _, err := hash.Write([]byte(value)); err != nil {
			t.Fatalf("frame digest value write: %v", err)
		}
	}
	writeField("loom-explicit-group-membership-v1")
	for _, pair := range pairs {
		writeField(pair[0])
		writeField(grpmProject)
		writeField(grpmGeneration)
		writeField(grpmResourceType)
		writeField(pair[1])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func grpmRow(group, resourceID string) string {
	return strings.Join([]string{group, grpmProject, grpmGeneration, grpmResourceType, resourceID}, "|")
}

func grpmRows(rows ...string) string {
	return strings.Join(rows, "\x1e")
}

func grpmSeam(release <-chan struct{}) func(context.Context, pgx.Tx) error {
	return func(ctx context.Context, tx pgx.Tx) error {
		select {
		case <-release:
			return nil
		case <-time.After(grpmSeamTimeout):
			return errors.New("grpm seam deadline exceeded: race schedule broken")
		}
	}
}

func grpmHoldCompletion(store *ExplicitGroupStore, ctx context.Context,
	revision explorer.ExplicitGroupRevision, token string,
	defDigest, memDigest string, groups, members int64) (release chan<- struct{}, complete <-chan error, entered <-chan struct{}) {
	releaseComplete := make(chan struct{})
	completeEntered := make(chan struct{})
	var once sync.Once
	store.beforeCompleteCommit = func(ctx context.Context, tx pgx.Tx) error {
		once.Do(func() { close(completeEntered) })
		select {
		case <-releaseComplete:
			return nil
		case <-time.After(grpmSeamTimeout):
			return errors.New("grpm completion seam deadline exceeded: race schedule broken")
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.CompleteExplicitGroupRevision(ctx, revision.ID, token,
			defDigest, memDigest, groups, members, time.Now().UTC())
		done <- err
	}()
	return releaseComplete, done, completeEntered
}

func (f *grpmFixture) grpmWatchLockWait(pid int32) (sawLockWait func() bool, stop func()) {
	var (
		mu   sync.Mutex
		seen bool
		done = make(chan struct{})
		wg   sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			var waiting int
			if err := f.admin.QueryRow(f.ctx,
				`SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND wait_event_type IN ('Lock', 'Transaction')`,
				pid).Scan(&waiting); err == nil && waiting > 0 {
				mu.Lock()
				seen = true
				mu.Unlock()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return func() bool {
			mu.Lock()
			defer mu.Unlock()
			return seen
		}, func() {
			close(done)
			wg.Wait()
		}
}

func (f *grpmFixture) rawInsertHeaderGRPM(t *testing.T, pool *pgxpool.Pool, revision explorer.ExplicitGroupRevision, writerToken string, writerExpiresAt *time.Time) {
	t.Helper()
	f.execOnGRPM(t, pool, "raw insert header",
		`INSERT INTO `+f.revisionTable()+` (id, project, generation, scope_digest, resource_type,`+
			` source_selection_revision_id, source_membership_digest, definition_digest, membership_digest,`+
			` group_count, member_count, state, idempotency_key, created_at, completed_at, writer_token, writer_expires_at)`+
			` VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', 0, 0, 'STAGING', $8, $9, NULL, $10, $11)`,
		string(revision.ID), revision.Project, revision.Generation, revision.ScopeDigest, revision.ResourceType,
		revision.SourceSelectionRevisionID, revision.SourceMembershipDigest, revision.IdempotencyKey,
		revision.CreatedAt, writerToken, writerExpiresAt)
}

func TestPostgresExplicitGroupMembershipsAppendOverlapRetryAndScope(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "scope", "r-both", "r-a", "r-b")

	batch := []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-both"),
		grpmMembership(grpmGroupA, "r-both"),
		grpmMembership(grpmGroupB, "r-both"),
		grpmMembership(grpmGroupA, "r-a"),
		grpmMembership(grpmGroupB, "r-b"),
	}
	inserted, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, batch)
	if err != nil {
		f.fail("append overlapping batch", err)
	}
	if len(inserted) != 4 {
		t.Fatalf("first append reported %d created, want 4: %+v", len(inserted), inserted)
	}
	want := grpmRows(
		grpmRow(string(grpmGroupA), "r-a"),
		grpmRow(string(grpmGroupA), "r-both"),
		grpmRow(string(grpmGroupB), "r-b"),
		grpmRow(string(grpmGroupB), "r-both"),
	)
	if got := f.membershipSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("membership rows = %q, want %q", got, want)
	}
	ctidsBefore := f.membershipCtidSnapshot(t, string(revision.ID))

	retry, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, batch)
	if err != nil {
		f.fail("retry overlapping batch", err)
	}
	if len(retry) != 0 {
		t.Fatalf("retry reported %d created, want 0 (insert-once): %+v", len(retry), retry)
	}
	if got := f.membershipSnapshot(t, string(revision.ID)); got != want {
		t.Fatalf("membership rows after retry = %q, want %q", got, want)
	}
	if got := f.membershipCtidSnapshot(t, string(revision.ID)); got != ctidsBefore {
		t.Fatalf("ctids after retry = %q, want byte-identical %q", got, ctidsBefore)
	}

	empty, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, nil)
	if err != nil || empty != nil {
		t.Fatalf("empty batch = (%v, %v), want (nil, nil)", empty, err)
	}
	if got := f.membershipCtidSnapshot(t, string(revision.ID)); got != ctidsBefore {
		t.Fatalf("ctids after empty batch moved: %q", got)
	}

	var expires time.Time
	if err := f.witness.QueryRow(f.ctx,
		`SELECT writer_expires_at FROM `+f.revisionTable()+` WHERE id = $1`, string(revision.ID)).Scan(&expires); err != nil {
		f.fail("read writer lease", err)
	}
	if expires.Before(time.Now().UTC().Add(explicitGroupWriterLease / 2)) {
		t.Fatalf("writer lease %v was not renewed forward by the accepted append", expires)
	}

	mustErr(t, "digest under foreign project", func() error {
		_, _, _, _, err := f.store.DigestExplicitGroupRevision(f.ctx, "verify-grpm-other", revision.ID)
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)
	mustErr(t, "visit under foreign project", func() error {
		_, err := f.store.VisitExplicitGroupMemberships(f.ctx, "verify-grpm-other", revision.ID,
			explorer.ExplicitGroupMembershipCursor{}, 10, func(explorer.ExplicitGroupMembership) error { return nil })
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)
}

func TestPostgresExplicitGroupMembershipsAppendWriterStateAndSourceGuards(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "guards", "r-a")
	emptyBefore := f.membershipSnapshot(t, string(revision.ID))
	headerBefore := f.grpmHeaderSnapshot(t, string(revision.ID))

	mustErr(t, "append with foreign writer token", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token+"-wrong",
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")})
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "append with blank token", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, " ",
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")})
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "append to blank revision", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, "  ", token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")})
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "append to absent revision", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, "verify-grpm-absent-revision", token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")})
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)
	if got := f.membershipSnapshot(t, string(revision.ID)); got != emptyBefore {
		t.Fatalf("refused appends wrote rows: %q", got)
	}
	if got := f.grpmHeaderSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("refused appends moved the header: %q != %q", got, headerBefore)
	}

	mustErr(t, "append resource absent from source selection", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-not-in-source")})
		return err
	}, explorer.ErrExplicitGroupMemberNotInSelection)
	mustErr(t, "append mixed good/bad batch", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{
				grpmMembership(grpmGroupA, "r-a"),
				grpmMembership(grpmGroupB, "r-not-in-source"),
			})
		return err
	}, explorer.ErrExplicitGroupMemberNotInSelection)
	if got := f.membershipSnapshot(t, string(revision.ID)); got != emptyBefore {
		t.Fatalf("refused source-guard appends wrote rows: %q", got)
	}

	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(explorer.ExplicitGroupID("verify-grpm-group-ghost"), "r-a")}); err == nil ||
		!strings.Contains(err.Error(), "undeclared explicit group") {
		t.Fatalf("append under undeclared group error = %v, want the undeclared-group refusal", err)
	}

	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{{GroupID: grpmGroupA, Ref: explorer.ResourceRef{
			Project: "verify-grpm-other", Generation: grpmGeneration, ResourceType: grpmResourceType, ID: "r-a"}}}); err == nil {
		t.Fatal("append with foreign project reference was accepted")
	} else if errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) || errors.Is(err, explorer.ErrExplicitGroupMemberNotInSelection) {
		t.Fatalf("foreign-scope reference got conflict-class error %v, want the domain validation error", err)
	}

	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("legal control append", err)
	}
	if got := f.membershipSnapshot(t, string(revision.ID)); got != grpmRows(grpmRow(string(grpmGroupA), "r-a")) {
		t.Fatalf("legal control wrote %q", got)
	}

	completed, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		"0711212b6e8e2eee92a289bec97557775e4435545972b8f21c93f03e532f3321",
		grpmFrameDigest(t, [2]string{string(grpmGroupA), "r-a"}), 2, 1,
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))
	if err != nil || completed == nil || completed.State != explorer.ExplicitGroupRevisionComplete {
		f.fail("complete before append-after-complete probe", err)
	}
	mustErr(t, "append after completion", func() error {
		_, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupB, "r-a")})
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, $6)`, string(revision.ID), string(grpmGroupB),
		grpmProject, grpmGeneration, grpmResourceType, "r-a")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "explorer_explicit_group_memberships guard",
		"direct insert under COMPLETE parent")
}

func TestPostgresExplicitGroupMembershipsDigestFrozenAndFencedComplete(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "digest", "r-1", "r-2")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-1"), grpmMembership(grpmGroupB, "r-1"),
		grpmMembership(grpmGroupA, "r-2"), grpmMembership(grpmGroupB, "r-2"),
	}); err != nil {
		f.fail("seed append", err)
	}
	const defDigest = "0711212b6e8e2eee92a289bec97557775e4435545972b8f21c93f03e532f3321"
	memDigest := grpmFrameDigest(t,
		[2]string{string(grpmGroupA), "r-1"}, [2]string{string(grpmGroupA), "r-2"},
		[2]string{string(grpmGroupB), "r-1"}, [2]string{string(grpmGroupB), "r-2"})
	gotDef, gotMem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("staging digest", err)
	}
	if gotDef != defDigest || gotMem != memDigest || groups != 2 || members != 4 {
		t.Fatalf("staging digest = (%s, %s, %d, %d), want (%s, %s, 2, 4)",
			gotDef, gotMem, groups, members, defDigest, memDigest)
	}

	for name, call := range map[string]func() error{
		"blank token": func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, "", defDigest, memDigest, 2, 4, time.Now().UTC())
			return err
		},
		"blank digests": func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, "", "", 2, 4, time.Now().UTC())
			return err
		},
		"zero groups": func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 0, 4, time.Now().UTC())
			return err
		},
		"negative members": func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 2, -1, time.Now().UTC())
			return err
		},
	} {
		mustErr(t, "complete with "+name, call, explorer.ErrExplicitGroupRevisionIncomplete)
	}
	mustErr(t, "complete with foreign token", func() error {
		_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token+"-wrong", defDigest, memDigest, 2, 4, time.Now().UTC())
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "complete with wrong membership digest", func() error {
		_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, strings.Repeat("0", 64), 2, 4, time.Now().UTC())
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got, _, _, _, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID); err != nil || got != defDigest {
		t.Fatalf("digest after refused completion broke: %s (%v)", got, err)
	}
	if state := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(state, "\x1fSTAGING\x1f") {
		t.Fatalf("header after refused completion is no longer STAGING: %q", state)
	}
	mustErr(t, "complete under foreign project id", func() error {
		_, err := f.store.CompleteExplicitGroupRevision(f.ctx, "verify-grpm-nope", token, defDigest, memDigest, 2, 4, time.Now().UTC())
		return err
	}, explorer.ErrExplicitGroupRevisionNotFound)

	completed, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 2, 4, time.Time{})
	if err != nil || completed == nil {
		f.fail("honest completion", err)
	}
	if completed.State != explorer.ExplicitGroupRevisionComplete ||
		completed.CompletedAt == nil || completed.CompletedAt.IsZero() {
		t.Fatalf("completed revision = %+v", completed)
	}
	if gotDef, gotMem, groups, members, err = f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID); err != nil ||
		gotDef != defDigest || gotMem != memDigest || groups != 2 || members != 4 {
		t.Fatalf("frozen digest = (%s, %s, %d, %d, %v)", gotDef, gotMem, groups, members, err)
	}
	retry, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 2, 4,
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC))
	if err != nil || retry == nil {
		f.fail("idempotent completion retry", err)
	}
	if retry.CompletedAt == nil || completed.CompletedAt == nil || !retry.CompletedAt.Equal(*completed.CompletedAt) {
		t.Fatalf("idempotent retry moved completed_at: %v != %v", retry.CompletedAt, completed.CompletedAt)
	}
	mustErr(t, "complete COMPLETE row with different facts", func() error {
		_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, strings.Repeat("a", 64), 2, 4, time.Now().UTC())
		return err
	}, explorer.ErrExplicitGroupRevisionConflict)
	mustErr(t, "abort COMPLETE revision", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token)
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.countMembers(t, string(revision.ID)); got != 4 {
		t.Fatalf("members after refused abort = %d, want 4", got)
	}
}

func TestPostgresExplicitGroupMembershipsGoldenFactsAndBreakOne(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, source := f.stagedGroupFixture(t, "golden", "r-gold-1", "r-gold-2")
	if source.ID != "verify-grpm-sel-golden" || source.MembershipDigest == "" || source.MemberCount != 2 {
		t.Fatalf("golden source = %+v", source)
	}
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-gold-1"), grpmMembership(grpmGroupA, "r-gold-2"),
		grpmMembership(grpmGroupB, "r-gold-1"), grpmMembership(grpmGroupB, "r-gold-2"),
	}); err != nil {
		f.fail("golden append", err)
	}

	const (
		defDigest = "0711212b6e8e2eee92a289bec97557775e4435545972b8f21c93f03e532f3321"
		memDigest = "15a005684f311244c87000d13ecbd9f4aa7cde4d4248d6a81c519b82dbbd6e28"
	)
	if gotDef, gotMem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID); err != nil ||
		gotDef != defDigest || gotMem != memDigest || groups != 2 || members != 4 {
		t.Fatalf("golden digest = (%s, %s, %d, %d, %v), want the pinned literals", gotDef, gotMem, groups, members, err)
	}
	if derived := grpmFrameDigest(t,
		[2]string{string(grpmGroupA), "r-gold-1"}, [2]string{string(grpmGroupA), "r-gold-2"},
		[2]string{string(grpmGroupB), "r-gold-1"}, [2]string{string(grpmGroupB), "r-gold-2"}); derived != memDigest {
		t.Fatalf("golden independent re-derivation %s != literal %s", derived, memDigest)
	}

	completedAt := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 2, 4, completedAt); err != nil {
		f.fail("golden completion", err)
	}
	want := strings.Join([]string{
		string(revision.ID), grpmProject, grpmGeneration, "verify-grpm-scope", grpmResourceType,
		"verify-grpm-sel-golden", source.MembershipDigest,
		defDigest, memDigest,
		"2", "4", "COMPLETE", "grpm-key-golden",
		"<any-created-at>", "2026-09-19 00:00:00.000000", token, "<any-lease>",
	}, "\x1f")
	got := f.grpmHeaderSnapshot(t, string(revision.ID))
	parts := strings.Split(got, "\x1f")
	wantParts := strings.Split(want, "\x1f")
	for i := range wantParts {
		if wantParts[i] == "<any-created-at>" || wantParts[i] == "<any-lease>" {
			continue
		}
		if parts[i] != wantParts[i] {
			t.Fatalf("COMPLETE header field %d = %q, want %q (full: %q)", i, parts[i], wantParts[i], got)
		}
	}

	flip := func(hexDigest string, pos int) string {
		bad := []byte(hexDigest)
		if bad[pos] == 'f' {
			bad[pos] = '0'
		} else {
			bad[pos] = 'f'
		}
		return string(bad)
	}
	breakCases := []struct {
		name     string
		claimDef string
		claimMem string
	}{
		{"membership digest flipped", defDigest, flip(memDigest, 40)},
		{"definition digest flipped", flip(defDigest, 7), memDigest},
	}
	for _, broken := range breakCases {
		broken := broken
		mustErr(t, "completion with "+broken.name, func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, broken.claimDef, broken.claimMem, 2, 4, completedAt)
			return err
		}, explorer.ErrExplicitGroupRevisionConflict)
	}
	singleRev, singleToken, _ := f.stagedGroupFixture(t, "golden-single", "r-single")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, singleRev.ID, singleToken,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-single")}); err != nil {
		f.fail("single append", err)
	}
	const singleDigest = "f728a0b986129118326ccdd7345695544660bbadc19933c1373c9975a1329338"
	if _, gotMem, _, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, singleRev.ID); err != nil ||
		gotMem != singleDigest || members != 1 {
		t.Fatalf("single digest = (%s, %d, %v), want (%s, 1, nil)", gotMem, members, err, singleDigest)
	}
	if derived := grpmFrameDigest(t, [2]string{string(grpmGroupA), "r-single"}); derived != singleDigest {
		t.Fatalf("single independent re-derivation %s != literal %s", derived, singleDigest)
	}

	overlapRev, overlapToken, _ := f.stagedGroupFixture(t, "golden-overlap", "r-both")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, overlapRev.ID, overlapToken,
		[]explorer.ExplicitGroupMembership{
			grpmMembership(grpmGroupA, "r-both"), grpmMembership(grpmGroupB, "r-both"),
		}); err != nil {
		f.fail("overlap append", err)
	}
	const overlapDigest = "2b30fa23d892241d12586ab15fc7bec37bbe975c77538ceda3d3eea1ff7b966d"
	if _, gotMem, _, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, overlapRev.ID); err != nil ||
		gotMem != overlapDigest || members != 2 {
		t.Fatalf("overlap digest = (%s, %d, %v), want (%s, 2, nil)", gotMem, members, err, overlapDigest)
	}

	emptyRev, emptyToken, _ := f.stagedGroupFixture(t, "golden-empty")
	const emptyDigest = "51a9413396bfc791022568e5db6dbc5001629854b2c17d7b07dfdb35a820ec09"
	if _, gotMem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, emptyRev.ID); err != nil ||
		gotMem != emptyDigest || groups != 2 || members != 0 {
		t.Fatalf("empty digest = (%s, %d, %d, %v), want (%s, 2, 0, nil)", gotMem, groups, members, err, emptyDigest)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, emptyRev.ID, emptyToken, defDigest, emptyDigest, 2, 0, completedAt); err != nil {
		f.fail("complete zero-member revision", err)
	}
}

func TestPostgresExplicitGroupMembershipsVisitBoundedResumableCancel(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	const total = 1001
	refs := make([]string, 0, total)
	for i := 0; i < total; i++ {
		refs = append(refs, fmt.Sprintf("r-%04d", i))
	}
	revision, token, _ := f.stagedGroupFixture(t, "visit", refs...)
	batch := make([]explorer.ExplicitGroupMembership, 0, total)
	for i, ref := range refs {
		group := grpmGroupA
		if i%2 == 1 {
			group = grpmGroupB
		}
		batch = append(batch, grpmMembership(group, ref))
	}
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, batch); err != nil {
		f.fail("append 1001 members", err)
	}

	mustErr(t, "visit STAGING revision", func() error {
		_, err := f.store.VisitExplicitGroupMemberships(f.ctx, grpmProject, revision.ID,
			explorer.ExplicitGroupMembershipCursor{}, 10, func(explorer.ExplicitGroupMembership) error { return nil })
		return err
	}, explorer.ErrExplicitGroupRevisionIncomplete)
	if _, err := f.store.VisitExplicitGroupMemberships(f.ctx, grpmProject, revision.ID,
		explorer.ExplicitGroupMembershipCursor{}, 10, nil); err == nil ||
		!strings.Contains(err.Error(), "visitor is required") {
		t.Fatalf("nil-visitor error = %v, want the required-visitor refusal", err)
	}

	var pages []int
	f.store.membershipPageStreamed = func(rows int) { pages = append(pages, rows) }
	defDigest, memDigest, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	f.store.membershipPageStreamed = nil
	if err != nil {
		f.fail("large digest", err)
	}
	if groups != 2 || members != total {
		t.Fatalf("large digest counts = (%d, %d), want (2, %d)", groups, members, total)
	}
	if len(pages) == 0 {
		t.Fatal("digest streamed no pages")
	}
	for _, n := range pages {
		if n > explicitGroupMembershipPageSize {
			t.Fatalf("digest page of %d exceeded the cap %d", n, explicitGroupMembershipPageSize)
		}
	}
	if pages[0] != explicitGroupMembershipPageSize || pages[len(pages)-1] != 1 {
		t.Fatalf("digest page shape %v, want first page %d and last page 1", pages, explicitGroupMembershipPageSize)
	}

	tx, err := f.witness.Begin(f.ctx)
	if err != nil {
		f.fail("begin plan-witness transaction", err)
	}
	defer tx.Rollback(f.ctx)
	if _, err := tx.Exec(f.ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		f.fail("disable seqscan for plan witness", err)
	}
	if _, err := tx.Exec(f.ctx, `ANALYZE `+f.membershipsTable()); err != nil {
		f.fail("analyze memberships for plan witness", err)
	}
	explainPlan := func(sql string) string {
		t.Helper()
		rows, err := tx.Query(f.ctx, `EXPLAIN (FORMAT TEXT) `+sql, string(revision.ID), "", "", 50)
		if err != nil {
			f.fail("explain membership page", err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				f.fail("scan plan line", err)
			}
			plan.WriteString(line)
			plan.WriteByte('\n')
		}
		if err := rows.Err(); err != nil {
			f.fail("read plan lines", err)
		}
		return plan.String()
	}
	fixedPlan := explainPlan(f.store.memberStmts.memberPage)
	if !strings.Contains(fixedPlan, "explorer_explicit_group_memberships_visit") {
		t.Fatalf("bounded page does not use the visit index:\n%s", fixedPlan)
	}
	if strings.Contains(fixedPlan, "Sort") {
		t.Fatalf("bounded page carries a Sort node -- the ORDER BY is not proven by the visit index:\n%s", fixedPlan)
	}
	tailedPlan := explainPlan(strings.Replace(f.store.memberStmts.memberPage,
		"ORDER BY group_id, resource_id", "ORDER BY group_id, resource_id, project, generation, resource_type", 1))
	if !strings.Contains(tailedPlan, "Sort") {
		t.Fatalf("plan-shape control: the trailing-scope-column page planned WITHOUT a Sort node -- the no-Sort witness proves nothing:\n%s", tailedPlan)
	}
	if err := tx.Commit(f.ctx); err != nil {
		f.fail("commit plan-witness transaction", err)
	}

	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, defDigest, memDigest, 2, total,
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)); err != nil {
		f.fail("large completion", err)
	}

	var seen []explorer.ExplicitGroupMembership
	cursor := explorer.ExplicitGroupMembershipCursor{}
	calls := 0
	for {
		calls++
		if calls > total {
			t.Fatal("visit resumption never terminated")
		}
		next, err := f.store.VisitExplicitGroupMemberships(f.ctx, grpmProject, revision.ID, cursor, 100000,
			func(membership explorer.ExplicitGroupMembership) error {
				seen = append(seen, membership)
				return nil
			})
		if err != nil {
			f.fail("visit page", err)
		}
		if next == cursor {
			break
		}
		cursor = next
	}
	if len(seen) != total {
		t.Fatalf("full visit yielded %d members, want %d", len(seen), total)
	}
	var wantOrder []explorer.ExplicitGroupMembership
	for _, group := range []explorer.ExplicitGroupID{grpmGroupA, grpmGroupB} {
		for _, m := range batch {
			if m.GroupID == group {
				wantOrder = append(wantOrder, m)
			}
		}
	}
	if len(wantOrder) != total {
		t.Fatalf("expected walk set = %d, want %d", len(wantOrder), total)
	}
	for i, want := range wantOrder {
		if seen[i].GroupID != want.GroupID || seen[i].Ref.ID != want.Ref.ID {
			t.Fatalf("visit order diverged at %d: got %s/%s want %s/%s", i,
				seen[i].GroupID, seen[i].Ref.ID, want.GroupID, want.Ref.ID)
		}
	}

	visCtx, cancel := context.WithCancel(f.ctx)
	visited := 0
	var partial []explorer.ExplicitGroupMembership
	cancelledCursor, err := f.store.VisitExplicitGroupMemberships(visCtx, grpmProject, revision.ID,
		explorer.ExplicitGroupMembershipCursor{}, 5, func(membership explorer.ExplicitGroupMembership) error {
			visited++
			partial = append(partial, membership)
			if visited == 3 {
				cancel()
			}
			return nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled visit error = %v, want context.Canceled", err)
	}
	if len(partial) != 3 {
		t.Fatalf("cancelled visit called the visitor %d times, want exactly 3", len(partial))
	}
	if cancelledCursor.GroupID != partial[2].GroupID || cancelledCursor.ResourceID != partial[2].Ref.ID {
		t.Fatalf("cancelled cursor = %+v, want the last visited member %+v", cancelledCursor, partial[2])
	}
	resumed := append([]explorer.ExplicitGroupMembership(nil), partial...)
	cursor = cancelledCursor
	for {
		next, err := f.store.VisitExplicitGroupMemberships(f.ctx, grpmProject, revision.ID, cursor, 100000,
			func(membership explorer.ExplicitGroupMembership) error {
				resumed = append(resumed, membership)
				return nil
			})
		if err != nil {
			f.fail("resumed visit", err)
		}
		if next == cursor {
			break
		}
		cursor = next
	}
	if len(resumed) != total {
		t.Fatalf("resumed visit total = %d, want %d (no gap, no duplicate)", len(resumed), total)
	}
	for i := range resumed {
		if resumed[i].GroupID != seen[i].GroupID || resumed[i].Ref.ID != seen[i].Ref.ID {
			t.Fatalf("resumed walk diverged from full walk at %d", i)
		}
	}
}

func TestPostgresExplicitGroupMembershipsDatabaseGuards(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "guardsdb", "r-a", "r-b")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("seed append", err)
	}
	stored := grpmRows(grpmRow(string(grpmGroupA), "r-a"))

	_, err := f.witness.Exec(f.ctx, `UPDATE `+f.membershipsTable()+` SET resource_id = 'r-c'`+
		` WHERE revision_id = $1 AND group_id = $2 AND resource_id = $3`,
		string(revision.ID), string(grpmGroupA), "r-a")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "write-once", "membership UPDATE mutation")
	if _, err := f.witness.Exec(f.ctx, `UPDATE `+f.membershipsTable()+` SET resource_id = 'r-a'`+
		` WHERE revision_id = $1 AND group_id = $2 AND resource_id = $3`,
		string(revision.ID), string(grpmGroupA), "r-a"); err != nil {
		f.fail("no-op membership UPDATE probe", err)
	}

	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.membershipsTable()+
		` WHERE revision_id = $1 AND group_id = $2 AND resource_id = $3`,
		string(revision.ID), string(grpmGroupA), "r-a")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "owner-abort or expired-writer-cleanup",
		"unauthorized membership DELETE")
	if got := f.membershipSnapshot(t, string(revision.ID)); got != stored {
		t.Fatalf("refused DELETE moved rows: %q", got)
	}

	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, $6)`,
		string(revision.ID), string(grpmGroupB), "verify-grpm-other", grpmGeneration, grpmResourceType, "r-b")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "does not repeat the parent scope",
		"foreign-scope membership insert")

	orphan := "verify-grpm-orphan-revision"
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, $6)`,
		orphan, string(grpmGroupA), grpmProject, grpmGeneration, grpmResourceType, "r-x")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "parent revision is absent", "orphan membership insert")

	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, $6)`,
		string(revision.ID), "verify-grpm-group-ghost", grpmProject, grpmGeneration, grpmResourceType, "r-b")
	requirePgErrConstraintGRPM(t, err, "23503",
		explicitGroupMembershipGroupFK, "undeclared-group membership insert")

	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, $6)`,
		string(revision.ID), "", grpmProject, grpmGeneration, grpmResourceType, "r-b")
	requirePgErrConstraintGRPM(t, err, grpmCheckViolation,
		"explorer_explicit_group_memberships_group_id_present", "empty group_id insert")

	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.membershipsTable()+
		` (revision_id, group_id, project, generation, resource_type, resource_id)`+
		` VALUES ($1, $2, $3, $4, $5, NULL)`,
		string(revision.ID), string(grpmGroupB), grpmProject, grpmGeneration, grpmResourceType)
	requirePgErrGRPM(t, err, "23502", "NULL resource_id insert")

	f.rawInsertMembership(t, f.witness, string(revision.ID), string(grpmGroupB), grpmProject, grpmGeneration, grpmResourceType, "r-b")
	if got := f.membershipSnapshot(t, string(revision.ID)); got != grpmRows(stored, grpmRow(string(grpmGroupB), "r-b")) {
		t.Fatalf("mutation-probe insert produced %q", got)
	}

	f.dropTriggerGRPM(t, f.membershipsTable(), "explorer_explicit_group_memberships_guard")
	f.execOnGRPM(t, f.witness, "guard-dropped delete", `DELETE FROM `+f.membershipsTable()+
		` WHERE revision_id = $1 AND group_id = $2 AND resource_id = $3`,
		string(revision.ID), string(grpmGroupB), "r-b")
	f.rerunMigrationGRPM(t, grpmMigrationVersion)
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.membershipsTable()+
		` WHERE revision_id = $1 AND group_id = $2 AND resource_id = $3`,
		string(revision.ID), string(grpmGroupA), "r-a")
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "owner-abort or expired-writer-cleanup",
		"membership DELETE after guard rerun")
}

func TestPostgresExplicitGroupMembershipsCompletionMachineDatabaseFacts(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, source := f.stagedGroupFixture(t, "machine", "r-a", "r-b")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{
			grpmMembership(grpmGroupA, "r-a"), grpmMembership(grpmGroupB, "r-b"),
		}); err != nil {
		f.fail("machine seed append", err)
	}
	headerBefore := f.grpmHeaderSnapshot(t, string(revision.ID))

	_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.revisionTable()+
		` (id, project, generation, scope_digest, resource_type, source_selection_revision_id,`+
		` source_membership_digest, definition_digest, membership_digest, group_count, member_count,`+
		` state, idempotency_key, created_at, completed_at, writer_token, writer_expires_at)`+
		` VALUES ('verify-grpm-born-complete', $1, $2, 's', 'Patient', 'src', 'md',`+
		` 'd', 'm', 1, 1, 'COMPLETE', 'k', now(), now(), 'w', now())`,
		grpmProject, grpmGeneration)
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "born STAGING", "direct COMPLETE header INSERT")

	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+
		` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
		` group_count = 2, member_count = 2, completed_at = now() WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "writer token", "completion without the token GUC")
	f.setGUC(t, "loom.explorer_explicit_group_writer_token", token+"-wrong")
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+
		` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
		` group_count = 2, member_count = 2, completed_at = now() WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "writer token", "completion with the wrong token GUC")

	f.setGUC(t, "loom.explorer_explicit_group_writer_token", token)
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+
		` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
		` group_count = 2, member_count = 3, completed_at = now() WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "claims member_count 3 but 2", "completion claiming a false member_count")
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+
		` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
		` group_count = 5, member_count = 2, completed_at = now() WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "claims group_count 5 but 2", "completion claiming a false group_count")
	if got := f.grpmHeaderSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("refused direct completions moved the header: %q != %q", got, headerBefore)
	}

	f.execOnGRPM(t, f.witness, "honest guarded direct completion",
		`UPDATE `+f.revisionTable()+
			` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
			` group_count = 2, member_count = 2, completed_at = now() WHERE id = $1`, string(revision.ID))
	if state := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(state, "\x1fCOMPLETE\x1f") {
		t.Fatalf("honest guarded completion did not land: %q", state)
	}
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET membership_digest = 'tampered' WHERE id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "COMPLETE history", "tampering with COMPLETE history")
	f.execOnGRPM(t, f.witness, "no-op UPDATE probe", `UPDATE `+f.revisionTable()+` SET state = 'COMPLETE' WHERE id = $1`, string(revision.ID))

	f.dropTriggerGRPM(t, f.revisionTable(), "explorer_explicit_group_revisions_guard")
	f.execOnGRPM(t, f.witness, "set up bogus-state probe", `UPDATE `+f.revisionTable()+
		` SET writer_expires_at = writer_expires_at WHERE id = $1`, string(revision.ID))
	bogusHeader := grpmRevision(source, "machine-bogus")
	bogus := string(bogusHeader.ID)
	f.rawInsertHeaderGRPM(t, f.witness, bogusHeader, "bogus-writer", nil)
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET state = 'BOGUS' WHERE id = $1`, bogus)
	requirePgErrConstraintGRPM(t, err, grpmCheckViolation, explicitGroupRevisionLifecycleValuesCheck,
		"BOGUS state under the lifecycle CHECK (trigger dropped)")
	f.rerunMigrationGRPM(t, grpmMigrationVersion)
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+` SET state = 'BOGUS' WHERE id = $1`, bogus)
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "the only admitted transition", "BOGUS state after guard rerun")
	f.setGUC(t, "loom.explorer_explicit_group_writer_token", "")
	other, _, _ := f.stagedGroupFixture(t, "machine-other", "r-a")
	_, err = f.witness.Exec(f.ctx, `UPDATE `+f.revisionTable()+
		` SET state = 'COMPLETE', definition_digest = 'd', membership_digest = 'm',`+
		` group_count = 2, member_count = 0, completed_at = now() WHERE id = $1`, string(other.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "writer token", "tokenless completion after guard rerun")
}

func TestPostgresExplicitGroupMembershipsAbortCleanupCascade(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "abort", "r-a", "r-b")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-a"), grpmMembership(grpmGroupB, "r-b"), grpmMembership(grpmGroupA, "r-b"),
	}); err != nil {
		f.fail("abort seed append", err)
	}
	if got := f.countMembers(t, string(revision.ID)); got != 3 {
		t.Fatalf("abort seed count = %d, want 3", got)
	}
	mustErr(t, "abort with the wrong token", func() error {
		return f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token+"-wrong")
	}, explorer.ErrExplicitGroupRevisionConflict)
	if got := f.countMembers(t, string(revision.ID)); got != 3 {
		t.Fatalf("refused abort moved memberships: %d", got)
	}
	if err := f.store.AbortExplicitGroupRevision(f.ctx, revision.ID, token); err != nil {
		f.fail("owner abort", err)
	}
	if got := f.countMembers(t, string(revision.ID)); got != 0 {
		t.Fatalf("memberships after abort = %d, want 0 (cascade)", got)
	}
	if got := f.countRowsOn(t, f.definitionsTable(), "revision_id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("definitions after abort = %d, want 0 (cascade)", got)
	}
	if got := f.countRowsOn(t, f.revisionTable(), "id = $1", string(revision.ID)); got != 0 {
		t.Fatalf("header after abort = %d, want 0", got)
	}

	live, liveToken, _ := f.stagedGroupFixture(t, "cleanup-live", "r-a")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, live.ID, liveToken,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("cleanup live seed append", err)
	}
	expiredLease := time.Now().UTC().Add(-time.Minute)
	source := f.stageSelection(t, "cleanup", "r-a")
	expired := grpmRevision(source, "grpm-key-cleanup-expired")
	f.rawInsertHeaderGRPM(t, f.witness, expired, "grpm-cleanup-writer", &expiredLease)
	f.rawInsertDefinition(t, f.witness, string(expired.ID), string(grpmGroupA), "Group A", 0)
	f.rawInsertDefinition(t, f.witness, string(expired.ID), string(grpmGroupB), "Group B", 1)
	f.rawInsertMembership(t, f.witness, string(expired.ID), string(grpmGroupA), grpmProject, grpmGeneration, grpmResourceType, "r-a")
	f.rawInsertMembership(t, f.witness, string(expired.ID), string(grpmGroupB), grpmProject, grpmGeneration, grpmResourceType, "r-a")

	_, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.membershipsTable()+` WHERE revision_id = $1`, string(expired.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "owner-abort or expired-writer-cleanup",
		"unauthorized membership DELETE on expired staging")

	if err := f.store.CleanupExplicitGroupStaging(f.ctx, time.Now().UTC(), 100); err != nil {
		f.fail("expired-writer cleanup", err)
	}
	if got := f.countRowsOn(t, f.revisionTable(), "id = $1", string(expired.ID)); got != 0 {
		t.Fatalf("expired header survived cleanup: %d", got)
	}
	if got := f.countMembers(t, string(expired.ID)); got != 0 {
		t.Fatalf("expired memberships survived cleanup: %d", got)
	}
	if got := f.countRowsOn(t, f.definitionsTable(), "revision_id = $1", string(expired.ID)); got != 0 {
		t.Fatalf("expired definitions survived cleanup: %d", got)
	}
	if got := f.countMembers(t, string(live.ID)); got != 1 {
		t.Fatalf("active writer's memberships were swept: %d, want 1", got)
	}
	if got := f.countRowsOn(t, f.revisionTable(), "id = $1", string(live.ID)); got != 1 {
		t.Fatalf("active writer's header was swept")
	}
	f.setGUC(t, "loom.explorer_explicit_group_abort_token", liveToken)
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(live.ID))
	pgErr := requirePgErrGRPM(t, err, "23001", "authorized header DELETE while children remain")
	if !strings.Contains(pgErr.Message, "explorer_explicit_group_definitions_revision_fkey") &&
		!strings.Contains(pgErr.Message, explicitGroupMembershipRevisionFK) {
		t.Fatalf("header DELETE refusal %q names neither child RESTRICT foreign key", pgErr.Message)
	}
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+` WHERE revision_id = $1`, string(live.ID))
	pgErr = requirePgErrGRPM(t, err, "23001", "authorized definition DELETE while memberships remain")
	if !strings.Contains(pgErr.Message, explicitGroupMembershipGroupFK) {
		t.Fatalf("definition DELETE refusal %q does not name the membership group RESTRICT foreign key", pgErr.Message)
	}
	f.execOnGRPM(t, f.witness, "authorized membership delete", `DELETE FROM `+f.membershipsTable()+` WHERE revision_id = $1`, string(live.ID))
	f.execOnGRPM(t, f.witness, "authorized definition delete", `DELETE FROM `+f.definitionsTable()+` WHERE revision_id = $1`, string(live.ID))
	f.execOnGRPM(t, f.witness, "authorized header delete", `DELETE FROM `+f.revisionTable()+` WHERE id = $1`, string(live.ID))
	f.setGUC(t, "loom.explorer_explicit_group_abort_token", "")
}

func TestPostgresExplicitGroupMembershipsFaultSeamRollback(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "fault-append", "r-a", "r-b")
	headerBefore := f.grpmHeaderSnapshot(t, string(revision.ID))
	storeA, _ := f.newDedicatedStore(t)
	var insideRows int64
	storeA.afterMembershipsInserted = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+f.membershipsTable()+
			` WHERE revision_id = $1`, string(revision.ID)).Scan(&insideRows); err != nil {
			return err
		}
		return errors.New("injected crash after membership inserts")
	}
	_, err := storeA.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-a"), grpmMembership(grpmGroupB, "r-b"),
	})
	if err == nil || !strings.Contains(err.Error(), "injected crash") {
		t.Fatalf("append fault error = %v, want the injected crash", err)
	}
	if insideRows != 2 {
		t.Fatalf("the seam saw %d rows inside the transaction, want 2 (the inserts were real)", insideRows)
	}
	if got := f.membershipSnapshot(t, string(revision.ID)); got != "" {
		t.Fatalf("crashed append leaked rows: %q", got)
	}
	if got := f.grpmHeaderSnapshot(t, string(revision.ID)); got != headerBefore {
		t.Fatalf("crashed append moved the header: %q != %q", got, headerBefore)
	}
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token, []explorer.ExplicitGroupMembership{
		grpmMembership(grpmGroupA, "r-a"), grpmMembership(grpmGroupB, "r-b"),
	}); err != nil {
		f.fail("honest append after fault", err)
	}

	storeB, _ := f.newDedicatedStore(t)
	var flippedState string
	storeB.beforeCompleteCommit = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT state FROM `+f.revisionTable()+
			` WHERE id = $1`, string(revision.ID)).Scan(&flippedState); err != nil {
			return err
		}
		return errors.New("injected crash before complete commit")
	}
	defDigest, memDigest, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("pre-fault digest", err)
	}
	if _, err := storeB.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		defDigest, memDigest, groups, members, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "injected crash") {
		t.Fatalf("complete fault error = %v, want the injected crash", err)
	}
	if flippedState != "COMPLETE" {
		t.Fatalf("the seam saw state %q inside the transaction, want COMPLETE (the CAS was real)", flippedState)
	}
	if got := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(got, "\x1fSTAGING\x1f") {
		t.Fatalf("crashed completion left the header non-STAGING: %q", got)
	}
	if got := f.membershipSnapshot(t, string(revision.ID)); got != grpmRows(
		grpmRow(string(grpmGroupA), "r-a"), grpmRow(string(grpmGroupB), "r-b")) {
		t.Fatalf("crashed completion disturbed memberships: %q", got)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		defDigest, memDigest, groups, members, time.Now().UTC()); err != nil {
		f.fail("completion after crash", err)
	}
}

func TestPostgresExplicitGroupMembershipsFencedCompleteRacesAppend(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	storeA, pidA := f.newDedicatedStore(t)
	storeB, pidB := f.newDedicatedStore(t)
	requireDistinctBackendsGRPM(t, map[string]int32{"appender": pidA, "completer": pidB, "witness": f.witnessID})

	var appendBlocks, completeBlocks, staleConflicts atomic.Int64
	for round := 0; round < grpmRaceRounds; round++ {
		roundStr := fmt.Sprintf("race-%d", round)
		revision, token, _ := f.stagedGroupFixture(t, roundStr, "r-a", "r-b", "r-c")
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("race seed append", err)
		}

		releaseAppend := make(chan struct{})
		appendEntered := make(chan struct{})
		var appendOnce sync.Once
		storeA.afterMembershipsInserted = func(context.Context, pgx.Tx) error {
			appendOnce.Do(func() { close(appendEntered) })
			<-releaseAppend
			return nil
		}
		watchB, stopB := f.grpmWatchLockWait(pidB)
		type appendOutcome struct {
			err error
		}
		appendDone := make(chan appendOutcome, 1)
		go func() {
			_, err := storeA.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
				[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupB, "r-b")})
			appendDone <- appendOutcome{err}
		}()
		<-appendEntered

		staleDef, staleMem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
		if err != nil {
			f.fail("race stale digest", err)
		}
		completeDone := make(chan error, 1)
		go func() {
			_, err := storeB.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
				staleDef, staleMem, groups, members, time.Now().UTC())
			completeDone <- err
		}()

		locked := false
		deadline := time.Now().Add(grpmSeamTimeout)
		for !locked && time.Now().Before(deadline) {
			locked = watchB()
			if locked {
				appendBlocks.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(releaseAppend)
		if err := (<-appendDone).err; err != nil {
			f.fail("race append commit", err)
		}
		completeErr := <-completeDone
		stopB()
		if !watchB() {
			t.Fatalf("round %d: the completer was never observed blocked on the header lock -- the race never actually raced", round)
		}
		completeBlocks.Add(1)

		if completeErr == nil {
			state := f.grpmHeaderSnapshot(t, string(revision.ID))
			if !strings.Contains(state, "\x1fCOMPLETE\x1f") {
				t.Fatalf("round %d: completer succeeded on a non-COMPLETE header: %q", round, state)
			}
			if _, _, _, frozenMembers, derr := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID); derr != nil || frozenMembers != members {
				t.Fatalf("round %d: COMPLETE frozen member_count %d != claimed %d", round, frozenMembers, members)
			}
			continue
		}
		if !errors.Is(completeErr, explorer.ErrExplicitGroupRevisionConflict) {
			f.fail("race completion", completeErr)
		}
		staleConflicts.Add(1)
		state := f.grpmHeaderSnapshot(t, string(revision.ID))
		if !strings.Contains(state, "\x1fSTAGING\x1f") {
			t.Fatalf("round %d: conflicted completion left state: %q", round, state)
		}
		if got := f.membershipSnapshot(t, string(revision.ID)); got != grpmRows(
			grpmRow(string(grpmGroupA), "r-a"), grpmRow(string(grpmGroupB), "r-b")) {
			t.Fatalf("round %d: post-race membership set = %q", round, got)
		}
		mustErr(t, "race stale retry", func() error {
			_, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token, strings.Repeat("0", 64), staleMem, groups, members, time.Now().UTC())
			return err
		}, explorer.ErrExplicitGroupRevisionConflict)
		honestDef, honestMem, honestGroups, honestMembers, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
		if err != nil {
			f.fail("race honest digest", err)
		}
		if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
			honestDef, honestMem, honestGroups, honestMembers, time.Now().UTC()); err != nil {
			f.fail("race honest completion", err)
		}
		if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
			honestDef, honestMem, honestGroups, honestMembers, time.Now().UTC()); err != nil {
			f.fail("race idempotent completion", err)
		}
		storeA.afterMembershipsInserted = nil
	}
	if appendBlocks.Load() == 0 {
		t.Fatalf("no round ever witnessed the completer blocked behind the appender's lock -- zero-churn control failed")
	}
	if staleConflicts.Load() == 0 {
		t.Fatalf("no round produced the stale-claim conflict -- the completion never fenced a real append")
	}
}

func TestPostgresExplicitGroupMembershipsOneWayMachineRefusesReversal(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	revision, token, _ := f.stagedGroupFixture(t, "oneway", "r-a", "r-b")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("one-way seed append", err)
	}
	defDigest, memDigest, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("one-way digest", err)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		defDigest, memDigest, groups, members, time.Now().UTC()); err != nil {
		f.fail("one-way completion", err)
	}

	fullReversal := `UPDATE ` + f.revisionTable() +
		` SET state = 'STAGING', definition_digest = '', membership_digest = '',` +
		` group_count = 0, member_count = 0, completed_at = NULL WHERE id = $1`
	stateOnlyReversal := `UPDATE ` + f.revisionTable() + ` SET state = 'STAGING' WHERE id = $1`

	for _, probe := range []struct {
		step string
		sql  string
	}{
		{"full COMPLETE->STAGING reversal", fullReversal},
		{"bare COMPLETE->STAGING state flip", stateOnlyReversal},
	} {
		_, err := f.witness.Exec(f.ctx, probe.sql, string(revision.ID))
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "COMPLETE history and may never be modified",
			probe.step)
		if state := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(state, "\x1fCOMPLETE\x1f") {
			t.Fatalf("%s: refused yet the header reads %q", probe.step, state)
		}
	}

	f.dropTriggerGRPM(t, f.revisionTable(), "explorer_explicit_group_revisions_guard")
	f.execOnGRPM(t, f.witness, "guard-dropped reversal", fullReversal, string(revision.ID))
	if state := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(state, "\x1fSTAGING\x1f") {
		t.Fatalf("guard-dropped reversal did not land (header %q) -- the probe is vacuous", state)
	}

	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupB, "r-b")}); err != nil {
		f.fail("revived append", err)
	}
	revivedDef, revivedMem, revivedGroups, revivedMembers, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("revived digest", err)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		revivedDef, revivedMem, revivedGroups, revivedMembers, time.Now().UTC()); err != nil {
		f.fail("revived completion", err)
	}
	if state := f.grpmHeaderSnapshot(t, string(revision.ID)); !strings.Contains(state, "\x1fCOMPLETE\x1f") {
		t.Fatalf("revived row did not re-complete: %q", state)
	}

	f.rerunMigrationGRPM(t, grpmMigrationVersion)
	for _, probe := range []struct {
		step string
		sql  string
	}{
		{"reversed-after-rerun", fullReversal},
		{"state-flip-after-rerun", stateOnlyReversal},
	} {
		_, err := f.witness.Exec(f.ctx, probe.sql, string(revision.ID))
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "COMPLETE history and may never be modified",
			probe.step)
	}
}

func TestPostgresExplicitGroupMembershipsGuardLocksParentUnderCompletionRace(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	storeB, pidB := f.newDedicatedStore(t)
	rawPool, rawPID := f.newDedicatedPool(t)
	requireDistinctBackendsGRPM(t, map[string]int32{"completer": pidB, "raw": rawPID, "witness": f.witnessID})

	rawInsertMember := func(table string) string {
		return `INSERT INTO ` + table + ` (revision_id, group_id, project, generation, resource_type, resource_id) VALUES ($1, $2, $3, $4, $5, $6)`
	}

	var insertBlocked, deleteBlocked, insertRefused, deleteRefused atomic.Int64
	for round := 0; round < grpmRaceRounds; round++ {
		roundStr := fmt.Sprintf("toctou-%d", round)

		revision, token, _ := f.stagedGroupFixture(t, roundStr+"-i", "r-a", "r-b")
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("toctou insert seed", err)
		}
		honestDef, honestMem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
		if err != nil {
			f.fail("toctou insert digest", err)
		}
		release, completeDone, entered := grpmHoldCompletion(storeB, f.ctx, revision, token, honestDef, honestMem, groups, members)
		<-entered

		watchA, stopA := f.grpmWatchLockWait(rawPID)
		insertErr := make(chan error, 1)
		go func() {
			_, err := rawPool.Exec(f.ctx, rawInsertMember(f.membershipsTable()),
				string(revision.ID), string(grpmGroupB), grpmProject, grpmGeneration, grpmResourceType, "r-b")
			insertErr <- err
		}()
		blocked := false
		deadline := time.Now().Add(grpmSeamTimeout)
		for !blocked && time.Now().Before(deadline) {
			if watchA() {
				blocked = true
				insertBlocked.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(release)
		if err := <-completeDone; err != nil {
			f.fail("toctou insert completion", err)
		}
		err = <-insertErr
		stopA()
		if !blocked || !watchA() {
			t.Fatalf("round %d: direct-SQL INSERT was never observed queued behind the completing header lock -- the race never actually raced", round)
		}
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "only STAGING revisions accept members",
			"direct insert authorized against a stale STAGING parent")
		if got := f.membershipSnapshot(t, string(revision.ID)); got != grpmRows(grpmRow(string(grpmGroupA), "r-a")) {
			t.Fatalf("round %d: refused intruder still changed the set: %q", round, got)
		}
		frozenDef, frozenMem, _, frozenMembers, derr := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
		if derr != nil || frozenDef != honestDef || frozenMem != honestMem || frozenMembers != members {
			t.Fatalf("round %d: COMPLETE frozen facts drifted after the refused insert: %v %v %d (wanted %v %v %d)",
				round, frozenDef, frozenMem, frozenMembers, honestDef, honestMem, members)
		}
		insertRefused.Add(1)

		control, controlToken, _ := f.stagedGroupFixture(t, roundStr+"-ic", "r-a", "r-b")
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, control.ID, controlToken,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("toctou insert control seed", err)
		}
		f.execOnGRPM(t, rawPool, "toctou control insert", rawInsertMember(f.membershipsTable()),
			string(control.ID), string(grpmGroupB), grpmProject, grpmGeneration, grpmResourceType, "r-b")
		if got := f.membershipSnapshot(t, string(control.ID)); got != grpmRows(
			grpmRow(string(grpmGroupA), "r-a"), grpmRow(string(grpmGroupB), "r-b")) {
			t.Fatalf("round %d: no-race control insert did not land: %q", round, got)
		}

		revision2, token2, _ := f.stagedGroupFixture(t, roundStr+"-d", "r-a", "r-b")
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision2.ID, token2,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("toctou delete seed", err)
		}
		dDef, dMem, dGroups, dMembers, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision2.ID)
		if err != nil {
			f.fail("toctou delete digest", err)
		}
		release2, completeDone2, entered2 := grpmHoldCompletion(storeB, f.ctx, revision2, token2, dDef, dMem, dGroups, dMembers)
		<-entered2

		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', $1, false)`, token2); err != nil {
			f.fail("toctou set abort GUC", err)
		}
		watchD, stopD := f.grpmWatchLockWait(rawPID)
		deleteErr := make(chan error, 1)
		go func() {
			_, err := rawPool.Exec(f.ctx, `DELETE FROM `+f.membershipsTable()+` WHERE revision_id = $1`,
				string(revision2.ID))
			deleteErr <- err
		}()
		blocked = false
		deadline = time.Now().Add(grpmSeamTimeout)
		for !blocked && time.Now().Before(deadline) {
			if watchD() {
				blocked = true
				deleteBlocked.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(release2)
		if err := <-completeDone2; err != nil {
			f.fail("toctou delete completion", err)
		}
		err = <-deleteErr
		stopD()
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', '', false)`); err != nil {
			f.fail("toctou reset abort GUC", err)
		}
		if !blocked || !watchD() {
			t.Fatalf("round %d: abort-channel DELETE was never observed queued behind the completing header lock -- the race never actually raced", round)
		}
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "DELETE refused: parent revision",
			"abort-channel DELETE authorized against a stale STAGING parent")
		if got := f.membershipSnapshot(t, string(revision2.ID)); got != grpmRows(grpmRow(string(grpmGroupA), "r-a")) {
			t.Fatalf("round %d: refused abort DELETE still removed rows: %q", round, got)
		}
		if state := f.grpmHeaderSnapshot(t, string(revision2.ID)); !strings.Contains(state, "\x1fCOMPLETE\x1f") {
			t.Fatalf("round %d: raced completion left header %q", round, state)
		}
		deleteRefused.Add(1)

		control2, controlToken2, _ := f.stagedGroupFixture(t, roundStr+"-dc", "r-a")
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, control2.ID, controlToken2,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("toctou delete control seed", err)
		}
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', $1, false)`, controlToken2); err != nil {
			f.fail("toctou control set abort GUC", err)
		}
		f.execOnGRPM(t, rawPool, "toctou control delete",
			`DELETE FROM `+f.membershipsTable()+` WHERE revision_id = $1`, string(control2.ID))
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', '', false)`); err != nil {
			f.fail("toctou control reset abort GUC", err)
		}
		if got := f.membershipSnapshot(t, string(control2.ID)); got != "" {
			t.Fatalf("round %d: no-race control abort DELETE did not land: %q", round, got)
		}

		storeB.beforeCompleteCommit = nil
	}
	if insertBlocked.Load() == 0 || deleteBlocked.Load() == 0 {
		t.Fatalf("no round queued the intruding direct-SQL operation behind the completing header lock (insert %d, delete %d) -- the guard is not locking the parent",
			insertBlocked.Load(), deleteBlocked.Load())
	}
	if insertRefused.Load() == 0 || deleteRefused.Load() == 0 {
		t.Fatalf("no round refused the stale-parent intruder (insert %d, delete %d)",
			insertRefused.Load(), deleteRefused.Load())
	}
}

func (f *grpmFixture) grpmDefinitionCount(t *testing.T, revisionID string) int64 {
	t.Helper()
	var n int64
	if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+f.definitionsTable()+
		` WHERE revision_id = $1`, revisionID).Scan(&n); err != nil {
		f.fail("independent definition count", err)
	}
	return n
}

func TestPostgresExplicitGroupDefinitionsGuardRefusesWritesUnderComplete(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	owner, err := OwnerMigrations()
	if err != nil {
		f.fail("load owner migrations", err)
	}
	var pinned bool
	for _, m := range owner {
		if m.Version == grpmDefinitionsGuardVersion {
			pinned = true
			if m.Name != grpmDefinitionsGuardName {
				t.Fatalf("migration 11 name = %q, want %q", m.Name, grpmDefinitionsGuardName)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256([]byte(m.SQL))); got != grpmDefinitionsGuardChecksum {
				t.Fatalf("000011 embedded bytes drifted: sha256 %s, want %s", got, grpmDefinitionsGuardChecksum)
			}
		}
	}
	if !pinned {
		t.Fatalf("migration %d (%s) missing from OwnerMigrations", grpmDefinitionsGuardVersion, grpmDefinitionsGuardName)
	}

	revision, token, _ := f.stagedGroupFixture(t, "defguard", "r-a", "r-b")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("definition-guard seed append", err)
	}
	defDigest, memDigest, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("definition-guard digest", err)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		defDigest, memDigest, groups, members, time.Now().UTC()); err != nil {
		f.fail("definition-guard completion", err)
	}

	rawDefInsert := func(group string, ordinal int64) error {
		_, err := f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+
			` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
			string(revision.ID), group, "Guard probe", ordinal)
		return err
	}
	rawDefDelete := func(group string) error {
		_, err := f.witness.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+
			` WHERE revision_id = $1 AND group_id = $2`, string(revision.ID), group)
		return err
	}
	if _, err := f.witness.Exec(f.ctx,
		`SELECT set_config('loom.explorer_explicit_group_abort_token', $1, false)`, token); err != nil {
		f.fail("definition-guard set abort GUC", err)
	}
	t.Cleanup(func() {
		_, _ = f.witness.Exec(f.ctx, `SELECT set_config('loom.explorer_explicit_group_abort_token', '', false)`)
	})

	requirePgErrTriggerGRPM(t, rawDefInsert("group-c", 2), grpmCheckViolation,
		"only STAGING revisions accept definitions", "definition INSERT under COMPLETE")
	requirePgErrTriggerGRPM(t, rawDefDelete(string(grpmGroupB)), grpmCheckViolation,
		"DELETE of definition", "definition DELETE under COMPLETE")
	if n := f.grpmDefinitionCount(t, string(revision.ID)); n != 2 {
		t.Fatalf("refused probes changed the stored definition count: %d", n)
	}

	f.dropTriggerGRPM(t, f.definitionsTable(), "explorer_explicit_group_definitions_guard")
	f.execOnGRPM(t, f.witness, "guard-dropped definition insert",
		`INSERT INTO `+f.definitionsTable()+` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		string(revision.ID), "group-c", "Guard probe", int64(2))
	f.execOnGRPM(t, f.witness, "guard-dropped definition delete",
		`DELETE FROM `+f.definitionsTable()+` WHERE revision_id = $1 AND group_id = $2`,
		string(revision.ID), string(grpmGroupB))
	if n := f.grpmDefinitionCount(t, string(revision.ID)); n != 2 {
		t.Fatalf("guard-dropped probes did not land as expected: %d rows", n)
	}
	var frozenCount int64
	if err := f.witness.QueryRow(f.ctx, `SELECT group_count FROM `+f.revisionTable()+
		` WHERE id = $1`, string(revision.ID)).Scan(&frozenCount); err != nil {
		f.fail("read frozen group_count", err)
	}
	if frozenCount != 2 {
		t.Fatalf("frozen group_count drifted under guard drop: %d", frozenCount)
	}

	f.rerunMigrationGRPM(t, grpmDefinitionsGuardVersion)
	requirePgErrTriggerGRPM(t, rawDefInsert("group-d", 3), grpmCheckViolation,
		"only STAGING revisions accept definitions", "definition INSERT after 000011 rerun")
	requirePgErrTriggerGRPM(t, rawDefDelete(string(grpmGroupA)), grpmCheckViolation,
		"DELETE of definition", "definition DELETE after 000011 rerun")
}

func TestPostgresExplicitGroupDefinitionsGuardLocksParentUnderCompletionRace(t *testing.T) {
	f := newGRPMFixture(t)
	f.migrateWithSelections(t)

	storeB, pidB := f.newDedicatedStore(t)
	rawPool, rawPID := f.newDedicatedPool(t)
	requireDistinctBackendsGRPM(t, map[string]int32{"completer": pidB, "raw": rawPID, "witness": f.witnessID})

	defCount := func(revisionID string) int64 {
		var n int64
		if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+f.definitionsTable()+
			` WHERE revision_id = $1`, revisionID).Scan(&n); err != nil {
			f.fail("independent definition count", err)
		}
		return n
	}
	frozenGroupCount := func(revisionID string) int64 {
		var n int64
		if err := f.witness.QueryRow(f.ctx, `SELECT group_count FROM `+f.revisionTable()+
			` WHERE id = $1`, revisionID).Scan(&n); err != nil {
			f.fail("independent group_count read", err)
		}
		return n
	}

	var insertBlocked, deleteBlocked, insertRefused, deleteRefused atomic.Int64
	for round := 0; round < grpmRaceRounds; round++ {
		roundStr := fmt.Sprintf("toctou-def-%d", round)

		revision, token, _ := f.stagedGroupFixture(t, roundStr+"-i", "r-a")
		defDigest, memDigest, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
		if err != nil {
			f.fail("def race insert digest", err)
		}
		release, completeDone, entered := grpmHoldCompletion(storeB, f.ctx, revision, token, defDigest, memDigest, groups, members)
		<-entered

		watchA, stopA := f.grpmWatchLockWait(rawPID)
		insertErr := make(chan error, 1)
		go func() {
			_, err := rawPool.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+
				` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
				string(revision.ID), "group-c", "Race intruder", int64(2))
			insertErr <- err
		}()
		blocked := false
		deadline := time.Now().Add(grpmSeamTimeout)
		for !blocked && time.Now().Before(deadline) {
			if watchA() {
				blocked = true
				insertBlocked.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(release)
		if err := <-completeDone; err != nil {
			f.fail("def race insert completion", err)
		}
		err = <-insertErr
		stopA()
		if !blocked || !watchA() {
			t.Fatalf("round %d: definition INSERT never queued behind the completing header lock -- the race never actually raced", round)
		}
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "only STAGING revisions accept definitions",
			"definition INSERT authorized against a stale STAGING parent")
		revisionID := string(revision.ID)
		if n := defCount(revisionID); n != 2 {
			t.Fatalf("round %d: refused definition INSERT changed the stored count: %d", round, n)
		}
		if frozen, stored := frozenGroupCount(revisionID), defCount(revisionID); frozen != stored || frozen != 2 {
			t.Fatalf("round %d: served-state drift -- frozen group_count %d vs stored definitions %d", round, frozen, stored)
		}
		insertRefused.Add(1)

		control, _, _ := f.stagedGroupFixture(t, roundStr+"-ic", "r-a")
		f.execOnGRPM(t, rawPool, "def race control insert", `INSERT INTO `+f.definitionsTable()+
			` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
			string(control.ID), "group-c", "Race control", int64(2))
		if n := defCount(string(control.ID)); n != 3 {
			t.Fatalf("round %d: no-race control INSERT did not land: %d rows", round, n)
		}

		revision2, token2, _ := f.stagedGroupFixture(t, roundStr+"-d", "r-a")
		dDef, dMem, dGroups, dMembers, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision2.ID)
		if err != nil {
			f.fail("def race delete digest", err)
		}
		release2, completeDone2, entered2 := grpmHoldCompletion(storeB, f.ctx, revision2, token2, dDef, dMem, dGroups, dMembers)
		<-entered2

		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', $1, false)`, token2); err != nil {
			f.fail("def race set abort GUC", err)
		}
		watchD, stopD := f.grpmWatchLockWait(rawPID)
		deleteErr := make(chan error, 1)
		go func() {
			_, err := rawPool.Exec(f.ctx, `DELETE FROM `+f.definitionsTable()+
				` WHERE revision_id = $1 AND group_id = $2`, string(revision2.ID), string(grpmGroupB))
			deleteErr <- err
		}()
		blocked = false
		deadline = time.Now().Add(grpmSeamTimeout)
		for !blocked && time.Now().Before(deadline) {
			if watchD() {
				blocked = true
				deleteBlocked.Add(1)
			}
			time.Sleep(2 * time.Millisecond)
		}
		close(release2)
		if err := <-completeDone2; err != nil {
			f.fail("def race delete completion", err)
		}
		err = <-deleteErr
		stopD()
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', '', false)`); err != nil {
			f.fail("def race reset abort GUC", err)
		}
		if !blocked || !watchD() {
			t.Fatalf("round %d: abort-channel definition DELETE never queued behind the completing header lock -- the race never actually raced", round)
		}
		requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "DELETE of definition",
			"definition DELETE authorized against a stale STAGING parent")
		revisionID2 := string(revision2.ID)
		if frozen, stored := frozenGroupCount(revisionID2), defCount(revisionID2); frozen != stored || frozen != 2 {
			t.Fatalf("round %d: E1 drift -- frozen group_count %d vs stored definitions %d", round, frozen, stored)
		}
		deleteRefused.Add(1)

		control2, controlToken2, _ := f.stagedGroupFixture(t, roundStr+"-dc", "r-a")
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', $1, false)`, controlToken2); err != nil {
			f.fail("def race control set abort GUC", err)
		}
		f.execOnGRPM(t, rawPool, "def race control delete", `DELETE FROM `+f.definitionsTable()+
			` WHERE revision_id = $1 AND group_id = $2`, string(control2.ID), string(grpmGroupB))
		if _, err := rawPool.Exec(f.ctx,
			`SELECT set_config('loom.explorer_explicit_group_abort_token', '', false)`); err != nil {
			f.fail("def race control reset abort GUC", err)
		}
		if n := defCount(string(control2.ID)); n != 1 {
			t.Fatalf("round %d: no-race control abort DELETE did not land: %d rows", round, n)
		}

		storeB.beforeCompleteCommit = nil
	}
	if insertBlocked.Load() == 0 || deleteBlocked.Load() == 0 {
		t.Fatalf("no round queued the intruding definition statement behind the completing header lock (insert %d, delete %d) -- the guard is not locking the parent",
			insertBlocked.Load(), deleteBlocked.Load())
	}
	if insertRefused.Load() == 0 || deleteRefused.Load() == 0 {
		t.Fatalf("no round refused the stale-parent definition intruder (insert %d, delete %d)",
			insertRefused.Load(), deleteRefused.Load())
	}
}

func TestPostgresExplicitGroupMembershipsMigrationLedgerConvergence(t *testing.T) {
	f := newGRPMFixture(t)
	result := f.migrateWithSelections(t)

	var versions []int64
	for _, rec := range result.Applied {
		versions = append(versions, rec.Version)
	}
	wantVersions := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if len(versions) != len(wantVersions) {
		t.Fatalf("ledger versions = %v, want %v", versions, wantVersions)
	}
	for i := range wantVersions {
		if versions[i] != wantVersions[i] {
			t.Fatalf("ledger versions = %v, want %v", versions, wantVersions)
		}
	}
	for _, rec := range result.Applied {
		switch rec.Version {
		case grpmMigrationVersion:
			if rec.Name != grpmMigrationName {
				t.Fatalf("ledger version %d name = %q, want %q", rec.Version, rec.Name, grpmMigrationName)
			}
			if rec.Checksum != grpmMigrationChecksum {
				t.Fatalf("ledger checksum for %d = %s, want the frozen pin %s",
					rec.Version, rec.Checksum, grpmMigrationChecksum)
			}
		case grpmSelectionsFixtureVersion:
			if rec.Checksum != grpmSelectionsFixtureChecksum {
				t.Fatalf("ledger checksum for %d = %s, want the frozen pin %s",
					rec.Version, rec.Checksum, grpmSelectionsFixtureChecksum)
			}
		}
	}

	migrations, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations for convergence", err)
	}
	var found bool
	for _, m := range migrations {
		if m.Version == grpmMigrationVersion {
			found = true
			sum := sha256.Sum256([]byte(m.SQL))
			if hex.EncodeToString(sum[:]) != grpmMigrationChecksum || m.Name != grpmMigrationName {
				t.Fatalf("embedded membership migration drifted: name %q checksum %s", m.Name, hex.EncodeToString(sum[:]))
			}
		}
	}
	if !found {
		t.Fatal("the membership migration is absent from the embedded owner history")
	}

	client, err := pgstore.Open(f.ctx, pgstore.Config{DSN: f.dsn, MaxConns: 2, MinConns: 0, AcquireTimeout: 10 * time.Second})
	if err != nil {
		f.fail("open rerun pool", err)
	}
	defer client.Close()
	rerunHistory, err := OwnerMigrations()
	if err != nil {
		f.fail("owner migrations for rerun", err)
	}
	runner, err := pgstore.NewMigrationRunner(client, f.schema, rerunHistory)
	if err != nil {
		f.fail("new rerun runner", err)
	}
	rerun, err := runner.Migrate(f.ctx)
	if err != nil {
		f.fail("convergent rerun", err)
	}
	if len(rerun.Applied) != len(result.Applied) {
		t.Fatalf("rerun ledger length = %d, want %d", len(rerun.Applied), len(result.Applied))
	}
	var ledgerRows int
	if err := f.witness.QueryRow(f.ctx, `SELECT count(*) FROM `+sanitizeTableRef(f.schema, "schema_migrations")).Scan(&ledgerRows); err != nil {
		f.fail("count ledger rows", err)
	}
	if ledgerRows != len(result.Applied) {
		t.Fatalf("ledger holds %d rows, want %d after a convergent rerun", ledgerRows, len(result.Applied))
	}

	revision, token, _ := f.stagedGroupFixture(t, "converge", "r-a")
	if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
		[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
		f.fail("seed append before guard probe", err)
	}
	_, err = f.witness.Exec(f.ctx, `DELETE FROM `+f.membershipsTable()+` WHERE revision_id = $1`, string(revision.ID))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "owner-abort or expired-writer-cleanup",
		"membership DELETE after convergent rerun")
}

func TestZZZeroLeftoverVerifyGrpmFixtures(t *testing.T) {
	raw := pgstate06TestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for leftover audit: %v", err)
	}
	defer admin.Close()
	var dbLeftovers int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\_grpm\_%' ESCAPE '\'`).Scan(&dbLeftovers); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if dbLeftovers != 0 {
		t.Fatalf("%d verify_grpm_* databases survive the suite", dbLeftovers)
	}
	if owned := grpmSnapshotRegistry(); len(owned) != 0 {
		t.Fatalf("creation registry still holds %d entries: %v", len(owned), owned)
	}
}
