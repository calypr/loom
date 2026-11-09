package postgres

// Real PostgreSQL 18 integration for pg-state-immutable-capability-
// receipts-v1. Everything here runs against the service named by
// LOOM_TEST_POSTGRES_URL: each test creates one uniquely-named isolated
// database (pgstate03_it_*) plus one owned schema (pgstate03_it_*) inside it
// and drops both in its cleanups, so the suite leaves zero leftovers. The
// fixture history is composed through the real pg-store-02 runner from this
// package's embedded owner history (ledger bootstrap + explorer owner, plus
// the sibling draft-revision migration once it lands) and this ticket's
// state-scoped own-dir fixture tests/migrations-capabilities/000004_
// capability_artifacts.sql; the ledger rows are asserted literally.
// Expected rows are asserted literally through direct SQL on a witness pool
// that is a third, physically distinct backend connection, and concurrent
// put proofs run over two single-connection clients with provably distinct
// pg_backend_pid values. Immutability is witnessed at the database: every
// violating direct write must fail with the guard in place and -- guard-
// drop probe -- the same statement must succeed once the trigger is
// dropped, proving the guard (not an accident) blocked it.
//
// Skip discipline: no test in this file runs green on a skip -- every
// assertion requires the live service, and a run presented as acceptance
// evidence fails if any test here skipped. Note the CI wiring honestly: the
// go-unit lane runs this package WITHOUT LOOM_TEST_POSTGRES_URL, so its
// PG-gated tests skip green there, and the pg-fixture job
// (scripts/run-postgres-integration.mjs) does not yet cover
// ./internal/explorer/postgres; extending it is a shared-infra change held
// for the coordinator. This suite's acceptance is therefore live PG18 runs
// (command, full log, and exit status attached to the PR evidence).
// Concurrency tests run serially (no t.Parallel anywhere). The TestZZ
// zero-leftover audit is ownership-scoped to pgstate03_it_* only: sibling
// suites sweep their own prefixes in their own files, and concurrent
// sibling sweeps must never share a global scan.

import (
	"bytes"
	"context"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

const (
	pgstate03FixtureDir    = "tests/migrations-capabilities"
	pgstate03FixtureFile   = "000004_capability_artifacts"
	pgstate03ImmutableCode = "45501"
)

var pgstate03ResourceNamePattern = regexp.MustCompile(`^pgstate03_it_[0-9]+_[0-9a-f]+$`)

// capabilityService owns one isolated database and one owned schema inside
// it, both named pgstate03_it_*, created for exactly one test and removed by
// that test's cleanups. Three single-connection clients give two real writer
// backends (storeA over clientA, storeB over clientB) plus a third backend
// for direct-SQL witnesses. Nothing outside these resources is touched;
// TestZZNoLeftoverPgstate03Resources audits that none survive.
type capabilityService struct {
	ctx     context.Context
	admin   *pgxpool.Pool // service database
	witness *pgstore.Client
	pidW    int32
	pidA    int32
	pidB    int32
	schema  string
	scrub   func(any) string
	fail    func(step string, err error)

	snapshots   *CapabilitySnapshotStore
	snapshotsB  *CapabilitySnapshotStore
	receipts    *TableShapeCapabilityStore
	receiptsB   *TableShapeCapabilityStore
	owner       []pgstore.Migration
	fixture     pgstore.Migration
	ledgerAfter []pgstore.LedgerRecord
}

func newCapabilityService(t *testing.T) *capabilityService {
	t.Helper()
	raw := testPostgresURL(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate03_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate03ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := admin.Exec(cleanupCtx,
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	// pgconn lets a dbname query parameter outrank the URL path; drop any
	// dbname the service URL carries so isolation is provable.
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	// Each client is capped at one physical connection, and pgstore.Open
	// pings the server and refuses anything but PostgreSQL 18, so the
	// witness, writer A, and writer B are provably three distinct PG18
	// backends.
	newClient := func(label string) *pgstore.Client {
		client, err := pgstore.Open(ctx, pgstore.Config{
			DSN:            dsn,
			MaxConns:       1,
			MinConns:       0,
			AcquireTimeout: 30 * time.Second,
		})
		if err != nil {
			fail("open client "+label, err)
		}
		t.Cleanup(client.Close)
		return client
	}
	witness := newClient("witness")
	clientA := newClient("writer-a")
	clientB := newClient("writer-b")

	schema := fmt.Sprintf("pgstate03_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate03ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if err := execOn(ctx, witness, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := execOn(cleanupCtx, witness, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &capabilityService{
		ctx: ctx, admin: admin, witness: witness,
		schema: schema, scrub: scrub, fail: fail,
	}
	s.migrate(t)
	s.pidW = s.backendPID(t, witness, "witness")
	s.pidA = s.backendPID(t, clientA, "writer A")
	s.pidB = s.backendPID(t, clientB, "writer B")
	if s.pidA == s.pidB || s.pidA == s.pidW || s.pidB == s.pidW {
		t.Fatalf("writer and witness backends are not distinct physical connections: A=%d B=%d W=%d", s.pidA, s.pidB, s.pidW)
	}

	snapshots, err := NewCapabilitySnapshotStore(clientA, schema)
	if err != nil {
		fail("build snapshot store A", err)
	}
	s.snapshots = snapshots
	snapshotsB, err := NewCapabilitySnapshotStore(clientB, schema)
	if err != nil {
		fail("build snapshot store B", err)
	}
	s.snapshotsB = snapshotsB
	receipts, err := NewTableShapeCapabilityStore(clientA, schema)
	if err != nil {
		fail("build receipt store A", err)
	}
	s.receipts = receipts
	receiptsB, err := NewTableShapeCapabilityStore(clientB, schema)
	if err != nil {
		fail("build receipt store B", err)
	}
	s.receiptsB = receiptsB
	return s
}

func execOn(ctx context.Context, client *pgstore.Client, sql string, args ...any) error {
	conn, err := client.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, sql, args...)
	return err
}

func queryRowOn[T any](ctx context.Context, client *pgstore.Client, dest *T, sql string, args ...any) error {
	conn, err := client.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	return conn.QueryRow(ctx, sql, args...).Scan(dest)
}

// backendPID asks one client (a single physical connection) to name its
// server backend.
func (s *capabilityService) backendPID(t *testing.T, client *pgstore.Client, label string) int32 {
	t.Helper()
	var pid int32
	if err := queryRowOn(s.ctx, client, &pid, `SELECT pg_backend_pid()`); err != nil {
		s.fail("backend pid "+label, err)
	}
	if pid <= 0 {
		t.Fatalf("%s backend pid = %d, want a positive server pid", label, pid)
	}
	return pid
}

// migrate composes and applies the owned history -- this package's embedded
// owner history (ledger bootstrap + explorer owner; the sibling draft-
// revision migration when present) plus this ticket's state-scoped own-dir
// capability fixture -- through the real pg-store-02 runner, and asserts the
// ledger literally. The capability tables live in the SAME Explorer control-
// plane schema, extending the same ledger: no competing history, no
// duplicated ledger bootstrap.
func (s *capabilityService) migrate(t *testing.T) {
	t.Helper()
	owner, err := OwnerMigrations()
	if err != nil {
		s.fail("owner migrations", err)
	}
	if len(owner) < 2 || owner[0].Version != 1 || owner[0].Name != "ledger" ||
		owner[1].Version != 2 || owner[1].Name != "explorer_owner" {
		t.Fatalf("owner history = %+v, want ledger(1) + explorer_owner(2) at the front", owner)
	}
	fixture, err := pgstore.LoadMigrations(os.DirFS("../../../"), pgstate03FixtureDir)
	if err != nil {
		s.fail("load capability fixture migrations", err)
	}
	if len(fixture) != 1 || fixture[0].Version != 4 || !strings.HasPrefix(fixture[0].Name, "capability_artifacts") {
		t.Fatalf("capability fixture history = %+v, want exactly version 4 capability_artifacts", fixture)
	}
	history := append(append([]pgstore.Migration{}, owner...), fixture[0])
	runner, err := pgstore.NewMigrationRunner(s.witness, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate capability history", err)
	}
	s.owner = owner
	s.fixture = fixture[0]
	s.ledgerAfter = result.Applied

	if result.LatestVersion != 4 {
		t.Fatalf("latest migration version = %d, want 4", result.LatestVersion)
	}
	want := []pgstore.LedgerRecord{
		{Version: 1, Name: "ledger", Checksum: owner[0].Checksum},
		{Version: 2, Name: "explorer_owner", Checksum: owner[1].Checksum},
	}
	for _, m := range owner[2:] {
		want = append(want, pgstore.LedgerRecord{Version: m.Version, Name: m.Name, Checksum: m.Checksum})
	}
	want = append(want, pgstore.LedgerRecord{Version: 4, Name: fixture[0].Name, Checksum: fixture[0].Checksum})
	if len(result.Applied) != len(want) {
		t.Fatalf("applied history = %+v, want %+v", result.Applied, want)
	}
	for i, w := range want {
		got := result.Applied[i]
		if got.Version != w.Version || got.Name != w.Name || got.Checksum != w.Checksum {
			t.Fatalf("applied[%d] = {Version:%d Name:%q Checksum:%q}, want {Version:%d Name:%q Checksum:%q}",
				i, got.Version, got.Name, got.Checksum, w.Version, w.Name, w.Checksum)
		}
	}
	for i := 1; i < len(result.Applied); i++ {
		if result.Applied[i-1].Version >= result.Applied[i].Version {
			t.Fatalf("ledger not strictly ascending: %+v", result.Applied)
		}
	}
}

// ---------------------------------------------------------------------------
// direct-SQL witnesses (test's own client, never a store connection)
// ---------------------------------------------------------------------------

func (s *capabilityService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *capabilityService) mustWitnessExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if err := execOn(s.ctx, s.witness, sql, args...); err != nil {
		s.fail("witness exec "+sql, err)
	}
}

func (s *capabilityService) witnessExecErr(sql string, args ...any) error {
	return execOn(s.ctx, s.witness, sql, args...)
}

func (s *capabilityService) mustWitnessBool(t *testing.T, sql string, args ...any) bool {
	t.Helper()
	var value bool
	if err := queryRowOn(s.ctx, s.witness, &value, sql, args...); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *capabilityService) mustWitnessText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := queryRowOn(s.ctx, s.witness, &value, sql, args...); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *capabilityService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := queryRowOn(s.ctx, s.witness, &n, sql, args...); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

// forEachWitnessText collects single-column text rows through the witness
// backend, holding the (single-connection) pool only for the query's full
// lifecycle: the connection is released when the scan completes.
func (s *capabilityService) forEachWitnessText(t *testing.T, dest *[]string, sql string, args ...any) {
	t.Helper()
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire text witness", err)
	}
	defer conn.Release()
	rows, err := conn.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("query text witness "+sql, err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			s.fail("scan text witness "+sql, err)
		}
		*dest = append(*dest, value)
	}
	if err := rows.Err(); err != nil {
		s.fail("iterate text witness "+sql, err)
	}
}

// snapshotRow is the literal content of one capability_snapshots row.
type snapshotRow struct {
	token       string
	identity    capability.SnapshotIdentity
	status      string
	complete    bool
	truncated   bool
	payloadText string
	ctid        string
	exists      bool
}

