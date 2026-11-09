package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/publication"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
)

// Real PostgreSQL 18 integration for the fenced execution-owner and
// reconciler checkpoints (pg-publication-leases-checkpoints-v1). Service-
// gated by LOOM_TEST_POSTGRES_URL: every acceptance assertion below runs
// against a real server. The tests create one uniquely owned
// pgleases01_it_* database and one owned schema per test, apply the ledger
// bootstrap plus the sibling 000004 catalog migration plus this ticket's
// 000006 checkpoint migration through the real pg-store-02 runner, and
// assert literal expected rows through separate physical connections. A
// skipped required case is a failure for this ticket's acceptance, so the
// check is run with the service configured; the gate exists only to keep
// unrelated CI lanes green.

var pgleases01ResourceNamePattern = regexp.MustCompile(`^pgleases01_it_[0-9]+_[0-9a-f]+$`)

// leaseService owns one isolated database and one owned schema inside it,
// both named pgleases01_it_*, plus the migrated catalog, a registry bound to
// that schema, and two keepers bound to two pools of one physical connection
// each -- the two real PG connections the stale-writer proof needs. Nothing
// outside these resources is touched; TestZeroLeftoverPgleasesResources,
// declared last in this file, audits that none survive.
type leaseService struct {
	ctx      context.Context
	admin    *pgxpool.Pool // service database
	iso      *pgxpool.Pool // isolated database, pool A (one backend)
	isoB     *pgxpool.Pool // isolated database, pool B (a second backend)
	schema   string
	registry *Registry
	keeperA  *FencedLeases
	keeperB  *FencedLeases
	scrub    func(any) string
	fail     func(step string, err error)
}

func newLeaseService(t *testing.T) *leaseService {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	// Scrub every credential-bearing string from failure output.
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

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgleases01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgleases01ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
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

	// Each pool is capped at one physical connection so pool A and pool B
	// are provably distinct PG backends.
	poolA, err := newOwnedPool(ctx, dsn)
	if err != nil {
		fail("connect pool A to isolated database", err)
	}
	t.Cleanup(poolA.Close)
	poolB, err := newOwnedPool(ctx, dsn)
	if err != nil {
		fail("connect pool B to isolated database", err)
	}
	t.Cleanup(poolB.Close)

	schema := fmt.Sprintf("pgleases01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgleases01ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := poolA.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := poolA.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &leaseService{ctx: ctx, admin: admin, iso: poolA, isoB: poolB, schema: schema, scrub: scrub, fail: fail}
	s.migrate(t)
	registry, err := New(poolA, schema)
	if err != nil {
		fail("build registry", err)
	}
	s.registry = registry
	keeperA, err := NewLeaseKeeper(poolA, schema)
	if err != nil {
		fail("build keeper A", err)
	}
	s.keeperA = keeperA
	keeperB, err := NewLeaseKeeper(poolB, schema)
	if err != nil {
		fail("build keeper B", err)
	}
	s.keeperB = keeperB
	return s
}

// newOwnedPool opens a pool pinned to exactly one physical connection.
func newOwnedPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 1
	return pgxpool.NewWithConfig(ctx, config)
}

// migrate applies this ticket's owned history -- the ledger bootstrap from
// the embedded pg-store-02 set, the sibling tests/migrations/000004
// publication bundle catalog that owns the lease table this keeper fences,
// and this ticket's tests/migrations-leases/000006_publication_lease_
// checkpoints.sql -- through the real pg-store-02 runner, and asserts the
// ledger literally records all three rows. The sibling's own catalog tests
// keep loading tests/migrations alone, so this ticket's SQL lives beside it.
func (s *leaseService) migrate(t *testing.T) {
	t.Helper()
	embedded, err := storepostgres.EmbeddedMigrations()
	if err != nil {
		s.fail("embedded migrations", err)
	}
	var ledger []storepostgres.Migration
	for _, m := range embedded {
		if m.Version == 1 && m.Name == "ledger" {
			ledger = append(ledger, m)
		}
	}
	if len(ledger) != 1 {
		t.Fatalf("embedded history has %d migrations, want exactly one version 1 ledger bootstrap to compose against", len(embedded))
	}
	catalog, err := storepostgres.LoadMigrations(os.DirFS("../../../../"), "tests/migrations")
	if err != nil {
		s.fail("load tests/migrations", err)
	}
	if len(catalog) != 1 || catalog[0].Version != 4 || catalog[0].Name != "publication_bundle_catalog" {
		t.Fatalf("tests/migrations history = %+v, want exactly version 4 publication_bundle_catalog", catalog)
	}
	checkpoints, err := storepostgres.LoadMigrations(os.DirFS("../../../../"), "tests/migrations-leases")
	if err != nil {
		s.fail("load tests/migrations-leases", err)
	}
	if len(checkpoints) != 1 || checkpoints[0].Version != 6 || checkpoints[0].Name != "publication_lease_checkpoints" {
		t.Fatalf("tests/migrations-leases history = %+v, want exactly version 6 publication_lease_checkpoints", checkpoints)
	}
	history := append(append(append([]storepostgres.Migration(nil), ledger...), catalog...), checkpoints...)
	runner, err := storepostgres.NewMigrationRunner(s.iso, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate checkpoint history", err)
	}
	if result.LatestVersion != 6 {
		t.Fatalf("latest migration version = %d, want 6", result.LatestVersion)
	}
	wantLedger := []storepostgres.LedgerRecord{
		{Version: 1, Name: "ledger", Checksum: ledger[0].Checksum},
		{Version: 4, Name: "publication_bundle_catalog", Checksum: catalog[0].Checksum},
		{Version: 6, Name: "publication_lease_checkpoints", Checksum: checkpoints[0].Checksum},
	}
	if len(result.Applied) != len(wantLedger) {
		t.Fatalf("applied history = %+v, want %+v", result.Applied, wantLedger)
	}
	for i, want := range wantLedger {
		got := result.Applied[i]
		if got.Version != want.Version || got.Name != want.Name || got.Checksum != want.Checksum {
			t.Fatalf("applied[%d] = {Version:%d Name:%q Checksum:%q}, want {Version:%d Name:%q Checksum:%q}",
				i, got.Version, got.Name, got.Checksum, want.Version, want.Name, want.Checksum)
		}
	}
}

