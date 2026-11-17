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

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/publication"
)

var pgleases01ResourceNamePattern = regexp.MustCompile(`^pgleases01_it_[0-9]+_[0-9a-f]+$`)

type leaseService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	isoB     *pgxpool.Pool
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

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

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

func newOwnedPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = 1
	return pgxpool.NewWithConfig(ctx, config)
}

func (s *leaseService) migrate(t *testing.T) {
	t.Helper()
	history := mustOwnerHistorySubset(t, 1, 4, 6)
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
		{Version: 1, Name: "ledger", Checksum: "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
		{Version: 4, Name: "publication_bundle_catalog", Checksum: "adcb4683ffd1611b9c68e7a49aaa4b8670d106d65df927cbb8e11866be224943"},
		{Version: 6, Name: "publication_lease_checkpoints", Checksum: "5a2fc578d1b24ee048275b060856d23b7e1a35ff251519bfb46bbc4c1e8a917c"},
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

func (s *leaseService) acquire(t *testing.T, k *FencedLeases, key, owner string, expires time.Time) int64 {
	t.Helper()
	generation, held, err := k.AcquireLease(s.ctx, key, owner, expires)
	if err != nil || !held {
		s.fail(fmt.Sprintf("acquire lease %s/%s", key, owner), fmt.Errorf("held=%v generation=%d err=%w", held, generation, err))
	}
	return generation
}

func (s *leaseService) checkpoint(t *testing.T, k *FencedLeases, key, owner string, generation int64, payload any) int64 {
	t.Helper()
	sequence, err := k.Checkpoint(s.ctx, key, owner, generation, payload)
	if err != nil {
		s.fail(fmt.Sprintf("checkpoint %s/%s/gen%d", key, owner, generation), err)
	}
	return sequence
}

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

func (s *leaseService) checkpointCount(t *testing.T, key string) int {
	t.Helper()
	return s.execCount(t, "SELECT count(*) FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1", key)
}

func futureExpiry() time.Time { return time.Now().UTC().Add(time.Hour) }
func pastExpiry() time.Time   { return time.Now().UTC().Add(-time.Hour) }

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

	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-b", 1, map[string]any{"stage": "steal"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("foreign-owner checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-a", 2, map[string]any{"stage": "stale"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("stale-generation checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-cp", "writer-a", 0, map[string]any{"stage": "zero"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("zero-generation checkpoint = %v, want ErrBundleLeaseLost", err)
	}
	if n := s.checkpointCount(t, "exec-cp"); n != 1 {
		t.Fatalf("checkpoint rows after refused writes = %d, want 1 (refusals wrote nothing)", n)
	}

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

func TestExpiredLeaseCannotBeRenewedOrResurrectedByOldOwner(t *testing.T) {
	s := newLeaseService(t)

	generation := s.acquire(t, s.keeperA, "exec-rn", "owner-a", pastExpiry())
	storedExpiry := s.mustWitnessTime(t,
		"SELECT expires_at FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", "exec-rn")
	if !storedExpiry.Before(time.Now().UTC()) {
		t.Fatalf("born-expired lease expires_at = %v, want strictly in the past", storedExpiry)
	}

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

	if _, held, err := s.keeperA.AcquireLease(s.ctx, "exec-rn", "owner-a", futureExpiry()); err != nil || held {
		t.Fatalf("old-owner re-acquire against live new owner = held %v err %v; want false, nil", held, err)
	}
	if owner, epoch := s.leaseRow(t, "exec-rn"); owner != "owner-b" || epoch != generation+1 {
		t.Fatalf("after refused resurrection lease = {%s %d}, want the untouched {owner-b %d}", owner, epoch, generation+1)
	}
}

func TestReleaseCannotClearNewOwner(t *testing.T) {
	s := newLeaseService(t)

	generation := s.acquire(t, s.keeperA, "exec-rel", "owner-a", futureExpiry())
	s.checkpoint(t, s.keeperA, "exec-rel", "owner-a", generation, map[string]any{"stage": "loading"})

	expired, err := s.keeperA.ExpireLease(s.ctx, "exec-rel", "owner-a", generation)
	if err != nil || !expired {
		s.fail("owner expire", fmt.Errorf("expired=%v err=%w", expired, err))
	}
	if renewed, err := s.keeperA.RenewLease(s.ctx, "exec-rel", "owner-a", generation, futureExpiry()); err != nil || renewed {
		t.Fatalf("renew after own expire = %v, %v; want false, nil", renewed, err)
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-rel", "owner-a", generation, map[string]any{"stage": "late"}); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("checkpoint after own expire = %v, want ErrBundleLeaseLost", err)
	}

	newGeneration := s.acquire(t, s.keeperB, "exec-rel", "owner-b", futureExpiry())
	if newGeneration != generation+1 {
		t.Fatalf("takeover generation = %d, want %d", newGeneration, generation+1)
	}
	s.checkpoint(t, s.keeperB, "exec-rel", "owner-b", newGeneration, map[string]any{"stage": "reconciling"})

	if released, err := s.keeperA.ReleaseLease(s.ctx, "exec-rel", "owner-a", generation); err != nil || released {
		t.Fatalf("stale-owner release = %v, %v; want false, nil", released, err)
	}
	if released, err := s.keeperB.ReleaseLease(s.ctx, "exec-rel", "owner-b", generation); err != nil || released {
		t.Fatalf("same-owner stale-generation release = %v, %v; want false, nil", released, err)
	}
	if owner, epoch := s.leaseRow(t, "exec-rel"); owner != "owner-b" || epoch != newGeneration {
		t.Fatalf("after refused releases lease = {%s %d}, want the untouched {owner-b %d}", owner, epoch, newGeneration)
	}
	s.checkpoint(t, s.keeperB, "exec-rel", "owner-b", newGeneration, map[string]any{"stage": "still-mine"})

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

func TestStalePausedWriterCannotCheckpointAfterTakeoverTwoConnections(t *testing.T) {
	s := newLeaseService(t)

	backendA := s.mustWitnessInt64On(t, s.iso, "SELECT pg_backend_pid()")
	backendB := s.mustWitnessInt64On(t, s.isoB, "SELECT pg_backend_pid()")
	if backendA == backendB {
		t.Fatalf("pools share backend pid %d, want two real PG connections", backendA)
	}

	writerGeneration := s.acquire(t, s.keeperA, "exec-2c", "stale-writer", futureExpiry())
	s.checkpoint(t, s.keeperA, "exec-2c", "stale-writer", writerGeneration, map[string]any{"stage": "queued"})
	s.acquire(t, s.keeperB, "exec-2c", "stale-writer", pastExpiry())

	reconcilerGeneration := s.acquire(t, s.keeperB, "exec-2c", "reconciler", futureExpiry())
	if reconcilerGeneration != writerGeneration+2 {
		t.Fatalf("reconciler generation = %d, want %d (lapse re-grant +1, takeover +2)",
			reconcilerGeneration, writerGeneration+2)
	}
	s.checkpoint(t, s.keeperB, "exec-2c", "reconciler", reconcilerGeneration, map[string]any{"stage": "dropping-stale-outputs"})

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

	if owner, epoch := s.leaseRow(t, "exec-2c"); owner != "reconciler" || epoch != reconcilerGeneration {
		t.Fatalf("lease after takeover = {%s %d}, want the untouched {reconciler %d}", owner, epoch, reconcilerGeneration)
	}
	s.checkpoint(t, s.keeperB, "exec-2c", "reconciler", reconcilerGeneration, map[string]any{"stage": "finalizing"})
	if n := s.checkpointCount(t, "exec-2c"); n != 3 {
		t.Fatalf("checkpoint rows after stale-writer attempts = %d, want exactly 3", n)
	}
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

func TestReleasedGenerationIsNeverReissued(t *testing.T) {
	s := newLeaseService(t)
	const key = "exec-aba"

	first := s.acquire(t, s.keeperA, key, "reconciler", futureExpiry())
	if first != 1 {
		t.Fatalf("first grant = %d, want 1", first)
	}
	s.checkpoint(t, s.keeperA, key, "reconciler", first, map[string]any{"episode": 1})

	if released, err := s.keeperA.ReleaseLease(s.ctx, key, "reconciler", first); err != nil || !released {
		s.fail("release first grant", fmt.Errorf("released=%v err=%w", released, err))
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key = $1", key); n != 0 {
		t.Fatalf("lease rows after release = %d, want 0", n)
	}

	second := s.acquire(t, s.keeperA, key, "reconciler", futureExpiry())
	if second <= first {
		t.Fatalf("re-acquire after release reissued generation %d (retired %d); want strictly greater", second, first)
	}
	if second != 2 {
		t.Fatalf("re-acquire generation = %d, want 2 (high-water survives deletion)", second)
	}

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
	if n := s.checkpointCount(t, key); n != 1 {
		t.Fatalf("checkpoint rows = %d, want 1 (straggler wrote nothing)", n)
	}
	if gen := s.mustWitnessInt64(t,
		"SELECT generation FROM "+s.qualified(ExecutionCheckpointsTable)+" WHERE key = $1 AND sequence = 1", key); gen != first {
		t.Fatalf("episode-1 row generation = %d, want %d", gen, first)
	}

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
	raw := string(record.Payload)
	if !regexp.MustCompile(`"note"\s*:\s*null`).MatchString(raw) ||
		!regexp.MustCompile(`"stage"\s*:\s*"loading"`).MatchString(raw) {
		t.Fatalf("returned payload bytes = %s, want the stored jsonb text with the null preserved", raw)
	}

	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, nil); err == nil {
		t.Fatal("nil payload = nil error, want rejection")
	}
	if _, err := s.keeperA.Checkpoint(s.ctx, "exec-null", "writer-a", generation, any(nil)); err == nil {
		t.Fatal("typed nil payload = nil error, want rejection")
	}

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

func TestAcquireGuardsAndNoFallback(t *testing.T) {
	s := newLeaseService(t)

	if _, held, err := s.keeperA.AcquireLease(s.ctx, "", "owner-a", futureExpiry()); err == nil || !errors.Is(err, ErrInvalidLeaseKey) || held {
		t.Fatalf("AcquireLease(empty key) = held %v err %v, want ErrInvalidLeaseKey", held, err)
	}
	if _, held, err := s.keeperA.AcquireLease(s.ctx, "exec-guard", "", futureExpiry()); err == nil || !errors.Is(err, ErrInvalidLeaseKey) || held {
		t.Fatalf("AcquireLease(empty owner) = held %v err %v, want ErrInvalidLeaseKey", held, err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleLeasesTable)+" WHERE key IN ($1, $2)", "", "exec-guard"); n != 0 {
		t.Fatalf("lease rows after guarded acquires = %d, want 0", n)
	}

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

func TestZeroLeftoverPgleasesResources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
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