// witnessSnapshot reads one capability_snapshots row literally through the
// witness backend: every mirror column, the full stored jsonb text, and the
// physical ctid (which changes on any UPDATE or re-INSERT).
func (s *capabilityService) witnessSnapshot(token string) snapshotRow {
	var w snapshotRow
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire snapshot witness", err)
	}
	defer conn.Release()
	var project, generation, authDigest, cacheVersion, schemaDigest, inventoryDigest,
		relationshipDigest, fieldDigest, shapeDigest, protocolVersion, compilerVersion,
		traversalVersion, projectionVersion string
	err = conn.QueryRow(s.ctx,
		`SELECT token, project, generation, authorization_scope_digest, cache_version, schema_digest,
		        resource_inventory_digest, relationship_digest, field_digest, shape_digest,
		        protocol_version, compiler_version, traversal_policy_version, projection_policy_version,
		        status, complete, truncated, payload::text, ctid::text
		 FROM `+s.qualified(CapabilitySnapshotsTable)+` WHERE token = $1`, token).
		Scan(&w.token, &project, &generation, &authDigest, &cacheVersion, &schemaDigest,
			&inventoryDigest, &relationshipDigest, &fieldDigest, &shapeDigest,
			&protocolVersion, &compilerVersion, &traversalVersion, &projectionVersion,
			&w.status, &w.complete, &w.truncated, &w.payloadText, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness snapshot "+token, err)
	}
	w.exists = true
	w.identity = capability.SnapshotIdentity{
		Project: project, Generation: generation, AuthorizationScopeDigest: authDigest,
		CacheVersion: cacheVersion, SchemaDigest: schemaDigest, ResourceInventoryDigest: inventoryDigest,
		RelationshipDigest: relationshipDigest, FieldDigest: fieldDigest, ShapeDigest: shapeDigest,
		ProtocolVersion: protocolVersion, CompilerVersion: compilerVersion,
		TraversalPolicyVersion: traversalVersion, ProjectionPolicyVersion: projectionVersion,
	}
	return w
}

func (s *capabilityService) assertSnapshotAbsent(t *testing.T, token string) {
	t.Helper()
	if w := s.witnessSnapshot(token); w.exists {
		t.Fatalf("expected no capability_snapshots row for %s, found %+v", token, w)
	}
}

// assertSnapshotLiteral witnesses the stored row against the exact expected
// snapshot: identity/status mirrors, and jsonb equality against the
// canonical full document (so every nested slice, audit array, details map
// entry, and the token itself is pinned).
func (s *capabilityService) assertSnapshotLiteral(t *testing.T, want capability.Snapshot) {
	t.Helper()
	document, err := capabilitySnapshotDocument(want)
	if err != nil {
		t.Fatal(err)
	}
	w := s.witnessSnapshot(want.Token)
	if !w.exists {
		t.Fatalf("capability snapshot %s does not exist", want.Token)
	}
	if w.token != want.Token || w.identity != want.Identity ||
		w.status != string(want.Status) || w.complete != want.Complete || w.truncated != want.Truncated {
		t.Fatalf("snapshot mirror columns = {token:%s identity:%+v status:%s complete:%v truncated:%v}, want {token:%s identity:%+v status:%s complete:%v truncated:%v}",
			w.token, w.identity, w.status, w.complete, w.truncated,
			want.Token, want.Identity, want.Status, want.Complete, want.Truncated)
	}
	if !s.mustWitnessBool(t,
		`SELECT payload = $1::jsonb FROM `+s.qualified(CapabilitySnapshotsTable)+` WHERE token = $2`,
		string(document), want.Token) {
		t.Fatalf("snapshot %s payload does not equal the literal canonical document\nwant: %s\ngot:  %s",
			want.Token, document, w.payloadText)
	}
}

// witnessedReceipt is the literal content of one table_shape_capabilities row.
type witnessedReceipt struct {
	id, kind, parentCatalogID string
	binding                   tableshapecap.Binding
	contentDigest, createdAt  string
	payloadText               string
	ctid                      string
	exists                    bool
}

func (s *capabilityService) witnessReceipt(id string) witnessedReceipt {
	var w witnessedReceipt
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire receipt witness", err)
	}
	defer conn.Release()
	var draftVersion int64
	err = conn.QueryRow(s.ctx,
		`SELECT id, kind, parent_catalog_id, project, explorer_id, output_id, snapshot_token,
		        authorization_scope, source_generation, draft_version, draft_digest,
		        base_document_digest, base_compilation_receipt_id, output_fingerprint,
		        compiler_schema_digest, content_digest, created_at, payload::text, ctid::text
		 FROM `+s.qualified(TableShapeCapabilitiesTable)+` WHERE id = $1`, id).
		Scan(&w.id, &w.kind, &w.parentCatalogID,
			&w.binding.Project, &w.binding.ExplorerID, &w.binding.OutputID, &w.binding.SnapshotToken,
			&w.binding.AuthorizationScope, &w.binding.SourceGeneration, &draftVersion,
			&w.binding.DraftDigest, &w.binding.BaseDocumentDigest, &w.binding.BaseCompilationReceiptID,
			&w.binding.OutputFingerprint, &w.binding.CompilerSchemaDigest,
			&w.contentDigest, &w.createdAt, &w.payloadText, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness receipt "+id, err)
	}
	w.exists = true
	w.binding.DraftVersion = uint64(draftVersion)
	return w
}

func (s *capabilityService) assertReceiptAbsent(t *testing.T, id string) {
	t.Helper()
	if w := s.witnessReceipt(id); w.exists {
		t.Fatalf("expected no table_shape_capabilities row for %s, found %+v", id, w)
	}
}

func (s *capabilityService) assertReceiptLiteral(t *testing.T, id, kind, parentCatalogID string, binding tableshapecap.Binding, contentDigest, createdAt string, want any) {
	t.Helper()
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	w := s.witnessReceipt(id)
	if !w.exists {
		t.Fatalf("receipt %s does not exist", id)
	}
	if w.id != id || w.kind != kind || w.parentCatalogID != parentCatalogID ||
		w.binding != binding || w.contentDigest != contentDigest || w.createdAt != createdAt {
		t.Fatalf("receipt %s mirror columns = %+v, want {id:%s kind:%s parent:%s binding:%+v digest:%s created:%s}",
			id, w, id, kind, parentCatalogID, binding, contentDigest, createdAt)
	}
	if !s.mustWitnessBool(t,
		`SELECT payload = $1::jsonb FROM `+s.qualified(TableShapeCapabilitiesTable)+` WHERE id = $2`,
		string(payload), id) {
		t.Fatalf("receipt %s payload does not equal the literal expected artifact\nwant: %s\ngot:  %s", id, payload, w.payloadText)
	}
}

// triggerPresent witnesses the database-level immutability guard by name.
func (s *capabilityService) triggerPresent(t *testing.T, trigger, table string) bool {
	t.Helper()
	var present bool
	err := queryRowOn(s.ctx, s.witness, &present,
		`SELECT EXISTS (
		   SELECT 1 FROM pg_trigger tg
		   JOIN pg_class c ON c.oid = tg.tgrelid
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE tg.tgname = $1 AND c.relname = $2 AND n.nspname = $3
		    AND NOT tg.tgisinternal)`,
		trigger, table, s.schema)
	if err != nil {
		s.fail("trigger catalog witness "+trigger, err)
	}
	return present
}

// guardDropProbe runs a violating statement through the witness backend
// under three conditions: it must fail with the guard in place (SQLSTATE
// 45501), it must succeed with the trigger dropped -- proving the guard,
// not an accident, blocked it (the pg-catalog-04 non-vacuity bar) -- and it
// must fail again once the trigger is restored. reseed, when non-nil, runs
// after the successful unguarded write to restore the disposable probe row
// (needed for repeatable DELETE probes).
func (s *capabilityService) guardDropProbe(t *testing.T, trigger, table string, reseed func(), stmt string, args ...any) {
	t.Helper()
	tableFQN := s.qualified(table)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("immutability trigger %s missing from %s before probe", trigger, table)
	}
	err := s.witnessExecErr(stmt, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate03ImmutableCode {
		t.Fatalf("guarded violating write on %s = %v, want SQLSTATE %s", table, s.scrub(err), pgstate03ImmutableCode)
	}
	s.mustWitnessExec(t, `DROP TRIGGER `+trigger+` ON `+tableFQN)
	if s.triggerPresent(t, trigger, table) {
		t.Fatalf("trigger %s still present after DROP", trigger)
	}
	conn, acquireErr := s.witness.Acquire(s.ctx)
	if acquireErr != nil {
		s.fail("acquire unguarded probe connection", acquireErr)
	}
	tag, execErr := conn.Exec(s.ctx, stmt, args...)
	conn.Release()
	if execErr != nil || tag.RowsAffected() != 1 {
		t.Fatalf("same violating write ran with the trigger dropped on %s: tag=%v err=%v; the guard was not what blocked it",
			table, tag, s.scrub(execErr))
	}
	if reseed != nil {
		reseed()
	}
	s.mustWitnessExec(t, `CREATE TRIGGER `+trigger+` BEFORE UPDATE OR DELETE ON `+tableFQN+
		` FOR EACH ROW EXECUTE FUNCTION `+s.qualified("loom_capability_artifact_immutable")+`()`)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("immutability trigger %s not restored on %s", trigger, table)
	}
	err = s.witnessExecErr(stmt, args...)
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate03ImmutableCode {
		t.Fatalf("violating write after trigger restore = %v, want SQLSTATE %s", s.scrub(err), pgstate03ImmutableCode)
	}
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func ptr[T any](value T) *T { return &value }

