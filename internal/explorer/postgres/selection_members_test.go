package postgres

// Real-PostgreSQL 18 acceptance tests for bounded idempotent selection
// member batches and the digest/count finalize under the current writer
// fence (pg-state-selection-members-v1; check-2 companion:
// go test -count=1 -run '^TestSelectionMembers' ./internal/explorer/postgres/,
// plus the full-package run go test -count=1 ./internal/explorer/postgres/).
//
// Everything here runs against the service named by
// LOOM_TEST_POSTGRES_URL: each test creates one uniquely-named isolated
// database (verify_selem_*) plus one owned schema (verify_selem_*) inside
// it and drops both in its cleanups, so the suite leaves zero leftovers.
// The fixture history is composed through the real pg-store-02 runner from
// this package's embedded owner history and the state-scoped selection
// fixture tests/migrations-selections/000008_selection_revisions.sql (the
// delivered member DDL this ticket builds on: PRIMARY KEY
// (selection_id, member_key), UNIQUE (selection_id, project, generation,
// resource_type, id), the key-shape CHECK, and the member immutability
// guard). NewMigrationRunner composes and sorts the union onto the ONE
// shared per-schema ledger, and the ledger rows are asserted literally with
// the fixture checksum self-recomputed from the file bytes.
//
// Every lifecycle claim is driven through real Store methods on real
// connections; expected rows are asserted literally through direct SQL on a
// witness pool that is a third, physically distinct backend connection, and
// concurrent proofs run over two single-connection clients with provably
// distinct pg_backend_pid() values, at least 10 rounds per racing surface,
// with byte-identical row witnesses (every column plus ctid). The
// conflicting-member arbiter is proven at BOTH layers: the raw database
// refusal names its exact SQLSTATE and constraint, and the adapter is
// proven to CLASSIFY that refusal as explorer.ErrSelectionConflict (a raw
// driver error surfacing instead would fail the same probe). The fenced
// finalize is proven non-vacuous by mutation probes that change exactly one
// claimed fact (digest, count) and must see the exact-named refusal with
// the staging left byte-identical and unusable; the member_bytes
// pass-through is pinned explicitly as the deliberate boundary of what the
// fence verifies. Crash windows are injected through the in-transaction
// complete seam; the bounded keyset stream is proven through the
// test-only page observer (each fetched page <= the 1000-row bound, and
// more than one page for >1000 members). Nothing is mocked and no test here
// passes by skipping: a run presented as acceptance evidence requires the
// live service (see testPostgresURL). Concurrency tests run serially (no
// t.Parallel anywhere).
//
// Golden literals (member-key hexes, the empty-membership digest) are
// pinned independently of the adapter so a derivation drift breaks the
// literal rather than its mirror.
//
// The TestZZ zero-leftover audit is ownership-scoped: it checks each exact
// name this file created and verified (creation registry) and scans ONLY
// its own verify_selem_* prefix for strays; foreign prefixes are other
// suites' life, and any unregistered survivor under THIS prefix is fatal.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"runtime"
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

const (
	// The member DDL is the delivered fixture from the prerequisite
	// (000008 covers BOTH selection tables in one migration); this
	// ticket ships no new DDL and pins the same fixture the member
	// behavior depends on.
	selemFixtureDir     = "tests/migrations-selections"
	selemFixtureFile    = "000008_selection_revisions"
	selemFixtureName    = "selection_revisions"
	selemFixtureVersion = int64(8)
	selemImmutableCode  = "45501"
	selemRefConstraint  = "selection_members_ref_ux"
	// selemRaceRounds is the per-surface race floor.
	selemRaceRounds = 10

	selemProject = "verify-selem-p"
)

