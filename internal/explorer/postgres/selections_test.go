package postgres

// Real-PostgreSQL 18 acceptance tests for staged selection revision
// identity, project-scoped idempotency key, and expiring writer owner
// (pg-state-selection-revision-owner-v1; check-2:
// go test -count=1 -run ^TestSelectionStorageAgainstPostgres$ ./internal/explorer/postgres/,
// plus the full-package run go test -count=1 ./internal/explorer/postgres/).
//
// Everything here runs against the service named by
// LOOM_TEST_POSTGRES_URL: each test creates one uniquely-named isolated
// database (pgstate05_it_*) plus one owned schema (pgstate05_it_*) inside
// it and drops both in its cleanups, so the suite leaves zero leftovers.
// The fixture history is composed through the real pg-store-02 runner from
// this package's embedded owner history and this ticket's state-scoped
// own-dir fixture tests/migrations-selections/000008_selection_revisions.sql;
// NewMigrationRunner composes and sorts the union onto the ONE shared
// per-schema ledger (proven precedent: the capability suite composes
// versions 1,2,3 + 4 the same way), and the ledger rows are asserted
// literally with the fixture checksum self-recomputed from the file bytes.
//
// Every lifecycle claim is driven through real Store methods on real
// connections; expected rows are asserted literally through direct SQL on a
// witness pool that is a third, physically distinct backend connection, and
// concurrent proofs run over two single-connection clients with provably
// distinct pg_backend_pid() values, at least 10 rounds per racing surface,
// with byte-identical row witnesses (every column plus ctid) and
// post-abort convergence checks. Database-enforced immutability is proven
// non-vacuous by live guard-drop probes: the violating write must fail with
// the trigger in place (SQLSTATE 45501, exact message), succeed with the
// trigger dropped, and fail again once restored; every constraint refusal
// asserts its exact SQLSTATE and constraint name. Crash windows are
// injected through the in-transaction seams. Nothing is mocked and no test
// here passes by skipping: a run presented as acceptance evidence requires
// the live service (see testPostgresURL). Concurrency tests run serially
// (no t.Parallel anywhere).
//
// The TestZZ zero-leftover audit is ownership-scoped: it checks each
// exact name this file created and verified, with no global scan, so
// sibling suites sweeping their own prefixes run concurrently without
// interference. Foreign prefixes are not this file's business.

import (
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

	"github.com/calypr/loom/internal/explorer"
	pgstore "github.com/calypr/loom/internal/store/postgres"
)

// The fixture pins below follow the coordinator allocation: version 8 of
// the Explorer control-plane ledger, one migration covering both selection
// tables (000004's two-table precedent), own-dir composition per the
// accepted 000004 capability precedent. The Explorer ledger and the
// store-dir ledger are separate tables in separate dirs -- the store
// history's own 000008 (internal/store/postgres/migrations/
// 000008_batch_write_ledger.sql) is a different ledger, so the shared
// number is not a collision. Ledger expectations elsewhere in this file are
// self-derived from the FS-loaded history, never literal counts.
const (
	pgstate05FixtureDir     = "tests/migrations-selections"
	pgstate05FixtureFile    = "000008_selection_revisions"
	pgstate05FixtureName    = "selection_revisions"
	pgstate05FixtureVersion = int64(8)
	pgstate05ImmutableCode  = "45501"
	// pgstate05RaceRounds is the per-surface race floor; the racing
	// surfaces each run at least this many fresh rounds.
	pgstate05RaceRounds = 10

	pgstate05Project = "pgstate05-p"
)

// Golden member-key hexes for (pgstate05-p, gen-a, DocumentReference,
// files00N), pinned independently of the adapter's derivation so a drift
// in selectionMemberKey breaks the literal, not just its mirror.
const (
	pgstate05GoldenKeyFiles001 = "881744de4921fab22a53cec80560023da4ef4071cbe1fec6130e0222c525724a"
	pgstate05GoldenKeyFiles002 = "a88d58e2d139439341429d35e241c7911ed4a42893a9ac38b21a80d70b684f3f"
	pgstate05GoldenKeyFiles003 = "0f26d620a48845ee8df20bdc13c8bb56f47479d3953c3e4ccb1965b5fd43279a"
)