// richSnapshot builds a READY snapshot exercising every nested payload
// family: availability with readiness + observed fields (examples,
// semantic observations), nodes/edges/candidates with operation slices,
// aggregate operations, concept candidates, repeated boundaries, audit
// arrays, and diagnostics with a details map.
func richSnapshot(project, generation, token string) capability.Snapshot {
	identity := capability.SnapshotIdentity{
		Project: project, Generation: generation,
		AuthorizationScopeDigest: "sha256:scope-" + token,
		CacheVersion:             "v2",
		SchemaDigest:             "sha256:schema-" + token,
		ResourceInventoryDigest:  "sha256:inventory-" + token,
		RelationshipDigest:       "sha256:relationship-" + token,
		FieldDigest:              "sha256:field-" + token,
		ShapeDigest:              "sha256:shape-" + token,
		ProtocolVersion:          "proto-3", CompilerVersion: "compiler-9",
		TraversalPolicyVersion: "traversal-2", ProjectionPolicyVersion: "projection-4",
	}
	policy := capability.Policy{
		Route:      capability.RoutePolicy{Version: "route-1", MaxHops: 4, AllowsRepeatedEdges: true, AllowsSelfLoops: false},
		Projection: capability.ProjectionPolicy{Version: "projection-1", Modes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionArray, capability.ProjectionDistinctArray}, SuggestionLimit: 5},
	}
	nodes := []capability.Node{
		{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "document", Populated: true, DocumentCount: 7, SupportedOperations: []capability.Operation{capability.OperationSelect, capability.OperationFilter}},
		{ID: "node-obs", ResourceType: "Observation", Populated: true, DocumentCount: 3, SupportedOperations: []capability.Operation{capability.OperationChart}, CapabilityVersion: "cap-1"},
	}
	edges := []capability.Edge{
		{ID: "edge-1", FromNodeID: "node-patient", ToNodeID: "node-obs", Label: "has", SourceResourceType: "Patient", TargetResourceType: "Observation", ObservedEdgeCount: 5, AllowsRepeatedTarget: true},
	}
	candidates := []capability.Candidate{
		{
			ID: "cand-1", NodeID: "node-obs", ResourceType: "Observation", FieldPath: "value.codeableConcept.text",
			Label: "Code text", LogicalType: "string", Cardinality: "many",
			RepeatedBoundaries:    []capability.RepeatedBoundary{{Path: "value.codeableConcept", MaxItems: 3}},
			ProjectionModes:       []capability.ProjectionMode{capability.ProjectionScalar},
			FilterOperators:       []capability.FilterOperator{capability.FilterEquals, capability.FilterIn},
			ChartAggregations:     []capability.ChartAggregation{capability.ChartCount},
			SupportedOperations:   []capability.Operation{capability.OperationSort},
			AggregateOperations:   []capability.AggregateOperationCapability{{Operation: capability.AggregateCount, RowContext: capability.AggregateRowsRecords, Supported: true}},
			Observed:              true,
			ObservedDocumentCount: 2, Populated: true,
			SuggestedValues:     []string{"alpha", "", "zulu"},
			SuggestionsComplete: false, SuggestionsTruncated: true,
			ConceptCandidates: []capability.ConceptCandidate{
				{SourceResourceType: "Observation", SourcePath: "category", Code: "vitals", Display: "Vital", Completeness: "PARTIAL", Status: "UNRESOLVED", Population: 1, Examples: []string{"x"}, ObservedUnits: []string{"kg"}, ObservedUnitsTruncated: true},
			},
		},
	}
	diagnostics := []capability.Diagnostic{
		{Severity: "WARNING", Stage: "scan", Code: "TRUNCATED_EXAMPLES", Message: "examples truncated", Details: map[string]any{"limit": float64(2), "note": "probe"}, Retryable: true},
	}
	snapshot := capability.NewSnapshot(identity, policy, capability.StatusReady, true, false, nodes, edges, candidates, diagnostics)
	snapshot.Availability = &capability.Availability{
		Readiness: capability.AvailabilityReadiness{
			Status: "READY", ResourceInventory: capability.AvailabilitySourceReadiness{Status: "READY"},
			Relationships: capability.AvailabilitySourceReadiness{Status: "READY"},
			FieldProfiles: capability.AvailabilitySourceReadiness{Status: "DEGRADED", Reason: "profiles stale"},
		},
		Resources:     []capability.ObservedResource{{ResourceType: "Observation", DocumentCount: 3, Populated: true}},
		Fields:        []capability.ObservedField{{ResourceType: "Observation", Path: "value", ShapeKind: "object", PathObservedCount: 3, Examples: []string{"a"}, ExamplesTruncated: true, SemanticObservations: []capability.ConceptCandidate{{SourceResourceType: "Observation", Completeness: "FULL", Status: "MATCHED", Population: 2}}}},
		Relationships: []capability.ObservedRelationship{{SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "has", StorageDirection: "out", BuilderDirection: "forward", ObservedEdgeCount: 5}},
	}
	snapshot.AuditNodes = []capability.Node{{ID: "audit-node", ResourceType: "Audit", Populated: false}}
	snapshot.AuditEdges = []capability.Edge{{ID: "audit-edge", FromNodeID: "audit-node", ToNodeID: "node-patient", Label: "audits", SourceResourceType: "Audit", TargetResourceType: "Patient"}}
	snapshot.AuditCandidates = []capability.Candidate{{ID: "audit-cand", NodeID: "audit-node", ResourceType: "Audit", FieldPath: "agent", Label: "Agent", LogicalType: "string"}}
	// Availability and audit arrays are part of the canonical payload (the
	// builder rehashes after setting them), so re-derive the token exactly
	// as capability.snapshotToken does before handing the snapshot to Put.
	sum := sha256.Sum256(snapshot.CanonicalPayload())
	snapshot.Token = "sha256:" + hex.EncodeToString(sum[:])
	return snapshot
}

func testBinding(project string) tableshapecap.Binding {
	return tableshapecap.Binding{
		Project: project, ExplorerID: "explorer-a", OutputID: "output-a", SnapshotToken: "snapshot-a",
		AuthorizationScope: "scope-a", SourceGeneration: "generation-a", DraftVersion: 7, DraftDigest: "draft-a",
		BaseDocumentDigest: "document-a", BaseCompilationReceiptID: "compilation-a", OutputFingerprint: "output-fingerprint-a",
		CompilerSchemaDigest: "schema-a",
	}
}

func testCatalog(t *testing.T, binding tableshapecap.Binding, createdAt string) tableshapecap.CatalogReceipt {
	t.Helper()
	columns := []tableshapecap.PublicColumn{
		{Key: "patient", Label: "Patient", LogicalType: tableshapecap.LogicalString},
		{Key: "category", Label: "Category", LogicalType: tableshapecap.LogicalString, Nullable: true},
		{Key: "value", Label: "Value", LogicalType: tableshapecap.LogicalDecimal, Nullable: true, UnitIdentity: "mg"},
	}
	roles := []tableshapecap.ChoiceRole{
		tableshapecap.RolePivotGroup, tableshapecap.RolePivotCategory, tableshapecap.RolePivotValue,
		tableshapecap.RoleUnpivotInput, tableshapecap.RoleDerivedOperand, tableshapecap.RoleDerivedOperator,
		tableshapecap.RolePolicyDuplicate, tableshapecap.RolePolicyMissing, tableshapecap.RolePolicyUnlisted,
		tableshapecap.RolePolicyUnpivotNull, tableshapecap.RolePolicyDerivedMissing, tableshapecap.RolePolicyDivisionByZero,
	}
	availability := make([]tableshapecap.RoleAvailability, 0, len(roles))
	for _, role := range roles {
		availability = append(availability, tableshapecap.RoleAvailability{Role: role, State: tableshapecap.AvailabilitySupported})
	}
	choices := tableshapecap.CatalogChoices{
		Columns: []tableshapecap.ColumnChoice{
			{Role: tableshapecap.RolePivotGroup, ColumnKey: "patient"},
			{Role: tableshapecap.RolePivotCategory, ColumnKey: "category"},
			{Role: tableshapecap.RolePivotValue, ColumnKey: "value"},
			{Role: tableshapecap.RoleUnpivotInput, ColumnKey: "category"},
			{Role: tableshapecap.RoleUnpivotInput, ColumnKey: "value"},
		},
		Operators: []tableshapecap.OperatorChoice{{Role: tableshapecap.RoleDerivedOperator, Operator: "ADD"}},
		Policies: []tableshapecap.PolicyChoice{
			{Role: tableshapecap.RolePolicyDuplicate, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyMissing, PolicyID: "NULL"},
			{Role: tableshapecap.RolePolicyUnlisted, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyUnpivotNull, PolicyID: "PRESERVE"},
			{Role: tableshapecap.RolePolicyDerivedMissing, PolicyID: "ERROR"},
			{Role: tableshapecap.RolePolicyDivisionByZero, PolicyID: "ERROR"},
		},
		Operands: []tableshapecap.OperandChoice{{Role: tableshapecap.RoleDerivedOperand, Operand: tableshapecap.OperandRef{Kind: tableshapecap.OperandColumn, ColumnKey: "value"}}},
	}
	receipt, err := tableshapecap.NewCatalogReceipt(binding, columns, availability, choices,
		tableshapecap.SavedShapeSummary{ReshapeKind: "PIVOT", DerivedColumns: 2, ShapeDigest: "shape-digest-1"}, createdAt)
	if err != nil {
		t.Fatalf("NewCatalogReceipt: %v", err)
	}
	return receipt
}

func scanValues(t *testing.T) []tableshapecap.Scalar {
	t.Helper()
	return []tableshapecap.Scalar{
		tableshapecap.StringScalar(""), tableshapecap.IntegerScalar(0),
		tableshapecap.DecimalScalar(2.5), tableshapecap.BooleanScalar(false),
		tableshapecap.NullScalar(), tableshapecap.MissingScalar(),
	}
}

func testScan(t *testing.T, catalog tableshapecap.CatalogReceipt, createdAt string) tableshapecap.CategoryScanReceipt {
	t.Helper()
	byRole := map[tableshapecap.ChoiceRole]string{}
	for _, choice := range catalog.Choices.Columns {
		if _, ok := byRole[choice.Role]; !ok {
			byRole[choice.Role] = choice.ID
		}
	}
	values := scanValues(t)
	valuesDigest, err := tableshapecap.CategoryValuesDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	proof := tableshapecap.CategoryProof{
		Complete: true, DistinctCount: len(values), MaxCategories: 256, ValuesDigest: valuesDigest,
		SourceGeneration: catalog.Binding.SourceGeneration, OutputFingerprint: catalog.Binding.OutputFingerprint,
		ScanFingerprint: "scan-fingerprint-v1", QueryProof: "query-proof-v1",
	}
	scan, err := tableshapecap.NewCategoryScanReceipt(catalog.Binding, catalog.ID,
		byRole[tableshapecap.RolePivotCategory], byRole[tableshapecap.RolePivotValue], values, proof, createdAt)
	if err != nil {
		t.Fatalf("NewCategoryScanReceipt: %v", err)
	}
	return scan
}

func testPivot(t *testing.T, catalog tableshapecap.CatalogReceipt, scan tableshapecap.CategoryScanReceipt, createdAt string) tableshapecap.ResolutionReceipt {
	t.Helper()
	byRole := map[tableshapecap.ChoiceRole]string{}
	for _, choice := range catalog.Choices.Columns {
		if _, ok := byRole[choice.Role]; !ok {
			byRole[choice.Role] = choice.ID
		}
	}
	policies := map[tableshapecap.ChoiceRole]string{}
	for _, choice := range catalog.Choices.Policies {
		policies[choice.Role] = choice.ID
	}
	pivot := &tableshapecap.PivotResolution{
		GroupColumnChoiceIDs:    []string{byRole[tableshapecap.RolePivotGroup]},
		CategoryColumnChoiceID:  byRole[tableshapecap.RolePivotCategory],
		ValueColumnChoiceID:     byRole[tableshapecap.RolePivotValue],
		CategoryDiscoveryID:     scan.ID,
		DuplicatePolicyChoiceID: policies[tableshapecap.RolePolicyDuplicate],
		MissingPolicyChoiceID:   policies[tableshapecap.RolePolicyMissing],
		UnlistedPolicyChoiceID:  policies[tableshapecap.RolePolicyUnlisted],
		CategoryProof:           scan.Proof,
	}
	for i, category := range scan.Categories {
		pivot.Categories = append(pivot.Categories, tableshapecap.FrozenCategory{
			ChoiceID: category.ChoiceID, Value: category.Value,
			OutputColumn: fmt.Sprintf("category_%d", i+1), OutputLabel: fmt.Sprintf("Category %d", i+1),
		})
	}
	resolution, err := tableshapecap.NewResolutionReceipt(catalog.Binding, catalog.ID,
		tableshapecap.ResolutionPivot, pivot, nil, nil, createdAt)
	if err != nil {
		t.Fatalf("NewResolutionReceipt pivot: %v", err)
	}
	return resolution
}

func testDerived(t *testing.T, catalog tableshapecap.CatalogReceipt, prior string, name string, createdAt string) tableshapecap.ResolutionReceipt {
	t.Helper()
	operatorID := catalog.Choices.Operators[0].ID
	baseChoiceID := catalog.Choices.Operands[0].ID
	missingPolicyID := ""
	for _, choice := range catalog.Choices.Policies {
		if choice.Role == tableshapecap.RolePolicyDerivedMissing {
			missingPolicyID = choice.ID
		}
	}
	left := tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandCatalogChoice, ChoiceID: baseChoiceID}
	if prior != "" {
		zero := 0
		left = tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandResolution, ResolutionID: prior, OutputIndex: &zero}
	}
	resolution, err := tableshapecap.NewResolutionReceipt(catalog.Binding, catalog.ID,
		tableshapecap.ResolutionDerived, nil, nil, &tableshapecap.DerivedResolution{
			Output:                       tableshapecap.NamedOutput{Name: name, Label: strings.ToUpper(name[:1]) + name[1:]},
			OperatorChoiceID:             operatorID,
			Left:                         left,
			Right:                        tableshapecap.ResolvedOperand{Kind: tableshapecap.ResolvedOperandLiteral, Literal: ptr(tableshapecap.DecimalScalar(2.5))},
			Result:                       tableshapecap.TypeFact{LogicalType: tableshapecap.LogicalDecimal, UnitIdentity: "mg"},
			MissingPolicyChoiceID:        missingPolicyID,
			DivisionByZeroPolicyChoiceID: "",
		}, createdAt)
	if err != nil {
		t.Fatalf("NewResolutionReceipt derived %s: %v", name, err)
	}
	return resolution
}