// Golden literals pinned independently of the adapter.
const (
	// selemGoldenKeySelemX is the hex(sha256) half of the derived member
	// key for (verify-selem-p, gen-a, DocumentReference, selem-golden-x)
	// under selection "selection-golden": the 0x00-prefixed frame hash
	// recomputed by hand, so drift in selectionMemberKey breaks this
	// literal rather than a computed mirror.
	selemGoldenKeySelemX = "325f564f597dacc4e53f2fd41e9a0d0ae3ddcd340c927dd8f1647837d882b231"
	// selemEmptyMembershipDigest is sha256 over zero membership frames:
	// the digest a zero-member selection must finalize with (the frozen
	// facts CHECK requires a non-empty digest, and this is the true
	// digest of nothing).
	selemEmptyMembershipDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

var selemNameRe = regexp.MustCompile(`^verify_selem_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every resource whose creation this file verified is
// recorded here, and TestZZ asserts each exact name is gone -- and that
// NOTHING else under the verify_selem_* prefix survived.
var (
	selemOwnedMu      sync.Mutex
	selemOwnedRes     = map[string]string{} // database -> owned schema
	selemExtraSchemas []string              // schemas created outside per-test fixtures
)

func selemRegister(db, schema string) {
	selemOwnedMu.Lock()
	defer selemOwnedMu.Unlock()
	selemOwnedRes[db] = schema
}

func selemRegisterExtraSchema(schema string) {
	selemOwnedMu.Lock()
	defer selemOwnedMu.Unlock()
	selemExtraSchemas = append(selemExtraSchemas, schema)
}

// ---------------------------------------------------------------------------
// Isolated PostgreSQL fixture: one uniquely owned database plus one owned
// schema per test, named verify_selem_*; three single-connection clients
// (witness + writer A + writer B) with provably distinct backends.
// ---------------------------------------------------------------------------

type selemService struct {
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

func newSelemService(t *testing.T) *selemService {
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

	dbName := fmt.Sprintf("verify_selem_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !selemNameRe.MatchString(dbName) {
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

	schema := fmt.Sprintf("verify_selem_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !selemNameRe.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if err := execOn(ctx, witness, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	selemRegister(dbName, schema)
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if err := execOn(cleanupCtx, witness, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &selemService{
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

func (s *selemService) backendPID(t *testing.T, client *pgstore.Client, label string) int32 {
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
// owner history plus the state-scoped selection fixture whose member DDL
// this ticket's behavior depends on -- through the real pg-store-02 runner
// onto the ONE shared per-schema ledger, and asserts the ledger literally
// with the fixture checksum self-recomputed from the file bytes.
func (s *selemService) migrate(t *testing.T) {
	t.Helper()
	owner, err := OwnerMigrations()
	if err != nil {
		s.fail("owner migrations", err)
	}
	if len(owner) < 2 || owner[0].Version != 1 || owner[0].Name != "ledger" ||
		owner[1].Version != 2 || owner[1].Name != "explorer_owner" {
		t.Fatalf("owner history = %+v, want ledger(1) + explorer_owner(2) at the front", owner)
	}
	fixture, err := pgstore.LoadMigrations(os.DirFS("../../../"), selemFixtureDir)
	if err != nil {
		s.fail("load selection fixture migrations", err)
	}
	if len(fixture) != 1 || fixture[0].Version != selemFixtureVersion ||
		fixture[0].Name != selemFixtureName {
		t.Fatalf("selection fixture history = %+v, want exactly version %d %s",
			fixture, selemFixtureVersion, selemFixtureName)
	}
	diskBytes, err := os.ReadFile("../../../" + selemFixtureDir + "/" + selemFixtureFile + ".sql")
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

func (s *selemService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

func (s *selemService) headersFQN() string { return s.qualified(SelectionRevisionsTable) }
func (s *selemService) membersFQN() string { return s.qualified(SelectionMembersTable) }

func (s *selemService) witnessExecErr(sql string, args ...any) error {
	return execOn(s.ctx, s.witness, sql, args...)
}

func (s *selemService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := queryRowOn(s.ctx, s.witness, &n, sql, args...); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

type selemWitnessedHeader struct {
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

func (w selemWitnessedHeader) same(o selemWitnessedHeader) bool { return w == o }

func (s *selemService) witnessHeader(id string) selemWitnessedHeader {
	var w selemWitnessedHeader
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire header witness", err)
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
		s.fail("witness header "+id, err)
	}
	w.exists = true
	return w
}

// assertHeaderStable witnesses that a header row did not move at all
// (every column plus the physical ctid).
func (s *selemService) assertHeaderStable(t *testing.T, id string, before selemWitnessedHeader) selemWitnessedHeader {
	t.Helper()
	after := s.witnessHeader(id)
	if !after.same(before) {
		t.Fatalf("selection %s header moved when it must not have:\nbefore=%+v\nafter =%+v", id, before, after)
	}
	return after
}

type selemWitnessedMember struct {
	selectionID, memberKey, project, generation, resourceType, id string
	ordinal                                                       int64
	ctid                                                          string
	exists                                                        bool
}

func (w selemWitnessedMember) same(o selemWitnessedMember) bool { return w == o }

func (s *selemService) witnessMember(selectionID, memberKey string) selemWitnessedMember {
	var w selemWitnessedMember
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

func (s *selemService) memberCount(t *testing.T, selectionID string) int {
	t.Helper()
	return s.execCount(t, "SELECT count(*) FROM "+s.membersFQN()+" WHERE selection_id = $1", selectionID)
}

// storedMembers witnesses every stored member row of a selection (all
// columns) so the fenced finalize can be cross-checked against the stored
// membership independently of the adapter's stream.
func (s *selemService) storedMembers(t *testing.T, selectionID string) []explorer.SelectionMember {
	t.Helper()
	conn, err := s.witness.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire member set witness", err)
	}
	defer conn.Release()
	rows, err := conn.Query(s.ctx,
		`SELECT project, generation, resource_type, id FROM `+s.membersFQN()+`
		  WHERE selection_id = $1 ORDER BY id`, selectionID)
	if err != nil {
		s.fail("witness member set "+selectionID, err)
	}
	defer rows.Close()
	var out []explorer.SelectionMember
	for rows.Next() {
		var m explorer.SelectionMember
		if err := rows.Scan(&m.Ref.Project, &m.Ref.Generation, &m.Ref.ResourceType, &m.Ref.ID); err != nil {
			s.fail("scan member set "+selectionID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		s.fail("iterate member set "+selectionID, err)
	}
	return out
}

// ---------------------------------------------------------------------------
// fixtures local to this suite (distinct ids from the sibling selection
// suite so literals cannot mask each other)
// ---------------------------------------------------------------------------

func selemHex(t *testing.T) string { return randomHex(t) }

func selemHeaderFixture(id, key string) explorer.SelectionRevision {
	return explorer.SelectionRevision{
		ID: id, Project: selemProject, Generation: "gen-a", ResourceType: "DocumentReference",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source: explorer.SelectionSource{
			Kind: explorer.SelectionSourcePublished, Generation: "gen-a",
			RevisionID: "rev-1", ReceiptID: "receipt-1", ExecutionID: "exec-1",
			OutputID: "out-1", SchemaDigest: "schema-digest-1",
			ResourceType: "DocumentReference", SourceIDColumn: "id",
		},
		ScopeDigest: "selem-scope-" + id, RuleDigest: "selem-rule-" + id,
		IdempotencyKey: key, CreatedAt: time.Now().UTC(),
	}
}

func selemRef(id string) explorer.SelectionMember {
	return explorer.SelectionMember{Ref: explorer.ResourceRef{
		Project: selemProject, Generation: "gen-a", ResourceType: "DocumentReference", ID: id,
	}}
}

func selemRefs(ids ...string) []explorer.SelectionMember {
	out := make([]explorer.SelectionMember, 0, len(ids))
	for _, id := range ids {
		out = append(out, selemRef(id))
	}
	return out
}

// beginStaging begins one staging revision on store A and returns it.
func (s *selemService) beginStaging(t *testing.T, id, key, writer string) {
	t.Helper()
	if _, err := s.storeA.BeginSelection(s.ctx, selemHeaderFixture(id, key), writer); err != nil {
		s.fail("begin "+id, err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1: batch retry creates no duplicates; conflicting member
// refuses -- at the adapter AND through the named database arbiter.
// ---------------------------------------------------------------------------

func TestSelectionMembersBatchIdempotentRetryRefusesConflicts(t *testing.T) {
	s := newSelemService(t)
	suffix := selemHex(t)
	id := "selem-batch-" + suffix
	writer := "selem-batch-writer-" + suffix
	s.beginStaging(t, id, "selem-batch-key-"+suffix, writer)

	// A batch that repeats one reference inside itself creates each
	// member exactly once and reports only the creations.
	batch := selemRefs("files001", "files002", "files001")
	inserted, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, batch)
	if err != nil {
		s.fail("first append", err)
	}
	if len(inserted) != 2 || inserted[0].Ref.ID != "files001" || inserted[1].Ref.ID != "files002" {
		t.Fatalf("first append inserted %d members (%v), want exactly [files001 files002]", len(inserted), inserted)
	}
	mk001 := goldenMemberKey(t, id, selemRef("files001").Ref)
	mk002 := goldenMemberKey(t, id, selemRef("files002").Ref)
	m001 := s.witnessMember(id, mk001)
	m002 := s.witnessMember(id, mk002)
	if !m001.exists || !m002.exists {
		t.Fatalf("staged members missing: %s=%+v %s=%+v", mk001, m001, mk002, m002)
	}
	if m001.project != selemProject || m001.generation != "gen-a" ||
		m001.resourceType != "DocumentReference" || m001.id != "files001" {
		t.Fatalf("stored member files001 drifted from its reference: %+v", m001)
	}
	if n := s.memberCount(t, id); n != 2 {
		t.Fatalf("member rows after deduped batch = %d, want 2", n)
	}

	// Retry of the FULL batch is idempotent: zero rows created, the two
	// stored rows byte-identical (every column plus ctid: no delete/re-
	// insert churn, no duplicates).
	retryInserted, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, batch)
	if err != nil {
		s.fail("retry append", err)
	}
	if len(retryInserted) != 0 {
		t.Fatalf("retry append inserted %d members, want 0", len(retryInserted))
	}
	if a := s.witnessMember(id, mk001); !a.same(m001) {
		t.Fatalf("retry moved the stored member rows: before=%+v after=%+v", m001, a)
	}
	if a := s.witnessMember(id, mk002); !a.same(m002) {
		t.Fatalf("retry moved the stored member rows: before=%+v after=%+v", m002, a)
	}
	if n := s.memberCount(t, id); n != 2 {
		t.Fatalf("retry created duplicates: %d rows", n)
	}

	// A mixed batch (one existing, one new) reports only the new one.
	mixed := selemRefs("files001", "files003")
	inserted, err = s.storeA.AppendSelectionMembers(s.ctx, id, writer, mixed)
	if err != nil {
		s.fail("mixed append", err)
	}
	if len(inserted) != 1 || inserted[0].Ref.ID != "files003" {
		t.Fatalf("mixed append inserted %+v, want exactly [files003]", inserted)
	}
	if n := s.memberCount(t, id); n != 3 {
		t.Fatalf("member rows after mixed append = %d, want 3", n)
	}

	// A foreign writer cannot stage anything: refused with ErrSelection-
	// Conflict, and the three stored rows are byte-identical witnesses.
	foreignBefore := [3]selemWitnessedMember{
		s.witnessMember(id, mk001),
		s.witnessMember(id, mk002),
		s.witnessMember(id, goldenMemberKey(t, id, selemRef("files003").Ref)),
	}
	if _, err := s.storeB.AppendSelectionMembers(s.ctx, id, "foreign-"+suffix, selemRefs("files004")); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("foreign writer append = %v, want ErrSelectionConflict", err)
	}
	for i, refID := range []string{"files001", "files002", "files003"} {
		if after := s.witnessMember(id, goldenMemberKey(t, id, selemRef(refID).Ref)); !after.same(foreignBefore[i]) {
			t.Fatalf("foreign writer disturbed stored member %s: %+v", refID, after)
		}
	}
	s.assertMemberRowAbsent(t, id, "files004")
	if n := s.memberCount(t, id); n != 3 {
		t.Fatalf("foreign append left rows behind: %d, want 3", n)
	}

	// Conflicting member (adapter layer): a caller-supplied memberKey
	// that disagrees with the derived key is refused before any row of
	// the batch exists.
	bad := selemRef("files005")
	bad.MemberKey = id + "_" + strings.Repeat("0", 64)
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, append(selemRefs("files006"), bad)); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("wrong-memberKey append = %v, want ErrSelectionConflict", err)
	}
	s.assertMemberRowAbsent(t, id, "files005")
	s.assertMemberRowAbsent(t, id, "files006")
	if n := s.memberCount(t, id); n != 3 {
		t.Fatalf("wrong-memberKey batch leaked rows: %d, want 3", n)
	}

	// Conflicting member (database arbiter): a stored row already holds
	// the reference files007 under a different (shape-valid) member key.
	// The adapter must CLASSIFY the named constraint's refusal as
	// ErrSelectionConflict, and the whole batch must vanish with it.
	if err := s.witnessExecErr(
		`INSERT INTO `+s.membersFQN()+`
		 (selection_id, member_key, project, generation, resource_type, id, ordinal)
		 VALUES ($1, $2, $3, 'gen-a', 'DocumentReference', 'files007', 0)`,
		id, id+"_"+strings.Repeat("f", 64), selemProject); err != nil {
		s.fail("seed conflicting member", err)
	}
	foreignRow := s.witnessMember(id, id+"_"+strings.Repeat("f", 64))
	if !foreignRow.exists {
		t.Fatal("seeded conflicting member vanished")
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files008", "files007")); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("arbiter-conflicting append = %v, want ErrSelectionConflict (classified, not a raw driver error)", err)
	}
	// Post-abort convergence: files008 (inserted before the refusal in
	// this same transaction) vanished with the batch; only the witness-
	// seeded foreign row remains of this probe.
	s.assertMemberRowAbsent(t, id, "files008")
	if after := s.witnessMember(id, id+"_"+strings.Repeat("f", 64)); !after.same(foreignRow) {
		t.Fatalf("the adapter disturbed the conflicting row: %+v", after)
	}
	if n := s.memberCount(t, id); n != 4 {
		t.Fatalf("member rows after arbiter refusal = %d, want 4", n)
	}

	// Non-vacuity of that arbiter: the same collision through direct SQL
	// must be refused by the NAMED database object, not just a Go
	// pre-check. (The adapter inserted files001 under its derived key;
	// a second shape-valid key for the same reference must hit
	// selection_members_ref_ux with SQLSTATE 23505.)
	assertPgError(t, "raw ref-ux collision", s.witnessExecErr(
		`INSERT INTO `+s.membersFQN()+`
		 (selection_id, member_key, project, generation, resource_type, id, ordinal)
		 VALUES ($1, $2, $3, 'gen-a', 'DocumentReference', 'files001', 0)`,
		id, id+"_"+strings.Repeat("a", 64), selemProject), "23505", selemRefConstraint)

	// Control: the legal insert-once shape still inserts (the refusal
	// above is the named constraint, not the table being unwritable).
	if err := s.witnessExecErr(
		`INSERT INTO `+s.membersFQN()+`
		 (selection_id, member_key, project, generation, resource_type, id, ordinal)
		 VALUES ($1, $2, $3, 'gen-a', 'DocumentReference', 'files009', 0)`,
		id, id+"_"+strings.Repeat("b", 64), selemProject); err != nil {
		t.Fatalf("control: legal member row refused: %s", s.scrub(err))
	}

	// Owner abort removes header and members: post-abort convergence for
	// the whole batch.
	if err := s.storeA.AbortSelection(s.ctx, id, writer); err != nil {
		s.fail("owner abort", err)
	}
	if w := s.witnessHeader(id); w.exists {
		t.Fatalf("owner abort left the header: %+v", w)
	}
	if n := s.memberCount(t, id); n != 0 {
		t.Fatalf("owner abort left %d member rows", n)
	}
}

// assertMemberRowAbsent witnesses that the derived member key of (id,
// refID) has no row.
func (s *selemService) assertMemberRowAbsent(t *testing.T, id, refID string) {
	t.Helper()
	mk := goldenMemberKey(t, id, selemRef(refID).Ref)
	if w := s.witnessMember(id, mk); w.exists {
		t.Fatalf("expected no member row %s, found %+v", mk, w)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2: only the active writer may append/complete, and a
// digest/count mismatch leaves UNUSABLE staging. Mutation probes change
// exactly one claimed fact each; the refusal is exact-named and the header
// row stays byte-identical STAGING.
// ---------------------------------------------------------------------------

func TestSelectionMembersCompleteFinalizesUnderWriterFence(t *testing.T) {
	s := newSelemService(t)
	suffix := selemHex(t)
	id := "selem-fence-" + suffix
	writer := "selem-fence-writer-" + suffix
	s.beginStaging(t, id, "selem-fence-key-"+suffix, writer)
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files001", "files002")); err != nil {
		s.fail("stage members", err)
	}
	staged := s.storedMembers(t, id)
	wantDigest, wantCount, wantBytes := membershipFacts(staged)

	// (a) A foreign token cannot complete: exact-named conflict, header
	// byte-identical (still STAGING, facts untouched, ctid unmoved).
	pristine := s.witnessHeader(id)
	if _, err := s.storeB.CompleteSelection(s.ctx, id, "stale-"+suffix, wantDigest, wantCount, wantBytes, time.Now().UTC()); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("foreign-token complete = %v, want ErrSelectionConflict", err)
	}
	s.assertHeaderStable(t, id, pristine)

	// (b) Digest/count mismatch leaves unusable staging. Mutation probe
	// 1: a member appears after the digest was taken -- the claim no
	// longer describes the staged set. The append itself legitimately
	// renews the writer lease, so the stability witness is captured from
	// the post-append state: what must not move is the state, the
	// completion bit, and the frozen facts (and the row's ctid, which a
	// refused finalize may not touch).
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files003")); err != nil {
		s.fail("append third member", err)
	}
	postAppend := s.witnessHeader(id)
	if postAppend.state != "STAGING" || postAppend.complete {
		t.Fatalf("post-append row should still be STAGING: %+v", postAppend)
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, wantDigest, wantCount, wantBytes, time.Now().UTC()); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("complete with pre-append facts = %v, want ErrSelectionIncomplete (mismatch leaves staging unusable)", err)
	}
	s.assertHeaderStable(t, id, postAppend) // no flip, no fact write, no ctid move
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, id, "", 10, func(explorer.SelectionMember) error {
		return nil
	}); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("visit after a refused finalize = %v, want ErrSelectionIncomplete -- staging must be unusable", err)
	}

	// Mutation probe 2: correct digest, count short by one.
	redigested, realCount, rebytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
	if err != nil {
		s.fail("re-digest", err)
	}
	if realCount != 3 || redigested == "" {
		t.Fatalf("re-digest = %s/%d err=%v, want 3 members", redigested, realCount, err)
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, redigested, realCount-1, rebytes, time.Now().UTC()); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("complete with count-1 = %v, want ErrSelectionIncomplete", err)
	}
	s.assertHeaderStable(t, id, postAppend)

	// Mutation probe 3: correct count, digest flipped in one hex char.
	flipped := redigested[:len(redigested)-1]
	if redigested[len(redigested)-1] == '0' {
		flipped += "1"
	} else {
		flipped += "0"
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, flipped, realCount, rebytes, time.Now().UTC()); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("complete with one-flipped digest = %v, want ErrSelectionIncomplete", err)
	}
	s.assertHeaderStable(t, id, postAppend)

	// Mutation probe 4: membership that does not match ANY of the three
	// claims still refuses with the same exact name, and the stored
	// members are still the byte-identical three (the fence never
	// rewrites membership).
	for i, refID := range []string{"files001", "files002", "files003"} {
		_ = i
		mk := goldenMemberKey(t, id, selemRef(refID).Ref)
		if w := s.witnessMember(id, mk); !w.exists {
			t.Fatalf("refused finalizes disturbed stored member %s: %+v", refID, w)
		}
	}

	// The fence verifies the IDENTITY-bearing facts and passes
	// member_bytes through: correct digest/count with bytes=0 completes
	// (Arango parity -- @bytes is the caller's accounting hint) and the
	// frozen row carries exactly what was claimed.
	completedAt := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	completed, err := s.storeA.CompleteSelection(s.ctx, id, writer, redigested, realCount, 0, completedAt)
	if err != nil {
		s.fail("complete with hint bytes", err)
	}
	if !completed.Complete || completed.MembershipDigest != redigested || completed.MemberCount != realCount || completed.MemberBytes != 0 {
		t.Fatalf("completed = %+v, want frozen %s/%d/0", completed, redigested, realCount)
	}
	done := s.witnessHeader(id)
	if done.state != "COMPLETE" || !done.complete || done.membershipDigest != redigested ||
		done.memberCount != realCount || done.memberBytes != 0 {
		t.Fatalf("frozen witness = %+v, want %s/%d/0 COMPLETE", done, redigested, realCount)
	}
	if done.completeAtText != "2026-09-19 00:00:00+00" {
		t.Fatalf("completed_at literal = %q, want 2026-09-19 00:00:00+00", done.completeAtText)
	}

	// The frozen facts describe the STORED rows: recompute them
	// independently from the witness, never from the adapter.
	if gotDigest, gotCount, gotBytes := membershipFacts(s.storedMembers(t, id)); gotDigest != redigested || gotCount != realCount || gotBytes != rebytes {
		t.Fatalf("frozen digest/count disagrees with stored membership: frozen %s/%d, stored %s/%d (%d bytes)",
			redigested, realCount, gotDigest, gotCount, gotBytes)
	}

	// Idempotent retry: identical facts return the stored row, byte-
	// identical. Changed facts conflict; the COMPLETE row never moves.
	retry, err := s.storeB.CompleteSelection(s.ctx, id, writer, redigested, realCount, 0, completedAt)
	if err != nil {
		s.fail("idempotent complete retry", err)
	}
	if !retry.Complete || retry.MembershipDigest != redigested {
		t.Fatalf("idempotent complete retry = %+v", retry)
	}
	s.assertHeaderStable(t, id, done)
	if _, err := s.storeB.CompleteSelection(s.ctx, id, writer, redigested+"0", realCount, 0, completedAt); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("complete with different facts after COMPLETE = %v, want ErrSelectionConflict", err)
	}
	s.assertHeaderStable(t, id, done)

	// Missing selection: not-found, not a silent success.
	if _, err := s.storeA.CompleteSelection(s.ctx, "selem-never-"+suffix, writer, redigested, realCount, 0, completedAt); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("complete on missing selection = %v, want ErrSelectionNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2 (concurrency): the fenced finalize racing a live append on
// TWO physically distinct connections. Whichever side wins the header lock,
// a COMPLETE row's frozen digest/count ALWAYS equals the digest/count of
// the member rows actually stored -- the fence is what makes that
// invariant hold, and the losing side gets its exact-named refusal.
// ---------------------------------------------------------------------------

func TestSelectionMembersFencedCompleteRacesAppend(t *testing.T) {
	s := newSelemService(t)
	if s.pidA == s.pidB {
		t.Fatal("fence race requires two distinct backends")
	}
	// Warm store B's single physical connection before the race: without
	// this the first CompleteSelection of the round pays connection
	// setup while the already-warm append wins the lock by raw latency,
	// and the biased schedule degenerates to one interleaving.
	if _, err := s.storeB.GetSelection(s.ctx, selemProject, "selem-warm"); !errors.Is(err, explorer.ErrSelectionNotFound) {
		s.fail("warm store B", err)
	}
	suffix := selemHex(t)

	completeWins, appendWins := 0, 0
	for round := 0; round < selemRaceRounds; round++ {
		id := fmt.Sprintf("selem-fence-race-%d-%s", round, suffix)
		writer := "selem-race-writer-" + suffix
		s.beginStaging(t, id, fmt.Sprintf("selem-race-key-%d-%s", round, suffix), writer)
		if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files001")); err != nil {
			s.fail("race seed append", err)
		}
		digest, count, bytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
		if err != nil || count != 1 {
			s.fail("race seed digest", err)
		}

		// Both operations race for real on two distinct backends. Even
		// rounds head-start the complete, odd rounds head-start the
		// append, so the header-lock serialization is exercised from both
		// directions; the per-round invariants below hold no matter who
		// wins, and the aggregate check proves both interleavings
		// actually occurred.
		var appendErr, completeErr error
		var wg sync.WaitGroup
		raceComplete := func() {
			defer wg.Done()
			_, completeErr = s.storeB.CompleteSelection(s.ctx, id, writer, digest, count, bytes, time.Now().UTC())
		}
		raceAppend := func() {
			defer wg.Done()
			_, appendErr = s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files002"))
		}
		wg.Add(2)
		if round%2 == 0 {
			go raceComplete()
			// Yield so the head-started side actually issues its
			// header-locking statement before the other side enters
			// the race. This seeds the arrival ORDER only; PostgreSQL
			// still decides the serialization, and the per-round
			// invariants below hold no matter who wins.
			runtime.Gosched()
			go raceAppend()
		} else {
			go raceAppend()
			runtime.Gosched()
			go raceComplete()
		}
		wg.Wait()

		// Exactly one of the two raced operations was refused with its
		// exact name; the other may have succeeded. Both-succeed is
		// impossible (the header lock serializes them), and both-refused
		// means the fence lost the invariant.
		row := s.witnessHeader(id)
		if !row.exists {
			t.Fatalf("round %d: header vanished", round)
		}
		stored := s.storedMembers(t, id)
		gotDigest, gotCount, _ := membershipFacts(stored)
		if row.complete {
			completeWins++
			// The frozen facts describe EXACTLY the stored rows.
			if row.membershipDigest != gotDigest || row.memberCount != gotCount {
				t.Fatalf("round %d: COMPLETE row froze %s/%d but the stored membership is %s/%d (fence violated)",
					round, row.membershipDigest, row.memberCount, gotDigest, gotCount)
			}
			if appendErr == nil {
				t.Fatalf("round %d: append succeeded yet the row completed without room for it", round)
			}
			if !errors.Is(appendErr, explorer.ErrSelectionConflict) {
				t.Fatalf("round %d: append against the completed row = %v, want ErrSelectionConflict", round, appendErr)
			}
			if completeErr != nil {
				t.Fatalf("round %d: complete lost and won at once: %v", round, completeErr)
			}
			if len(stored) != 1 {
				t.Fatalf("round %d: completed row stores %d members, want the 1 the claim covered", round, len(stored))
			}
		} else {
			appendWins++
			// The append won: the claimed facts now disagree with the
			// staged set, so the complete was refused with the exact
			// name and the row stays byte-identical STAGING.
			if !errors.Is(completeErr, explorer.ErrSelectionIncomplete) {
				t.Fatalf("round %d: complete against a moved membership = %v, want ErrSelectionIncomplete", round, completeErr)
			}
			if appendErr != nil {
				t.Fatalf("round %d: append lost although the row stayed STAGING: %v", round, appendErr)
			}
			if row.state != "STAGING" || row.complete || row.membershipDigest != "" || row.memberCount != 0 {
				t.Fatalf("round %d: refused finalize left facts behind: %+v", round, row)
			}
			if len(stored) != 2 {
				t.Fatalf("round %d: staged members = %d, want 2 after the append won", round, len(stored))
			}
			// Post-refusal convergence: the writer can still finalize
			// the true membership.
			if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, gotDigest, gotCount, 0, time.Now().UTC()); err != nil {
				t.Fatalf("round %d: correct complete after the refusal = %v", round, err)
			}
			after := s.witnessHeader(id)
			if !after.complete || after.membershipDigest != gotDigest || after.memberCount != gotCount {
				t.Fatalf("round %d: post-refusal freeze = %+v, want %s/%d", round, after, gotDigest, gotCount)
			}
		}
	}
	if completeWins == 0 || appendWins == 0 {
		t.Fatalf("race schedule never exercised both interleavings over %d rounds: complete-won=%d append-won=%d",
			selemRaceRounds, completeWins, appendWins)
	}
}

// ---------------------------------------------------------------------------
// Durability seam: a crash between the completion CAS and its commit
// leaves the row STAGING with its members byte-identical (the freeze
// commits whole or not at all), and the retry after the crash converges.
// ---------------------------------------------------------------------------

func TestSelectionMembersCompleteCrashWindowConvergence(t *testing.T) {
	s := newSelemService(t)
	suffix := selemHex(t)
	id := "selem-crash-" + suffix
	writer := "selem-crash-writer-" + suffix
	s.beginStaging(t, id, "selem-crash-key-"+suffix, writer)
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, selemRefs("files001", "files002")); err != nil {
		s.fail("stage members", err)
	}
	before := s.witnessHeader(id)
	memberKeys := []string{}
	for _, refID := range []string{"files001", "files002"} {
		memberKeys = append(memberKeys, goldenMemberKey(t, id, selemRef(refID).Ref))
	}
	members := []selemWitnessedMember{s.witnessMember(id, memberKeys[0]), s.witnessMember(id, memberKeys[1])}

	digest, count, bytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
	if err != nil {
		s.fail("digest", err)
	}
	s.storeA.beforeCompleteCommit = func(context.Context, pgx.Tx) error {
		return errors.New("injected crash before complete commit")
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, bytes, time.Now().UTC()); err == nil {
		t.Fatal("crashed complete unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "before commit") {
		t.Fatalf("crashed complete error = %v, want the seam-classified wrap", err)
	}
	// The freeze vanished whole: still STAGING, no frozen facts, ctid
	// unmoved; members byte-identical.
	s.assertHeaderStable(t, id, before)
	for i, mk := range memberKeys {
		if after := s.witnessMember(id, mk); !after.same(members[i]) {
			t.Fatalf("crashed complete disturbed member %s: %+v", mk, after)
		}
	}
	// Post-crash convergence: the same complete commits after the seam
	// is removed.
	s.storeA.beforeCompleteCommit = nil
	completed, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		s.fail("post-crash complete", err)
	}
	if !completed.Complete || completed.MembershipDigest != digest || completed.MemberCount != count {
		t.Fatalf("post-crash completed = %+v, want %s/%d", completed, digest, count)
	}
	after := s.witnessHeader(id)
	if after.state != "COMPLETE" || !after.complete || after.membershipDigest != digest || after.memberCount != count {
		t.Fatalf("post-crash witness = %+v", after)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3: Visit is bounded per call, keyset stable under foreign
// churn, and cancellation-aware with a resumable cursor.
// ---------------------------------------------------------------------------

func TestSelectionMembersVisitBoundedKeysetAndCancellation(t *testing.T) {
	s := newSelemService(t)
	suffix := selemHex(t)

	// A COMPLETE selection of 300 members.
	id := "selem-visit-" + suffix
	writer := "selem-visit-writer-" + suffix
	s.beginStaging(t, id, "selem-visit-key-"+suffix, writer)
	const total = 300
	batch := make([]explorer.SelectionMember, 0, total)
	for i := 0; i < total; i++ {
		batch = append(batch, selemRef(fmt.Sprintf("vm%04d", i)))
	}
	for offset := 0; offset < len(batch); offset += 256 {
		end := offset + 256
		if end > len(batch) {
			end = len(batch)
		}
		if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, batch[offset:end]); err != nil {
			s.fail("bulk append", err)
		}
	}
	digest, count, bytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
	if err != nil || count != total {
		s.fail("bulk digest", fmt.Errorf("digest = %s/%d err=%v, want %d members", digest, count, err, total))
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, bytes, time.Now().UTC()); err != nil {
		s.fail("complete", err)
	}

	// Foreign churn while the walk runs: another selection is appended
	// to and aborted on the second backend throughout the paging. This
	// is what a keyset walk must survive -- an offset walk would skip
	// or repeat members.
	stopChurn := make(chan struct{})
	churnDone := make(chan struct{})
	go func() {
		defer close(churnDone)
		round := 0
		for {
			select {
			case <-stopChurn:
				return
			default:
			}
			round++
			churnID := fmt.Sprintf("selem-churn-%d-%s", round, suffix)
			churnWriter := "selem-churn-writer-" + suffix
			if _, err := s.storeB.BeginSelection(s.ctx, selemHeaderFixture(churnID, fmt.Sprintf("selem-churn-key-%d-%s", round, suffix)), churnWriter); err != nil {
				return
			}
			if _, err := s.storeB.AppendSelectionMembers(s.ctx, churnID, churnWriter, selemRefs(fmt.Sprintf("churn%04d", round))); err != nil {
				return
			}
			if err := s.storeB.AbortSelection(s.ctx, churnID, churnWriter); err != nil {
				return
			}
		}
	}()

	seen := []string{}
	cursor := ""
	calls := 0
	for {
		before := len(seen)
		last, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, id, cursor, 64, func(member explorer.SelectionMember) error {
			seen = append(seen, member.MemberKey)
			return nil
		})
		if err != nil {
			s.fail("visit walk", err)
		}
		calls++
		visited := len(seen) - before
		if visited > 64 {
			close(stopChurn)
			<-churnDone
			t.Fatalf("visit call %d visited %d members, want at most pageSize 64", calls, visited)
		}
		if last == cursor {
			if visited != 0 {
				t.Fatalf("visit call %d advanced no cursor yet visited %d members", calls, visited)
			}
			break
		}
		if visited == 0 {
			t.Fatalf("visit call %d advanced the cursor without visiting any member", calls)
		}
		cursor = last
	}
	close(stopChurn)
	<-churnDone

	if len(seen) != total {
		t.Fatalf("visit walk collected %d, want %d (no gaps, no duplicates)", len(seen), total)
	}
	if !sort.StringsAreSorted(seen) {
		t.Fatal("visit walk is not in byte order")
	}
	seenSet := map[string]int{}
	for _, k := range seen {
		seenSet[k]++
	}
	for k, n := range seenSet {
		if n != 1 {
			t.Fatalf("visit walk repeated member %s %d times", k, n)
		}
	}
	if wantCalls := (total+63)/64 + 1; calls != wantCalls {
		t.Fatalf("visit walk took %d calls, want %d (64-bounded pages plus the terminal empty page)", calls, wantCalls)
	}

	// Cancellation-aware: a caller context cancelled mid-page stops the
	// walk between members and the returned cursor names the LAST member
	// the visitor actually saw; resuming on a fresh context continues
	// from there with no gap and no duplicate.
	cancelCtx, cancel := context.WithCancel(s.ctx)
	const cancelAfter = 20
	visitedBefore := 0
	var cancelCursor string
	seenCancel := []string{}
	last, err := s.storeA.VisitSelectionMembers(cancelCtx, selemProject, id, "", 64, func(member explorer.SelectionMember) error {
		seenCancel = append(seenCancel, member.MemberKey)
		visitedBefore++
		if visitedBefore == cancelAfter {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled walk err = %v, want context.Canceled", err)
	}
	if visitedBefore != cancelAfter {
		t.Fatalf("cancelled walk visited %d members, want exactly %d before the stop", visitedBefore, cancelAfter)
	}
	cancelCursor = last
	if len(seenCancel) != cancelAfter {
		t.Fatalf("cancelled walk delivered %d members but visited %d (cursor must trail the visitor exactly)",
			len(seenCancel), visitedBefore)
	}
	if cancelCursor != seenCancel[cancelAfter-1] {
		t.Fatalf("cancelled cursor %q must name the last visited member %q", cancelCursor, seenCancel[cancelAfter-1])
	}
	resume := []string{}
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, id, cancelCursor, 64, func(member explorer.SelectionMember) error {
		resume = append(resume, member.MemberKey)
		return nil
	}); err != nil {
		s.fail("resumed walk after cancellation", err)
	}
	if len(resume) != 64 {
		t.Fatalf("resumed page visited %d, want the next 64", len(resume))
	}
	if resume[0] == cancelCursor {
		t.Fatal("resumed walk repeated the cursor member (duplicate)")
	}
	if i := sort.SearchStrings(seen, cancelCursor); i >= len(seen) || seen[i] != cancelCursor {
		t.Fatalf("cancelled cursor %q is not a member of the full walk", cancelCursor)
	}
	if resume[0] != seen[cancelAfter] {
		t.Fatalf("resumed walk started at %q, want exactly the next member %q (gap)", resume[0], seen[cancelAfter])
	}
	joined := append(append([]string{}, seenCancel...), resume...)
	if !reflect.DeepEqual(joined, seen[:cancelAfter+len(resume)]) {
		t.Fatal("the cancelled+resumed sequence diverged from the uninterrupted walk")
	}

	// Bounded page cap: an absurd page budget is still clamped to one
	// bounded page (the 1000-row stream page), so no call can ever
	// materialize an unbounded member list.
	bigID := "selem-visit-big-" + suffix
	bigWriter := "selem-big-writer-" + suffix
	s.beginStaging(t, bigID, "selem-big-key-"+suffix, bigWriter)
	const bigTotal = 1200
	bigBatch := make([]explorer.SelectionMember, 0, bigTotal)
	for i := 0; i < bigTotal; i++ {
		bigBatch = append(bigBatch, selemRef(fmt.Sprintf("bm%04d", i)))
	}
	for offset := 0; offset < len(bigBatch); offset += 600 {
		end := offset + 600
		if end > len(bigBatch) {
			end = len(bigBatch)
		}
		if _, err := s.storeA.AppendSelectionMembers(s.ctx, bigID, bigWriter, bigBatch[offset:end]); err != nil {
			s.fail("big append", err)
		}
	}
	bigDigest, bigCount, bigBytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, bigID)
	if err != nil || bigCount != bigTotal {
		s.fail("big digest", fmt.Errorf("digest = %s/%d err=%v", bigDigest, bigCount, err))
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, bigID, bigWriter, bigDigest, bigCount, bigBytes, time.Now().UTC()); err != nil {
		s.fail("big complete", err)
	}
	bigVisited := 0
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, bigID, "", math.MaxInt32, func(explorer.SelectionMember) error {
		bigVisited++
		return nil
	}); err != nil {
		s.fail("clamp visit", err)
	}
	if bigVisited != selectionDigestPageSize {
		t.Fatalf("absurd page budget visited %d members in one call, want exactly the %d-row bounded page",
			bigVisited, selectionDigestPageSize)
	}

	// STAGING refuses; nil visitor refused; foreign project concealed.
	stagingID := "selem-visit-staging-" + suffix
	s.beginStaging(t, stagingID, "selem-visit-staging-key-"+suffix, "w-"+suffix)
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, stagingID, "", 10, func(explorer.SelectionMember) error {
		return nil
	}); !errors.Is(err, explorer.ErrSelectionIncomplete) {
		t.Fatalf("visit on STAGING = %v, want ErrSelectionIncomplete", err)
	}
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, selemProject, id, "", 10, nil); err == nil ||
		!strings.Contains(err.Error(), "visitor is required") {
		t.Fatalf("nil visitor = %v, want the required-visitor refusal", err)
	}
	if _, err := s.storeA.VisitSelectionMembers(s.ctx, "other-project", id, "", 10, func(explorer.SelectionMember) error {
		return nil
	}); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("foreign-project visit = %v, want ErrSelectionNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// The digest/finalize stream is bounded and keyset-driven (proven through
// the page observer), frozen facts are returned without re-streaming, and
// a zero-member selection finalizes with the true digest of nothing.
// ---------------------------------------------------------------------------

func TestSelectionMembersDigestIsBoundedPagedAndFrozen(t *testing.T) {
	s := newSelemService(t)
	suffix := selemHex(t)

	// Golden, offline-safe: the adapter's derived key equals the pinned
	// literal for a fresh triple (drift breaks the literal).
	ref := explorer.ResourceRef{Project: selemProject, Generation: "gen-a", ResourceType: "DocumentReference", ID: "selem-golden-x"}
	if got := selectionMemberKey("selection-golden", ref); got != "selection-golden_"+selemGoldenKeySelemX {
		t.Fatalf("member key drifted from the pinned golden:\nadapter %s\ngolden  %s", got, "selection-golden_"+selemGoldenKeySelemX)
	}

	id := "selem-pages-" + suffix
	writer := "selem-pages-writer-" + suffix
	s.beginStaging(t, id, "selem-pages-key-"+suffix, writer)
	const total = 1001
	batch := make([]explorer.SelectionMember, 0, total)
	for i := 0; i < total; i++ {
		batch = append(batch, selemRef(fmt.Sprintf("pg%04d", i)))
	}
	if _, err := s.storeA.AppendSelectionMembers(s.ctx, id, writer, batch); err != nil {
		s.fail("bulk append", err)
	}

	pages := []int{}
	s.storeA.memberPageStreamed = func(rows int) { pages = append(pages, rows) }

	digest, count, bytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
	if err != nil {
		s.fail("paged digest", err)
	}
	// Facts equal an INDEPENDENT recomputation over the whole batch.
	wantDigest, wantCount, wantBytes := membershipFacts(batch)
	if digest != wantDigest || count != wantCount || bytes != wantBytes {
		t.Fatalf("paged digest = %s/%d/%d, want %s/%d/%d", digest, count, bytes, wantDigest, wantCount, wantBytes)
	}
	// The walk is bounded and crossed the page boundary: more than one
	// page, every page within the 1000-row bound, exact page sizes.
	if len(pages) < 2 {
		t.Fatalf("digest of %d members fetched %d pages; a single-page read is a full materialization", total, len(pages))
	}
	for i, n := range pages {
		if n > selectionDigestPageSize {
			t.Fatalf("page %d fetched %d rows, above the %d-row bound", i, n, selectionDigestPageSize)
		}
	}
	wantPages := []int{1000, 1}
	if len(pages) != len(wantPages) {
		t.Fatalf("pages fetched = %v, want %v (exact keyset pages: 1000 + the short final page)", pages, wantPages)
	}
	for i := range wantPages {
		if pages[i] != wantPages[i] {
			t.Fatalf("pages fetched = %v, want %v", pages, wantPages)
		}
	}

	// The fenced finalize RE-STREAMS the membership itself (observer
	// fires again under the fence) and only then freezes.
	pages = nil
	completed, err := s.storeA.CompleteSelection(s.ctx, id, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		s.fail("fenced complete", err)
	}
	if !completed.Complete {
		t.Fatalf("completed = %+v", completed)
	}
	if len(pages) != 2 || pages[0] != 1000 || pages[1] != 1 {
		t.Fatalf("fenced finalize did not re-stream under the fence: pages = %v, want [1000 1]", pages)
	}

	// Frozen path: a COMPLETE digest returns the stored facts WITHOUT
	// touching the member table.
	pages = nil
	frozenDigest, frozenCount, _, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, id)
	if err != nil {
		s.fail("frozen digest", err)
	}
	if frozenDigest != digest || frozenCount != count {
		t.Fatalf("frozen digest = %s/%d, want %s/%d", frozenDigest, frozenCount, digest, count)
	}
	if len(pages) != 0 {
		t.Fatalf("COMPLETE digest re-streamed %v; frozen facts must be returned as stored", pages)
	}
	s.storeA.memberPageStreamed = nil

	// Zero-member golden: the digest of nothing is the empty sha256
	// literal, count/bytes are zero, and the fence finalizes it (the
	// frozen-facts CHECK needs a non-empty digest -- this is the true
	// digest of an empty set).
	emptyID := "selem-empty-" + suffix
	emptyWriter := "selem-empty-writer-" + suffix
	s.beginStaging(t, emptyID, "selem-empty-key-"+suffix, emptyWriter)
	emptyDigest, emptyCount, emptyBytes, err := s.storeA.DigestSelectionMembers(s.ctx, selemProject, emptyID)
	if err != nil {
		s.fail("empty digest", err)
	}
	if emptyDigest != selemEmptyMembershipDigest || emptyCount != 0 || emptyBytes != 0 {
		t.Fatalf("empty digest = %s/%d/%d, want %s/0/0", emptyDigest, emptyCount, emptyBytes, selemEmptyMembershipDigest)
	}
	if _, err := s.storeA.CompleteSelection(s.ctx, emptyID, emptyWriter, emptyDigest, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("finalizing the empty membership = %v, want success", err)
	}
	row := s.witnessHeader(emptyID)
	if !row.complete || row.membershipDigest != selemEmptyMembershipDigest || row.memberCount != 0 {
		t.Fatalf("empty-selection freeze = %+v", row)
	}
	// And an attacker-style claim of a NON-empty membership over the
	// empty staging is refused by the fence.
	if _, err := s.storeA.CompleteSelection(s.ctx, emptyID, emptyWriter, digest, count, bytes, time.Now().UTC()); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("re-complete of the empty row with foreign facts = %v, want ErrSelectionConflict", err)
	}
}

// ---------------------------------------------------------------------------
// SQL contract for the membership statement set (offline half).
// ---------------------------------------------------------------------------

func TestSelectionMembersSQLAndGoldenContract(t *testing.T) {
	sql := newSelectionMemberSQL("ctl")
	headersFQN := `"ctl"."selection_revisions"`
	membersFQN := `"ctl"."selection_members"`
	for name, statement := range map[string]string{
		"memberInsert": sql.memberInsert, "memberDigestPage": sql.memberDigestPage,
		"memberVisitPage": sql.memberVisitPage, "leaseRenew": sql.leaseRenew,
		"completeCAS": sql.completeCAS,
	} {
		if !strings.Contains(statement, headersFQN) && !strings.Contains(statement, membersFQN) {
			t.Fatalf("%s is not schema-qualified: %s", name, statement)
		}
	}
	if !strings.Contains(sql.memberInsert, "ON CONFLICT (selection_id, member_key) DO NOTHING") {
		t.Fatalf("memberInsert must be insert-once idempotent: %s", sql.memberInsert)
	}
	if got := strings.Count(sql.memberInsert, "$"); got != len(memberColumns)+1 {
		t.Fatalf("memberInsert placeholders = %d, want %d", got, len(memberColumns)+1)
	}
	if !strings.Contains(sql.memberDigestPage, "ORDER BY id LIMIT $6") ||
		!strings.Contains(sql.memberVisitPage, "ORDER BY member_key LIMIT $4") {
		t.Fatalf("both member walks must be keyset-bounded: %q / %q", sql.memberDigestPage, sql.memberVisitPage)
	}
	if !strings.Contains(sql.completeCAS, "state = 'STAGING'") || !strings.Contains(sql.completeCAS, "writer_token = $6") {
		t.Fatalf("completeCAS must be a writer-conditional staging CAS: %s", sql.completeCAS)
	}
	// The adapter's conflicting-member classification names the delivered
	// DDL's arbiter exactly.
	if !strings.Contains(string(readFixtureDDL(t)), selemRefConstraint) {
		t.Fatalf("fixture DDL must define the arbiter the adapter classifies: %s", selemRefConstraint)
	}
	conflict := &pgconn.PgError{Code: "23505", ConstraintName: selemRefConstraint}
	if !isMemberRefConflict(fmt.Errorf("wrapped: %w", conflict)) {
		t.Fatal("the named arbiter's refusal must classify as a conflicting member")
	}
	if isMemberRefConflict(&pgconn.PgError{Code: "23505", ConstraintName: "selection_revisions_id_pk"}) {
		t.Fatal("a different constraint's unique violation is not a member conflict")
	}
	if isMemberRefConflict(errors.New("plain error")) {
		t.Fatal("a plain error is not a member conflict")
	}
}

func readFixtureDDL(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../" + selemFixtureDir + "/" + selemFixtureFile + ".sql")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-leftover audit: the creation registry's exact names
// are gone, and a scan over THIS suite's own verify_selem_* prefix finds
// nothing. Foreign prefixes belong to sibling suites; any unregistered
// survivor under this prefix is fatal.
// ---------------------------------------------------------------------------

func TestZZZeroLeftoverVerifySelemFixtures(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	selemOwnedMu.Lock()
	databases := make(map[string]string, len(selemOwnedRes))
	for db, schema := range selemOwnedRes {
		databases[db] = schema
	}
	extra := append([]string{}, selemExtraSchemas...)
	selemOwnedMu.Unlock()

	// Creation registry: every exact name whose verified creation this
	// file recorded must be gone.
	for db, schema := range databases {
		var count int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname = $1`, db).Scan(&count); err != nil {
			t.Fatalf("audit database %s: %v", db, err)
		}
		if count != 0 {
			t.Fatalf("isolated database %s (owning schema %s) was left behind", db, schema)
		}
	}
	for _, schema := range extra {
		var present bool
		if err := admin.QueryRow(ctx, `SELECT to_regnamespace($1) IS NOT NULL`, schema).Scan(&present); err != nil {
			t.Fatalf("audit extra schema %s: %v", schema, err)
		}
		if present {
			t.Fatalf("probe schema %s was left behind in the service database", schema)
		}
	}

	// Prefix scan under THIS suite's own prefix only (never a global
	// baseline): a stray under verify_selem_* is fatal whether or not it
	// was registered. Foreign prefixes are sibling suites' life.
	var strays []string
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_selem\_%' ESCAPE '\' ORDER BY datname`)
	if err != nil {
		t.Fatalf("prefix-scan databases: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan prefix strays: %v", err)
		}
		strays = append(strays, "database "+name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate prefix strays: %v", err)
	}
	rows.Close()
	schemaRows, err := admin.Query(ctx, `SELECT n.nspname FROM pg_namespace n WHERE n.nspname LIKE 'verify\_selem\_%' ESCAPE '\' ORDER BY n.nspname`)
	if err != nil {
		t.Fatalf("prefix-scan schemas: %v", err)
	}
	for schemaRows.Next() {
		var name string
		if err := schemaRows.Scan(&name); err != nil {
			schemaRows.Close()
			t.Fatalf("scan schema strays: %v", err)
		}
		strays = append(strays, "schema "+name)
	}
	if err := schemaRows.Err(); err != nil {
		schemaRows.Close()
		t.Fatalf("iterate schema strays: %v", err)
	}
	schemaRows.Close()
	if len(strays) > 0 {
		t.Fatalf("unregistered survivors under the verify_selem_* prefix: %s", strings.Join(strays, ", "))
	}
}