var pgstate05NameRe = regexp.MustCompile(`^pgstate05_it_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every resource whose creation this file verified is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE, exactly as the per-test cleanup drops them.
var (
	pgstate05OwnedMu      sync.Mutex
	pgstate05OwnedRes     = map[string]string{} // database -> owned schema
	pgstate05ExtraSchemas []string              // schemas created outside per-test fixtures (fail-closed probe)
)

func pgstate05Register(db, schema string) {
	pgstate05OwnedMu.Lock()
	defer pgstate05OwnedMu.Unlock()
	pgstate05OwnedRes[db] = schema
}

func pgstate05RegisterExtraSchema(schema string) {
	pgstate05OwnedMu.Lock()
	defer pgstate05OwnedMu.Unlock()
	pgstate05ExtraSchemas = append(pgstate05ExtraSchemas, schema)
}

// ---------------------------------------------------------------------------
// Isolated PostgreSQL fixture: one uniquely owned database plus one owned
// schema per test, named pgstate05_it_*; three single-connection clients
// (witness + writer A + writer B) with provably distinct backends.
// ---------------------------------------------------------------------------

type selectionService struct {
	ctx     context.Context
	admin   *pgxpool.Pool
	witness *pgstore.Client
	pidW    int32
	pidA    int32
	pidB    int32
	schema  string
	scrub   func(any) string
	fail    func(step string, err error)

	storeA *Store
	storeB *Store

	owner       []pgstore.Migration
	fixture     pgstore.Migration
	ledgerAfter []pgstore.LedgerRecord
}

func newSelectionService(t *testing.T) *selectionService {
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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstate05_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate05NameRe.MatchString(dbName) {
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

	schema := fmt.Sprintf("pgstate05_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstate05NameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if err := execOn(ctx, witness, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	pgstate05Register(dbName, schema)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := execOn(cleanupCtx, witness, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &selectionService{
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

	storeA, err := New(clientA, schema)
	if err != nil {
		fail("build store A", err)
	}
	s.storeA = storeA
	storeB, err := New(clientB, schema)
	if err != nil {
		fail("build store B", err)
	}
	s.storeB = storeB
	return s
}

// backendPID asks one client (a single physical connection) to name its
// server backend.
func (s *selectionService) backendPID(t *testing.T, client *pgstore.Client, label string) int32 {
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
// owner history plus this ticket's state-scoped own-dir selection fixture --
// through the real pg-store-02 runner onto the ONE shared per-schema ledger,
// and asserts the ledger literally with the fixture checksum
// self-recomputed from the file bytes. The composition-and-sort behavior is
// the proven tests/migrations-capabilities precedent: NewMigrationRunner
// copies, recomputes checksums, and sorts by version, so a future owner
// history [1,2,3,5,6,7] plus this fixture [8] converges to one ascending
// union regardless of compose order.
func (s *selectionService) migrate(t *testing.T) {
	t.Helper()
	owner, err := OwnerMigrations()
	if err != nil {
		s.fail("owner migrations", err)
	}
	if len(owner) < 2 || owner[0].Version != 1 || owner[0].Name != "ledger" ||
		owner[1].Version != 2 || owner[1].Name != "explorer_owner" {
		t.Fatalf("owner history = %+v, want ledger(1) + explorer_owner(2) at the front", owner)
	}
	fixture, err := pgstore.LoadMigrations(os.DirFS("../../../"), pgstate05FixtureDir)
	if err != nil {
		s.fail("load selection fixture migrations", err)
	}
	if len(fixture) != 1 || fixture[0].Version != pgstate05FixtureVersion ||
		fixture[0].Name != pgstate05FixtureName {
		t.Fatalf("selection fixture history = %+v, want exactly version %d %s",
			fixture, pgstate05FixtureVersion, pgstate05FixtureName)
	}
	// Self-recomputed sha256 pin: the ledger row the runner records must
	// equal the digest this test computes independently over the exact
	// bytes on disk.
	diskBytes, err := os.ReadFile("../../../" + pgstate05FixtureDir + "/" + pgstate05FixtureFile + ".sql")
	if err != nil {
		s.fail("read selection fixture file", err)
	}
	sum := sha256.Sum256(diskBytes)
	if got := hex.EncodeToString(sum[:]); got != fixture[0].Checksum {
		t.Fatalf("fixture checksum self-recompute = %s, runner computed %s", got, fixture[0].Checksum)
	}
	history := append(append([]pgstore.Migration{}, owner...), fixture[0])
	runner, err := pgstore.NewMigrationRunner(s.witness, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate selection history", err)
	}
	s.owner = owner
	s.fixture = fixture[0]
	s.ledgerAfter = result.Applied

	want := make([]pgstore.LedgerRecord, 0, len(owner)+1)
	for _, m := range owner {
		want = append(want, pgstore.LedgerRecord{Version: m.Version, Name: m.Name, Checksum: m.Checksum})
	}
	want = append(want, pgstore.LedgerRecord{Version: fixture[0].Version, Name: fixture[0].Name, Checksum: fixture[0].Checksum})
	sort.Slice(want, func(i, j int) bool { return want[i].Version < want[j].Version })
	var maxVersion int64
	for _, w := range want {
		if w.Version > maxVersion {
			maxVersion = w.Version
		}
	}
	if result.LatestVersion != maxVersion {
		t.Fatalf("latest migration version = %d, want %d", result.LatestVersion, maxVersion)
	}
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

func (s *selectionService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *selectionService) headersFQN() string { return s.qualified(SelectionRevisionsTable) }
func (s *selectionService) membersFQN() string { return s.qualified(SelectionMembersTable) }

func (s *selectionService) mustWitnessExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if err := execOn(s.ctx, s.witness, sql, args...); err != nil {
		s.fail("witness exec "+sql, err)
	}
}

func (s *selectionService) witnessExecErr(sql string, args ...any) error {
	return execOn(s.ctx, s.witness, sql, args...)
}

// witnessExecTag runs a witness write and reports the affected row count.
func (s *selectionService) witnessExecTag(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire tag witness", err)
	}
	defer conn.Release()
	tag, err := conn.Exec(s.ctx, sql, args...)
	if err != nil {
		s.fail("witness tag exec "+sql, err)
	}
	return tag.RowsAffected()
}

func (s *selectionService) mustWitnessBool(t *testing.T, sql string, args ...any) bool {
	t.Helper()
	var value bool
	if err := queryRowOn(s.ctx, s.witness, &value, sql, args...); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

// parseWitnessTime decodes the text rendering of a timestamptz column
// (PostgreSQL's DateStyle ISO output, e.g. "2026-09-19 00:00:00+00").
func parseWitnessTime(t *testing.T, text string) time.Time {
	t.Helper()
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999-0700",
		"2006-01-02 15:04:05-07",
	} {
		if ts, err := time.Parse(layout, text); err == nil {
			return ts
		}
	}
	t.Fatalf("cannot parse witness timestamptz text %q", text)
	return time.Time{}
}

func (s *selectionService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := queryRowOn(s.ctx, s.witness, &n, sql, args...); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

// witnessedSelection is the literal content of one selection_revisions row:
// every column rendered as text (byte-identity witness) plus the physical
// ctid, which moves on any UPDATE.
type witnessedSelection struct {
	id, project, generation, resourceType            string
	ruleDigest, scopeDigest, idempotencyKey          string
	state, writerToken, membershipDigest             string
	writerExpiresText, createdAtText, completeAtText string
	ruleText, sourceText, exclusionsText             string
	memberCount, memberBytes                         int64
	complete                                         bool
	ctid                                             string
	exists                                           bool
}

func (w witnessedSelection) same(o witnessedSelection) bool {
	return w == o
}

func (s *selectionService) witnessSelection(id string) witnessedSelection {
	var w witnessedSelection
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire selection witness", err)
	}
	defer conn.Release()
	err = conn.QueryRow(s.ctx,
		`SELECT id, project, generation, resource_type, rule_digest, scope_digest, idempotency_key,
		        state, writer_token, membership_digest,
		        writer_expires_at::text, created_at::text, COALESCE(completed_at::text, '<sql-null>'),
		        rule::text, source::text, COALESCE(exclusions::text, '<sql-null>'),
		        member_count, member_bytes, complete, ctid::text
		 FROM `+s.headersFQN()+` WHERE id = $1`, id).
		Scan(&w.id, &w.project, &w.generation, &w.resourceType, &w.ruleDigest, &w.scopeDigest,
			&w.idempotencyKey, &w.state, &w.writerToken, &w.membershipDigest,
			&w.writerExpiresText, &w.createdAtText, &w.completeAtText,
			&w.ruleText, &w.sourceText, &w.exclusionsText,
			&w.memberCount, &w.memberBytes, &w.complete, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness selection "+id, err)
	}
	w.exists = true
	return w
}

func (s *selectionService) assertSelectionAbsent(t *testing.T, id string) {
	t.Helper()
	if w := s.witnessSelection(id); w.exists {
		t.Fatalf("expected no selection_revisions row for %s, found %+v", id, w)
	}
}

func (s *selectionService) assertSelectionStable(t *testing.T, id string, before witnessedSelection) witnessedSelection {
	t.Helper()
	after := s.witnessSelection(id)
	if !after.same(before) {
		t.Fatalf("selection %s row moved when it must not have:\nbefore=%+v\nafter =%+v", id, before, after)
	}
	return after
}

// assertHeaderJSONB witnesses jsonb equality of the rule/source/exclusions
// payloads against literal documents.
func (s *selectionService) assertHeaderJSONB(t *testing.T, id, column string, want any, isNull bool) {
	t.Helper()
	if isNull {
		if !s.mustWitnessBool(t, `SELECT `+column+` IS NULL FROM `+s.headersFQN()+` WHERE id = $1`, id) {
			t.Fatalf("selection %s %s must be SQL NULL", id, column)
		}
		return
	}
	document, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !s.mustWitnessBool(t, `SELECT `+column+` = $1::jsonb FROM `+s.headersFQN()+` WHERE id = $2`, string(document), id) {
		w := s.witnessSelection(id)
		var text string
		switch column {
		case "rule":
			text = w.ruleText
		case "source":
			text = w.sourceText
		default:
			text = w.exclusionsText
		}
		t.Fatalf("selection %s %s does not equal the literal document\nwant: %s\ngot:  %s", id, column, document, text)
	}
}

// witnessedMember is the literal content of one selection_members row.
type witnessedMember struct {
	selectionID, memberKey, project, generation, resourceType, id string
	ordinal                                                       int64
	ctid                                                          string
	exists                                                        bool
}

func (w witnessedMember) same(o witnessedMember) bool { return w == o }

func (s *selectionService) witnessMember(selectionID, memberKey string) witnessedMember {
	var w witnessedMember
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire member witness", err)
	}
	defer conn.Release()
	err = conn.QueryRow(s.ctx,
		`SELECT selection_id, member_key, project, generation, resource_type, id, ordinal, ctid::text
		 FROM `+s.membersFQN()+` WHERE selection_id = $1 AND member_key = $2`, selectionID, memberKey).
		Scan(&w.selectionID, &w.memberKey, &w.project, &w.generation, &w.resourceType, &w.id, &w.ordinal, &w.ctid)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness member "+memberKey, err)
	}
	w.exists = true
	return w
}

func (s *selectionService) memberCount(t *testing.T, selectionID string) int {
	t.Helper()
	return s.execCount(t, "SELECT count(*) FROM "+s.membersFQN()+" WHERE selection_id = $1", selectionID)
}

func (s *selectionService) assertMemberAbsent(t *testing.T, selectionID, memberKey string) {
	t.Helper()
	if w := s.witnessMember(selectionID, memberKey); w.exists {
		t.Fatalf("expected no member row %s, found %+v", memberKey, w)
	}
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func pgstate05Hex(t *testing.T) string { return randomHex(t) }

// selectionHeaderFixture builds a valid, rich, incomplete selection
// revision: an ALL_MATCHING rule with typed filters, a fully bound
// published source, and per-instance digests/key.
func selectionHeaderFixture(id, key string) explorer.SelectionRevision {
	return explorer.SelectionRevision{
		ID: id, Project: pgstate05Project, Generation: "gen-a", ResourceType: "DocumentReference",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleAllMatching, Filters: []explorer.SelectionFilter{
			{Column: "status", Op: "EQ", Value: "final"},
			{Column: "category", Op: "IN", Value: []any{"vitals", "labs"}},
		}},
		Source: explorer.SelectionSource{
			Kind: explorer.SelectionSourcePublished, Generation: "gen-a",
			RevisionID: "rev-1", ReceiptID: "receipt-1", ExecutionID: "exec-1",
			OutputID: "out-1", SchemaDigest: "schema-digest-1",
			ResourceType: "DocumentReference", SourceIDColumn: "id",
		},
		ScopeDigest: "scope-" + id, RuleDigest: "rule-" + id,
		IdempotencyKey: key, CreatedAt: time.Now().UTC(),
	}
}

func selectionRef(id string) explorer.SelectionMember {
	return explorer.SelectionMember{Ref: explorer.ResourceRef{
		Project: pgstate05Project, Generation: "gen-a", ResourceType: "DocumentReference", ID: id,
	}}
}

// goldenMemberKey mirrors the derivation independently of the adapter:
// selectionID + "_" + hex(sha256 over 0x00-prefixed parts).
func goldenMemberKey(t *testing.T, selectionID string, ref explorer.ResourceRef) string {
	t.Helper()
	h := sha256.New()
	for _, part := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return selectionID + "_" + hex.EncodeToString(h.Sum(nil))
}

func membershipFacts(refs []explorer.SelectionMember) (string, int64, int64) {
	hash := sha256.New()
	var count, byteTotal int64
	type refKey struct{ p, g, t, i string }
	seen := map[refKey]struct{}{}
	unique := make([]explorer.ResourceRef, 0, len(refs))
	for _, m := range refs {
		k := refKey{m.Ref.Project, m.Ref.Generation, m.Ref.ResourceType, m.Ref.ID}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		unique = append(unique, m.Ref)
	}
	sort.Slice(unique, func(i, j int) bool {
		li := []string{unique[i].Project, unique[i].Generation, unique[i].ResourceType, unique[i].ID}
		lj := []string{unique[j].Project, unique[j].Generation, unique[j].ResourceType, unique[j].ID}
		for k := range li {
			if li[k] != lj[k] {
				return li[k] < lj[k]
			}
		}
		return false
	})
	for _, ref := range unique {
		explorer.WriteMembershipFrame(hash, ref)
		for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
			byteTotal += int64(8 + len(value))
		}
		count++
	}
	return hex.EncodeToString(hash.Sum(nil)), count, byteTotal
}

// assertRevisionEquals compares the returned revision against the expected
// domain value field by field (instants via Equal, canonical rule/source,
// exclusions order).
func assertRevisionEquals(t *testing.T, label string, got *explorer.SelectionRevision, want explorer.SelectionRevision) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want the stored revision", label)
	}
	want = want.Canonical()
	if got.ID != want.ID || got.Project != want.Project || got.Generation != want.Generation ||
		got.ResourceType != want.ResourceType || got.ScopeDigest != want.ScopeDigest ||
		got.RuleDigest != want.RuleDigest || got.MembershipDigest != want.MembershipDigest ||
		got.MemberCount != want.MemberCount || got.MemberBytes != want.MemberBytes ||
		got.Complete != want.Complete || got.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("%s = %+v, want %+v", label, got, want)
	}
	if !reflect.DeepEqual(got.Rule, want.Rule) {
		t.Fatalf("%s rule = %+v, want %+v", label, got.Rule, want.Rule)
	}
	if got.Source != want.Source {
		t.Fatalf("%s source = %+v, want %+v", label, got.Source, want.Source)
	}
	if !reflect.DeepEqual(got.Exclusions, want.Exclusions) {
		t.Fatalf("%s exclusions = %+v, want %+v", label, got.Exclusions, want.Exclusions)
	}
	if !got.CreatedAt.Truncate(time.Microsecond).Equal(want.CreatedAt.Truncate(time.Microsecond)) {
		// timestamptz has microsecond precision; the stored instant is
		// authoritative and callers compare at that grain (the Arango
		// adapter round-trips the same field at millisecond precision).
		t.Fatalf("%s createdAt: got %s want %s", label, got.CreatedAt, want.CreatedAt)
	}
	switch {
	case want.CompletedAt == nil && got.CompletedAt != nil:
		t.Fatalf("%s completedAt = %v, want missing", label, *got.CompletedAt)
	case want.CompletedAt != nil && got.CompletedAt == nil:
		t.Fatalf("%s completedAt missing, want %v", label, *want.CompletedAt)
	case want.CompletedAt != nil && !got.CompletedAt.Equal(*want.CompletedAt):
		t.Fatalf("%s completedAt: got %v want %v", label, *got.CompletedAt, *want.CompletedAt)
	}
}

// ---------------------------------------------------------------------------
// literal witness-INSERT builders (schema-complete defaults, per-column
// overrides for the constraint probes)
// ---------------------------------------------------------------------------

var selectionHeaderDefaults = []struct{ column, literal string }{
	{"id", "'p5-header-1'"},
	{"project", "'" + pgstate05Project + "'"},
	{"generation", "'gen-a'"},
	{"resource_type", "'DocumentReference'"},
	{"rule_digest", "'rule-1'"},
	{"scope_digest", "'scope-1'"},
	{"idempotency_key", "'key-1'"},
	{"state", "'STAGING'"},
	{"writer_token", "'writer-1'"},
	{"writer_expires_at", "'2099-01-01T00:00:00Z'::timestamptz"},
	{"membership_digest", "''"},
	{"member_count", "0"},
	{"member_bytes", "0"},
	{"complete", "false"},
	{"created_at", "'2026-09-19T00:00:00Z'::timestamptz"},
	{"completed_at", "NULL"},
	{"rule", `'{"kind":"EXPLICIT"}'::jsonb`},
	{"source", `'{"kind":"EXPLICIT_REFS"}'::jsonb`},
	{"exclusions", "NULL"},
}

var selectionMemberDefaults = []struct{ column, literal string }{
	{"selection_id", "'p5-header-1'"},
	{"member_key", "'p5-header-1_" + pgstate05GoldenKeyFiles001 + "'"},
	{"project", "'" + pgstate05Project + "'"},
	{"generation", "'gen-a'"},
	{"resource_type", "'DocumentReference'"},
	{"id", "'files001'"},
	{"ordinal", "0"},
}

func selectionInsertSQL(columns []struct{ column, literal string }, overrides map[string]string) string {
	cols := make([]string, 0, len(columns))
	vals := make([]string, 0, len(columns))
	for _, c := range columns {
		lit := c.literal
		if override, ok := overrides[c.column]; ok {
			lit = override
		}
		cols = append(cols, c.column)
		vals = append(vals, lit)
	}
	return "INSERT INTO %s (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")"
}

func (s *selectionService) headerInsertSQL(overrides map[string]string) string {
	return fmt.Sprintf(selectionInsertSQL(selectionHeaderDefaults, overrides), s.headersFQN())
}

func (s *selectionService) memberInsertSQL(overrides map[string]string) string {
	return fmt.Sprintf(selectionInsertSQL(selectionMemberDefaults, overrides), s.membersFQN())
}

// assertPgError witnesses an exact SQLSTATE plus constraint name so every
// refusal is attributed to the named database object, not just the class.
func assertPgError(t *testing.T, label string, err error, wantState, wantConstraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("%s: err = %v, want SQLSTATE %s constraint %s", label, err, wantState, wantConstraint)
	}
	if pgErr.Code != wantState {
		t.Fatalf("%s: SQLSTATE = %s (%s), want %s", label, pgErr.Code, pgErr.Message, wantState)
	}
	if wantConstraint != "" && pgErr.ConstraintName != wantConstraint {
		t.Fatalf("%s: constraint = %q (SQLSTATE %s, message %s), want %q",
			label, pgErr.ConstraintName, pgErr.Code, pgErr.Message, wantConstraint)
	}
}

// guardDropProbe proves a guard is not vacuous (the pg-catalog-04 /
// pg-state-immutable-capability bar): the violating statement must fail
// with SQLSTATE 45501 while the trigger is attached, must SUCCEED once the
// trigger is dropped, and must fail again once restored. reseed, when
// non-nil, runs after the successful unguarded write to restore the probe
// row for the restore check.
func (s *selectionService) guardDropProbe(t *testing.T, trigger, table, guardFunction string, reseed func(), stmt string, args ...any) {
	t.Helper()
	tableFQN := s.qualified(table)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s missing from %s before probe", trigger, table)
	}
	err := s.witnessExecErr(stmt, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate05ImmutableCode {
		t.Fatalf("guarded violating write on %s = %v, want SQLSTATE %s", table, s.scrub(err), pgstate05ImmutableCode)
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
		t.Fatalf("same violating write did NOT run with the trigger dropped on %s: tag=%v err=%v; the guard was not what blocked it",
			table, tag, s.scrub(execErr))
	}
	if reseed != nil {
		reseed()
	}
	s.mustWitnessExec(t, `CREATE TRIGGER `+trigger+` BEFORE UPDATE ON `+tableFQN+
		` FOR EACH ROW EXECUTE FUNCTION `+s.qualified(guardFunction)+`()`)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s not restored on %s", trigger, table)
	}
	err = s.witnessExecErr(stmt, args...)
	if !errors.As(err, &pgErr) || pgErr.Code != pgstate05ImmutableCode {
		t.Fatalf("violating write after trigger restore = %v, want SQLSTATE %s", s.scrub(err), pgstate05ImmutableCode)
	}
}

func (s *selectionService) triggerPresent(t *testing.T, trigger, table string) bool {
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

// guardThenCheckProbe proves a layered defense: the BEFORE UPDATE trigger
// refuses the write first (45501); with the trigger dropped the statement
// must be refused by the named table CHECK instead (a different layer
// catching what the guard would have blocked), and with the trigger
// restored the guard refuses first again. Used for tamper attempts whose
// payload is ALSO illegal by table constraint (un-completing a row), so
// the guard's first-line refusal is what is proven non-vacuous.
func (s *selectionService) guardThenCheckProbe(t *testing.T, trigger, table, guardFunction, stmt string, wantState, wantConstraint string, args ...any) {
	t.Helper()
	tableFQN := s.qualified(table)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s missing from %s before probe", trigger, table)
	}
	assertPgError(t, "guarded write "+stmt, s.witnessExecErr(stmt, args...), pgstate05ImmutableCode, "")
	s.mustWitnessExec(t, `DROP TRIGGER `+trigger+` ON `+tableFQN)
	assertPgError(t, "un-guarded write "+stmt, s.witnessExecErr(stmt, args...), wantState, wantConstraint)
	s.mustWitnessExec(t, `CREATE TRIGGER `+trigger+` BEFORE UPDATE ON `+tableFQN+
		` FOR EACH ROW EXECUTE FUNCTION `+s.qualified(guardFunction)+`()`)
	if !s.triggerPresent(t, trigger, table) {
		t.Fatalf("guard trigger %s not restored on %s", trigger, table)
	}
	assertPgError(t, "restored-guard write "+stmt, s.witnessExecErr(stmt, args...), pgstate05ImmutableCode, "")
}

// expireLease moves a staged row's writer lease into the past through the
// witness backend. writer_expires_at is deliberately OUTSIDE the frozen
// identity set, so this is a legal unguarded write even with the trigger
// attached (proven as a control in the guard test).
func (s *selectionService) expireLease(t *testing.T, id string) {
	t.Helper()
	if n := s.witnessExecTag(t,
		`UPDATE `+s.headersFQN()+` SET writer_expires_at = '2020-01-01T00:00:00Z'::timestamptz WHERE id = $1`, id); n != 1 {
		t.Fatalf("expire lease %s affected %d rows, want 1", id, n)
	}
}

// beginConcurrently runs two store.BeginSelection calls on the two
// physically distinct writer backends, released together. Each side may
// carry its own revision and token.
func (s *selectionService) beginConcurrently(storeA *Store, selectionA explorer.SelectionRevision, storeB *Store, selectionB explorer.SelectionRevision, tokenA, tokenB string) (*explorer.SelectionRevision, error, *explorer.SelectionRevision, error) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	var revA, revB *explorer.SelectionRevision
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		revA, errA = storeA.BeginSelection(s.ctx, selectionA, tokenA)
	}()
	go func() {
		defer wg.Done()
		<-start
		revB, errB = storeB.BeginSelection(s.ctx, selectionB, tokenB)
	}()
	close(start)
	wg.Wait()
	return revA, errA, revB, errB
}

// ---------------------------------------------------------------------------
// Acceptance 1-3 headline mirror: the full staged-selection lifecycle
// against real PG18, mirroring TestSelectionStorageAgainstArango, with the
// racing surfaces (>= 10 rounds each) and byte-identical row witnesses.
// ---------------------------------------------------------------------------

func TestSelectionStorageAgainstPostgres(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)
	selectionID := "selection-storage-" + suffix
	writerToken := "writer-" + suffix
	header := selectionHeaderFixture(selectionID, "key-"+suffix)

	// --- begin binds identity, idempotency key, and a live writer lease --
	started, err := s.storeA.BeginSelection(s.ctx, header, writerToken)
	if err != nil {
		s.fail("begin selection", err)
	}
	assertRevisionEquals(t, "begin", started, header)
	w := s.witnessSelection(selectionID)
	if !w.exists {
		t.Fatal("begun selection has no stored row")
	}
	if w.state != "STAGING" || w.complete || w.writerToken != writerToken {
		t.Fatalf("staged witness = %+v, want STAGING/uncomplete under %s", w, writerToken)
	}
	if w.exclusionsText != "<sql-null>" {
		t.Fatalf("missing exclusions must be SQL NULL, got %q", w.exclusionsText)
	}
	s.assertHeaderJSONB(t, selectionID, "rule", header.Canonical().Rule, false)
	s.assertHeaderJSONB(t, selectionID, "source", header.Canonical().Source, false)
	s.assertHeaderJSONB(t, selectionID, "exclusions", nil, true)

	// --- idempotent retry converges on the identical stored row ----------
	retry, err := s.storeB.BeginSelection(s.ctx, header, writerToken)
	if err != nil {
		s.fail("idempotent begin", err)
	}
	assertRevisionEquals(t, "retry", retry, header)
	s.assertSelectionStable(t, selectionID, w)

	// --- insert-once membership, lease renewal, foreign-writer refusal ---
	refs := []explorer.SelectionMember{selectionRef("files001"), selectionRef("files002"), selectionRef("files001")}
	inserted, err := s.storeA.AppendSelectionMembers(s.ctx, selectionID, writerToken, refs)
	if err != nil {
		s.fail("append members", err)
	}
	if len(inserted) != 2 {
		t.Fatalf("first append inserted %d, want 2 (deduped batch)", len(inserted))
	}
	mk001 := goldenMemberKey(t, selectionID, refs[0].Ref)
	mk002 := goldenMemberKey(t, selectionID, refs[1].Ref)
	m001 := s.witnessMember(selectionID, mk001)
	m002 := s.witnessMember(selectionID, mk002)
	if !m001.exists || !m002.exists {
		t.Fatalf("staged members missing: %s=%+v %s=%+v", mk001, m001, mk002, m002)
	}
	if !strings.HasSuffix(m001.memberKey, "_"+pgstate05GoldenKeyFiles001) ||
		!strings.HasSuffix(m002.memberKey, "_"+pgstate05GoldenKeyFiles002) {
		t.Fatalf("member keys %q/%q do not carry the golden hex suffixes", m001.memberKey, m002.memberKey)
	}
	renewed := s.witnessSelection(selectionID)
	if renewed.writerExpiresText == w.writerExpiresText || renewed.writerToken != writerToken {
		t.Fatalf("append must renew the lease for the same writer: %q -> %q", w.writerExpiresText, renewed.writerExpiresText)
	}
	if _, err := s.storeB.AppendSelectionMembers(s.ctx, selectionID, "foreign-"+suffix, refs[:1]); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("foreign writer append = %v, want ErrSelectionConflict", err)
	}
	retryInserted, err := s.storeA.AppendSelectionMembers(s.ctx, selectionID, writerToken, refs)
	if err != nil || len(retryInserted) != 0 {
		t.Fatalf("retry append inserted %d err=%v, want 0 rows (idempotent)", len(retryInserted), err)
	}
	s.assertMemberAbsent(t, "no-such-selection", mk001)

	// --- digest streams exactly the frozen membership facts -------------
	digest, count, byteTotal, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, selectionID)
	if err != nil {
		s.fail("digest members", err)
	}
	wantDigest, wantCount, wantBytes := membershipFacts(refs)
	if digest != wantDigest || count != wantCount || byteTotal != wantBytes {
		t.Fatalf("digest = %s/%d/%d, want %s/%d/%d", digest, count, byteTotal, wantDigest, wantCount, wantBytes)
	}

	// --- completion CAS freezes the facts; post-complete guards ---------
	completedAt := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	completed, err := s.storeA.CompleteSelection(s.ctx, selectionID, writerToken, digest, count, byteTotal, completedAt)
	if err != nil {
		s.fail("complete selection", err)
	}
	if !completed.Complete || completed.MembershipDigest != digest {
		t.Fatalf("completed = %+v, want complete with digest %s", completed, digest)
	}
	done := s.witnessSelection(selectionID)
	if done.state != "COMPLETE" || !done.complete || done.membershipDigest != digest ||
		done.memberCount != count || done.memberBytes != byteTotal {
		t.Fatalf("completed witness = %+v, want frozen facts %s/%d/%d", done, digest, count, byteTotal)
	}
	if done.completeAtText == "<sql-null>" {
		t.Fatal("completed_at must be set on a COMPLETE row")
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, selectionID, writerToken, refs[:1]); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("append after complete = %v, want ErrSelectionConflict", err)
	}

	// --- idempotent begin after completion, changed intent refused ------
	again, err := s.storeB.BeginSelection(s.ctx, header, "post-complete-"+suffix)
	if err != nil {
		s.fail("begin after complete", err)
	}
	if !again.Complete || again.MembershipDigest != digest {
		t.Fatalf("begin after complete = %+v, want the COMPLETE row", again)
	}
	s.assertSelectionStable(t, selectionID, done)
	changed := header
	changed.RuleDigest = "different-intent"
	if _, err := s.storeB.BeginSelection(s.ctx, changed, "changed-"+suffix); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("changed-intent begin = %v, want ErrSelectionConflict", err)
	}
	s.assertSelectionStable(t, selectionID, done)

	// --- unexpired writer cannot be claimed; expired recovery follows the
	// current lease boundary -----------------------------------------------
	// CreatedAt is deliberately ahead of "now" so the row is outside the
	// cleanup sweep's age predicate: recovery must then succeed only via
	// the database-evaluated lease-boundary claim, never because cleanup
	// happened to reap the row first.
	activeID := "selection-active-" + suffix
	active := selectionHeaderFixture(activeID, "active-key-"+suffix)
	active.CreatedAt = time.Now().UTC().Add(time.Hour)
	activeToken := "active-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, active, activeToken); err != nil {
		s.fail("begin active", err)
	}
	activeRow := s.witnessSelection(activeID)
	if _, err := s.storeB.BeginSelection(s.ctx, active, "usurper-"+suffix); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("active-lease begin = %v, want ErrSelectionConflict (unexpired writer)", err)
	}
	s.assertSelectionStable(t, activeID, activeRow)

	s.expireLease(t, activeID)
	recoveredToken := "recovered-writer-" + suffix
	recovered, err := s.storeB.BeginSelection(s.ctx, active, recoveredToken)
	if err != nil {
		s.fail("expired recovery begin", err)
	}
	assertRevisionEquals(t, "expired recovery", recovered, active)
	rebound := s.witnessSelection(activeID)
	oldExpiry := parseWitnessTime(t, activeRow.writerExpiresText)
	newExpiry := parseWitnessTime(t, rebound.writerExpiresText)
	if rebound.writerToken != recoveredToken || !newExpiry.After(oldExpiry) {
		t.Fatalf("expired recovery must rebind a fresh lease: %q -> %q", activeRow.writerExpiresText, rebound.writerExpiresText)
	}
	if rebound.id != activeRow.id || rebound.generation != activeRow.generation ||
		rebound.ruleDigest != activeRow.ruleDigest || rebound.scopeDigest != activeRow.scopeDigest ||
		rebound.idempotencyKey != activeRow.idempotencyKey || rebound.ruleText != activeRow.ruleText ||
		rebound.sourceText != activeRow.sourceText {
		t.Fatalf("expired recovery tampered with identity: before=%+v after=%+v", activeRow, rebound)
	}

	// --- expired cleanup: same-key retry recovers a clean revision ------
	expiredMultiID := "selection-expired-multi-" + suffix
	expiredMulti := selectionHeaderFixture(expiredMultiID, "expired-multi-key-"+suffix)
	expiredWriter := "expired-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, expiredMulti, expiredWriter); err != nil {
		s.fail("begin expired multi", err)
	}
	expiredRefs := []explorer.SelectionMember{selectionRef("files001"), selectionRef("files003")}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, expiredMultiID, expiredWriter, expiredRefs); err != nil {
		s.fail("append expired multi", err)
	}
	if s.memberCount(t, expiredMultiID) != 2 {
		t.Fatalf("expired multi members = %d, want 2", s.memberCount(t, expiredMultiID))
	}
	s.expireLease(t, expiredMultiID)
	recoveredWriter := "recovered-multi-" + suffix
	if _, err := s.storeB.BeginSelection(s.ctx, expiredMulti, recoveredWriter); err != nil {
		s.fail("expired multi recovery", err)
	}
	if n := s.memberCount(t, expiredMultiID); n != 0 {
		t.Fatalf("expired multi rows survived cleanup: count=%d", n)
	}
	fresh := s.witnessSelection(expiredMultiID)
	if fresh.writerToken != recoveredWriter || fresh.ctid == "" || !fresh.exists {
		t.Fatalf("recovered revision is not a clean fresh insert: %+v", fresh)
	}

	// --- owner abort removes header and members; foreign abort is a no-op
	multiID := "selection-abort-multi-" + suffix
	multi := selectionHeaderFixture(multiID, "abort-multi-key-"+suffix)
	multiWriter := "abort-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, multi, multiWriter); err != nil {
		s.fail("begin abort multi", err)
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, multiID, multiWriter, expiredRefs); err != nil {
		s.fail("append abort multi", err)
	}
	if err := s.storeB.AbortSelection(s.ctx, multiID, "not-the-writer"); err != nil {
		s.fail("foreign abort", err)
	}
	multiRow := s.witnessSelection(multiID)
	if !multiRow.exists || s.memberCount(t, multiID) != 2 {
		t.Fatalf("foreign abort disturbed the live writer: row=%+v members=%d", multiRow, s.memberCount(t, multiID))
	}
	if err := s.storeA.AbortSelection(s.ctx, multiID, multiWriter); err != nil {
		s.fail("owner abort", err)
	}
	s.assertSelectionAbsent(t, multiID)
	if n := s.memberCount(t, multiID); n != 0 {
		t.Fatalf("owner abort left %d member rows", n)
	}

	// --- mixed-project batch is refused with zero leaked rows -----------
	partialID := "selection-partial-" + suffix
	partial := selectionHeaderFixture(partialID, "partial-key-"+suffix)
	partialWriter := "partial-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, partial, partialWriter); err != nil {
		s.fail("begin partial", err)
	}
	mixedRefs := append([]explorer.SelectionMember{selectionRef("files001")},
		explorer.SelectionMember{Ref: explorer.ResourceRef{Project: "other", Generation: "gen-a", ResourceType: "DocumentReference", ID: "files003"}})
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, partialID, partialWriter, mixedRefs); err == nil {
		t.Fatal("mixed-project append unexpectedly succeeded")
	}
	if n := s.memberCount(t, partialID); n != 0 {
		t.Fatalf("partial append leaked rows: count=%d", n)
	}

	// --- project-scoped reads conceal foreign rows -----------------------
	if _, err := s.storeA.GetSelection(s.ctx, "other-project", selectionID); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("foreign-project GetSelection = %v, want ErrSelectionNotFound", err)
	}
	if _, err := s.storeA.GetSelection(s.ctx, "  ", selectionID); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("blank-project GetSelection = %v, want ErrSelectionNotFound", err)
	}
	if _, err := s.storeA.GetSelection(s.ctx, pgstate05Project, "selection-nope"); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("missing GetSelection = %v, want ErrSelectionNotFound", err)
	}

	// --- member visit walks byte-order keys ------------------------------
	visited := []string{}
	cursor := ""
	for {
		last, err := s.storeA.VisitSelectionMembers(s.ctx, pgstate05Project, selectionID, cursor, 1, func(member explorer.SelectionMember) error {
			visited = append(visited, member.MemberKey)
			return nil
		})
		if err != nil {
			s.fail("visit members", err)
		}
		if last == cursor {
			break
		}
		cursor = last
	}
	if len(visited) != 2 || visited[0] != mk001 || visited[1] != mk002 {
		t.Fatalf("visit walk = %v, want [%s %s]", visited, mk001, mk002)
	}
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, pgstate05Project, activeID, "", 10, func(explorer.SelectionMember) error {
		return nil
	}); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("visit on STAGING = %v, want ErrSelectionIncomplete", err)
	}

	// --- race surface 1: concurrent same-key/same-content begin converges
	// (>= pgstate05RaceRounds rounds on two distinct pg_backend_pid writers)
	for round := 0; round < pgstate05RaceRounds; round++ {
		id := fmt.Sprintf("selection-race-converge-%d-%s", round, suffix)
		key := fmt.Sprintf("race-converge-key-%d-%s", round, suffix)
		raceHeader := selectionHeaderFixture(id, key)
		sharedToken := "shared-writer-" + suffix
		revA, errA, revB, errB := s.beginConcurrently(s.storeA, raceHeader, s.storeB, raceHeader, sharedToken, sharedToken)
		if errA != nil || errB != nil {
			t.Fatalf("round %d same-content begin convergence: A=%v B=%v (pids A=%d B=%d)", round, errA, errB, s.pidA, s.pidB)
		}
		if revA.ID != id || revB.ID != id {
			t.Fatalf("round %d returned foreign revisions: %+v %+v", round, revA, revB)
		}
		if n := s.execCount(t, "SELECT count(*) FROM "+s.headersFQN()+" WHERE id = $1", id); n != 1 {
			t.Fatalf("round %d header rows = %d, want exactly 1", round, n)
		}
		row := s.witnessSelection(id)
		if row.writerToken != sharedToken || row.state != "STAGING" {
			t.Fatalf("round %d converged row = %+v", round, row)
		}
		// Post-abort convergence: the shared writer's abort erases all.
		if err := s.storeB.AbortSelection(s.ctx, id, sharedToken); err != nil {
			s.fail("race cleanup abort", err)
		}
		s.assertSelectionAbsent(t, id)
	}

	// --- race surface 2: concurrent same-key/same-content/different-token:
	// exactly one owner; the other is refused while the lease is live.
	for round := 0; round < pgstate05RaceRounds; round++ {
		id := fmt.Sprintf("selection-race-owner-%d-%s", round, suffix)
		key := fmt.Sprintf("race-owner-key-%d-%s", round, suffix)
		raceHeader := selectionHeaderFixture(id, key)
		revA, errA, revB, errB := s.beginConcurrently(s.storeA, raceHeader, s.storeB, raceHeader, "owner-A-"+suffix, "owner-B-"+suffix)
		ownerWins := (errA == nil) != (errB == nil)
		if !ownerWins {
			t.Fatalf("round %d must produce exactly one writer: A=%v B=%v", round, errA, errB)
		}
		if errA != nil && !errors.Is(errA, explorer.ErrSelectionConflict) {
			t.Fatalf("round %d writer A error = %v, want ErrSelectionConflict", round, errA)
		}
		if errB != nil && !errors.Is(errB, explorer.ErrSelectionConflict) {
			t.Fatalf("round %d writer B error = %v, want ErrSelectionConflict", round, errB)
		}
		if errA == nil && (revA == nil || revA.ID != id) {
			t.Fatalf("round %d winner A returned %+v, want id %s", round, revA, id)
		}
		if errB == nil && (revB == nil || revB.ID != id) {
			t.Fatalf("round %d winner B returned %+v, want id %s", round, revB, id)
		}
		row := s.witnessSelection(id)
		if !row.exists || row.writerToken == "" {
			t.Fatalf("round %d winner row missing: %+v", round, row)
		}
		winnerToken := row.writerToken
		if err := s.storeB.AbortSelection(s.ctx, id, winnerToken); err != nil {
			s.fail("race owner cleanup", err)
		}
		s.assertSelectionAbsent(t, id)
	}

	// --- race surface 3: stale foreign abort cannot remove a live
	// writer's staged members (Arango's 50-round loop, 10-round floor here).
	for round := 0; round < pgstate05RaceRounds; round++ {
		id := fmt.Sprintf("selection-race-abort-%d-%s", round, suffix)
		key := fmt.Sprintf("race-abort-key-%d-%s", round, suffix)
		raceHeader := selectionHeaderFixture(id, key)
		owner := "round-writer-" + suffix
		if _, err := s.storeA.BeginSelection(s.ctx, raceHeader, owner); err != nil {
			s.fail("race abort begin", err)
		}
		var appendErr, abortErr error
		doneCh := make(chan struct{}, 2)
		go func() {
			_, appendErr = s.storeA.AppendSelectionMembers(s.ctx, id, owner, []explorer.SelectionMember{selectionRef("files001")})
			doneCh <- struct{}{}
		}()
		go func() {
			abortErr = s.storeB.AbortSelection(s.ctx, id, "stale-"+suffix)
			doneCh <- struct{}{}
		}()
		<-doneCh
		<-doneCh
		if appendErr != nil || abortErr != nil {
			t.Fatalf("round %d abort race append=%v abort=%v", round, appendErr, abortErr)
		}
		if n := s.memberCount(t, id); n != 1 {
			t.Fatalf("round %d stale abort removed or duplicated staged members: count=%d", round, n)
		}
		mk := goldenMemberKey(t, id, selectionRef("files001").Ref)
		mw := s.witnessMember(id, mk)
		if !mw.exists || mw.memberKey != mk {
			t.Fatalf("round %d staged member drifted: %+v", round, mw)
		}
		dig, cnt, _, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, id)
		if err != nil || cnt != 1 {
			t.Fatalf("round %d post-race digest = %s/%d err=%v", round, dig, cnt, err)
		}
		if _, err := s.storeA.CompleteSelection(s.ctx, id, owner, dig, cnt, 0, completedAt); err != nil {
			t.Fatalf("round %d post-race complete = %v", round, err)
		}
		// Foreign abort after completion is a documented no-op; the
		// completed row is byte-identical across it.
		completedRow := s.witnessSelection(id)
		if err := s.storeB.AbortSelection(s.ctx, id, "stale-"+suffix); err != nil {
			s.fail("post-complete foreign abort", err)
		}
		s.assertSelectionStable(t, id, completedRow)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1 deep: key/content mismatch refuses under race, with
// byte-identical winner witnesses and post-abort convergence.
// ---------------------------------------------------------------------------

func TestSelectionBeginConcurrentMismatchRefused(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)

	for round := 0; round < pgstate05RaceRounds; round++ {
		// (a) same (project, idempotency_key), different ids.
		key := fmt.Sprintf("mismatch-key-%d-%s", round, suffix)
		headerA := selectionHeaderFixture(fmt.Sprintf("selection-mismatch-a-%d-%s", round, suffix), key)
		headerB := headerA
		headerB.ID = fmt.Sprintf("selection-mismatch-b-%d-%s", round, suffix)
		headerB.ScopeDigest = headerB.ScopeDigest + "-b"
		headerB.RuleDigest = headerB.RuleDigest + "-b"
		revA, errA, revB, errB := s.beginConcurrently(s.storeA, headerA, s.storeB, headerB, "mw-"+suffix, "mw-"+suffix)
		_ = revA
		_ = revB
		accepted := 0
		if errA == nil {
			accepted++
		}
		if errB == nil {
			accepted++
		}
		if accepted != 1 {
			t.Fatalf("round %d same-key different-id: accepted %d of 2 (A=%v B=%v), want exactly 1", round, accepted, errA, errB)
		}
		if accepted == 1 && ((errA != nil && !errors.Is(errA, explorer.ErrSelectionConflict)) ||
			(errB != nil && !errors.Is(errB, explorer.ErrSelectionConflict))) {
			t.Fatalf("round %d loser error not a typed conflict: A=%v B=%v", round, errA, errB)
		}
		winner := headerA
		if errA != nil {
			winner = headerB
		}
		w := s.witnessSelection(winner.ID)
		if !w.exists {
			t.Fatalf("round %d winner row missing", round)
		}
		s.assertSelectionStable(t, winner.ID, w)
		loser := headerB
		if errA != nil {
			loser = headerA
		}
		s.assertSelectionAbsent(t, loser.ID)
		// Post-abort convergence: the winner's abort leaves zero rows for
		// either id behind.
		if err := s.storeA.AbortSelection(s.ctx, winner.ID, "mw-"+suffix); err != nil {
			s.fail("mismatch abort", err)
		}
		s.assertSelectionAbsent(t, winner.ID)
		s.assertSelectionAbsent(t, loser.ID)

		// (b) same id, different intent (rule digest) -- content mismatch
		// under the id arbiter.
		id := fmt.Sprintf("selection-mismatch-id-%d-%s", round, suffix)
		first := selectionHeaderFixture(id, fmt.Sprintf("mismatch-id-key-%d-%s", round, suffix))
		if _, err := s.storeA.BeginSelection(s.ctx, first, "imw-"+suffix); err != nil {
			s.fail("mismatch-id seed", err)
		}
		base := s.witnessSelection(id)
		changed := first
		changed.RuleDigest = "tampered-intent"
		if _, err := s.storeB.BeginSelection(s.ctx, changed, "imw-"+suffix); !errors.Is(err, explorer.ErrSelectionConflict) {
			t.Fatalf("round %d same-id changed-content begin = %v, want conflict", round, err)
		}
		s.assertSelectionStable(t, id, base)
		changedKey := first
		changedKey.IdempotencyKey = "different-key"
		if _, err := s.storeB.BeginSelection(s.ctx, changedKey, "imw-"+suffix); !errors.Is(err, explorer.ErrSelectionConflict) {
			t.Fatalf("round %d same-id changed-key begin = %v, want conflict", round, err)
		}
		s.assertSelectionStable(t, id, base)
		if err := s.storeA.AbortSelection(s.ctx, id, "imw-"+suffix); err != nil {
			s.fail("mismatch-id abort", err)
		}
		s.assertSelectionAbsent(t, id)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2 deep: the lease boundary is evaluated by PostgreSQL itself.
// A direct-SQL replica of the claim statement must match zero rows while
// the writer is unexpired -- no adapter-side check is trusted for this.
// ---------------------------------------------------------------------------

func TestSelectionWriterLeaseBoundaryIsDatabaseEnforced(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)
	id := "selection-lease-boundary-" + suffix
	header := selectionHeaderFixture(id, "lease-key-"+suffix)
	if _, err := s.storeA.BeginSelection(s.ctx, header, "lease-writer-"+suffix); err != nil {
		s.fail("seed begin", err)
	}
	base := s.witnessSelection(id)

	claimSQL := `UPDATE ` + s.headersFQN() + ` SET writer_token = $2, writer_expires_at = $3` +
		` WHERE id = $1 AND state = 'STAGING' AND writer_expires_at < $4 RETURNING writer_token`

	// (1) Unexpired: the boundary predicate -- evaluated against the
	// CURRENT instant, exactly as the adapter binds it -- matches zero
	// rows and the row is byte-identical afterwards.
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire claim witness", err)
	}
	future := time.Now().UTC().Add(time.Hour)
	tag, err := conn.Exec(s.ctx, claimSQL, id, "usurper", future, time.Now().UTC())
	conn.Release()
	if err != nil {
		s.fail("boundary claim (unexpired)", err)
	}
	if tag.RowsAffected() != 0 {
		t.Fatalf("unexpired lease claim affected %d rows, want 0", tag.RowsAffected())
	}
	s.assertSelectionStable(t, id, base)

	// The adapter agrees: a foreign token conflicts while the lease lives.
	if _, err := s.storeB.BeginSelection(s.ctx, header, "usurper"); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("adapter begin on live lease = %v, want ErrSelectionConflict", err)
	}
	s.assertSelectionStable(t, id, base)

	// (2) Expired: the same statement now matches the row.
	s.expireLease(t, id)
	tag, err = func() (pgconn.CommandTag, error) {
		c, aerr := s.witness.Acquire(s.ctx)
		if aerr != nil {
			return pgconn.CommandTag{}, aerr
		}
		defer c.Release()
		return c.Exec(s.ctx, claimSQL, id, "direct-claimer", future.Add(time.Hour*2), time.Now().UTC())
	}()
	if err != nil {
		s.fail("boundary claim (expired)", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expired lease claim affected %d rows, want 1", tag.RowsAffected())
	}
	claimed := s.witnessSelection(id)
	if claimed.writerToken != "direct-claimer" {
		t.Fatalf("expired claim did not rebind the writer: %+v", claimed)
	}
	if claimed.id != base.id || claimed.generation != base.generation ||
		claimed.ruleDigest != base.ruleDigest || claimed.scopeDigest != base.scopeDigest ||
		claimed.idempotencyKey != base.idempotencyKey || claimed.ruleText != base.ruleText {
		t.Fatalf("boundary claim tampered with identity columns: %+v", claimed)
	}

	// (3) The fresh lease is again unclaimable by a foreign token through
	// the adapter, and the owner's own begin round-trips idempotently.
	if _, err := s.storeB.BeginSelection(s.ctx, header, "second-usurper"); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("adapter begin after direct claim = %v, want ErrSelectionConflict", err)
	}
	own, err := s.storeA.BeginSelection(s.ctx, header, "direct-claimer")
	if err != nil || own.ID != id {
		t.Fatalf("owner begin under claimed lease = %+v err=%v", own, err)
	}
}

// ---------------------------------------------------------------------------
// Database-enforced immutability with non-vacuity guard-drop probes.
// ---------------------------------------------------------------------------

func TestSelectionDatabaseGuardsAreEnforcedAtDatabaseLevel(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)

	id := "selection-guard-" + suffix
	header := selectionHeaderFixture(id, "guard-key-"+suffix)
	if _, err := s.storeA.BeginSelection(s.ctx, header, "guard-writer"); err != nil {
		s.fail("seed begin", err)
	}

	// Capture the pristine column values so each guard-drop probe reseeds
	// the tampered value back BEFORE the trigger is restored: the restore
	// check must fail again for the same reason (the guard fires on
	// NEW IS DISTINCT FROM OLD, so a stuck-tampered value would silently
	// make the restored guard look vacuous).
	var (
		origGeneration, origResourceType string
		origRuleDigest, origScopeDigest  string
		origIdempotencyKey               string
		origCreatedAt                    time.Time
		origRuleText, origSourceText     string
		origExclusionsText               string
	)
	func() {
		conn, aerr := s.witness.Acquire(s.ctx)
		if aerr != nil {
			s.fail("acquire pristine capture", aerr)
		}
		defer conn.Release()
		if err := conn.QueryRow(s.ctx,
			`SELECT generation, resource_type, rule_digest, scope_digest, idempotency_key, created_at,
			        rule::text, source::text, COALESCE(exclusions::text, '<sql-null>')
			 FROM `+s.headersFQN()+` WHERE id = $1`, id).
			Scan(&origGeneration, &origResourceType, &origRuleDigest, &origScopeDigest,
				&origIdempotencyKey, &origCreatedAt, &origRuleText, &origSourceText, &origExclusionsText); err != nil {
			s.fail("capture pristine columns", err)
		}
	}()

	// Identity columns are insert-once: each tampering attempt is refused
	// by 45501 while the trigger stands, succeeds once dropped, and is
	// refused again once restored (value reseeded in between).
	type tamper struct {
		column   string
		tampered any
		restore  any
		cast     string // "", "::timestamptz", or "::jsonb"
	}
	var exclusionsRestore any
	if origExclusionsText != "<sql-null>" {
		exclusionsRestore = []byte(origExclusionsText)
	}
	identityTampers := []tamper{
		{"generation", "tampered", origGeneration, ""},
		{"resource_type", "Observation", origResourceType, ""},
		{"rule_digest", "tampered-digest", origRuleDigest, ""},
		{"scope_digest", "tampered-scope", origScopeDigest, ""},
		{"idempotency_key", "tampered-guard-key", origIdempotencyKey, ""},
		{"created_at", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), origCreatedAt, "::timestamptz"},
		{"rule", []byte(`{"kind":"ALL_MATCHING"}`), []byte(origRuleText), "::jsonb"},
		{"source", []byte(`{"kind":"SELECTION_REVISION"}`), []byte(origSourceText), "::jsonb"},
		{"exclusions", []byte(`[{"project":"p"}]`), exclusionsRestore, "::jsonb"},
	}
	for _, tm := range identityTampers {
		stmt := `UPDATE ` + s.headersFQN() + ` SET ` + tm.column + ` = $2` + tm.cast + ` WHERE id = $1`
		reseed := func(tm tamper) func() {
			return func() {
				if n := s.witnessExecTag(t, `UPDATE `+s.headersFQN()+` SET `+tm.column+` = $2`+tm.cast+` WHERE id = $1`, id, tm.restore); n != 1 {
					t.Fatalf("reseed %s affected %d rows, want 1", tm.column, n)
				}
			}
		}(tm)
		s.guardDropProbe(t, "selection_revisions_immutable", SelectionRevisionsTable, "loom_selection_revision_immutable", reseed, stmt, id, tm.tampered)
	}

	// Controls proving the guard does not over-block: the writer lease and
	// pre-completion staging facts are OUTSIDE the frozen set and update
	// legally with the trigger attached (this is the same witness write
	// expireLease relies on).
	s.expireLease(t, id)
	if n := s.witnessExecTag(t,
		`UPDATE `+s.headersFQN()+` SET membership_digest = 'staging-is-live', writer_token = 'guard-writer' WHERE id = $1`, id); n != 1 {
		t.Fatalf("staging-fact control write affected %d rows, want 1", n)
	}

	// Completion freezes the facts one-way.
	digest, count, byteTotal, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, id)
	if err != nil {
		s.fail("digest", err)
	}
	completedAt := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	if _, err := s.storeA.CompleteSelection(s.ctx, id, "guard-writer", digest, count, byteTotal, completedAt); err != nil {
		s.fail("complete", err)
	}
	frozenTampers := []tamper{
		{"membership_digest", "tampered", digest, ""},
		{"member_count", int64(99), count, ""},
		{"member_bytes", int64(99), byteTotal, ""},
		{"completed_at", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), completedAt, "::timestamptz"},
	}
	for _, tm := range frozenTampers {
		stmt := `UPDATE ` + s.headersFQN() + ` SET ` + tm.column + ` = $2` + tm.cast + ` WHERE id = $1`
		reseed := func(tm tamper) func() {
			return func() {
				if n := s.witnessExecTag(t, `UPDATE `+s.headersFQN()+` SET `+tm.column+` = $2`+tm.cast+` WHERE id = $1`, id, tm.restore); n != 1 {
					t.Fatalf("reseed %s affected %d rows, want 1", tm.column, n)
				}
			}
		}(tm)
		s.guardDropProbe(t, "selection_revisions_immutable", SelectionRevisionsTable, "loom_selection_revision_immutable", reseed, stmt, id, tm.tampered)
	}
	// Un-completing a row is refused FIRST by the guard; with the guard
	// dropped, the named table CHECK refuses the same write -- completion
	// irreversibility is enforced by two independent database layers.
	for _, tamper := range []struct{ column, literal string }{
		{"complete", "false"},
		{"state", "'STAGING'"},
	} {
		stmt := `UPDATE ` + s.headersFQN() + ` SET ` + tamper.column + ` = ` + tamper.literal + ` WHERE id = $1`
		s.guardThenCheckProbe(t, "selection_revisions_immutable", SelectionRevisionsTable, "loom_selection_revision_immutable",
			stmt, "23514", "selection_revisions_complete_matches_state", id)
	}

	// The member guard blocks UPDATE (content is insert-once) and, as a
	// lifecycle control, still allows DELETE with the trigger attached.
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, "guard-writer", []explorer.SelectionMember{selectionRef("files001")}); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("append after complete = %v, want conflict (row is COMPLETE)", err)
	}
	// Re-stage via a fresh selection for member probes.
	memberSelID := "selection-guard-member-" + suffix
	mk := goldenMemberKey(t, memberSelID, selectionRef("files001").Ref)
	memberSel := selectionHeaderFixture(memberSelID, "guard-member-key-"+suffix)
	if _, err := s.storeA.BeginSelection(s.ctx, memberSel, "member-writer"); err != nil {
		s.fail("member probe begin", err)
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, memberSelID, "member-writer", []explorer.SelectionMember{selectionRef("files001")}); err != nil {
		s.fail("member probe append", err)
	}
	mw := s.witnessMember(memberSelID, mk)
	if !mw.exists {
		t.Fatalf("member probe row missing: %+v", mw)
	}
	memberUpdate := `UPDATE ` + s.membersFQN() + ` SET ordinal = 42 WHERE selection_id = $1 AND member_key = $2`
	reseed := func() {
		if n := s.witnessExecTag(t, `UPDATE `+s.membersFQN()+` SET ordinal = 0 WHERE selection_id = $1 AND member_key = $2`, memberSelID, mk); n != 1 {
			t.Fatalf("member probe reseed affected %d rows, want 1", n)
		}
	}
	s.guardDropProbe(t, "selection_members_immutable", SelectionMembersTable, "loom_selection_member_immutable", reseed, memberUpdate, memberSelID, mk)
	// Member tampering on identity parts is blocked by the same guard:
	s.guardDropProbe(t, "selection_members_immutable", SelectionMembersTable, "loom_selection_member_immutable", func() {
		if n := s.witnessExecTag(t, `UPDATE `+s.membersFQN()+` SET id = 'files001' WHERE selection_id = $1 AND member_key = $2`, memberSelID, mk); n != 1 {
			t.Fatalf("member id reseed affected %d rows, want 1", n)
		}
	}, `UPDATE `+s.membersFQN()+` SET id = 'tampered' WHERE selection_id = $1 AND member_key = $2`, memberSelID, mk)
	// Lifecycle control: DELETE of a whole member row is legal WITH the
	// trigger attached (abort/cleanup remove rows; they never rewrite one).
	if n := s.witnessExecTag(t, `DELETE FROM `+s.membersFQN()+` WHERE selection_id = $1 AND member_key = $2`, memberSelID, mk); n != 1 {
		t.Fatalf("lifecycle member DELETE affected %d rows, want 1 (guard must not over-block)", n)
	}
	if !s.triggerPresent(t, "selection_members_immutable", SelectionMembersTable) {
		t.Fatal("member guard must remain attached after the legal lifecycle DELETE")
	}
}

// ---------------------------------------------------------------------------
// Exact named constraint refusals via direct SQL, with legal controls.
// ---------------------------------------------------------------------------

func TestSelectionSchemaConstraintsRejectInvalidRows(t *testing.T) {
	s := newSelectionService(t)

	// Seed the rows the duplicate probes collide with.
	if err := s.witnessExecErr(s.headerInsertSQL(nil)); err != nil {
		s.fail("seed header", err)
	}
	if err := s.witnessExecErr(s.memberInsertSQL(nil)); err != nil {
		s.fail("seed member", err)
	}

	type negative struct {
		name           string
		sql            string
		wantState      string
		wantConstraint string
	}
	cases := []negative{
		{"duplicate selection id", s.headerInsertSQL(map[string]string{"id": "'p5-header-1'", "idempotency_key": "'other-key-1'"}), "23505", "selection_revisions_id_pk"},
		{"duplicate project-scoped idempotency key", s.headerInsertSQL(map[string]string{"id": "'p5-header-dup-key'"}), "23505", "selection_revisions_project_key_ux"},
		{"state is a closed domain", s.headerInsertSQL(map[string]string{"id": "'p5-h-state'", "state": "'BOGUS'"}), "23514", "selection_revisions_state_known"},
		{"complete requires COMPLETE state", s.headerInsertSQL(map[string]string{"id": "'p5-h-mismatch'", "complete": "true", "membership_digest": "'d'", "completed_at": "'2026-09-19T00:00:00Z'::timestamptz"}), "23514", "selection_revisions_complete_matches_state"},
		{"COMPLETE without a membership digest", s.headerInsertSQL(map[string]string{"id": "'p5-h-nodigest'", "state": "'COMPLETE'", "complete": "true", "completed_at": "'2026-09-19T00:00:00Z'::timestamptz"}), "23514", "selection_revisions_complete_facts"},
		{"COMPLETE without completed_at", s.headerInsertSQL(map[string]string{"id": "'p5-h-nocomplete'", "state": "'COMPLETE'", "complete": "true", "membership_digest": "'d'"}), "23514", "selection_revisions_complete_facts"},
		{"negative member_count", s.headerInsertSQL(map[string]string{"id": "'p5-h-negcount'", "member_count": "-1"}), "23514", "selection_revisions_membership_non_negative"},
		{"negative member_bytes", s.headerInsertSQL(map[string]string{"id": "'p5-h-negbytes'", "member_bytes": "-1"}), "23514", "selection_revisions_membership_non_negative"},
		{"empty id", s.headerInsertSQL(map[string]string{"id": "''"}), "23514", "selection_revisions_identity_non_empty"},
		{"empty generation", s.headerInsertSQL(map[string]string{"id": "'p5-h-emptygen'", "generation": "''"}), "23514", "selection_revisions_identity_non_empty"},
		{"missing digests", s.headerInsertSQL(map[string]string{"id": "'p5-h-nodigs'", "rule_digest": "''"}), "23514", "selection_revisions_digests_present"},
		{"writer token is never empty", s.headerInsertSQL(map[string]string{"id": "'p5-h-nowriter'", "writer_token": "''"}), "23514", "selection_revisions_writer_present"},
		{"rule must be a JSON object", s.headerInsertSQL(map[string]string{"id": "'p5-h-scalerule'", "rule": "'5'::jsonb"}), "23514", "selection_revisions_rule_object"},
		{"source JSON null is not an object", s.headerInsertSQL(map[string]string{"id": "'p5-h-nullsrc'", "source": "'null'::jsonb"}), "23514", "selection_revisions_source_object"},
		{"exclusions must be a JSON array", s.headerInsertSQL(map[string]string{"id": "'p5-h-objexcl'", "exclusions": `'{"project":"p"}'::jsonb`}), "23514", "selection_revisions_exclusions_array"},
		{"member id must keep its derived shape", s.memberInsertSQL(map[string]string{"member_key": "'p5-header-1_not-hex'"}), "23514", "selection_members_key_shape"},
		{"duplicate member reference under a different key", s.memberInsertSQL(map[string]string{"member_key": "'p5-header-1_" + pgstate05GoldenKeyFiles002 + "'"}), "23505", "selection_members_ref_ux"},
		{"member reference columns are never empty", s.memberInsertSQL(map[string]string{"selection_id": "'p5-header-2'", "member_key": "'p5-header-2_" + pgstate05GoldenKeyFiles002 + "'", "id": "''"}), "23514", "selection_members_identity_non_empty"},
	}
	for _, c := range cases {
		assertPgError(t, c.name, s.witnessExecErr(c.sql), c.wantState, c.wantConstraint)
	}

	// Controls: the documented legal shapes insert cleanly, so every
	// refusal above was the named constraint doing its job.
	if err := s.witnessExecErr(s.headerInsertSQL(map[string]string{
		"id": "'p5-h-complete-legal'", "idempotency_key": "'key-legal-complete'",
		"state": "'COMPLETE'", "complete": "true",
		"membership_digest": "'digest'", "member_count": "2", "member_bytes": "3",
		"completed_at": "'2026-09-19T00:00:00Z'::timestamptz",
		"rule":         `'{"kind":"ALL_MATCHING","filters":[{"column":"a","op":"EQ","value":1}]}'::jsonb`,
	})); err != nil {
		t.Fatalf("control: legal COMPLETE header refused: %s", s.scrub(err))
	}
	if err := s.witnessExecErr(s.headerInsertSQL(map[string]string{
		"id": "'p5-h-keyless-1'", "idempotency_key": "''",
	})); err != nil {
		t.Fatalf("control: first keyless header refused: %s", s.scrub(err))
	}
	if err := s.witnessExecErr(s.headerInsertSQL(map[string]string{
		"id": "'p5-h-keyless-2'", "idempotency_key": "''",
	})); err != nil {
		t.Fatalf("control: second keyless header refused (partial unique must ignore empty keys): %s", s.scrub(err))
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.headersFQN()+" WHERE project = $1 AND idempotency_key = ''", pgstate05Project); n != 2 {
		t.Fatalf("keyless headers = %d, want 2", n)
	}
	if err := s.witnessExecErr(s.memberInsertSQL(map[string]string{
		"selection_id": "'p5-header-2'", "member_key": "'p5-header-2_" + pgstate05GoldenKeyFiles003 + "'", "id": "'files003'", "ordinal": "7",
	})); err != nil {
		t.Fatalf("control: legal member row refused: %s", s.scrub(err))
	}
}

// ---------------------------------------------------------------------------
// Crash-window injection through the in-transaction seams: aborted writes
// converge (nothing half-applied survives), and the retry after the crash
// completes cleanly.
// ---------------------------------------------------------------------------

func TestSelectionCrashWindowConvergence(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)

	// (1) Crash between the member inserts and the lease renewal: the
	// whole append transaction vanishes -- no members, no renewal.
	id := "selection-crash-append-" + suffix
	header := selectionHeaderFixture(id, "crash-append-key-"+suffix)
	writer := "crash-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, header, writer); err != nil {
		s.fail("crash seed begin", err)
	}
	base := s.witnessSelection(id)
	s.storeB.afterMemberInsert = func(context.Context, pgx.Tx) error {
		return errors.New("injected crash after member insert")
	}
	refs := []explorer.SelectionMember{selectionRef("files001"), selectionRef("files002")}
	if _, err := s.storeB.AppendSelectionMembers(s.ctx, id, writer, refs); err == nil {
		t.Fatal("crashed append unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "after member insert") {
		t.Fatalf("crashed append error = %v, want the seam-classified wrap", err)
	}
	if n := s.memberCount(t, id); n != 0 {
		t.Fatalf("crashed append leaked %d member rows", n)
	}
	s.assertSelectionStable(t, id, base) // lease not renewed, row byte-identical
	// Post-crash convergence: the retry (with the injected crash removed)
	// commits the full batch.
	s.storeB.afterMemberInsert = nil
	inserted, err := s.storeB.AppendSelectionMembers(s.ctx, id, writer, refs)
	if err != nil || len(inserted) != 2 {
		t.Fatalf("post-crash retry append inserted %d err=%v, want 2", len(inserted), err)
	}

	// (2) Crash between the cleanup delete pair and its commit: the
	// abandoned revision AND its members survive together (cleanup is one
	// atomic unit), and the retry after the crash reaps both.
	victimID := "selection-crash-cleanup-" + suffix
	victim := selectionHeaderFixture(victimID, "crash-cleanup-key-"+suffix)
	if _, err := s.storeA.BeginSelection(s.ctx, victim, "victim-writer-"+suffix); err != nil {
		s.fail("victim begin", err)
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, victimID, "victim-writer-"+suffix, []explorer.SelectionMember{selectionRef("files002")}); err != nil {
		s.fail("victim append", err)
	}
	s.expireLease(t, victimID)
	victimRow := s.witnessSelection(victimID)
	victimMemberKey := goldenMemberKey(t, victimID, selectionRef("files002").Ref)
	victimMember := s.witnessMember(victimID, victimMemberKey)
	s.storeA.beforeCleanupCommit = func(context.Context, pgx.Tx) error {
		return errors.New("injected crash before cleanup commit")
	}
	if err := s.storeA.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 100); err == nil {
		t.Fatal("crashed cleanup unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "before commit") {
		t.Fatalf("crashed cleanup error = %v, want the seam-classified wrap", err)
	}
	s.assertSelectionStable(t, victimID, victimRow)
	if after := s.witnessMember(victimID, victimMemberKey); !after.same(victimMember) {
		t.Fatalf("crashed cleanup removed members: %+v", after)
	}
	// Post-crash convergence: the same cleanup now removes header+members.
	s.storeA.beforeCleanupCommit = nil
	if err := s.storeA.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 100); err != nil {
		s.fail("post-crash cleanup", err)
	}
	s.assertSelectionAbsent(t, victimID)
	if n := s.memberCount(t, victimID); n != 0 {
		t.Fatalf("post-crash cleanup left %d members", n)
	}

	// (3) A cleanup racing a live writer's renewal converges to exactly one
	// outcome per row: the writer's FOR UPDATE serialization means the
	// batch either reaps a fully expired revision or leaves it whole. Run
	// the racing pair across rounds on the two distinct backends.
	for round := 0; round < pgstate05RaceRounds; round++ {
		raceID := fmt.Sprintf("selection-crash-race-%d-%s", round, suffix)
		raceHeader := selectionHeaderFixture(raceID, fmt.Sprintf("crash-race-key-%d-%s", round, suffix))
		raceWriter := "race-writer-" + suffix
		if _, err := s.storeA.BeginSelection(s.ctx, raceHeader, raceWriter); err != nil {
			s.fail("race begin", err)
		}
		if _, err := s.storeA.AppendSelectionMembers(s.ctx, raceID, raceWriter, []explorer.SelectionMember{selectionRef("files003")}); err != nil {
			s.fail("race append", err)
		}
		if round%2 == 0 {
			s.expireLease(t, raceID)
		}
		var cleanupErr, appendErr error
		done := make(chan struct{}, 2)
		go func() {
			cleanupErr = s.storeB.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 100)
			done <- struct{}{}
		}()
		go func() {
			_, appendErr = s.storeA.AppendSelectionMembers(s.ctx, raceID, raceWriter, []explorer.SelectionMember{selectionRef("files001")})
			done <- struct{}{}
		}()
		<-done
		<-done
		if cleanupErr != nil {
			s.fail("racing cleanup", cleanupErr)
		}
		row := s.witnessSelection(raceID)
		if !row.exists {
			if round%2 != 0 {
				t.Fatalf("round %d: live (unexpired) writer was reaped", round)
			}
			if appendErr != nil && !errors.Is(appendErr, explorer.ErrSelectionNotFound) {
				t.Fatalf("round %d append into reaped revision = %v, want typed not-found", round, appendErr)
			}
			if n := s.memberCount(t, raceID); n != 0 {
				t.Fatalf("round %d reaped revision left %d members", round, n)
			}
			continue
		}
		if appendErr != nil {
			t.Fatalf("round %d append into surviving revision = %v", round, appendErr)
		}
		if round%2 == 0 && row.writerToken != raceWriter {
			t.Fatalf("round %d row survived with foreign writer %+v", round, row)
		}
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3 deep: identity includes project/generation/resource/rule/
// scope and is reread after conflict -- every field probed individually.
// ---------------------------------------------------------------------------

func TestSelectionIdentityRereadAfterConflict(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)
	id := "selection-identity-" + suffix
	key := "identity-key-" + suffix
	stored := selectionHeaderFixture(id, key)
	writer := "identity-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, stored, writer); err != nil {
		s.fail("seed begin", err)
	}
	base := s.witnessSelection(id)

	variants := []struct {
		name   string
		mutate func(*explorer.SelectionRevision)
	}{
		{"generation", func(r *explorer.SelectionRevision) { r.Generation = "gen-b"; r.Source.Generation = "gen-b" }},
		{"resource type", func(r *explorer.SelectionRevision) {
			r.ResourceType = "Observation"
			r.Source.ResourceType = "Observation"
		}},
		{"rule digest", func(r *explorer.SelectionRevision) { r.RuleDigest = "other-rule" }},
		{"scope digest", func(r *explorer.SelectionRevision) { r.ScopeDigest = "other-scope" }},
		{"idempotency key", func(r *explorer.SelectionRevision) { r.IdempotencyKey = "other-key" }},
		{"project", func(r *explorer.SelectionRevision) { r.Project = "pgstate05-other" }},
	}
	for _, variant := range variants {
		probe := stored
		probe.Rule = stored.Rule
		probe.Source = stored.Source
		variant.mutate(&probe)
		_, err := s.storeB.BeginSelection(s.ctx, probe, writer)
		if !errors.Is(err, explorer.ErrSelectionConflict) {
			t.Fatalf("%s tampering begin = %v, want ErrSelectionConflict", variant.name, err)
		}
		s.assertSelectionStable(t, id, base)
	}

	// The reread is the decision point: after a conflict the stored row is
	// byte-identical AND GetSelection still round-trips the frozen identity.
	reread, err := s.storeA.GetSelection(s.ctx, pgstate05Project, id)
	if err != nil {
		s.fail("reread", err)
	}
	assertRevisionEquals(t, "reread", reread, stored)

	// Completion validation runs on the reread path: a COMPLETE row whose
	// stored facts are coherent returns; the guard already proved
	// incoherent writes impossible at the database.
	digest, count, byteTotal, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, id)
	if err != nil {
		s.fail("digest", err)
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, byteTotal, time.Now().UTC()); err != nil {
		s.fail("complete", err)
	}
	completeRow := s.witnessSelection(id)
	done, err := s.storeB.BeginSelection(s.ctx, stored, writer)
	if err != nil || !done.Complete || done.MembershipDigest != digest {
		t.Fatalf("begin after complete = %+v err=%v", done, err)
	}
	s.assertSelectionStable(t, id, completeRow)
}

// ---------------------------------------------------------------------------
// Offline contract checks: statement construction, derivation parity, and
// the self-recomputed fixture digest. No connection is opened.
// ---------------------------------------------------------------------------

func TestSelectionStoreValidationAndSQLContract(t *testing.T) {
	if _, err := New(nil, "ctl"); err == nil {
		t.Fatal("New(nil, schema) = nil error, want refusal")
	}
	if _, err := New(&pgstore.Client{}, "Bad Schema!"); err == nil {
		t.Fatal("New(client, \"Bad Schema!\") = nil error, want schema refusal")
	}

	sql := newSelectionSQL("ctl")
	headersFQN := `"ctl"."selection_revisions"`
	membersFQN := `"ctl"."selection_members"`
	for name, statement := range map[string]string{
		"beginInsert": sql.beginInsert, "headerByID": sql.headerByID, "headerScoped": sql.headerScoped,
		"keyOwner": sql.keyOwner, "leaseClaim": sql.leaseClaim, "headerForAppend": sql.headerForAppend,
		"memberInsert": sql.memberInsert, "leaseRenew": sql.leaseRenew, "completeCAS": sql.completeCAS,
		"memberDigestPage": sql.memberDigestPage, "memberVisitPage": sql.memberVisitPage,
		"cleanupVictims": sql.cleanupVictims, "cleanupMembers": sql.cleanupMembers,
		"cleanupHeaders": sql.cleanupHeaders, "abortMembers": sql.abortMembers, "abortHeader": sql.abortHeader,
	} {
		if !strings.Contains(statement, headersFQN) && !strings.Contains(statement, membersFQN) {
			t.Fatalf("%s is not schema-qualified: %s", name, statement)
		}
	}
	if got := strings.Count(sql.beginInsert, "$"); got != len(selectionWriteColumns) {
		t.Fatalf("beginInsert placeholders = %d, want %d (arguments match by construction)", got, len(selectionWriteColumns))
	}
	if !strings.Contains(sql.leaseClaim, "writer_expires_at < $4") {
		t.Fatalf("leaseClaim must evaluate the boundary in the database: %s", sql.leaseClaim)
	}
	if !strings.Contains(sql.completeCAS, "state = 'STAGING'") || !strings.Contains(sql.completeCAS, "writer_token = $6") {
		t.Fatalf("completeCAS must be a writer-conditional staging CAS: %s", sql.completeCAS)
	}
	if !strings.Contains(sql.keyOwner, "$2 <> ''") {
		t.Fatalf("keyOwner must never match an empty key: %s", sql.keyOwner)
	}
	if !strings.Contains(sql.memberInsert, "ON CONFLICT (selection_id, member_key) DO NOTHING") {
		t.Fatalf("memberInsert must be insert-once idempotent: %s", sql.memberInsert)
	}
	if !strings.Contains(sql.cleanupVictims, "FOR UPDATE") {
		t.Fatalf("cleanupVictims must lock its victims: %s", sql.cleanupVictims)
	}
	if got := strings.Count(sql.memberInsert, "$"); got != len(memberColumns)+1 {
		t.Fatalf("memberInsert placeholders = %d, want %d", got, len(memberColumns)+1)
	}

	// Derivation parity: the adapter's member key equals an independent
	// reimplementation AND the pinned golden literals.
	for _, tc := range []struct{ id, wantHex string }{
		{"files001", pgstate05GoldenKeyFiles001},
		{"files002", pgstate05GoldenKeyFiles002},
		{"files003", pgstate05GoldenKeyFiles003},
	} {
		ref := explorer.ResourceRef{Project: pgstate05Project, Generation: "gen-a", ResourceType: "DocumentReference", ID: tc.id}
		adapterKey := selectionMemberKey("selection-1", ref)
		golden := goldenMemberKey(t, "selection-1", ref)
		if adapterKey != golden {
			t.Fatalf("selectionMemberKey(%s) = %s, independent derivation says %s", tc.id, adapterKey, golden)
		}
		if want := "selection-1_" + tc.wantHex; adapterKey != want {
			t.Fatalf("selectionMemberKey(%s) = %s, pinned golden says %s", tc.id, adapterKey, want)
		}
	}

	// Fixture digest self-recompute (offline half of the ledger pin).
	sqlBytes, err := os.ReadFile("../../../" + pgstate05FixtureDir + "/" + pgstate05FixtureFile + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(sqlBytes)
	migrations, err := pgstore.LoadMigrations(os.DirFS("../../../"), pgstate05FixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Checksum != hex.EncodeToString(sum[:]) ||
		migrations[0].Version != pgstate05FixtureVersion || migrations[0].Name != pgstate05FixtureName {
		t.Fatalf("fixture migration = %+v, want version %d %s at the self-recomputed digest %s",
			migrations, pgstate05FixtureVersion, pgstate05FixtureName, hex.EncodeToString(sum[:]))
	}
	for _, needle := range []string{"selection_revisions", "selection_members", "loom_selection_revision_immutable", "loom_selection_member_immutable", "45501"} {
		if !strings.Contains(string(sqlBytes), needle) {
			t.Fatalf("fixture DDL must mention %s", needle)
		}
	}
}

// ---------------------------------------------------------------------------
// Digest paging across the 1000-row page boundary + member visit walk.
// ---------------------------------------------------------------------------

func TestSelectionDigestPagingAndVisitWalk(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)
	id := "selection-pages-" + suffix
	header := selectionHeaderFixture(id, "pages-key-"+suffix)
	writer := "pages-writer-" + suffix
	if _, err := s.storeA.BeginSelection(s.ctx, header, writer); err != nil {
		s.fail("begin", err)
	}
	const memberCount = 1001
	batch := make([]explorer.SelectionMember, 0, memberCount)
	for i := 0; i < memberCount; i++ {
		batch = append(batch, selectionRef(fmt.Sprintf("m%04d", i)))
	}
	inserted, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, batch)
	if err != nil || len(inserted) != memberCount {
		t.Fatalf("bulk append inserted %d err=%v, want %d", len(inserted), err, memberCount)
	}
	digest, count, byteTotal, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, id)
	if err != nil {
		s.fail("paging digest", err)
	}
	wantDigest, wantCount, wantBytes := membershipFacts(batch)
	if digest != wantDigest || count != wantCount || byteTotal != wantBytes {
		t.Fatalf("paging digest crossed a page boundary incorrectly: got %s/%d/%d want %s/%d/%d",
			digest, count, byteTotal, wantDigest, wantCount, wantBytes)
	}
	if n := s.memberCount(t, id); n != memberCount {
		t.Fatalf("stored members = %d, want %d", n, memberCount)
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, byteTotal, time.Now().UTC()); err != nil {
		s.fail("complete", err)
	}
	// Visit walk at pageSize 7: every member exactly once in byte order.
	seen := []string{}
	cursor := ""
	for {
		last, err := s.storeA.VisitSelectionMembers(s.ctx, pgstate05Project, id, cursor, 7, func(member explorer.SelectionMember) error {
			seen = append(seen, member.MemberKey)
			return nil
		})
		if err != nil {
			s.fail("visit walk", err)
		}
		if last == cursor {
			break
		}
		cursor = last
	}
	if len(seen) != memberCount {
		t.Fatalf("visit walk collected %d, want %d", len(seen), memberCount)
	}
	if !sort.StringsAreSorted(seen) {
		t.Fatal("visit walk is not in byte order")
	}
	// Idempotent facts: the COMPLETE digest reread streams nothing new.
	digest2, count2, _, err := s.storeA.DigestSelectionMembers(s.ctx, pgstate05Project, id)
	if err != nil || digest2 != digest || count2 != count {
		t.Fatalf("complete digest reread = %s/%d err=%v, want %s/%d", digest2, count2, err, digest, count)
	}
}

// ---------------------------------------------------------------------------
// CleanupSelectionStaging: strict boundary (unexpired untouched, expired
// reaped with members) and the bounded limit.
// ---------------------------------------------------------------------------

func TestSelectionCleanupBoundaryAndLimit(t *testing.T) {
	s := newSelectionService(t)
	suffix := pgstate05Hex(t)

	// Three victim-shaped rows via witness INSERTs with literal, fixed
	// created_at instants and explicit lease states (golden literals).
	type probe struct {
		id      string
		created string
		expires string
		expired bool
		members int
	}
	probes := []probe{
		{"p5-cleanup-1-" + suffix, "'2026-09-01T00:00:00Z'::timestamptz", "'2026-09-02T00:00:00Z'::timestamptz", true, 2},
		{"p5-cleanup-2-" + suffix, "'2026-09-02T00:00:00Z'::timestamptz", "'2026-09-03T00:00:00Z'::timestamptz", true, 1},
		{"p5-cleanup-3-" + suffix, "'2026-09-03T00:00:00Z'::timestamptz", "'2099-01-01T00:00:00Z'::timestamptz", false, 1},
	}
	memberOf := map[string][]string{}
	for i, p := range probes {
		if err := s.witnessExecErr(s.headerInsertSQL(map[string]string{
			"id": "'" + p.id + "'", "idempotency_key": "'cleanup-key-" + fmt.Sprint(i) + "'",
			"created_at": p.created, "writer_expires_at": p.expires,
		})); err != nil {
			s.fail("cleanup seed header "+p.id, err)
		}
		keys := []string{}
		for m := 0; m < p.members; m++ {
			ref := selectionRef(fmt.Sprintf("c%d-%04d", i, m))
			mk := goldenMemberKey(t, p.id, ref.Ref)
			if err := s.witnessExecErr(s.memberInsertSQL(map[string]string{
				"selection_id": "'" + p.id + "'", "member_key": "'" + mk + "'", "id": "'" + ref.Ref.ID + "'",
			})); err != nil {
				s.fail("cleanup seed member "+mk, err)
			}
			keys = append(keys, mk)
		}
		memberOf[p.id] = keys
	}
	before := map[string]witnessedSelection{}
	for _, p := range probes {
		before[p.id] = s.witnessSelection(p.id)
	}

	// Boundary + limit: only the two expired rows qualify (older
	// created_at first); limit 2 reaps exactly them.
	if err := s.storeA.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 2); err != nil {
		s.fail("bounded cleanup", err)
	}
	s.assertSelectionAbsent(t, probes[0].id)
	s.assertSelectionAbsent(t, probes[1].id)
	for _, p := range probes[:2] {
		if n := s.memberCount(t, p.id); n != 0 {
			t.Fatalf("reaped %s left %d members", p.id, n)
		}
	}
	survivor := s.assertSelectionStable(t, probes[2].id, before[probes[2].id])
	if survivor.writerToken != "writer-1" {
		t.Fatalf("survivor drifted: %+v", survivor)
	}
	for _, mk := range memberOf[probes[2].id] {
		if !s.witnessMember(probes[2].id, mk).exists {
			t.Fatalf("cleanup removed an unexpired writer's member %s", mk)
		}
	}

	// The remaining unexpired row is untouched by an unbounded sweep.
	if err := s.storeB.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 0); err != nil {
		s.fail("default-limit cleanup", err)
	}
	s.assertSelectionStable(t, probes[2].id, before[probes[2].id])

	// Same-key retry against an abandoned revision recovers cleanly through
	// BeginSelection's own reclamation (Arango's expired-empty path).
	abandoned := "selection-cleanup-retry-" + suffix
	abandonedKey := "cleanup-retry-key-" + suffix
	abandonedHeader := selectionHeaderFixture(abandoned, abandonedKey)
	if _, err := s.storeA.BeginSelection(s.ctx, abandonedHeader, "abandoned-writer"); err != nil {
		s.fail("abandoned begin", err)
	}
	s.expireLease(t, abandoned)
	retry, err := s.storeB.BeginSelection(s.ctx, abandonedHeader, "retry-writer")
	if err != nil {
		s.fail("abandoned retry begin", err)
	}
	assertRevisionEquals(t, "abandoned retry", retry, abandonedHeader)
	retryRow := s.witnessSelection(abandoned)
	if retryRow.writerToken != "retry-writer" || retryRow.memberCount != 0 {
		t.Fatalf("abandoned retry row = %+v, want clean retry-writer ownership", retryRow)
	}
}

// ---------------------------------------------------------------------------
// No-fallback guarantee: an unmigrated schema surfaces the missing relation
// (SQLSTATE 42P01) on every selection method -- never a typed miss, never a
// fallback.
// ---------------------------------------------------------------------------

func TestSelectionStoresFailClosedWithoutTheirTables(t *testing.T) {
	s := newSelectionService(t)
	// One extra owned schema, NO selection tables: same database, distinct
	// owned name, registered for the TestZZ audit.
	bare := fmt.Sprintf("pgstate05_it_%d_%s", time.Now().UnixNano(), pgstate05Hex(t))
	if !pgstate05NameRe.MatchString(bare) {
		t.Fatalf("bare schema name %q does not match the pattern", bare)
	}
	if err := execOn(s.ctx, s.witness, `CREATE SCHEMA "`+bare+`"`); err != nil {
		s.fail("create bare schema", err)
	}
	pgstate05RegisterExtraSchema(bare)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := execOn(cleanupCtx, s.witness, `DROP SCHEMA IF EXISTS "`+bare+`" CASCADE`); err != nil {
			t.Errorf("drop bare schema: %s", s.scrub(err))
		}
	})
	// Owner tables exist here only through the ledger bootstrap of the
	// migrated schema; the bare schema has NOTHING. Build the store against
	// it directly on the witness-backed client is not possible (one conn),
	// so reuse writer A: the methods only prepare statements server-side.
	bareStore, err := New(s.storeA.client, bare)
	if err != nil {
		s.fail("build bare store", err)
	}
	header := selectionHeaderFixture("selection-bare", "bare-key")
	_, err = bareStore.BeginSelection(s.ctx, header, "w")
	assertPgError(t, "begin on unmigrated schema", err, "42P01", "")
	_, err = bareStore.AppendSelectionMembers(s.ctx, "selection-bare", "w", []explorer.SelectionMember{selectionRef("files001")})
	assertPgError(t, "append on unmigrated schema", err, "42P01", "")
	_, err = bareStore.GetSelection(s.ctx, pgstate05Project, "selection-bare")
	assertPgError(t, "get on unmigrated schema", err, "42P01", "")
	_, _, _, err = bareStore.DigestSelectionMembers(s.ctx, pgstate05Project, "selection-bare")
	assertPgError(t, "digest on unmigrated schema", err, "42P01", "")
	if err := bareStore.CleanupSelectionStaging(s.ctx, time.Now().UTC(), 10); err == nil {
		t.Fatal("cleanup on unmigrated schema succeeded, want 42P01")
	}
	if _, err := bareStore.CompleteSelection(s.ctx, "selection-bare", "w", "d", 1, 1, time.Now().UTC()); err == nil {
		t.Fatal("complete on unmigrated schema succeeded, want an error")
	}
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit: exact names this file created, no
// global scan (concurrent sibling suites own their prefixes).
// ---------------------------------------------------------------------------

func TestZZZeroLeftoverPgstate05Fixtures(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	pgstate05OwnedMu.Lock()
	databases := make(map[string]string, len(pgstate05OwnedRes))
	for db, schema := range pgstate05OwnedRes {
		databases[db] = schema
	}
	extra := append([]string{}, pgstate05ExtraSchemas...)
	pgstate05OwnedMu.Unlock()

	for db, schema := range databases {
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, db).Scan(&count); err != nil {
			t.Fatalf("audit database %s: %v", db, err)
		}
		if count != 0 {
			// The owned schema lived inside that database, so a surviving
			// database is itself the leftover; name it for the operator.
			t.Fatalf("isolated database %s (owning schema %s) was left behind", db, schema)
		}
	}
	for _, schema := range extra {
		var present bool
		if err := admin.QueryRow(ctx, `SELECT to_regnamespace($1) IS NOT NULL`, schema).Scan(&present); err != nil {
			t.Fatalf("audit extra schema %s: %v", schema, err)
		}
		if present {
			t.Fatalf("bare probe schema %s was left behind in the service database", schema)
		}
	}
}