// ---------------------------------------------------------------------------
// construction contract (no server needed)
// ---------------------------------------------------------------------------

func TestNewCapabilityStoresRejectNilClientAndUnsafeSchema(t *testing.T) {
	for _, schema := range []string{"", "Public", "a;drop", "0starts_with_digit", "has space", "tab\tname", strings.Repeat("a", 64)} {
		if _, err := NewCapabilitySnapshotStore(nil, schema); err == nil {
			t.Fatalf("NewCapabilitySnapshotStore(nil, %q) = nil error, want failure", schema)
		}
		if _, err := NewTableShapeCapabilityStore(nil, schema); err == nil {
			t.Fatalf("NewTableShapeCapabilityStore(nil, %q) = nil error, want failure", schema)
		}
	}
	// The schema guard fires before any connection use, and a valid schema
	// builds a store without touching the database.
	if _, err := NewCapabilitySnapshotStore(&pgstore.Client{}, "ok_schema"); err != nil {
		t.Fatalf("NewCapabilitySnapshotStore(valid) = %v, want success", err)
	}
	if _, err := NewTableShapeCapabilityStore(&pgstore.Client{}, "ok_schema"); err != nil {
		t.Fatalf("NewTableShapeCapabilityStore(valid) = %v, want success", err)
	}
}

// ---------------------------------------------------------------------------
// schema composition, literal column contract, and guard presence
// ---------------------------------------------------------------------------