// ---------------------------------------------------------------------------
// Service helpers (literal-row witnesses over both pools).
// ---------------------------------------------------------------------------

func (s *leaseService) mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(s.ctx, sql, args...); err != nil {
		s.fail("exec "+sql, err)
	}
}

func (s *leaseService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.isoB.QueryRow(s.ctx, sql, args...).Scan(&n); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

func (s *leaseService) mustWitnessText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := s.isoB.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *leaseService) mustWitnessInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := s.isoB.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *leaseService) mustWitnessTime(t *testing.T, sql string, args ...any) time.Time {
	t.Helper()
	var value time.Time
	if err := s.isoB.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *leaseService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

// acquire claims a keeper lease or dies, returning the generation token.
func (s *leaseService) acquire(t *testing.T, k *FencedLeases, key, owner string, expires time.Time) int64 {
	t.Helper()
	generation, held, err := k.AcquireLease(s.ctx, key, owner, expires)
	if err != nil || !held {
		s.fail(fmt.Sprintf("acquire lease %s/%s", key, owner), fmt.Errorf("held=%v generation=%d err=%w", held, generation, err))
	}
	return generation
}

// checkpoint writes a checkpoint or dies, returning its sequence.
func (s *leaseService) checkpoint(t *testing.T, k *FencedLeases, key, owner string, generation int64, payload any) int64 {
	t.Helper()
	sequence, err := k.Checkpoint(s.ctx, key, owner, generation, payload)
	if err != nil {
		s.fail(fmt.Sprintf("checkpoint %s/%s/gen%d", key, owner, generation), err)
	}
	return sequence
}

// leaseRow reads the literal (owner, epoch) of one lease key.
func (s *leaseService) leaseRow(t *testing.T, key string) (string, int64) {
	t.Helper()
	var owner string
	var epoch int64
	if err := s.isoB.QueryRow(s.ctx,
		"SELECT owner_id, epoch FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", key).
		Scan(&owner, &epoch); err != nil {
		s.fail("witness lease "+key, err)
	}
	return owner, epoch
}

// checkpointCount counts stored checkpoint rows for one key.
func (s *leaseService) checkpointCount(t *testing.T, key string) int {
	t.Helper()
	return s.execCount(t, "SELECT count(*) FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1", key)
}

func futureExpiry() time.Time { return time.Now().UTC().Add(time.Hour) }
func pastExpiry() time.Time   { return time.Now().UTC().Add(-time.Hour) }

// ---------------------------------------------------------------------------
// Construction contract (no server needed).
// ---------------------------------------------------------------------------

func TestNewLeaseKeeperRejectsNilPoolAndUnsafeSchemas(t *testing.T) {
	if _, err := NewLeaseKeeper(nil, "ok_schema"); err == nil {
		t.Fatal("NewLeaseKeeper(nil pool) = nil error, want rejection")
	}
	for _, schema := range []string{"", "Public", "a;drop", "0starts_with_digit", "has space", "tab\tname", strings.Repeat("a", 64)} {
		if _, err := NewLeaseKeeper(neverUsedPool{}, schema); !errors.Is(err, ErrInvalidSchema) {
			t.Fatalf("NewLeaseKeeper(schema %q) = %v, want ErrInvalidSchema", schema, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Acceptance 1: every checkpoint compares owner and unexpired lease in the
// write SQL itself.
// ---------------------------------------------------------------------------

func TestCheckpointWriteSQLComparesOwnerAndUnexpiredLease(t *testing.T) {
	s := newLeaseService(t)

	generation := s.acquire(t, s.keeperA, "exec-cp", "writer-a", futureExpiry())
	if generation != 1 {
		t.Fatalf("first acquire generation = %d, want 1", generation)
	}
	sequence := s.checkpoint(t, s.keeperA, "exec-cp", "writer-a", 1, map[string]any{"stage": "queued"})
	if sequence != 1 {
		t.Fatalf("first checkpoint sequence = %d, want 1", sequence)
	}

	// Literal stored row, read on the second physical connection.
	var (
		gotKey, gotOwner           string
		gotSequence, gotGeneration int64
	)
	if err := s.isoB.QueryRow(s.ctx,
		"SELECT key, sequence, owner_id, generation FROM "+s.qualified(ExecutionCheckpointsTable)+
			" WHERE key = $1 AND sequence = $2", "exec-cp", 1).
		Scan(&gotKey, &gotSequence, &gotOwner, &gotGeneration); err != nil {
		s.fail("witness checkpoint row", err)
	}
	if gotKey != "exec-cp" || gotSequence != 1 || gotOwner != "writer-a" || gotGeneration != 1 {
		t.Fatalf("checkpoint row = {%s %d %s %d}, want {exec-cp 1 writer-a 1}",
			gotKey, gotSequence, gotOwner, gotGeneration)
	}
	if stage := s.mustWitnessText(t,
		"SELECT payload->>'stage' FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = $2",
		"exec-cp", 1); stage != "queued" {
		t.Fatalf("stored payload stage = %q, want queued", stage)
	}

	// The write refuses a foreign owner on a live fence.
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-b", 1, map[string]any{"stage": "steal"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("foreign-owner checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	// The write refuses a stale generation of the correct owner.
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-a", 2, map[string]any{"stage": "stale"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("stale-generation checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	// The write refuses a non-positive token without reaching the server.
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-a", 0, map[string]any{"stage": "zero"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("zero-generation checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	if n := s.checkpointCount(t, "exec-cp"); n != 1 {
		t.Fatalf("checkpoint rows after refused writes = %d, want 1 (refusals wrote nothing)", n)
	}

	// The unexpired comparison lives inside the write: this lease key's row
	// carries the exact owner and generation the checkpoint presents, and is
	// refused solely because expires_at < the call instant.
	generation = s.acquire(t, s.keeperA, "exec-cp-expired", "writer-a", pastExpiry())
	if owner, epoch := s.leaseRow(t, "exec-cp-expired"); owner != "writer-a" || epoch != generation {
		t.Fatalf("expired-at-birth lease = {%s %d}, want {writer-a %d}", owner, epoch, generation)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp-expired", "writer-a", generation,
		map[string]any{"stage": "zombie"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("checkpoint under expired lease with matching owner+generation = %v, want ErrBundleLeaseLost", err)
	}
	if n := s.checkpointCount(t, "exec-cp-expired"); n != 0 {
		t.Fatalf("checkpoint rows under expired lease = %d, want 0", n)
	}
	if owner, epoch := s.leaseRow(t, "exec-cp-expired"); owner != "writer-a" || epoch != generation {
		t.Fatalf("refused checkpoint mutated the lease = {%s %d}, want {writer-a %d} untouched", owner, epoch, generation)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2a: an expired lease cannot be renewed or resurrected by its
// old owner.
// ---------------------------------------------------------------------------

func TestExpiredLeaseCannotBeRenewedOrResurrectedByOldOwner(t *testing.T) {
	s := newLeaseService(t)

	// A lease born expired: the row exists with the old owner, but every
	// guard sees it as expired.
	generation := s.acquire(t, s.keeperA, "exec-rn", "owner-a", pastExpiry())
	storedExpiry := s.mustWitnessTime(t,
		"SELECT expires_at FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", "exec-rn")
	if !storedExpiry.Before(time.Now().UTC()) {
		t.Fatalf("born-expired lease expires_at = %v, want strictly in the past", storedExpiry)
	}

	// The old owner, even presenting the exact generation, can never renew
	// an expired lease.
	if renewed, err := s.keeperA.RenewLease(s.ctx, "exec-rn", "owner-a", generation, futureExpiry()); err != nil || renewed {
		t.Fatalf("old-owner renew of expired lease = %v, %v; want false, nil", renewed, err)
	}
	if after := s.mustWitnessTime(t,
		"SELECT expires_at FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", "exec-rn"); !after.Equal(storedExpiry) {
		t.Fatalf("expired lease expires_at after refused renew = %v, want the literal stored %v", after, storedExpiry)
	}
	if _, epoch := s.leaseRow(t, "exec-rn"); epoch != generation {
		t.Fatalf("refused renew bumped epoch = %d, want %d", epoch, generation)
	}

	// A different owner takes the expired key over.
	takeover, held, err := s.keeperB.AcquireLease(s.ctx, "exec-rn", "owner-b", futureExpiry())
	if err != nil || !held {
		s.fail("takeover of expired lease", fmt.Errorf("held=%v err=%w", held, err))
	}
	if takeover != generation+1 {
		t.Fatalf("takeover returned generation %d, want %d", takeover, generation+1)
	}
	if owner, epoch := s.leaseRow(t, "exec-rn"); owner != "owner-b" || epoch != generation+1 {
		t.Fatalf("after takeover lease = {%s %d}, want {owner-b %d}", owner, epoch, generation+1)
	}

	// The old owner can no longer resurrect the key while the new owner's
	// lease is live.
	if _, held, err := s.keeperA.AcquireLease(s.ctx, "exec-rn", "owner-a", futureExpiry()); err != nil || held {
		t.Fatalf("old-owner re-acquire against live new owner = held %v err %v; want false, nil", held, err)
	}
	if owner, epoch := s.leaseRow(t, "exec-rn"); owner != "owner-b" || epoch != generation+1 {
		t.Fatalf("after refused resurrection lease = {%s %d}, want the untouched {owner-b %d}", owner, epoch, generation+1)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 2b: release cannot clear the new owner, and expire is the
// owner-and-generation-conditional yield.
// ---------------------------------------------------------------------------

func TestReleaseCannotClearNewOwner(t *testing.T) {
	s := newLeaseService(t)

	generation := s.acquire(t, s.keeperA, "exec-rel", "owner-a", futureExpiry())
	s.checkpoint(t, s.keeperA, "exec-rel", "owner-a", generation, map[string]any{"stage": "loading"})

	// The exact owner yields gracefully.
	expired, err := s.keeperA.ExpireLease(s.ctx, "exec-rel", "owner-a", generation)
	if err != nil || !expired {
		s.fail("owner expire", fmt.Errorf("expired=%v err=%w", expired, err))
	}
	// The yield is absolute for that grant: the same token cannot renew or
	// checkpoint afterwards, even microseconds later.
	if renewed, err := s.keeperA.RenewLease(s.ctx, "exec-rel", "owner-a", generation, futureExpiry()); err != nil || renewed {
		t.Fatalf("renew after own expire = %v, %v; want false, nil", renewed, err)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-rel", "owner-a", generation, map[string]any{"stage": "late"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("checkpoint after own expire = %v, want ErrBundleLeaseLost", err)
	}

	// The successor takes over and works.
	newGeneration := s.acquire(t, s.keeperB, "exec-rel", "owner-b", futureExpiry())
	if newGeneration != generation+1 {
		t.Fatalf("takeover generation = %d, want %d", newGeneration, generation+1)
	}
	s.checkpoint(t, s.keeperB, "exec-rel", "owner-b", newGeneration, map[string]any{"stage": "reconciling"})

	// The old owner's release cannot clear the new owner's fence.
	if released, err := s.keeperA.ReleaseLease(s.ctx, "exec-rel", "owner-a", generation); err != nil || released {
		t.Fatalf("stale-owner release = %v, %v; want false, nil", released, err)
	}
	// Even the correct owner at a stale generation cannot clear its own row.
	if released, err := s.keeperB.ReleaseLease(s.ctx, "exec-rel", "owner-b", generation); err != nil || released {
		t.Fatalf("same-owner stale-generation release = %v, %v; want false, nil", released, err)
	}
	if owner, epoch := s.leaseRow(t, "exec-rel"); owner != "owner-b" || epoch != newGeneration {
		t.Fatalf("after refused releases lease = {%s %d}, want the untouched {owner-b %d}", owner, epoch, newGeneration)
	}
	// The new owner still fences its own checkpoints.
	s.checkpoint(t, s.keeperB, "exec-rel", "owner-b", newGeneration, map[string]any{"stage": "still-mine"})

	// The exact owner at the exact generation releases.
	if released, err := s.keeperB.ReleaseLease(s.ctx, "exec-rel", "owner-b", newGeneration); err != nil || !released {
		t.Fatalf("owner release = %v, %v; want true, nil", released, err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", "exec-rel"); n != 0 {
		t.Fatalf("lease rows after owner release = %d, want 0", n)
	}
	if released, err := s.keeperB.ReleaseLease(s.ctx, "exec-rel", "owner-b", newGeneration); err != nil || released {
		t.Fatalf("double release = %v, %v; want false, nil", released, err)
	}
}

// ---------------------------------------------------------------------------
// Acceptance 3: two real PG connections prove a stale paused writer cannot
// checkpoint after takeover.
// ---------------------------------------------------------------------------

func TestStalePausedWriterCannotCheckpointAfterTakeoverTwoConnections(t *testing.T) {
	s := newLeaseService(t)

	// The two pools must be distinct physical backends, or the proof below
	// would be a session-local illusion.
	backendA := s.mustWitnessInt64On(t, s.iso, "SELECT pg_backend_pid()")
	backendB := s.mustWitnessInt64On(t, s.isoB, "SELECT pg_backend_pid()")
	if backendA == backendB {
		t.Fatalf("pools share backend pid %d, want two real PG connections", backendA)
	}

	// The execution owner checkpoints on connection A and then pauses: it
	// stops renewing, and its lease lapses. The lapse is forced
	// deterministically -- same-owner re-grant with a past expiry, the
	// sibling suite's expiry discipline -- executed on connection B.
	writerGeneration := s.acquire(t, s.keeperA, "exec-2c", "stale-writer", futureExpiry())
	s.checkpoint(t, s.keeperA, "exec-2c", "stale-writer", writerGeneration, map[string]any{"stage": "queued"})
	s.acquire(t, s.keeperB, "exec-2c", "stale-writer", pastExpiry())

	// The reconciler takes the lapsed lease over on connection B and
	// checkpoints its sweep progress.
	reconcilerGeneration := s.acquire(t, s.keeperB, "exec-2c", "reconciler", futureExpiry())
	if reconcilerGeneration != writerGeneration+2 {
		t.Fatalf("reconciler generation = %d, want %d (lapse re-grant +1, takeover +2)",
			reconcilerGeneration, writerGeneration+2)
	}
	s.checkpoint(t, s.keeperB, "exec-2c", "reconciler", reconcilerGeneration, map[string]any{"stage": "dropping-stale-outputs"})

	// The paused writer wakes on connection A still believing it owns
	// (stale-writer, generation 1). Every fenced operation must fail and
	// write nothing, on a different backend than the one that committed the
	// takeover -- no session-local fence can rescue it.
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-2c", "stale-writer", writerGeneration,
		map[string]any{"stage": "resurrect-me"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("stale paused writer checkpoint after takeover = %v, want ErrBundleLeaseLost", err)
	}
	if renewed, err := s.keeperA.RenewLease(s.ctx, "exec-2c", "stale-writer", writerGeneration, futureExpiry()); err != nil || renewed {
		t.Fatalf("stale paused writer renew = %v, %v; want false, nil", renewed, err)
	}
	if expired, err := s.keeperA.ExpireLease(s.ctx, "exec-2c", "stale-writer", writerGeneration); err != nil || expired {
		t.Fatalf("stale paused writer expire = %v, %v; want false, nil", expired, err)
	}
	if released, err := s.keeperA.ReleaseLease(s.ctx, "exec-2c", "stale-writer", writerGeneration); err != nil || released {
		t.Fatalf("stale paused writer release = %v, %v; want false, nil", released, err)
	}

	// The fence and the checkpoint table literally belong to the reconciler,
	// and the refused stale writes consumed nothing: the next reconciler
	// checkpoint continues the gap-free sequence (1, 2, 3).
	if owner, epoch := s.leaseRow(t, "exec-2c"); owner != "reconciler" || epoch != reconcilerGeneration {
		t.Fatalf("lease after takeover = {%s %d}, want the untouched {reconciler %d}", owner, epoch, reconcilerGeneration)
	}
	s.checkpoint(t, s.keeperB, "exec-2c", "reconciler", reconcilerGeneration, map[string]any{"stage": "finalizing"})
	if n := s.checkpointCount(t, "exec-2c"); n != 3 {
		t.Fatalf("checkpoint rows after stale-writer attempts = %d, want exactly 3", n)
	}
	// The deterministic lapse (same-owner re-grant at a past expiry, epoch
	// 2) and the takeover (epoch 3) leave the literal ownership history
	// pinned on every checkpoint row.
	for _, want := range []struct {
		sequence, generation int64
		owner                string
	}{
		{1, writerGeneration, "stale-writer"},
		{2, reconcilerGeneration, "reconciler"},
		{3, reconcilerGeneration, "reconciler"},
	} {
		owner := s.mustWitnessText(t,
			"SELECT owner_id FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = $2",
			"exec-2c", want.sequence)
		generation := s.mustWitnessInt64(t,
			"SELECT generation FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = $2",
			"exec-2c", want.sequence)
		if owner != want.owner || generation != want.generation {
			t.Fatalf("checkpoint %d = {%s gen %d}, want {%s gen %d}",
				want.sequence, owner, generation, want.owner, want.generation)
		}
	}

	// The reconciler's own resume read agrees, and a foreign read proves
	// the stale writer's attempts left no row under its retired token.
	latest, ok, err := s.keeperB.LatestCheckpoint(s.ctx, "exec-2c")
	if err != nil || !ok {
		s.fail("latest checkpoint", err)
	}
	if latest.Sequence != 3 || latest.OwnerID != "reconciler" || latest.Generation != reconcilerGeneration {
		t.Fatalf("latest checkpoint = %+v, want sequence 3 reconciler generation %d", latest, reconcilerGeneration)
	}
	if n := s.execCount(t,
		"SELECT count(*) FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND owner_id = $2 AND generation = $3",
		"exec-2c", "stale-writer", writerGeneration); n != 1 {
		t.Fatalf("checkpoints under the retired token = %d, want exactly 1 (the pre-pause one)", n)
	}
}

func (s *leaseService) mustWitnessInt64On(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := pool.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

// ---------------------------------------------------------------------------
// Generation pinning: repeated grants on one key carry monotonic tokens and
// every stale token is inert.
// ---------------------------------------------------------------------------

func TestGenerationPinningCorrelatesRepeatedOwnerGrants(t *testing.T) {
	s := newLeaseService(t)

	first := s.acquire(t, s.keeperA, "exec-gen", "owner-a", futureExpiry())
	second := s.acquire(t, s.keeperA, "exec-gen", "owner-a", futureExpiry())
	third := s.acquire(t, s.keeperA, "exec-gen", "owner-a", futureExpiry())
	if first != 1 || second != 2 || third != 3 {
		t.Fatalf("same-owner grant generations = %d/%d/%d, want 1/2/3", first, second, third)
	}
	if owner, epoch := s.leaseRow(t, "exec-gen"); owner != "owner-a" || epoch != 3 {
		t.Fatalf("lease after regrants = {%s %d}, want {owner-a 3}", owner, epoch)
	}

	// Only the current generation fences; every retired token is inert
	// against checkpoint, renew, expire, and release alike.
	s.checkpoint(t, s.keeperA, "exec-gen", "owner-a", 3, map[string]any{"stage": "current"})
	for _, stale := range []int64{1, 2} {
		if _, err := s.keeperA.Checkpoint(s.ctx, "exec-gen", "owner-a", stale, map[string]any{"stage": "stale"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
			t.Fatalf("checkpoint on retired generation %d = %v, want ErrBundleLeaseLost", stale, err)
		}
		if renewed, err := s.keeperA.RenewLease(s.ctx, "exec-gen", "owner-a", stale, futureExpiry()); err != nil || renewed {
			t.Fatalf("renew on retired generation %d = %v, %v; want false, nil", stale, renewed, err)
		}
		if expired, err := s.keeperA.ExpireLease(s.ctx, "exec-gen", "owner-a", stale); err != nil || expired {
			t.Fatalf("expire on retired generation %d = %v, %v; want false, nil", stale, expired, err)
		}
		if released, err := s.keeperA.ReleaseLease(s.ctx, "exec-gen", "owner-a", stale); err != nil || released {
			t.Fatalf("release on retired generation %d = %v, %v; want false, nil", stale, released, err)
		}
	}
	if owner, epoch := s.leaseRow(t, "exec-gen"); owner != "owner-a" || epoch != 3 {
		t.Fatalf("lease after inert stale tokens = {%s %d}, want the untouched {owner-a 3}", owner, epoch)
	}
	if n := s.checkpointCount(t, "exec-gen"); n != 1 {
		t.Fatalf("checkpoint rows = %d, want 1 (retired tokens wrote nothing)", n)
	}

	// The keeper fences the very same lease registry the sibling catalog's
	// SaveExecution reads: a keeper grant lets the execution owner save
	// metadata, and retiring the grant fences SaveExecution too.
	execution := baseExecution("exec-gen-save", "exec-gen", publication.BundleRunning,
		stamps(leaseCreated, leaseUpdated, time.Time{}, time.Time{}))
	if err := s.registry.SaveExecution(s.ctx, execution, "owner-a"); err != nil {
		s.fail("save under keeper grant", err)
	}
	if _, err := s.keeperA.ExpireLease(s.ctx, "exec-gen", "owner-a", 3); err != nil {
		s.fail("expire current grant", err)
	}
	if err := s.registry.SaveExecution(s.ctx, execution, "owner-a"); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution after keeper expire = %v, want ErrBundleLeaseLost (shared lease registry)", err)
	}
}

var (
	leaseCreated = baseTime.Add(3 * time.Hour)
	leaseUpdated = baseTime.Add(3*time.Hour + time.Minute)
)

// ---------------------------------------------------------------------------
// Fencing-token monotonicity across lease-row deletion: a generation handed
// out by a committed Acquire is never reissued, however the lease row died
// (ReleaseLease or the sibling Registry.ReleaseBundleLease alike), so a
// straggler carrying a retired token can never match a later grant -- even
// under the same owner id.
// ---------------------------------------------------------------------------

func TestReleasedGenerationIsNeverReissued(t *testing.T) {
	s := newLeaseService(t)
	const key = "exec-aba"

	first := s.acquire(t, s.keeperA, key, "reconciler", futureExpiry())
	if first != 1 {
		t.Fatalf("first grant = %d, want 1", first)
	}
	s.checkpoint(t, s.keeperA, key, "reconciler", first, map[string]any{"episode": 1})

	// The owner releases cleanly: the lease row is gone.
	if released, err := s.keeperA.ReleaseLease(s.ctx, key, "reconciler", first); err != nil || !released {
		s.fail("release first grant", fmt.Errorf("released=%v err=%w", released, err))
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", key); n != 0 {
		t.Fatalf("lease rows after release = %d, want 0", n)
	}

	// The SAME owner id re-acquires (a reconciler restart, e.g.). The token
	// must be strictly newer than the retired one, never a reissue.
	second := s.acquire(t, s.keeperA, key, "reconciler", futureExpiry())
	if second <= first {
		t.Fatalf("re-acquire after release reissued generation %d (retired %d); want strictly greater", second, first)
	}
	if second != 2 {
		t.Fatalf("re-acquire generation = %d, want 2 (high-water survives deletion)", second)
	}

	// The straggler still holding (reconciler, first) is inert against the
	// live grant in every fenced operation, and writes nothing.
	if _, err := s.keeperB.Checkpoint(s.ctx, key, "reconciler", first, map[string]any{"episode": "straggler"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("straggler checkpoint with re-issued-looking token = %v, want ErrBundleLeaseLost", err)
	}
	if renewed, err := s.keeperB.RenewLease(s.ctx, key, "reconciler", first, futureExpiry()); err != nil || renewed {
		t.Fatalf("straggler renew = %v, %v; want false, nil", renewed, err)
	}
	if expired, err := s.keeperB.ExpireLease(s.ctx, key, "reconciler", first); err != nil || expired {
		t.Fatalf("straggler expire = %v, %v; want false, nil", expired, err)
	}
	if released, err := s.keeperB.ReleaseLease(s.ctx, key, "reconciler", first); err != nil || released {
		t.Fatalf("straggler release = %v, %v; want false, nil", released, err)
	}
	if owner, epoch := s.leaseRow(t, key); owner != "reconciler" || epoch != second {
		t.Fatalf("lease after straggler attempts = {%s %d}, want the untouched {reconciler %d}", owner, epoch, second)
	}
	// The audit table keeps the episodes distinct: exactly one row, under
	// the retired generation, and nothing under it can be added again.
	if n := s.checkpointCount(t, key); n != 1 {
		t.Fatalf("checkpoint rows = %d, want 1 (straggler wrote nothing)", n)
	}
	if gen := s.mustWitnessInt64(t,
		"SELECT generation FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = 1", key); gen != first {
		t.Fatalf("episode-1 row generation = %d, want %d", gen, first)
	}

	// A second full cycle, this time deleted by the SIBLING registry path,
	// must also be unable to reopen the high-water.
	if err := s.registry.ReleaseBundleLease(s.ctx, key, "reconciler"); err != nil {
		s.fail("sibling release", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", key); n != 0 {
		t.Fatalf("lease rows after sibling release = %d, want 0", n)
	}
	third := s.acquire(t, s.keeperA, key, "reconciler", futureExpiry())
	if third <= second {
		t.Fatalf("re-acquire after sibling release reissued generation %d (retired %d); want strictly greater", third, second)
	}
	if third != 3 {
		t.Fatalf("third grant generation = %d, want 3", third)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, key, "reconciler", second, map[string]any{"episode": "late-second"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("checkpoint on retired second token = %v, want ErrBundleLeaseLost", err)
	}
	sequence := s.checkpoint(t, s.keeperA, key, "reconciler", third, map[string]any{"episode": 3})
	if sequence != 2 {
		t.Fatalf("episode-3 checkpoint sequence = %d, want 2", sequence)
	}
	if gen := s.mustWitnessInt64(t,
		"SELECT generation FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = $2", key, 2); gen != third {
		t.Fatalf("episode-3 row generation = %d, want %d", gen, third)
	}

	// Failed acquires burn no generations: a refused claim rolls its
	// high-water bump back with its transaction.
	if _, held, err := s.keeperB.AcquireLease(s.ctx, key, "challenger", futureExpiry()); err != nil || held {
		t.Fatalf("challenger acquire against live owner = held %v err %v; want false, nil", held, err)
	}
	if owner, epoch := s.leaseRow(t, key); owner != "reconciler" || epoch != third {
		t.Fatalf("lease after refused challenger = {%s %d}, want the untouched {reconciler %d}", owner, epoch, third)
	}
	if hw := s.mustWitnessInt64(t,
		"SELECT last_generation FROM "+s.qualified(LeaseGrantsTable)+" WHERE key = $1", key); hw != third {
		t.Fatalf("high-water after refused acquire = %d, want %d (bump rolled back, not burned)", hw, third)
	}
}

// ---------------------------------------------------------------------------
// Payload discipline: missing versus JSON-null stays distinct inside jsonb,
// and malformed payloads never reach the table.
// ---------------------------------------------------------------------------

func TestCheckpointPayloadPreservesMissingVersusJSONNull(t *testing.T) {
	s := newLeaseService(t)
	generation := s.acquire(t, s.keeperA, "exec-null", "writer-a", futureExpiry())

	sequence, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, map[string]any{
		"stage": "loading",
		"note":  nil,
	})
	if err != nil || sequence != 1 {
		s.fail("null-bearing checkpoint", fmt.Errorf("sequence=%d err=%w", sequence, err))
	}

	// "note" was present-and-null; "absent" was missing. The distinction
	// survives in the stored jsonb row itself.
	if present := s.mustWitnessBool(t,
		"SELECT payload ? 'note' FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = 1",
		"exec-null"); !present {
		t.Fatal("stored payload lost the explicit null member")
	}
	if kind := s.mustWitnessText(t,
		"SELECT jsonb_typeof(payload->'note') FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = 1",
		"exec-null"); kind != "null" {
		t.Fatalf("stored note type = %q, want null", kind)
	}
	if present := s.mustWitnessBool(t,
		"SELECT payload ? 'absent' FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = 1",
		"exec-null"); present {
		t.Fatal("stored payload gained a member that was never sent")
	}

	record, ok, err := s.keeperA.LatestCheckpoint(s.ctx, "exec-null")
	if err != nil || !ok {
		s.fail("latest checkpoint", err)
	}
	if record.Sequence != 1 || record.OwnerID != "writer-a" || record.Generation != generation || record.CheckpointedAt.IsZero() {
		t.Fatalf("latest checkpoint record = %+v, want sequence 1 writer-a generation %d with a stored stamp",
			record, generation)
	}
	// The returned bytes are PostgreSQL's canonical jsonb text; the explicit
	// null must still be a null member, not dropped and not stringified.
	raw := string(record.Payload)
	if !regexp.MustCompile(`"note"\s*:\s*null`).MatchString(raw) ||
		!regexp.MustCompile(`"stage"\s*:\s*"loading"`).MatchString(raw) {
		t.Fatalf("returned payload bytes = %s, want the stored jsonb text with the null preserved", raw)
	}

	// A nil payload is refused at the boundary: no row, no sequence burn.
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, nil); err == nil {
		t.Fatal("nil payload = nil error, want rejection")
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, any(nil)); err == nil {
		t.Fatal("typed nil payload = nil error, want rejection")
	}

	// Every non-object encoding is refused cleanly at the Go boundary -- the
	// promised "must be a JSON object" message, never a wrapped 23514 -- with
	// no stored row and no burned sequence.
	for name, nonObject := range map[string]any{
		"number": 42,
		"string": "just text",
		"array":  []any{1, 2},
		"bool":   true,
		"null":   json.RawMessage("null"),
	} {
		_, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, nonObject)
		if err == nil {
			t.Fatalf("payload %s = nil error, want rejection", name)
		}
		if !strings.Contains(err.Error(), "must be a JSON object") {
			t.Fatalf("payload %s error = %v, want the promised must-be-a-JSON-object message", name, err)
		}
		if strings.Contains(err.Error(), "23514") {
			t.Fatalf("payload %s surfaced a raw CHECK violation: %v", name, err)
		}
	}
	if n := s.checkpointCount(t, "exec-null"); n != 1 {
		t.Fatalf("checkpoint rows after refused payloads = %d, want 1", n)
	}
	// No burn: the next valid checkpoint is sequence 2, gap-free.
	sequence, err = s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, map[string]any{"stage": "next"})
	if err != nil || sequence != 2 {
		s.fail("post-refusal checkpoint", fmt.Errorf("sequence=%d err=%w", sequence, err))
	}
	if n := s.checkpointCount(t, "exec-null"); n != 2 {
		t.Fatalf("checkpoint rows = %d, want 2", n)
	}
}

func (s *leaseService) mustWitnessBool(t *testing.T, sql string, args ...any) bool {
	t.Helper()
	var value bool
	if err := s.isoB.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

// ---------------------------------------------------------------------------
// Acquisition guards and the no-fallback guarantee.
// ---------------------------------------------------------------------------

func TestAcquireGuardsAndNoFallback(t *testing.T) {
	s := newLeaseService(t)

	// Empty key or owner is a caller bug refused before the shared lease
	// table is touched.
	if _, held, err := s.keeperA.AcquireLease(s.ctx, "", "owner-a", futureExpiry()); err == nil || !errors.Is(err, ErrInvalidLeaseKey) || held {
		t.Fatalf("AcquireLease(empty key) = held %v err %v, want ErrInvalidLeaseKey", held, err)
	}
	if _, held, err := s.keeperA.AcquireLease(s.ctx, "exec-guard", "", futureExpiry()); err == nil || !errors.Is(err, ErrInvalidLeaseKey) || held {
		t.Fatalf("AcquireLease(empty owner) = held %v err %v, want ErrInvalidLeaseKey", held, err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key IN ($1, $2)", "", "exec-guard"); n != 0 {
		t.Fatalf("lease rows after guarded acquires = %d, want 0", n)
	}

	// A keeper bound to a schema without the lease and checkpoint tables
	// surfaces the missing relation; it never falls back to another schema,
	// another database, or a no-op success.
	emptySchema := fmt.Sprintf("pgleases01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgleases01ResourceNamePattern.MatchString(emptySchema) {
		t.Fatalf("owned schema name %q does not match the required pattern", emptySchema)
	}
	s.mustExec(t, s.iso, `CREATE SCHEMA "`+emptySchema+`"`)
	t.Cleanup(func() {
		if _, err := s.iso.Exec(context.WithoutCancel(s.ctx), `DROP SCHEMA IF EXISTS "`+emptySchema+`" CASCADE`); err != nil {
			t.Errorf("drop empty schema: %s", s.scrub(err))
		}
	})
	orphan, err := NewLeaseKeeper(s.isoB, emptySchema)
	if err != nil {
		s.fail("build orphan keeper", err)
	}
	_, _, err = orphan.AcquireLease(s.ctx, "exec-missing", "owner-a", futureExpiry())
	if err == nil {
		t.Fatal("acquire against unmigrated schema = nil error, want the missing relation surfaced")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("acquire against unmigrated schema = %v, want SQLSTATE 42P01 undefined_table", s.scrub(err))
	}
}

// ---------------------------------------------------------------------------
// Schema composition: 000006 adds exactly the checkpoint records table,
// metadata-only, beside the sibling's catalog tables.
// ---------------------------------------------------------------------------

func TestCheckpointSchemaCompositionIsMetadataOnly(t *testing.T) {
	s := newLeaseService(t)
	rows, err := s.isoB.Query(s.ctx,
		"SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = $1 ORDER BY tablename", s.schema)
	if err != nil {
		s.fail("list tables", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			s.fail("scan table", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		s.fail("list tables", err)
	}
	rows.Close()
	want := []string{
		"publication_bundle_executions",
		"publication_bundle_leases",
		"publication_bundle_pointers",
		"publication_execution_checkpoints",
		"publication_lease_grants",
		"schema_migrations",
	}
	if strings.Join(tables, ",") != strings.Join(want, ",") {
		t.Fatalf("schema tables = %#v, want exactly %#v (metadata only; no row-store tables)", tables, want)
	}

	// Literal column contract of this ticket's table.
	type column struct {
		name, typ string
		nullable  bool
	}
	rows, err = s.isoB.Query(s.ctx,
		"SELECT column_name, data_type, is_nullable = 'YES' FROM information_schema.columns"+
			" WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position",
		s.schema, ExecutionCheckpointsTable)
	if err != nil {
		s.fail("list columns", err)
	}
	var columns []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.name, &c.typ, &c.nullable); err != nil {
			s.fail("scan column", err)
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		s.fail("list columns", err)
	}
	rows.Close()
	wantColumns := []column{
		{"key", "text", false},
		{"sequence", "bigint", false},
		{"owner_id", "text", false},
		{"generation", "bigint", false},
		{"checkpointed_at", "timestamp with time zone", false},
		{"payload", "jsonb", false},
	}
	if len(columns) != len(wantColumns) {
		t.Fatalf("columns = %+v, want %+v", columns, wantColumns)
	}
	for i, wantCol := range wantColumns {
		if columns[i] != wantCol {
			t.Fatalf("column %d = %+v, want %+v", i, columns[i], wantCol)
		}
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit: no pgleases01_it_* database or schema may survive.
// This test MUST stay the last test declared in this file: Go runs a
// package's tests in declaration order within each source file, and only
// tests in this file create pgleases01_it_* resources.
// ---------------------------------------------------------------------------

func TestZeroLeftoverPgleasesResources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgleases01_it_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d pgleases01_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgleases01_it_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d pgleases01_it_* schemas still exist in the service database", schemas)
	}
}