func TestCapabilitySchemaCompositionColumnContractAndGuards(t *testing.T) {
	s := newCapabilityService(t)

	// Exactly the tables the composed history declares -- nothing from the
	// source-store or publication planes leaked in.
	var tables []string
	s.forEachWitnessText(t, &tables,
		"SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = $1 ORDER BY tablename", s.schema)
	want := []string{"capability_snapshots", "explorers", "schema_migrations", "table_shape_capabilities"}
	for _, m := range s.owner {
		if m.Version == 3 && m.Name == "explorer_draft_revision" {
			want = append(want, "explorers_draft_revisions")
			break
		}
	}
	sort.Strings(want)
	if strings.Join(tables, ",") != strings.Join(want, ",") {
		t.Fatalf("schema tables = %#v, want exactly %#v", tables, want)
	}

	type column struct {
		name, typ string
		nullable  bool
	}
	for _, wantTable := range []struct {
		table   string
		columns []column
	}{
		{CapabilitySnapshotsTable, []column{
			{"token", "text", false},
			{"project", "text", false},
			{"generation", "text", false},
			{"authorization_scope_digest", "text", false},
			{"cache_version", "text", false},
			{"schema_digest", "text", false},
			{"resource_inventory_digest", "text", false},
			{"relationship_digest", "text", false},
			{"field_digest", "text", false},
			{"shape_digest", "text", false},
			{"protocol_version", "text", false},
			{"compiler_version", "text", false},
			{"traversal_policy_version", "text", false},
			{"projection_policy_version", "text", false},
			{"status", "text", false},
			{"complete", "boolean", false},
			{"truncated", "boolean", false},
			{"payload", "jsonb", false},
		}},
		{TableShapeCapabilitiesTable, []column{
			{"id", "text", false},
			{"kind", "text", false},
			{"parent_catalog_id", "text", false},
			{"project", "text", false},
			{"explorer_id", "text", false},
			{"output_id", "text", false},
			{"snapshot_token", "text", false},
			{"authorization_scope", "text", false},
			{"source_generation", "text", false},
			{"draft_version", "bigint", false},
			{"draft_digest", "text", false},
			{"base_document_digest", "text", false},
			{"base_compilation_receipt_id", "text", false},
			{"output_fingerprint", "text", false},
			{"compiler_schema_digest", "text", false},
			{"content_digest", "text", false},
			{"created_at", "text", false},
			{"payload", "jsonb", false},
		}},
	} {
		conn, acquireErr := s.witness.Acquire(s.ctx)
		if acquireErr != nil {
			s.fail("acquire column witness", acquireErr)
		}
		rows, err := conn.Query(s.ctx,
			"SELECT column_name, data_type, is_nullable = 'YES' FROM information_schema.columns"+
				" WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position",
			s.schema, wantTable.table)
		if err != nil {
			rows.Close()
			conn.Release()
			s.fail("list columns "+wantTable.table, err)
		}
		var columns []column
		for rows.Next() {
			var c column
			if err := rows.Scan(&c.name, &c.typ, &c.nullable); err != nil {
				rows.Close()
				conn.Release()
				s.fail("scan column", err)
			}
			columns = append(columns, c)
		}
		rows.Close()
		conn.Release()
		if len(columns) != len(wantTable.columns) {
			t.Fatalf("%s columns = %+v, want %+v", wantTable.table, columns, wantTable.columns)
		}
		for i, wantCol := range wantTable.columns {
			if columns[i] != wantCol {
				t.Fatalf("%s column %d = %+v, want %+v", wantTable.table, i, columns[i], wantCol)
			}
		}
	}

	// The database-level immutability guards exist by name, backed by the
	// shared guard function.
	if !s.triggerPresent(t, "capability_snapshots_immutable", CapabilitySnapshotsTable) {
		t.Fatal("capability_snapshots_immutable trigger missing")
	}
	if !s.triggerPresent(t, "table_shape_capabilities_immutable", TableShapeCapabilitiesTable) {
		t.Fatal("table_shape_capabilities_immutable trigger missing")
	}
	var functionPresent bool
	if err := queryRowOn(s.ctx, s.witness, &functionPresent,
		`SELECT to_regprocedure($1) IS NOT NULL`, s.schema+".loom_capability_artifact_immutable()"); err != nil {
		s.fail("guard function witness", err)
	}
	if !functionPresent {
		t.Fatal("loom_capability_artifact_immutable() guard function missing")
	}
	for _, index := range []string{
		"capability_snapshots_identity_ix", "capability_snapshots_status_ix",
		"table_shape_capabilities_binding_ix", "table_shape_capabilities_parent_ix",
	} {
		var present bool
		if err := queryRowOn(s.ctx, s.witness, &present,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = $1 AND indexname = $2)`, s.schema, index); err != nil {
			s.fail("index witness "+index, err)
		}
		if !present {
			t.Fatalf("index %s missing", index)
		}
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1 (snapshots): same key/content idempotent; changed content
// collision; invalid input refused; the stored row never changes.
// ---------------------------------------------------------------------------

func TestCapabilitySnapshotPutIdempotentAndCollisionRefused(t *testing.T) {
	s := newCapabilityService(t)
	snapshot := richSnapshot("pgstate03-snapshots", "generation-1", "alpha")

	created, err := s.snapshots.Put(s.ctx, snapshot)
	if err != nil {
		s.fail("put snapshot", err)
	}
	assertSnapshotEquivalent(t, "Put", created, snapshot)
	s.assertSnapshotLiteral(t, snapshot)
	before := s.witnessSnapshot(snapshot.Token)

	// Same key, same content: idempotent. The stored row is byte-identical
	// (same ctid -- no UPDATE ran) and the returned snapshot matches.
	again, err := s.snapshots.Put(s.ctx, snapshot)
	if err != nil {
		s.fail("idempotent re-put", err)
	}
	assertSnapshotEquivalent(t, "idempotent re-put", again, snapshot)
	after := s.witnessSnapshot(snapshot.Token)
	if before.payloadText != after.payloadText || before.ctid != after.ctid {
		t.Fatalf("idempotent re-put rewrote the snapshot row: before=%+v after=%+v", before, after)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(CapabilitySnapshotsTable)); n != 1 {
		t.Fatalf("snapshot rows = %d, want exactly 1", n)
	}

	// Changed content under the same token is the typed collision. The row
	// is first tampered underneath the store (a path only the guard-drop
	// probe permits -- see TestCapabilityImmutabilityGuards), then the
	// honest Put must refuse and change nothing.
	s.mustWitnessExec(t, `DROP TRIGGER capability_snapshots_immutable ON `+s.qualified(CapabilitySnapshotsTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(CapabilitySnapshotsTable)+
		` SET payload = jsonb_set(payload, '{status}', '"FAILED"') WHERE token = $1`, snapshot.Token)
	s.mustWitnessExec(t, `CREATE TRIGGER capability_snapshots_immutable BEFORE UPDATE OR DELETE ON `+
		s.qualified(CapabilitySnapshotsTable)+` FOR EACH ROW EXECUTE FUNCTION `+s.qualified("loom_capability_artifact_immutable")+`()`)
	tampered := s.witnessSnapshot(snapshot.Token)
	if tampered.ctid == before.ctid || tampered.payloadText == before.payloadText {
		t.Fatalf("tampering did not change the stored row: %+v", tampered)
	}
	if _, err := s.snapshots.Put(s.ctx, snapshot); !errors.Is(err, capability.ErrTokenCollision) {
		t.Fatalf("Put over tampered payload = %v, want capability.ErrTokenCollision", err)
	}
	untouched := s.witnessSnapshot(snapshot.Token)
	if untouched.payloadText != tampered.payloadText || untouched.ctid != tampered.ctid {
		t.Fatalf("refused Put mutated the stored row: tampered=%+v after=%+v", tampered, untouched)
	}

	// A foreign token and a lying READY flag are refused before storage.
	lying := snapshot
	lying.Token = "sha256:" + strings.Repeat("0", 64)
	if _, err := s.snapshots.Put(s.ctx, lying); !errors.Is(err, capability.ErrInvalidSnapshot) {
		t.Fatalf("Put with foreign token = %v, want capability.ErrInvalidSnapshot", err)
	}
	s.assertSnapshotAbsent(t, lying.Token)
	notActuallyComplete := capability.NewSnapshot(snapshot.Identity, snapshot.Policy,
		capability.StatusReady, false, false, nil, nil, nil, nil)
	if _, err := s.snapshots.Put(s.ctx, notActuallyComplete); !errors.Is(err, capability.ErrInvalidSnapshot) {
		t.Fatalf("Put of incomplete READY = %v, want capability.ErrInvalidSnapshot", err)
	}

	// Reads re-validate: the tampered row is never served as if healthy.
	if _, err := s.snapshots.GetByToken(s.ctx, snapshot.Token); !errors.Is(err, capability.ErrInvalidSnapshot) {
		t.Fatalf("GetByToken over tampered payload = %v, want capability.ErrInvalidSnapshot", err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2 (snapshots): retrieval is fully identity-scoped; cross-
// project/generation/scope/digest retrieval is refused with ErrNotFound,
// and READY wins the best-available ordering.
// ---------------------------------------------------------------------------

func TestCapabilitySnapshotIdentityRetrievalIsFullyScoped(t *testing.T) {
	s := newCapabilityService(t)
	ready := richSnapshot("pgstate03-identity", "generation-1", "beta")
	if _, err := s.snapshots.Put(s.ctx, ready); err != nil {
		s.fail("put ready snapshot", err)
	}

	got, err := s.snapshots.GetByIdentity(s.ctx, ready.Identity)
	if err != nil {
		s.fail("get by identity", err)
	}
	assertSnapshotEquivalent(t, "GetByIdentity", got, ready)

	mutations := map[string]func(id *capability.SnapshotIdentity){
		"project":                  func(id *capability.SnapshotIdentity) { id.Project = "pgstate03-identity-other" },
		"generation":               func(id *capability.SnapshotIdentity) { id.Generation += "-other" },
		"authorizationScopeDigest": func(id *capability.SnapshotIdentity) { id.AuthorizationScopeDigest += "-other" },
		"cacheVersion":             func(id *capability.SnapshotIdentity) { id.CacheVersion = "v3" },
		"schemaDigest":             func(id *capability.SnapshotIdentity) { id.SchemaDigest += "-other" },
		"resourceInventoryDigest":  func(id *capability.SnapshotIdentity) { id.ResourceInventoryDigest += "-other" },
		"relationshipDigest":       func(id *capability.SnapshotIdentity) { id.RelationshipDigest += "-other" },
		"fieldDigest":              func(id *capability.SnapshotIdentity) { id.FieldDigest += "-other" },
		"shapeDigest":              func(id *capability.SnapshotIdentity) { id.ShapeDigest += "-other" },
		"protocolVersion":          func(id *capability.SnapshotIdentity) { id.ProtocolVersion += "-other" },
		"compilerVersion":          func(id *capability.SnapshotIdentity) { id.CompilerVersion += "-other" },
		"traversalPolicyVersion":   func(id *capability.SnapshotIdentity) { id.TraversalPolicyVersion += "-other" },
		"projectionPolicyVersion":  func(id *capability.SnapshotIdentity) { id.ProjectionPolicyVersion += "-other" },
	}
	for name, mutate := range mutations {
		foreign := ready.Identity
		mutate(&foreign)
		if _, err := s.snapshots.GetByIdentity(s.ctx, foreign); !errors.Is(err, capability.ErrNotFound) {
			t.Fatalf("GetByIdentity with mutated %s = %v, want capability.ErrNotFound", name, err)
		}
	}

	// Unknown token: typed miss, no leak.
	if _, err := s.snapshots.GetByToken(s.ctx, "sha256:"+strings.Repeat("f", 64)); !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("GetByToken(unknown) = %v, want capability.ErrNotFound", err)
	}

	// Best-available ordering: READY wins over BUILDING/FAILED under the
	// same identity; with only diagnostic rows present, one is returned.
	building := capability.NewSnapshot(ready.Identity, ready.Policy, capability.StatusBuilding, false, false, nil, nil, nil, nil)
	if _, err := s.snapshots.Put(s.ctx, building); err != nil {
		s.fail("put building snapshot", err)
	}
	got, err = s.snapshots.GetByIdentity(s.ctx, ready.Identity)
	if err != nil {
		s.fail("get by identity with building sibling", err)
	}
	assertSnapshotEquivalent(t, "GetByIdentity with building sibling", got, ready)
	diagnostic := richSnapshot("pgstate03-identity-diagnostic", "generation-1", "gamma")
	diagnostic.Status = capability.StatusFailed
	diagnostic.Complete = false
	sum := sha256.Sum256(diagnostic.CanonicalPayload())
	diagnostic.Token = "sha256:" + hex.EncodeToString(sum[:])
	if _, err := s.snapshots.Put(s.ctx, diagnostic); err != nil {
		s.fail("put failed snapshot", err)
	}
	got, err = s.snapshots.GetByIdentity(s.ctx, diagnostic.Identity)
	if err != nil {
		s.fail("get diagnostic identity", err)
	}
	assertSnapshotEquivalent(t, "GetByIdentity FAILED", got, diagnostic)

	// Shape-digest siblings (AO review finding on 4e85103c): two READY
	// snapshots that agree on every identity column except shape_digest are
	// both healthy rows. Each identity query must return exactly the
	// requested artifact -- never the sibling, and never a corruption error
	// for a row that is internally consistent. The SQL identity filter and
	// the post-read mirror check therefore have to agree on shape_digest.
	sibling := richSnapshot("pgstate03-identity", "generation-1", "beta")
	sibling.Identity.ShapeDigest = "sha256:shape-beta-sibling"
	siblingSum := sha256.Sum256(sibling.CanonicalPayload())
	sibling.Token = "sha256:" + hex.EncodeToString(siblingSum[:])
	if sibling.Token == ready.Token {
		t.Fatal("shape-digest sibling did not produce a distinct token")
	}
	if _, err := s.snapshots.Put(s.ctx, sibling); err != nil {
		s.fail("put shape-digest sibling", err)
	}
	got, err = s.snapshots.GetByIdentity(s.ctx, ready.Identity)
	if err != nil {
		s.fail("get by identity with shape-digest sibling", err)
	}
	if got.Token != ready.Token {
		t.Fatalf("GetByIdentity(ready) returned sibling token %q, want %q", got.Token, ready.Token)
	}
	assertSnapshotEquivalent(t, "GetByIdentity ready beside shape sibling", got, ready)
	got, err = s.snapshots.GetByIdentity(s.ctx, sibling.Identity)
	if err != nil {
		s.fail("get by identity for shape-digest sibling", err)
	}
	if got.Token != sibling.Token {
		t.Fatalf("GetByIdentity(sibling) returned token %q, want %q", got.Token, sibling.Token)
	}
	assertSnapshotEquivalent(t, "GetByIdentity shape-digest sibling", got, sibling)

	// An identity that differs from both rows only on shape_digest is a
	// typed miss, never the sibling and never a corruption error.
	ghost := sibling.Identity
	ghost.ShapeDigest = "sha256:shape-beta-ghost"
	if _, err := s.snapshots.GetByIdentity(s.ctx, ghost); !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("GetByIdentity(ghost shape digest) = %v, want capability.ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1 (receipts): same key/content idempotent (createdAt excluded,
// stored original survives); changed content collides and changes nothing.
// ---------------------------------------------------------------------------

func TestTableShapeCatalogPutIdempotentAndChangedContentCollides(t *testing.T) {
	s := newCapabilityService(t)
	binding := testBinding("pgstate03-catalog")
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")

	created, err := s.receipts.PutCatalog(s.ctx, catalog)
	if err != nil {
		s.fail("put catalog", err)
	}
	if !reflect.DeepEqual(created, catalog) {
		t.Fatalf("PutCatalog = %+v, want %+v", created, catalog)
	}
	s.assertReceiptLiteral(t, catalog.ID, kindCatalog, "", binding, catalog.ContentDigest, catalog.CreatedAt, catalog)
	before := s.witnessReceipt(catalog.ID)

	// Same key, same content, later createdAt: the identity is the content
	// (createdAt is excluded from the digest), so the retry is idempotent
	// and returns the stored row -- original createdAt included -- with the
	// row physically untouched.
	retry := catalog
	retry.CreatedAt = "2027-01-02T03:04:05Z"
	stored, err := s.receipts.PutCatalog(s.ctx, retry)
	if err != nil {
		s.fail("idempotent re-put", err)
	}
	if !reflect.DeepEqual(stored, catalog) {
		t.Fatalf("idempotent re-put returned %+v, want the stored %+v", stored, catalog)
	}
	after := s.witnessReceipt(catalog.ID)
	if before.payloadText != after.payloadText || before.ctid != after.ctid || before.createdAt != "2026-09-19T00:00:00Z" {
		t.Fatalf("idempotent re-put rewrote the catalog row: before=%+v after=%+v", before, after)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(TableShapeCapabilitiesTable)); n != 1 {
		t.Fatalf("receipt rows = %d, want exactly 1", n)
	}

	// Changed content under the same ID: tamper a content field (through a
	// guard-dropped window), then the honest Put must collide and leave the
	// stored row byte-identical.
	s.mustWitnessExec(t, `DROP TRIGGER table_shape_capabilities_immutable ON `+s.qualified(TableShapeCapabilitiesTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(TableShapeCapabilitiesTable)+
		` SET payload = jsonb_set(payload, '{columns,0,label}', '"tampered"') WHERE id = $1`, catalog.ID)
	s.mustWitnessExec(t, `CREATE TRIGGER table_shape_capabilities_immutable BEFORE UPDATE OR DELETE ON `+
		s.qualified(TableShapeCapabilitiesTable)+` FOR EACH ROW EXECUTE FUNCTION `+s.qualified("loom_capability_artifact_immutable")+`()`)
	tampered := s.witnessReceipt(catalog.ID)
	if tampered.ctid == before.ctid || tampered.payloadText == before.payloadText {
		t.Fatalf("tampering did not change the stored row: %+v", tampered)
	}
	if _, err := s.receipts.PutCatalog(s.ctx, catalog); !errors.Is(err, tableshapecap.ErrIdentityClash) {
		t.Fatalf("PutCatalog over tampered payload = %v, want tableshapecap.ErrIdentityClash", err)
	}
	untouched := s.witnessReceipt(catalog.ID)
	if untouched.payloadText != tampered.payloadText || untouched.ctid != tampered.ctid {
		t.Fatalf("refused PutCatalog mutated the stored row: tampered=%+v after=%+v", tampered, untouched)
	}

	// Invalid input is refused before storage.
	badID := catalog
	badID.ID = "tsc_" + strings.Repeat("a", 32)
	if _, err := s.receipts.PutCatalog(s.ctx, badID); !errors.Is(err, tableshapecap.ErrInvalid) {
		t.Fatalf("PutCatalog with forged ID = %v, want tableshapecap.ErrInvalid", err)
	}
	s.assertReceiptAbsent(t, badID.ID)
}

// ---------------------------------------------------------------------------
// Acceptance 2 (receipts): every retrieval is bound. Cross-project, cross-
// Explorer, cross-output, cross-snapshot, cross-generation, cross-draft,
// cross-parent reads are all refused; an out-of-scope parent never accepts
// a child insert.
// ---------------------------------------------------------------------------

func TestTableShapeReadsRefuseCrossProjectOutputSnapshotAndParent(t *testing.T) {
	s := newCapabilityService(t)
	binding := testBinding("pgstate03-binding")
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	if _, err := s.receipts.PutCatalog(s.ctx, catalog); err != nil {
		s.fail("put catalog", err)
	}
	scan := testScan(t, catalog, "2026-09-19T00:00:01Z")
	if _, err := s.receipts.PutCategoryScan(s.ctx, scan); err != nil {
		s.fail("put scan", err)
	}
	pivot := testPivot(t, catalog, scan, "2026-09-19T00:00:02Z")
	if _, err := s.receipts.PutResolution(s.ctx, pivot); err != nil {
		s.fail("put pivot", err)
	}

	// A second catalog under the same binding exercises cross-parent.
	otherSavedShape := catalog.SavedShape
	otherSavedShape.DerivedColumns++
	otherReceipt, err := tableshapecap.NewCatalogReceipt(binding, clonePublicColumns(catalog.Columns),
		cloneRoleAvailability(catalog.Availability), cloneCatalogChoicesForTest(catalog.Choices), otherSavedShape, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.receipts.PutCatalog(s.ctx, otherReceipt); err != nil {
		s.fail("put second catalog", err)
	}

	mutations := map[string]func(b *tableshapecap.Binding){
		"project":                  func(b *tableshapecap.Binding) { b.Project = "pgstate03-binding-other" },
		"explorer":                 func(b *tableshapecap.Binding) { b.ExplorerID += "-other" },
		"output":                   func(b *tableshapecap.Binding) { b.OutputID += "-other" },
		"snapshot":                 func(b *tableshapecap.Binding) { b.SnapshotToken += "-other" },
		"authorizationScope":       func(b *tableshapecap.Binding) { b.AuthorizationScope += "-other" },
		"sourceGeneration":         func(b *tableshapecap.Binding) { b.SourceGeneration += "-other" },
		"draftVersion":             func(b *tableshapecap.Binding) { b.DraftVersion++ },
		"draftDigest":              func(b *tableshapecap.Binding) { b.DraftDigest += "-other" },
		"baseDocumentDigest":       func(b *tableshapecap.Binding) { b.BaseDocumentDigest += "-other" },
		"baseCompilationReceiptId": func(b *tableshapecap.Binding) { b.BaseCompilationReceiptID += "-other" },
		"outputFingerprint":        func(b *tableshapecap.Binding) { b.OutputFingerprint += "-other" },
		"compilerSchemaDigest":     func(b *tableshapecap.Binding) { b.CompilerSchemaDigest += "-other" },
	}
	for name, mutate := range mutations {
		foreign := binding
		mutate(&foreign)
		if _, err := s.receipts.GetCatalog(s.ctx, foreign, catalog.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
			t.Fatalf("GetCatalog with mutated %s = %v, want tableshapecap.ErrNotFound", name, err)
		}
		if _, err := s.receipts.GetCategoryScan(s.ctx, foreign, catalog.ID, scan.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
			t.Fatalf("GetCategoryScan with mutated %s = %v, want tableshapecap.ErrNotFound", name, err)
		}
		if _, err := s.receipts.GetResolution(s.ctx, foreign, catalog.ID, pivot.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
			t.Fatalf("GetResolution with mutated %s = %v, want tableshapecap.ErrNotFound", name, err)
		}
	}

	// The lookup path is scoped to its nine request-bound columns.
	lookup := tableshapecap.CatalogLookup{
		Project: binding.Project, ExplorerID: binding.ExplorerID, OutputID: binding.OutputID,
		SnapshotToken: binding.SnapshotToken, AuthorizationScope: binding.AuthorizationScope,
		SourceGeneration: binding.SourceGeneration, DraftVersion: binding.DraftVersion,
		DraftDigest: binding.DraftDigest, BaseDocumentDigest: binding.BaseDocumentDigest,
	}
	found, err := s.receipts.GetCatalogForLookup(s.ctx, lookup, catalog.ID)
	if err != nil {
		s.fail("lookup under correct binding", err)
	}
	if !reflect.DeepEqual(found, catalog) {
		t.Fatalf("GetCatalogForLookup = %+v, want %+v", found, catalog)
	}
	lookupMutations := map[string]func(l *tableshapecap.CatalogLookup){
		"project":            func(l *tableshapecap.CatalogLookup) { l.Project = "pgstate03-binding-other" },
		"explorerId":         func(l *tableshapecap.CatalogLookup) { l.ExplorerID += "-other" },
		"outputId":           func(l *tableshapecap.CatalogLookup) { l.OutputID += "-other" },
		"snapshotToken":      func(l *tableshapecap.CatalogLookup) { l.SnapshotToken += "-other" },
		"authorizationScope": func(l *tableshapecap.CatalogLookup) { l.AuthorizationScope += "-other" },
		"sourceGeneration":   func(l *tableshapecap.CatalogLookup) { l.SourceGeneration += "-other" },
		"draftVersion":       func(l *tableshapecap.CatalogLookup) { l.DraftVersion++ },
		"draftDigest":        func(l *tableshapecap.CatalogLookup) { l.DraftDigest += "-other" },
		"baseDocumentDigest": func(l *tableshapecap.CatalogLookup) { l.BaseDocumentDigest += "-other" },
	}
	for name, mutate := range lookupMutations {
		foreignLookup := lookup
		mutate(&foreignLookup)
		if _, err := s.receipts.GetCatalogForLookup(s.ctx, foreignLookup, catalog.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
			t.Fatalf("GetCatalogForLookup with mutated %s = %v, want tableshapecap.ErrNotFound", name, err)
		}
	}

	// Cross-parent retrieval is refused: neither child reads under the
	// other catalog, and neither ID leaks through.
	if _, err := s.receipts.GetCategoryScan(s.ctx, binding, otherReceipt.ID, scan.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("GetCategoryScan under foreign parent = %v, want tableshapecap.ErrNotFound", err)
	}
	if _, err := s.receipts.GetResolution(s.ctx, binding, otherReceipt.ID, pivot.ID); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("GetResolution under foreign parent = %v, want tableshapecap.ErrNotFound", err)
	}

	// A child insert against a foreign but EXISTING parent is refused by
	// the persisted-parent validation: the scan's choices belong to the
	// other catalog, so it is invalid against that parent -- and no row
	// lands.
	orphanScan, err := tableshapecap.NewCategoryScanReceipt(binding, otherReceipt.ID,
		scan.CategoryColumnChoiceID, scan.ValueColumnChoiceID, scanValues(t), scan.Proof, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.receipts.PutCategoryScan(s.ctx, orphanScan); !errors.Is(err, tableshapecap.ErrInvalid) {
		t.Fatalf("PutCategoryScan against foreign existing parent = %v, want tableshapecap.ErrInvalid", err)
	}
	s.assertReceiptAbsent(t, orphanScan.ID)

	// A child insert against a parent that does not exist at all fails on
	// the parent read: the binding-scoped parent lookup misses.
	ghostParent := "tsc_" + strings.Repeat("f", 32)
	ghostScan, err := tableshapecap.NewCategoryScanReceipt(binding, ghostParent,
		scan.CategoryColumnChoiceID, scan.ValueColumnChoiceID, scanValues(t), scan.Proof, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.receipts.PutCategoryScan(s.ctx, ghostScan); !errors.Is(err, tableshapecap.ErrNotFound) {
		t.Fatalf("PutCategoryScan against missing parent = %v, want tableshapecap.ErrNotFound", err)
	}
	s.assertReceiptAbsent(t, ghostScan.ID)
}

func clonePublicColumns(v []tableshapecap.PublicColumn) []tableshapecap.PublicColumn {
	return append([]tableshapecap.PublicColumn(nil), v...)
}
func cloneRoleAvailability(v []tableshapecap.RoleAvailability) []tableshapecap.RoleAvailability {
	return append([]tableshapecap.RoleAvailability(nil), v...)
}
func cloneCatalogChoicesForTest(v tableshapecap.CatalogChoices) tableshapecap.CatalogChoices {
	out := tableshapecap.CatalogChoices{
		Columns:   append([]tableshapecap.ColumnChoice(nil), v.Columns...),
		Operators: append([]tableshapecap.OperatorChoice(nil), v.Operators...),
		Policies:  append([]tableshapecap.PolicyChoice(nil), v.Policies...),
		Operands:  append([]tableshapecap.OperandChoice(nil), v.Operands...),
	}
	return out
}

// ---------------------------------------------------------------------------
// Acceptance 3: nested parent receipts and canonical tokens read back
// exactly -- the full chain snapshot -> catalog -> category scan -> pivot
// -> derived(-> derived) round-trips through PostgreSQL byte-for-byte
// (deep equality of every nested value, including the six scalar arms).
// ---------------------------------------------------------------------------

func TestNestedParentReceiptsAndCanonicalTokensReadBackExactly(t *testing.T) {
	s := newCapabilityService(t)

	snapshot := richSnapshot("pgstate03-nested", "generation-nested", "delta")
	if _, err := s.snapshots.Put(s.ctx, snapshot); err != nil {
		s.fail("put seed snapshot", err)
	}
	s.assertSnapshotLiteral(t, snapshot)

	binding := testBinding("pgstate03-nested")
	binding.SnapshotToken = snapshot.Token
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	if _, err := s.receipts.PutCatalog(s.ctx, catalog); err != nil {
		s.fail("put catalog", err)
	}
	s.assertReceiptLiteral(t, catalog.ID, kindCatalog, "", binding, catalog.ContentDigest, catalog.CreatedAt, catalog)

	scan := testScan(t, catalog, "2026-09-19T00:00:01Z")
	if _, err := s.receipts.PutCategoryScan(s.ctx, scan); err != nil {
		s.fail("put scan", err)
	}
	s.assertReceiptLiteral(t, scan.ID, kindCategoryScan, catalog.ID, binding, scan.ContentDigest, scan.CreatedAt, scan)

	pivot := testPivot(t, catalog, scan, "2026-09-19T00:00:02Z")
	if _, err := s.receipts.PutResolution(s.ctx, pivot); err != nil {
		s.fail("put pivot", err)
	}
	s.assertReceiptLiteral(t, pivot.ID, kindResolution, catalog.ID, binding, pivot.ContentDigest, pivot.CreatedAt, pivot)

	derivedOne := testDerived(t, catalog, "", "total", "2026-09-19T00:00:03Z")
	if _, err := s.receipts.PutResolution(s.ctx, derivedOne); err != nil {
		s.fail("put derived one", err)
	}
	s.assertReceiptLiteral(t, derivedOne.ID, kindResolution, catalog.ID, binding, derivedOne.ContentDigest, derivedOne.CreatedAt, derivedOne)

	derivedTwo := testDerived(t, catalog, derivedOne.ID, "scaled", "2026-09-19T00:00:04Z")
	if _, err := s.receipts.PutResolution(s.ctx, derivedTwo); err != nil {
		s.fail("put derived two", err)
	}
	s.assertReceiptLiteral(t, derivedTwo.ID, kindResolution, catalog.ID, binding, derivedTwo.ContentDigest, derivedTwo.CreatedAt, derivedTwo)

	// Reads come back exact: deep equality on every artifact, the canonical
	// snapshot token on the mirror column, and the canonical chain IDs
	// pinned inside the stored documents.
	gotCatalog, err := s.receipts.GetCatalog(s.ctx, binding, catalog.ID)
	if err != nil || !reflect.DeepEqual(gotCatalog, catalog) {
		t.Fatalf("GetCatalog = %+v (%v), want %+v", gotCatalog, err, catalog)
	}
	gotScan, err := s.receipts.GetCategoryScan(s.ctx, binding, catalog.ID, scan.ID)
	if err != nil || !reflect.DeepEqual(gotScan, scan) {
		t.Fatalf("GetCategoryScan = %+v (%v), want %+v", gotScan, err, scan)
	}
	if w := s.witnessReceipt(scan.ID); w.payloadText == "" || !strings.Contains(w.payloadText, scan.Proof.ValuesDigest) {
		t.Fatalf("scan payload lost its canonical values digest: %s", w.payloadText)
	}
	gotPivot, err := s.receipts.GetResolution(s.ctx, binding, catalog.ID, pivot.ID)
	if err != nil || !reflect.DeepEqual(gotPivot, pivot) {
		t.Fatalf("GetResolution pivot = %+v (%v), want %+v", gotPivot, err, pivot)
	}
	// The six scalar arms (empty string, zero integer, decimal, false,
	// JSON null, missing) round-trip as distinct tagged values inside the
	// nested frozen categories.
	for i, want := range scan.Categories {
		if !reflect.DeepEqual(gotPivot.Pivot.Categories[i].Value, want.Value) {
			t.Fatalf("pivot category %d = %+v, want %+v", i, gotPivot.Pivot.Categories[i].Value, want.Value)
		}
	}
	gotDerivedOne, err := s.receipts.GetResolution(s.ctx, binding, catalog.ID, derivedOne.ID)
	if err != nil || !reflect.DeepEqual(gotDerivedOne, derivedOne) {
		t.Fatalf("GetResolution derived one = %+v (%v), want %+v", gotDerivedOne, err, derivedOne)
	}
	gotDerivedTwo, err := s.receipts.GetResolution(s.ctx, binding, catalog.ID, derivedTwo.ID)
	if err != nil || !reflect.DeepEqual(gotDerivedTwo, derivedTwo) {
		t.Fatalf("GetResolution derived two = %+v (%v), want %+v", gotDerivedTwo, err, derivedTwo)
	}

	// Canonical tokens: the binding column carries the exact content token
	// of the snapshot, and the pivot payload names its scan by canonical ID.
	if w := s.witnessReceipt(catalog.ID); !strings.Contains(w.payloadText, snapshot.Token) || w.binding.SnapshotToken != snapshot.Token {
		t.Fatalf("catalog binding lost the canonical snapshot token: %+v", w)
	}
	if v := s.mustWitnessText(t,
		`SELECT payload -> 'pivot' ->> 'categoryDiscoveryId' FROM `+s.qualified(TableShapeCapabilitiesTable)+` WHERE id = $1`,
		pivot.ID); v != scan.ID {
		t.Fatalf("pivot categoryDiscoveryId = %q, want %q", v, scan.ID)
	}
	if v := s.mustWitnessText(t,
		`SELECT payload -> 'derived' -> 'left' ->> 'resolutionId' FROM `+s.qualified(TableShapeCapabilitiesTable)+` WHERE id = $1`,
		derivedTwo.ID); v != derivedOne.ID {
		t.Fatalf("derived nested resolutionId = %q, want %q", v, derivedOne.ID)
	}
}

// ---------------------------------------------------------------------------
// Two-real-connection proofs: concurrent same-key puts from two physically
// distinct backends converge on exactly one immutable row; a stale re-put
// against a changed-content row is refused with the row byte-identical.
// ---------------------------------------------------------------------------

func TestConcurrentSameKeyPutsConvergeOnOneImmutableRow(t *testing.T) {
	s := newCapabilityService(t)
	binding := testBinding("pgstate03-race")
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	snapshot := richSnapshot("pgstate03-race", "generation-race", "epsilon")

	var wg sync.WaitGroup
	catalogResults := make([]error, 2)
	stores := []*TableShapeCapabilityStore{s.receipts, s.receiptsB}
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := stores[i].PutCatalog(s.ctx, catalog)
			catalogResults[i] = err
		}(i)
	}
	snapshotResults := make([]error, 2)
	snapshotStores := []*CapabilitySnapshotStore{s.snapshots, s.snapshotsB}
	for i := range snapshotStores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := snapshotStores[i].Put(s.ctx, snapshot)
			snapshotResults[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range catalogResults {
		t.Logf("catalog put %d (writer backends A=%d B=%d, witness W=%d): err=%v", i, s.pidA, s.pidB, s.pidW, err)
		if err != nil {
			t.Fatalf("concurrent catalog put %d failed: %v", i, s.scrub(err))
		}
	}
	for i, err := range snapshotResults {
		if err != nil {
			t.Fatalf("concurrent snapshot put %d failed: %v", i, s.scrub(err))
		}
	}

	// Exactly one winner's INSERT exists per artifact: one row each, and
	// both callers converged on the identical stored content.
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(TableShapeCapabilitiesTable)); n != 1 {
		t.Fatalf("catalog rows after race = %d, want exactly 1", n)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(CapabilitySnapshotsTable)); n != 1 {
		t.Fatalf("snapshot rows after race = %d, want exactly 1", n)
	}
	s.assertReceiptLiteral(t, catalog.ID, kindCatalog, "", binding, catalog.ContentDigest, catalog.CreatedAt, catalog)
	s.assertSnapshotLiteral(t, snapshot)

	// Stale-refusal witness: a re-put of the ORIGINAL content against the
	// row whose content was changed underneath is refused, and the witness
	// row is byte-identical across the refusal.
	w := s.witnessReceipt(catalog.ID)
	snapshotBefore := s.witnessSnapshot(snapshot.Token)
	s.mustWitnessExec(t, `DROP TRIGGER table_shape_capabilities_immutable ON `+s.qualified(TableShapeCapabilitiesTable))
	s.mustWitnessExec(t, `UPDATE `+s.qualified(TableShapeCapabilitiesTable)+
		` SET payload = jsonb_set(payload, '{savedShape,derivedColumns}', '42') WHERE id = $1`, catalog.ID)
	s.mustWitnessExec(t, `CREATE TRIGGER table_shape_capabilities_immutable BEFORE UPDATE OR DELETE ON `+
		s.qualified(TableShapeCapabilitiesTable)+` FOR EACH ROW EXECUTE FUNCTION `+s.qualified("loom_capability_artifact_immutable")+`()`)
	staleWitness := s.witnessReceipt(catalog.ID)
	if staleWitness.ctid == w.ctid {
		t.Fatal("stale fixture tampering did not move the row")
	}
	if _, err := s.receiptsB.PutCatalog(s.ctx, catalog); !errors.Is(err, tableshapecap.ErrIdentityClash) {
		t.Fatalf("stale PutCatalog from writer B = %v, want tableshapecap.ErrIdentityClash", err)
	}
	after := s.witnessReceipt(catalog.ID)
	if after.payloadText != staleWitness.payloadText || after.ctid != staleWitness.ctid {
		t.Fatalf("refused stale PutCatalog changed the witness row: before=%+v after=%+v", staleWitness, after)
	}
	snapshotAfter := s.witnessSnapshot(snapshot.Token)
	if snapshotAfter.payloadText != snapshotBefore.payloadText || snapshotAfter.ctid != snapshotBefore.ctid {
		t.Fatalf("the untouched snapshot moved during the race: before=%+v after=%+v", snapshotBefore, snapshotAfter)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 4: no update path mutates immutable content -- the database
// itself refuses UPDATE and DELETE on both artifact tables, and the guards
// are proven non-vacuous by live guard-drop probes (the violating write must
// succeed only with the guard removed).
// ---------------------------------------------------------------------------

func TestCapabilityImmutabilityGuardsAreEnforcedAtDatabaseLevel(t *testing.T) {
	s := newCapabilityService(t)
	binding := testBinding("pgstate03-immutable")
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	if _, err := s.receipts.PutCatalog(s.ctx, catalog); err != nil {
		s.fail("put catalog", err)
	}
	snapshot := richSnapshot("pgstate03-immutable", "generation-immutable", "zeta")
	if _, err := s.snapshots.Put(s.ctx, snapshot); err != nil {
		s.fail("put snapshot", err)
	}

	// The store-written artifacts are the ones whose integrity is asserted.
	// They are inserted once, never touched by any probe, and re-witnessed
	// byte-for-byte -- including their physical tuple -- at the end.
	beforeReceipt := s.witnessReceipt(catalog.ID)
	beforeSnapshot := s.witnessSnapshot(snapshot.Token)

	// The destructive guard-drop probes run on DEDICATED probe rows, seeded
	// by direct INSERT (inserts are unguarded). Proving a guard non-vacuous
	// necessarily lets the violating write through once, so it cannot target
	// the artifacts whose byte-identity we assert.
	probeToken := "sha256:" + strings.Repeat("7", 64)
	seedProbeSnapshot := func() {
		if err := s.witnessExecErr(`INSERT INTO `+s.qualified(CapabilitySnapshotsTable)+
			` (token, status, complete, truncated, payload) VALUES ($1, 'READY', true, false, '{}'::jsonb)`, probeToken); err != nil {
			s.fail("seed probe snapshot", err)
		}
	}
	probeReceiptSQL := `INSERT INTO ` + s.qualified(TableShapeCapabilitiesTable) + ` (id, kind, parent_catalog_id,
		project, explorer_id, output_id, snapshot_token, authorization_scope, source_generation, draft_version,
		draft_digest, base_document_digest, base_compilation_receipt_id, output_fingerprint, compiler_schema_digest,
		content_digest, created_at, payload)
		VALUES ('tsr_` + strings.Repeat("9", 32) + `', 'RESOLUTION', 'tsc_` + strings.Repeat("8", 32) + `',
		'p', 'e', 'o', 'st', 'as', 'sg', 1, 'dd', 'bd', 'bc', 'of', 'cs', 'sha256:` + strings.Repeat("e", 64) +
		`', '', '{}'::jsonb)`
	probeReceiptID := "tsr_" + strings.Repeat("9", 32)
	seedProbeReceipt := func() {
		if err := s.witnessExecErr(probeReceiptSQL); err != nil {
			s.fail("seed probe receipt", err)
		}
	}
	seedProbeSnapshot()
	seedProbeReceipt()

	// Direct UPDATE and DELETE from the witness backend -- a session with
	// the schema owner's own rights -- are refused by the triggers. The
	// guard-drop probe then proves each refusal was the guard: with the
	// trigger dropped the very same statement succeeds, and after restore
	// it is refused again. The DELETE probes reseed their probe row so the
	// post-restore refusal is exercised against a live tuple.
	s.guardDropProbe(t, "capability_snapshots_immutable", CapabilitySnapshotsTable, nil,
		`UPDATE `+s.qualified(CapabilitySnapshotsTable)+` SET status = 'FAILED' WHERE token = $1`, probeToken)
	s.guardDropProbe(t, "capability_snapshots_immutable", CapabilitySnapshotsTable, seedProbeSnapshot,
		`DELETE FROM `+s.qualified(CapabilitySnapshotsTable)+` WHERE token = $1`, probeToken)
	s.guardDropProbe(t, "table_shape_capabilities_immutable", TableShapeCapabilitiesTable, nil,
		`UPDATE `+s.qualified(TableShapeCapabilitiesTable)+` SET content_digest = $2 WHERE id = $1`,
		probeReceiptID, "sha256:"+strings.Repeat("0", 64))
	s.guardDropProbe(t, "table_shape_capabilities_immutable", TableShapeCapabilitiesTable, seedProbeReceipt,
		`DELETE FROM `+s.qualified(TableShapeCapabilitiesTable)+` WHERE id = $1`, probeReceiptID)

	// The store-written artifacts were never reachable by any of that: same
	// bytes, same physical tuple, exactly as the stores first wrote them.
	afterReceipt := s.witnessReceipt(catalog.ID)
	if afterReceipt.payloadText != beforeReceipt.payloadText || afterReceipt.ctid != beforeReceipt.ctid {
		t.Fatalf("catalog row changed across the guard probes: before=%+v after=%+v", beforeReceipt, afterReceipt)
	}
	afterSnapshot := s.witnessSnapshot(snapshot.Token)
	if afterSnapshot.payloadText != beforeSnapshot.payloadText || afterSnapshot.ctid != beforeSnapshot.ctid {
		t.Fatalf("snapshot row changed across the guard probes: before=%+v after=%+v", beforeSnapshot, afterSnapshot)
	}
}

// ---------------------------------------------------------------------------
// Schema constraint negatives: every column-level rule is refused by the
// database, and the documented legal shapes insert cleanly (the negatives
// refuse only what they claim -- non-vacuity on the constraints side).
// ---------------------------------------------------------------------------

func TestCapabilitySchemaConstraintsRejectInvalidRows(t *testing.T) {
	s := newCapabilityService(t)
	binding := testBinding("pgstate03-constraints")
	catalog := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	if _, err := s.receipts.PutCatalog(s.ctx, catalog); err != nil {
		s.fail("put catalog", err)
	}
	snapshot := richSnapshot("pgstate03-constraints", "generation-constraints", "eta")
	if _, err := s.snapshots.Put(s.ctx, snapshot); err != nil {
		s.fail("put snapshot", err)
	}
	snapFQN := s.qualified(CapabilitySnapshotsTable)
	recFQN := s.qualified(TableShapeCapabilitiesTable)

	type negative struct {
		name      string
		sql       string
		args      []any
		wantState string
	}
	tokenA := "sha256:" + strings.Repeat("a", 64)
	tokenC := "sha256:" + strings.Repeat("c", 64)
	tokenD := "sha256:" + strings.Repeat("d", 64)
	sqlA := `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ($1, $2, $3, $4, $5::jsonb)`
	argsA := []any{tokenA, "BUILDING", false, false, `{"token":"` + tokenA + `"}`}
	// Seed the row the duplicate-token negative below collides with through
	// a plain legal INSERT (inserts are not guarded).
	if err := s.witnessExecErr(sqlA, argsA...); err != nil {
		s.fail("seed duplicate-token fixture", err)
	}

	validReceiptSQL := `INSERT INTO ` + recFQN + ` (id, kind, parent_catalog_id, project, explorer_id, output_id,
		snapshot_token, authorization_scope, source_generation, draft_version, draft_digest, base_document_digest,
		base_compilation_receipt_id, output_fingerprint, compiler_schema_digest, content_digest, created_at, payload)
		VALUES ('tsd_` + strings.Repeat("c", 32) + `', 'CATEGORY_SCAN', 'tsc_` + strings.Repeat("d", 32) + `',
		'p', 'e', 'o', 'st', 'as', 'sg', 1, 'dd', 'bd', 'bc', 'of', 'cs', 'sha256:` + strings.Repeat("e", 64) + `', '', '{}'::jsonb)`

	cases := []negative{
		// --- capability_snapshots ---
		{name: "snapshot token is never SQL NULL", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES (NULL, 'BUILDING', false, false, '{}'::jsonb)`, wantState: "23502"},
		{name: "snapshot token keeps its content-address shape", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('not-a-token', 'BUILDING', false, false, '{}'::jsonb)`, wantState: "23514"},
		{name: "snapshot status is a closed domain", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("1", 64) + `', 'WEIRD', false, false, '{}'::jsonb)`, wantState: "23514"},
		{name: "READY implies complete and not-truncated", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("2", 64) + `', 'READY', false, false, '{}'::jsonb)`, wantState: "23514"},
		{name: "READY implies complete and not-truncated (truncated)", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("3", 64) + `', 'READY', true, true, '{}'::jsonb)`, wantState: "23514"},
		{name: "snapshot payload is never SQL NULL", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("4", 64) + `', 'BUILDING', false, false, NULL)`, wantState: "23502"},
		{name: "snapshot payload JSON null is not an object", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("5", 64) + `', 'BUILDING', false, false, 'null'::jsonb)`, wantState: "23514"},
		{name: "snapshot payload JSON array is not an object", sql: `INSERT INTO ` + snapFQN + ` (token, status, complete, truncated, payload) VALUES ('sha256:` + strings.Repeat("6", 64) + `', 'BUILDING', false, false, '[]'::jsonb)`, wantState: "23514"},
		{name: "duplicate tokens are impossible", sql: sqlA, args: argsA, wantState: "23505"},

		// --- table_shape_capabilities ---
		{name: "receipt kind is a closed domain", sql: strings.Replace(validReceiptSQL, "'CATEGORY_SCAN'", "'BOGUS'", 1), wantState: "23514"},
		{name: "catalog kind requires the tsc_ prefix", sql: strings.Replace(strings.Replace(validReceiptSQL, "'CATEGORY_SCAN'", "'CATALOG'", 1),
			"'tsd_"+strings.Repeat("c", 32)+"'", "'tsd_"+strings.Repeat("c", 32)+"'", 1), wantState: "23514"},
		{name: "receipt binding columns are never SQL NULL", sql: strings.Replace(validReceiptSQL, "'p', 'e', 'o'", "NULL, 'e', 'o'", 1), wantState: "23502"},
		{name: "receipt binding columns are never empty", sql: strings.Replace(validReceiptSQL, "'p', 'e', 'o'", "'', 'e', 'o'", 1), wantState: "23514"},
		{name: "draft_version is never SQL NULL", sql: strings.Replace(validReceiptSQL, "1, 'dd'", "NULL, 'dd'", 1), wantState: "23502"},
		{name: "draft_version has a floor of 1", sql: strings.Replace(validReceiptSQL, "1, 'dd'", "0, 'dd'", 1), wantState: "23514"},
		{name: "content_digest keeps its sha256 shape", sql: strings.Replace(validReceiptSQL, "'sha256:"+strings.Repeat("e", 64)+"'", "'md5:deadbeef'", 1), wantState: "23514"},
		{name: "non-catalog rows are parent-bound", sql: strings.Replace(validReceiptSQL, "'CATEGORY_SCAN', 'tsc_"+strings.Repeat("d", 32)+"'", "'CATEGORY_SCAN', ''", 1), wantState: "23514"},
		{name: "receipt payload is never SQL NULL", sql: strings.Replace(validReceiptSQL, "'{}'::jsonb", "NULL", 1), wantState: "23502"},
		{name: "receipt payload JSON scalar is not an object", sql: strings.Replace(validReceiptSQL, "'{}'::jsonb", "'7'::jsonb", 1), wantState: "23514"},
		{name: "duplicate receipt IDs are impossible", sql: `INSERT INTO ` + recFQN + ` (id, kind, parent_catalog_id, project, explorer_id, output_id,
			snapshot_token, authorization_scope, source_generation, draft_version, draft_digest, base_document_digest,
			base_compilation_receipt_id, output_fingerprint, compiler_schema_digest, content_digest, created_at, payload)
			SELECT id, kind, parent_catalog_id, project, explorer_id, output_id, snapshot_token, authorization_scope,
			source_generation, draft_version, draft_digest, base_document_digest, base_compilation_receipt_id,
			output_fingerprint, compiler_schema_digest, content_digest, created_at, payload FROM ` + recFQN + ` WHERE id = $1`,
			args: []any{catalog.ID}, wantState: "23505"},
	}
	for _, c := range cases {
		err := s.witnessExecErr(c.sql, c.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != c.wantState {
			t.Errorf("%s: err = %v, want SQLSTATE %s", c.name, s.scrub(err), c.wantState)
		}
	}

	// Controls: the documented legal shapes insert cleanly, so every
	// refusal above was the constraint doing its job and nothing else.
	if err := s.witnessExecErr(`INSERT INTO `+snapFQN+` (token, status, complete, truncated, payload) VALUES ($1, 'BUILDING', false, true, '{}'::jsonb)`, tokenC); err != nil {
		t.Errorf("control: legal BUILDING/truncated snapshot insert refused: %s", s.scrub(err))
	}
	if err := s.witnessExecErr(`INSERT INTO `+snapFQN+` (token, status, complete, truncated, payload) VALUES ($1, 'FAILED', true, true, '{}'::jsonb)`, tokenD); err != nil {
		t.Errorf("control: legal FAILED snapshot insert refused: %s", s.scrub(err))
	}
	if err := s.witnessExecErr(validReceiptSQL); err != nil {
		t.Errorf("control: legal category-scan row insert refused: %s", s.scrub(err))
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+snapFQN+" WHERE token = ANY($1)", []string{tokenC, tokenD}); n != 2 {
		t.Fatalf("control snapshot rows = %d, want 2", n)
	}
}

// ---------------------------------------------------------------------------
// no-fallback guarantee: an unmigrated schema surfaces the missing relation
// (SQLSTATE 42P01) on every store method -- never a typed miss, never a
// fallback.
// ---------------------------------------------------------------------------

func TestCapabilityStoresFailClosedWithoutTheirTables(t *testing.T) {
	s := newCapabilityService(t)
	emptySchema := fmt.Sprintf("pgstate03_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate03ResourceNamePattern.MatchString(emptySchema) {
		t.Fatalf("owned schema name %q does not match the required pattern", emptySchema)
	}
	s.mustWitnessExec(t, `CREATE SCHEMA "`+emptySchema+`"`)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := execOn(cleanupCtx, s.witness, `DROP SCHEMA IF EXISTS "`+emptySchema+`" CASCADE`); err != nil {
			t.Errorf("drop empty schema: %s", s.scrub(err))
		}
	})
	orphanSnapshots, err := NewCapabilitySnapshotStore(s.witness, emptySchema)
	if err != nil {
		s.fail("build orphan snapshot store", err)
	}
	orphanReceipts, err := NewTableShapeCapabilityStore(s.witness, emptySchema)
	if err != nil {
		s.fail("build orphan receipt store", err)
	}

	binding := testBinding("pgstate03-unmigrated")
	catalog := testCatalog(t, binding, "")
	scan := testScan(t, catalog, "")
	pivot := testPivot(t, catalog, scan, "")
	lookup := tableshapecap.CatalogLookup{
		Project: binding.Project, ExplorerID: binding.ExplorerID, OutputID: binding.OutputID,
		SnapshotToken: binding.SnapshotToken, AuthorizationScope: binding.AuthorizationScope,
		SourceGeneration: binding.SourceGeneration, DraftVersion: binding.DraftVersion,
		DraftDigest: binding.DraftDigest, BaseDocumentDigest: binding.BaseDocumentDigest,
	}
	snapshot := richSnapshot("pgstate03-unmigrated", "generation-unmigrated", "theta")

	operations := []struct {
		name string
		run  func() error
	}{
		{"snapshot put", func() error { _, err := orphanSnapshots.Put(s.ctx, snapshot); return err }},
		{"snapshot get by token", func() error { _, err := orphanSnapshots.GetByToken(s.ctx, snapshot.Token); return err }},
		{"snapshot get by identity", func() error { _, err := orphanSnapshots.GetByIdentity(s.ctx, snapshot.Identity); return err }},
		{"catalog put", func() error { _, err := orphanReceipts.PutCatalog(s.ctx, catalog); return err }},
		{"catalog get", func() error { _, err := orphanReceipts.GetCatalog(s.ctx, binding, catalog.ID); return err }},
		{"catalog lookup", func() error { _, err := orphanReceipts.GetCatalogForLookup(s.ctx, lookup, catalog.ID); return err }},
		{"scan put", func() error { _, err := orphanReceipts.PutCategoryScan(s.ctx, scan); return err }},
		{"scan get", func() error {
			_, err := orphanReceipts.GetCategoryScan(s.ctx, binding, catalog.ID, scan.ID)
			return err
		}},
		{"resolution put", func() error { _, err := orphanReceipts.PutResolution(s.ctx, pivot); return err }},
		{"resolution get", func() error { _, err := orphanReceipts.GetResolution(s.ctx, binding, catalog.ID, pivot.ID); return err }},
	}
	for _, op := range operations {
		err := op.run()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
			t.Fatalf("%s against unmigrated schema = %v, want SQLSTATE 42P01, never a typed miss or fallback",
				op.name, s.scrub(err))
		}
	}
}

// ---------------------------------------------------------------------------
// zero leftovers, ownership-scoped: the suite must not leave any
// pgstate03_it_* resource. This test MUST stay the last test declared in
// this file. It sweeps ONLY this ticket's prefix: concurrent sibling suites
// (pgpubrel01_it_*, pgstate01v1_it_*, ...) audit their own prefixes in
// their own files, and a global scan here would couple unrelated tickets'
// results to each other.
// ---------------------------------------------------------------------------

func TestZZNoLeftoverPgstate03Resources(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var leftoverDBs int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstate03\_it\_%' ESCAPE '\'`).Scan(&leftoverDBs); err != nil {
		t.Fatalf("count leftover databases: %v", err)
	}
	if leftoverDBs != 0 {
		var names []string
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgstate03\_it\_%' ESCAPE '\'`)
		if err == nil {
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err == nil {
					names = append(names, n)
				}
			}
			rows.Close()
		}
		t.Fatalf("%d isolated databases left behind: %v", leftoverDBs, names)
	}

	var leftoverSchemas int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstate03\_it\_%' ESCAPE '\'`).Scan(&leftoverSchemas); err != nil {
		t.Fatalf("count leftover schemas: %v", err)
	}
	if leftoverSchemas != 0 {
		t.Fatalf("%d owned schemas left behind in the service database", leftoverSchemas)
	}
}

// assertSnapshotEquivalent asserts the returned snapshot is the very
// content-addressed artifact that was stored: the token still binds the
// returned canonical payload bytes (sha256 over CanonicalPayload), those
// bytes equal the want snapshot's canonical payload, and the status triple
// round-trips. Raw struct equality would assert the fixture's input array
// order rather than storage fidelity: the domain canonicalises ordering
// when it builds the payload that the token hashes and the stores persist
// (exactly like the Arango adapter's stored documents).
func assertSnapshotEquivalent(t *testing.T, label string, got *capability.Snapshot, want capability.Snapshot) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want the stored snapshot", label)
	}
	if got.Token != want.Token {
		t.Fatalf("%s token = %q, want %q", label, got.Token, want.Token)
	}
	gotPayload := got.CanonicalPayload()
	sum := sha256.Sum256(gotPayload)
	if got.Token != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("%s returned a snapshot whose token does not bind its canonical payload", label)
	}
	if !bytes.Equal(gotPayload, want.CanonicalPayload()) {
		t.Fatalf("%s returned canonical payload bytes that differ from the stored artifact", label)
	}
	if got.Status != want.Status || got.Complete != want.Complete || got.Truncated != want.Truncated {
		t.Fatalf("%s status triple = %v/%v/%v, want %v/%v/%v", label,
			got.Status, got.Complete, got.Truncated, want.Status, want.Complete, want.Truncated)
	}
}
