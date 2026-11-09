package postgres

// Real-PostgreSQL acceptance tests for the pg-catalog-03 relationship
// evidence (check-2: go test ./internal/catalog/postgres -run Relationship
// -count=1).
//
// Every live claim here drives the real store-05 fhir_edge_occurrence table
// on the PostgreSQL 18 service named by LOOM_TEST_POSTGRES_URL: duplicate
// occurrences and cross-scope controls are seeded as literal rows,
// witnesses are literal rows read back by direct SQL, and the aggregation
// runs through the real RelationshipEvidenceRepository SQL and the real
// store-05 EdgeRepository write path. Nothing is mocked for a live claim.
//
// Concurrency surfaces (>= 10 rounds each, two real connections with
// distinct pg_backend_pid values): concurrent identical-occurrence inserts
// (exactly one winner, exact-count convergence) and an in-flight writer
// whose rows must stay invisible to the aggregation until commit, with
// byte-identical row witnesses before, during, and after abort.
//
// Guard non-vacuity: the claimed database guard is the named constraint
// fhir_edge_occurrence_identity_ux. It is live-DROPped: without it the
// duplicate insert is accepted and the aggregate provably double-counts;
// with it restored, the refusal names the exact constraint again.
//
// Fixture databases and schemas are uniquely owned as verify_pgcat03_* and
// dropped by the test that created them; TestZZ audits the owned namespace
// by exact name and against the process-start pg_database baseline.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const relationshipFixturePrefix = "verify_pgcat03_"

var relationshipFixtureNamePattern = regexp.MustCompile(`^verify_pgcat03_[0-9]+_[0-9a-f]+$`)

var (
	relationshipOwnedMu       sync.Mutex
	relationshipOwnedByName   = map[string]string{} // live owned database -> owned schema (cleanups delete)
	relationshipCreatedTotal  int                   // databases verified into existence by this process (never decremented)
	relationshipBaselineDBs   = -1                  // pg_database matches at process start; -1 = baseline unavailable
	relationshipBaselineError string
)

// TestMain records the pg_database baseline of the verify_pgcat03_*
// namespace before any test in this process creates one, so TestZZ can
// compare the live count against the baseline it witnessed itself.
func TestMain(m *testing.M) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if pool, err := pgxpool.New(ctx, raw); err == nil {
			var count int
			if err := pool.QueryRow(ctx,
				"SELECT COUNT(*) FROM pg_database WHERE datname ~ $1", relationshipFixtureNamePattern.String()).Scan(&count); err != nil {
				relationshipBaselineError = err.Error()
			} else {
				relationshipBaselineDBs = count
			}
			pool.Close()
		} else {
			relationshipBaselineError = err.Error()
		}
		cancel()
	}
	os.Exit(m.Run())
}

func relationshipTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func relationshipRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

type relationshipEvidenceService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

// newRelationshipEvidenceService owns one isolated database, one schema
// inside it, and the eight merged store migrations applied to that schema.
// Ownership begins at verified create and is recorded in the process
// registry that TestZZ audits.
func newRelationshipEvidenceService(t *testing.T) *relationshipEvidenceService {
	t.Helper()
	raw := relationshipTestPostgresURL(t)

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

	var dbName, schema string

	// Registered while the admin pool is open, so (LIFO) it runs after every
	// drop: an ownership-scoped zero-leftover audit for exactly the
	// resources this test created.
	t.Cleanup(func() {
		var dbCount int
		if err := admin.QueryRow(context.WithoutCancel(ctx),
			"SELECT COUNT(*) FROM pg_database WHERE datname = $1", dbName).Scan(&dbCount); err != nil {
			t.Errorf("audit isolated database %s: %s", dbName, scrub(err))
		} else if dbCount != 0 {
			t.Errorf("leftover isolated database %s after cleanup", dbName)
		}
		var schemaCount int
		if err := admin.QueryRow(context.WithoutCancel(ctx),
			"SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
			t.Errorf("audit owned schema %s: %s", schema, scrub(err))
		} else if schemaCount != 0 {
			t.Errorf("leftover owned schema %s in the service database after cleanup", schema)
		}
	})

	dbName = fmt.Sprintf("verify_pgcat03_%d_%s", time.Now().UnixNano(), relationshipRandomHex(t))
	if !relationshipFixtureNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	relationshipOwnedMu.Lock()
	relationshipOwnedByName[dbName] = ""
	relationshipCreatedTotal++
	relationshipOwnedMu.Unlock()
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
		relationshipOwnedMu.Lock()
		delete(relationshipOwnedByName, dbName)
		relationshipOwnedMu.Unlock()
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema = fmt.Sprintf("verify_pgcat03_%d_%s", time.Now().UnixNano(), relationshipRandomHex(t))
	if !relationshipFixtureNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	relationshipOwnedMu.Lock()
	relationshipOwnedByName[dbName] = schema
	relationshipOwnedMu.Unlock()
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	service := &relationshipEvidenceService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *relationshipEvidenceService) openClient(t *testing.T) *storepostgres.Client {
	t.Helper()
	client, err := storepostgres.Open(s.ctx, storepostgres.Config{
		DSN:            s.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

// openSingleConnPool opens a pool that can hold exactly one backend, so a
// race test can prove the two writers (or writer and reader) are distinct
// real PostgreSQL connections.
func (s *relationshipEvidenceService) openSingleConnPool(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(s.ctx, s.dsn)
	if err != nil {
		s.fail("open "+name, err)
	}
	pool.Close()
	cfg, err := pgxpool.ParseConfig(s.dsn)
	if err != nil {
		s.fail("parse "+name+" config", err)
	}
	cfg.MaxConns = 1
	pool, err = pgxpool.NewWithConfig(s.ctx, cfg)
	if err != nil {
		s.fail("open "+name, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func backendPID(t *testing.T, pool *pgxpool.Pool) int32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire pool for pid: %v", err)
	}
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatalf("read pg_backend_pid: %v", err)
	}
	return pid
}

func (s *relationshipEvidenceService) edgeTable() string {
	return pgx.Identifier{s.schema, "fhir_edge_occurrence"}.Sanitize()
}

func (s *relationshipEvidenceService) resourceTable() string {
	return pgx.Identifier{s.schema, "fhir_resource"}.Sanitize()
}

func (s *relationshipEvidenceService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *relationshipEvidenceService) seedResource(t *testing.T, project, generation, resourceType, logicalID, authPath string) {
	t.Helper()
	s.exec(t, "INSERT INTO "+s.resourceTable()+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path)"+
		" VALUES ($1, $2, $3, $4, $5)",
		project, generation, resourceType, logicalID, authPath)
}

// seedOccurrence writes one literal occurrence row directly — the fixture
// channel that can store endpoints the generated registry cannot classify,
// which the repository-insert path must still be able to surface as
// diagnosable raw evidence.
func (s *relationshipEvidenceService) seedOccurrence(t *testing.T, project, generation, authPath, sourceType, sourceID, label, targetType, targetID, sourcePath string, ordinal int64) {
	t.Helper()
	s.exec(t, "INSERT INTO "+s.edgeTable()+
		" (project, dataset_generation, auth_resource_path, label,"+
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id,"+
		" source_path, source_ordinal)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)",
		project, generation, authPath, label, sourceType, sourceID, targetType, targetID, sourcePath, ordinal)
}

// witnessRowCount counts stored occurrences for one exact scoped key — the
// direct-SQL truth the adapter's aggregate must agree with.
func (s *relationshipEvidenceService) witnessRowCount(t *testing.T, project, generation, authPath, sourceType, label, targetType string) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3"+
		" AND source_resource_type = $4 AND label = $5 AND target_resource_type = $6",
		project, generation, authPath, sourceType, label, targetType).Scan(&count); err != nil {
		s.fail("witness occurrence rows", err)
	}
	return count
}

func (s *relationshipEvidenceService) newStore(t *testing.T) *Store {
	t.Helper()
	repo, err := NewRelationshipEvidenceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new relationship evidence repository", err)
	}
	store, err := NewRelationshipEvidenceStore(repo)
	if err != nil {
		s.fail("new relationship evidence store", err)
	}
	return store
}

// requireValuesByteIdentical pins the adapter output against the literal
// expected rows: byte-identical JSON over RelationshipObservation, then the
// typed comparison for a readable failure message.
func requireValuesByteIdentical(t *testing.T, got []catalog.RelationshipObservation, want []catalog.RelationshipObservation) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("row witness mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func observation(project, generation, auth, storageFrom, label, storageTo string, count int64, builderFrom, builderTo, builderDirection string) catalog.RelationshipObservation {
	return catalog.RelationshipObservation{
		Project: project, DatasetGeneration: generation, AuthResourcePath: auth,
		FromType: builderFrom, Label: label, ToType: builderTo, EdgeCount: count,
		StorageFromType: storageFrom, StorageToType: storageTo, StorageDirection: "OUTBOUND",
		BuilderFromType: builderFrom, BuilderToType: builderTo, BuilderDirection: builderDirection,
	}
}

func relationshipOpts(project, generation string, unrestricted *bool, paths []string, types ...string) catalog.RelationshipObservationOptions {
	return catalog.RelationshipObservationOptions{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: unrestricted,
		AuthResourcePaths:             paths,
		ResourceTypes:                 types,
	}
}

// TestRelationshipObservationsExactOccurrenceCountsOnRealPostgreSQL is the
// aggregation acceptance: exact COUNT(*) per
// project/generation/auth-path/storage-endpoint/label with duplicate
// occurrences preserved and cross-scope controls that must never join the
// sum. The naive controls (endpoint dedup, auth flattening) are computed
// against the same live rows and must disagree — the test fails if the
// adapter ever collapses like they do.
func TestRelationshipObservationsExactOccurrenceCountsOnRealPostgreSQL(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-p1", "g1"

	s.seedResource(t, project, generation, "Condition", "cond-src1", "clinic/a")
	s.seedResource(t, project, generation, "Condition", "cond-src2", "clinic/a")
	s.seedResource(t, project, "g2", "Condition", "cond-src1", "clinic/a")
	s.seedResource(t, "pgcat03-p2", generation, "Condition", "cond-src1", "clinic/a")

	// In-scope aggregate: three repeated occurrences of one reference from
	// cond-src1 (identical endpoints, label, path, only the ordinal
	// differs — the multiplicity an endpoint-keyed store collapses) plus a
	// second source document at path "entry".
	for ordinal := int64(0); ordinal < 3; ordinal++ {
		s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", ordinal)
	}
	s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-src2", "subject_Patient", "Patient", "pat-t1", "entry", 0)
	// Cross-scope controls with the identical scoped relationship triple.
	s.seedOccurrence(t, "pgcat03-p2", generation, "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", 0)
	s.seedOccurrence(t, "pgcat03-p2", generation, "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", 1)
	s.seedOccurrence(t, project, "g2", "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", 0)
	s.seedOccurrence(t, project, "g2", "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", 1)
	s.seedOccurrence(t, project, "g2", "clinic/a", "Condition", "cond-src1", "subject_Patient", "Patient", "pat-t1", "", 2)
	// Same project/generation, different auth path: never summed in. The
	// store-05 occurrence identity is auth-path-free, so this control uses
	// its own source document — a second path cannot hold the fully
	// identical occurrence of the first.
	s.seedResource(t, project, generation, "Condition", "cond-src3", "other")
	for ordinal := int64(0); ordinal < 5; ordinal++ {
		s.seedOccurrence(t, project, generation, "other", "Condition", "cond-src3", "subject_Patient", "Patient", "pat-t1", "", ordinal)
	}

	if got := s.witnessRowCount(t, project, generation, "clinic/a", "Condition", "subject_Patient", "Patient"); got != 4 {
		t.Fatalf("fixture witness drifted: clinic/a rows = %d, want 4", got)
	}

	// (1) Unrestricted read: one exact aggregate per auth path — the
	// duplicate occurrences count (4), the other-path rows stay their own
	// aggregate (5), and no other project/generation row joins either sum.
	result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("unrestricted read: %v", err)
	}
	wantInScope := []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 4, "Patient", "Condition", "INBOUND"),
	}
	wantUnrestricted := []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 4, "Patient", "Condition", "INBOUND"),
		observation(project, generation, "other", "Condition", "subject_Patient", "Patient", 5, "Patient", "Condition", "INBOUND"),
	}
	requireValuesByteIdentical(t, result.Values, wantUnrestricted)
	if result.Status != catalog.EvidenceAvailable || !result.Available || !result.Complete || len(result.Diagnostics) != 0 {
		t.Fatalf("unrestricted result = %+v, want available complete with no diagnostics", result)
	}
	// Golden digest for these two literal observations (pinned from the
	// live run).
	if result.Digest != relationshipExactCountsDigest {
		t.Fatalf("exact-counts digest = %q, want golden %q", result.Digest, relationshipExactCountsDigest)
	}

	// (2) Naive endpoint-dedup control on the same rows: it collapses the
	// scope to one row per endpoint triple — proof the exact-count claim is
	// non-vacuous (the adapter would match this if it deduplicated).
	var dedupGroups int
	var dedupTotal int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*), COALESCE(SUM(1),0) FROM (SELECT DISTINCT source_resource_type, label, target_resource_type FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2) d", project, generation).Scan(&dedupGroups, &dedupTotal); err != nil {
		s.fail("dedup control", err)
	}
	if dedupGroups != 1 || dedupTotal != 1 {
		t.Fatalf("dedup control unexpectedly not collapsed: groups=%d total=%d", dedupGroups, dedupTotal)
	}

	// (3) Naive auth-flattening control: grouping without auth_resource_path
	// sums clinic/a with other — the adapter must never produce 9.
	var flattened int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2", project, generation).Scan(&flattened); err != nil {
		s.fail("flatten control", err)
	}
	if flattened != 9 {
		t.Fatalf("flatten control = %d, want 9 (4 in-scope + 5 in other)", flattened)
	}

	// (4) Restricted one-path read sees only clinic/a's 4, never other's 5.
	restricted, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(false), []string{"clinic/a"}))
	if err != nil {
		t.Fatalf("restricted read: %v", err)
	}
	requireValuesByteIdentical(t, restricted.Values, wantInScope)

	// Repeated reads are deterministic: identical digest.
	for round := 0; round < 3; round++ {
		again, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
			catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
		if err != nil {
			t.Fatalf("repeat read: %v", err)
		}
		if again.Digest != result.Digest {
			t.Fatalf("read %d digest %q != first %q", round, again.Digest, result.Digest)
		}
	}
}

// TestRelationshipObservationsAuthModesCompleteAndEmptyOnRealPostgreSQL
// pins the four authorization modes against live rows that exist in every
// path: unrestricted, one-path, multiple-path, and restricted-empty all
// return the correct complete/empty status without cross-scope sums.
func TestRelationshipObservationsAuthModesCompleteAndEmptyOnRealPostgreSQL(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-auth", "g1"

	s.seedResource(t, project, generation, "Condition", "cond-a", "")
	s.seedResource(t, project, generation, "Condition", "cond-b", "clinic/a")
	s.seedResource(t, project, generation, "BodyStructure", "bs-c", "clinic/b")
	s.seedResource(t, project, generation, "Condition", "cond-c", "clinic/c")

	for ordinal := int64(0); ordinal < 2; ordinal++ {
		s.seedOccurrence(t, project, generation, "", "Condition", "cond-a", "subject_Patient", "Patient", "pat-1", "", ordinal)
	}
	for ordinal := int64(0); ordinal < 3; ordinal++ {
		s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-b", "subject_Patient", "Patient", "pat-1", "", ordinal)
	}
	s.seedOccurrence(t, project, generation, "clinic/b", "BodyStructure", "bs-c", "patient", "Patient", "pat-1", "", 0)
	for ordinal := int64(0); ordinal < 4; ordinal++ {
		s.seedOccurrence(t, project, generation, "clinic/c", "Condition", "cond-c", "subject_Patient", "Patient", "pat-1", "", ordinal)
	}

	// Unrestricted: four separate auth-scoped aggregates.
	all, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("unrestricted: %v", err)
	}
	wantAll := []catalog.RelationshipObservation{
		observation(project, generation, "", "Condition", "subject_Patient", "Patient", 2, "Patient", "Condition", "INBOUND"),
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 3, "Patient", "Condition", "INBOUND"),
		observation(project, generation, "clinic/b", "BodyStructure", "patient", "Patient", 1, "Patient", "BodyStructure", "INBOUND"),
		observation(project, generation, "clinic/c", "Condition", "subject_Patient", "Patient", 4, "Patient", "Condition", "INBOUND"),
	}
	requireValuesByteIdentical(t, all.Values, wantAll)
	if all.Status != catalog.EvidenceAvailable || !all.Available || !all.Complete {
		t.Fatalf("unrestricted status = %+v", all)
	}
	if all.Digest != relationshipAuthModesDigest {
		t.Fatalf("auth-modes digest = %q, want golden %q", all.Digest, relationshipAuthModesDigest)
	}

	// One path: exactly its rows; the other paths hold live rows it must
	// not see.
	one, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(false), []string{"clinic/a"}))
	if err != nil {
		t.Fatalf("one-path: %v", err)
	}
	requireValuesByteIdentical(t, one.Values, wantAll[1:2])

	// Multiple paths: exactly their rows summed per key, never clinic/c.
	multi, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(false), []string{"clinic/a", "clinic/b"}))
	if err != nil {
		t.Fatalf("multi-path: %v", err)
	}
	requireValuesByteIdentical(t, multi.Values, []catalog.RelationshipObservation{wantAll[1], wantAll[2]})

	// Restricted with zero authorized paths: a legitimate authorization
	// state — complete, empty, and the adapter never queries.
	empty, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(false), nil))
	if err != nil {
		t.Fatalf("restricted-empty: %v", err)
	}
	if empty.Status != catalog.EvidenceEmpty || !empty.Available || !empty.Complete || len(empty.Values) != 0 {
		t.Fatalf("restricted-empty result = %+v, want available complete empty", empty)
	}
	if total := s.witnessRowCount(t, project, generation, "clinic/c", "Condition", "subject_Patient", "Patient"); total != 4 {
		t.Fatalf("fixture witness drifted: clinic/c rows = %d, want 4", total)
	}

	// Repository-level posture: a restricted scan with no paths binds the
	// empty array (reads nothing) and never falls back to an unfiltered
	// scan of the ten live rows.
	repo, err := NewRelationshipEvidenceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new repository", err)
	}
	groups, err := repo.ScanRelationshipOccurrences(s.ctx, RelationshipScope{
		Project: project, DatasetGeneration: generation,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil {
		t.Fatalf("restricted-empty repository scan: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("restricted-empty scan widened: %+v", groups)
	}
}

// TestRelationshipObservationsInvalidEndpointsDiagnosedRawRowsRemain is
// acceptance item four: invalid generated FHIR endpoint types are diagnosed
// and excluded from Builder capability evidence while the raw stored
// occurrences remain inspectable — nothing this read does rewrites or
// deletes them.
func TestRelationshipObservationsInvalidEndpointsDiagnosedRawRowsRemain(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-invalid", "g1"

	s.seedResource(t, project, generation, "NotAThing", "bad-src", "clinic/a")
	s.seedResource(t, project, generation, "Resource", "abstract-src", "clinic/a")
	s.seedResource(t, project, generation, "Patient", "pat-src", "clinic/a")
	s.seedResource(t, project, generation, "Condition", "cond-good", "clinic/a")

	s.seedOccurrence(t, project, generation, "clinic/a", "NotAThing", "bad-src", "subject_Patient", "Patient", "pat-t", "", 0)
	s.seedOccurrence(t, project, generation, "clinic/a", "NotAThing", "bad-src", "subject_Patient", "Patient", "pat-t", "", 1)
	s.seedOccurrence(t, project, generation, "clinic/a", "Resource", "abstract-src", "subject_Patient", "Patient", "pat-t", "", 0)
	s.seedOccurrence(t, project, generation, "clinic/a", "Patient", "pat-src", "not_a_generated_label", "Practitioner", "prac-t", "", 0)
	s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-good", "subject_Patient", "Patient", "pat-t", "", 0)

	result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 1, "Patient", "Condition", "INBOUND"),
	}
	requireValuesByteIdentical(t, result.Values, want)
	if result.Status != catalog.EvidenceAvailable || !result.Available {
		t.Fatalf("result with a valid subset must stay available: %+v", result)
	}
	if len(result.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v, want three", result.Diagnostics)
	}
	endpointCodes, traversalCodes := 0, 0
	for _, d := range result.Diagnostics {
		switch d.Code {
		case "INVALID_RELATIONSHIP_ENDPOINT":
			endpointCodes++
			if d.Message != "relationship endpoint is not a concrete generated FHIR resource" {
				t.Errorf("endpoint diagnostic message = %q", d.Message)
			}
			switch d.StorageFromType {
			case "NotAThing":
				if d.StorageToType != "Patient" || d.Label != "subject_Patient" || d.Count != 2 {
					t.Errorf("NotAThing diagnostic = %+v", d)
				}
			case "Resource":
				if d.StorageToType != "Patient" || d.Label != "subject_Patient" || d.Count != 1 {
					t.Errorf("Resource diagnostic = %+v", d)
				}
			default:
				t.Errorf("unexpected endpoint diagnostic %q", d.StorageFromType)
			}
		case "UNSUPPORTED_GENERATED_TRAVERSAL":
			traversalCodes++
			if d.Message != "relationship endpoints and label are not a supported generated traversal" ||
				d.StorageFromType != "Patient" || d.StorageToType != "Practitioner" ||
				d.Label != "not_a_generated_label" || d.Count != 1 {
				t.Errorf("traversal diagnostic = %+v", d)
			}
		default:
			t.Errorf("unexpected diagnostic code %q", d.Code)
		}
	}
	if endpointCodes != 2 || traversalCodes != 1 {
		t.Fatalf("diagnostic split endpoints=%d traversals=%d, want 2/1", endpointCodes, traversalCodes)
	}

	// Raw occurrences remain inspectable, unmodified, in full count.
	var rawRows int
	var invalidRows int
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2", project, generation).Scan(&rawRows); err != nil {
		s.fail("raw witness", err)
	}
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 AND source_resource_type IN ('NotAThing','Resource')"+
		" AND label = 'subject_Patient' AND target_resource_type = 'Patient'", project, generation).Scan(&invalidRows); err != nil {
		s.fail("raw invalid witness", err)
	}
	if rawRows != 5 || invalidRows != 3 {
		t.Fatalf("raw rows after diagnosed read: total=%d invalid=%d, want 5/3", rawRows, invalidRows)
	}

	// Requested-subset posture: with concrete types bound, non-canonical
	// rows are filtered inside SQL exactly as the Arango adapter's IN list
	// behaves — no endpoint diagnostics are produced for rows the caller
	// never asked to see.
	subset, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil, "condition", "Patient"))
	if err != nil {
		t.Fatalf("subset read: %v", err)
	}
	requireValuesByteIdentical(t, subset.Values, want)
	for _, d := range subset.Diagnostics {
		if d.Code == "INVALID_RELATIONSHIP_ENDPOINT" || d.Code == "UNSUPPORTED_GENERATED_TRAVERSAL" {
			t.Fatalf("subset read diagnosed rows outside the requested types: %+v", d)
		}
	}
}

// TestRelationshipObservationsFilteredReadKeepsCaseVariantRows pins the
// filtered/unfiltered reconciliation: store-05 stores writer-supplied
// endpoint strings, and classification canonicalizes them, so a
// requested-type read must return exactly the rows the unfiltered read
// returns — a row stored as "condition" or "PatIent" may be silently
// dropped by a raw string pushdown and must not be.
func TestRelationshipObservationsFilteredReadKeepsCaseVariantRows(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-case", "g1"

	s.seedResource(t, project, generation, "condition", "cond-lower", "clinic/a")
	s.seedResource(t, project, generation, "Condition", "cond-mixed", "clinic/a")
	s.seedOccurrence(t, project, generation, "clinic/a", "condition", "cond-lower", "subject_Patient", "Patient", "pat-x", "", 0)
	s.seedOccurrence(t, project, generation, "clinic/a", "condition", "cond-lower", "subject_Patient", "Patient", "pat-x", "", 1)
	s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-mixed", "subject_Patient", "PatIent", "pat-x", "", 0)

	want := []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 3, "Patient", "Condition", "INBOUND"),
	}

	unfiltered, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("unfiltered read: %v", err)
	}
	requireValuesByteIdentical(t, unfiltered.Values, want)

	// Canonical and caller-casing requests both keep the case-variant rows.
	for _, requested := range [][]string{{"Condition", "Patient"}, {"condition", "patient"}} {
		filtered, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
			catalog.ExplicitAuthResourcePathsUnrestricted(true), nil, requested...))
		if err != nil {
			t.Fatalf("filtered read %v: %v", requested, err)
		}
		requireValuesByteIdentical(t, filtered.Values, want)
		for _, d := range filtered.Diagnostics {
			if d.Code != "INVALID_RESOURCE_TYPE" {
				t.Fatalf("filtered read %v diagnosed rows it should match: %+v", requested, d)
			}
		}
	}

	// The filter is still a filter: a request that cannot match returns
	// nothing even though every row canonicalizes to Condition->Patient.
	missed, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil, "BodyStructure", "Patient"))
	if err != nil {
		t.Fatalf("non-matching filter: %v", err)
	}
	if len(missed.Values) != 0 || missed.Status != catalog.EvidenceEmpty {
		t.Fatalf("non-matching filter returned %+v status %s, want empty", missed.Values, missed.Status)
	}

	// The raw stored spellings are untouched by the read.
	var lowerSources, mixedTargets int
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 AND source_resource_type = 'condition'", project, generation).Scan(&lowerSources); err != nil {
		s.fail("raw lower witness", err)
	}
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 AND target_resource_type = 'PatIent'", project, generation).Scan(&mixedTargets); err != nil {
		s.fail("raw mixed witness", err)
	}
	if lowerSources != 2 || mixedTargets != 1 {
		t.Fatalf("raw spellings rewritten: lower=%d mixed=%d, want 2/1", lowerSources, mixedTargets)
	}
}

// TestRelationshipObservationsBuilderDirectionDerivedFromRegistryOnRealPostgreSQL
// drives the full path: occurrences written by the real store-05
// EdgeRepository are read back as observations whose physical storage
// direction and registry-derived Builder direction are separately pinned
// (literal structs plus the golden digest).
func TestRelationshipObservationsBuilderDirectionDerivedFromRegistryOnRealPostgreSQL(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-direction", "g1"

	s.seedResource(t, project, generation, "Condition", "cond-d", "clinic/a")
	s.seedResource(t, project, generation, "BodyStructure", "bs-d", "clinic/a")

	edgeRepo, err := storepostgres.NewEdgeRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new edge repository", err)
	}
	// Repeated-owner correlation through the real write path: identical
	// endpoints and path, only the ordinal differs — two stored rows, one
	// aggregate of 2.
	for ordinal := int64(0); ordinal < 2; ordinal++ {
		if _, err := edgeRepo.InsertEdge(s.ctx, storepostgres.EdgeOccurrenceRow{
			Project: project, DatasetGeneration: generation, AuthResourcePath: "clinic/a",
			Label: "subject_Patient", SourceResourceType: "Condition", SourceLogicalID: "cond-d",
			TargetResourceType: "Patient", TargetLogicalID: "pat-d",
			SourcePath: "entry", SourceOrdinal: ordinal,
		}); err != nil {
			s.fail("insert repeated occurrence", err)
		}
	}
	if _, err := edgeRepo.InsertEdge(s.ctx, storepostgres.EdgeOccurrenceRow{
		Project: project, DatasetGeneration: generation, AuthResourcePath: "clinic/a",
		Label: "patient", SourceResourceType: "BodyStructure", SourceLogicalID: "bs-d",
		TargetResourceType: "Patient", TargetLogicalID: "pat-d",
		SourcePath: "", SourceOrdinal: 0,
	}); err != nil {
		s.fail("insert patient occurrence", err)
	}

	result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []catalog.RelationshipObservation{
		{
			Project: project, DatasetGeneration: generation, AuthResourcePath: "clinic/a",
			FromType: "Patient", Label: "patient", ToType: "BodyStructure", EdgeCount: 1,
			StorageFromType: "BodyStructure", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "BodyStructure", BuilderDirection: "INBOUND",
		},
		{
			Project: project, DatasetGeneration: generation, AuthResourcePath: "clinic/a",
			FromType: "Patient", Label: "subject_Patient", ToType: "Condition", EdgeCount: 2,
			StorageFromType: "Condition", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "Condition", BuilderDirection: "INBOUND",
		},
	}
	requireValuesByteIdentical(t, result.Values, want)
	if result.Digest != relationshipDirectionDigest {
		t.Fatalf("direction digest = %q, want golden %q", result.Digest, relationshipDirectionDigest)
	}

	// The witness side: physical storage direction stays exactly as the
	// store-05 rows recorded it, independent of the Builder projection.
	rows, err := s.iso.Query(s.ctx, "SELECT source_resource_type, label, target_resource_type, COUNT(*) FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 GROUP BY 1,2,3 ORDER BY 1,2,3", project, generation)
	if err != nil {
		s.fail("storage witness", err)
	}
	defer rows.Close()
	type storageRow struct {
		from, label, to string
		count           int64
	}
	var seen []storageRow
	for rows.Next() {
		var r storageRow
		if err := rows.Scan(&r.from, &r.label, &r.to, &r.count); err != nil {
			s.fail("storage witness scan", err)
		}
		seen = append(seen, r)
	}
	if err := rows.Err(); err != nil {
		s.fail("storage witness rows", err)
	}
	wantStorage := []storageRow{
		{from: "BodyStructure", label: "patient", to: "Patient", count: 1},
		{from: "Condition", label: "subject_Patient", to: "Patient", count: 2},
	}
	if len(seen) != len(wantStorage) {
		t.Fatalf("storage witness rows = %+v, want %+v", seen, wantStorage)
	}
	for i := range seen {
		if seen[i] != wantStorage[i] {
			t.Fatalf("storage witness row %d = %+v, want %+v", i, seen[i], wantStorage[i])
		}
	}
}

// TestRelationshipObservationsConcurrentIdenticalOccurrenceRace pins the
// concurrency surface between the store-05 write identity and the
// aggregation: two real connections (distinct pg_backend_pid) race the
// fully identical occurrence each round; exactly one insert wins, the
// loser gets the typed conflict, and the aggregate — witnessed by direct
// SQL every round — gains exactly one row, never two.
func TestRelationshipObservationsConcurrentIdenticalOccurrenceRace(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	const project, generation = "pgcat03-conflict", "g1"
	s.seedResource(t, project, generation, "Condition", "cond-race", "clinic/a")

	poolA := s.openSingleConnPool(t, "writer A")
	poolB := s.openSingleConnPool(t, "writer B")
	pidA, pidB := backendPID(t, poolA), backendPID(t, poolB)
	if pidA == pidB {
		t.Fatalf("race connections share backend pid %d", pidA)
	}
	repoA, err := storepostgres.NewEdgeRepository(poolA, s.schema)
	if err != nil {
		s.fail("edge repository A", err)
	}
	repoB, err := storepostgres.NewEdgeRepository(poolB, s.schema)
	if err != nil {
		s.fail("edge repository B", err)
	}
	adapterRepo, err := NewRelationshipEvidenceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("aggregation repository", err)
	}
	store, err := NewRelationshipEvidenceStore(adapterRepo)
	if err != nil {
		s.fail("evidence store", err)
	}

	const rounds = 12
	for round := 1; round <= rounds; round++ {
		rec := storepostgres.EdgeOccurrenceRow{
			Project: project, DatasetGeneration: generation, AuthResourcePath: "clinic/a",
			Label: "subject_Patient", SourceResourceType: "Condition", SourceLogicalID: "cond-race",
			TargetResourceType: "Patient", TargetLogicalID: "pat-race",
			SourcePath: "", SourceOrdinal: int64(round),
		}
		results := make([]error, 2)
		var seqs [2]int64
		start := make(chan struct{})
		var wg sync.WaitGroup
		race := func(index int, repo *storepostgres.EdgeRepository) {
			defer wg.Done()
			<-start
			seq, err := repo.InsertEdge(s.ctx, rec)
			results[index], seqs[index] = err, seq
		}
		wg.Add(2)
		go race(0, repoA)
		go race(1, repoB)
		close(start)
		wg.Wait()

		winnerA := results[0] == nil
		loserOK := results[1] != nil && errors.Is(results[1], storepostgres.ErrEdgeConflict)
		loserA := results[1] == nil
		loserBOK := results[0] != nil && errors.Is(results[0], storepostgres.ErrEdgeConflict)
		if !(winnerA && loserOK) && !(loserA && loserBOK) {
			t.Fatalf("round %d: race outcomes A=%v B=%v, want exactly one success and one ErrEdgeConflict",
				round, results[0], results[1])
		}
		if winnerA && seqs[0] == 0 || loserA && seqs[1] == 0 {
			t.Fatalf("round %d: winner returned seq 0: A=%d B=%d", round, seqs[0], seqs[1])
		}
		if got := s.witnessRowCount(t, project, generation, "clinic/a", "Condition", "subject_Patient", "Patient"); got != int64(round) {
			t.Fatalf("round %d: stored rows = %d, want %d (identity never stored twice)", round, got, round)
		}
		result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
			catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
		if err != nil {
			t.Fatalf("round %d read: %v", round, err)
		}
		requireValuesByteIdentical(t, result.Values, []catalog.RelationshipObservation{
			observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", int64(round), "Patient", "Condition", "INBOUND"),
		})
	}
}

// TestRelationshipObservationsConcurrentAbortConvergenceRace pins the
// read path against an in-flight writer on two distinct backends: the
// aggregation never observes a row mid-flight, converges to the exact same
// byte-identical witness after every abort, and includes it exactly once
// after commit.
func TestRelationshipObservationsConcurrentAbortConvergenceRace(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	const project, generation = "pgcat03-abort", "g1"
	s.seedResource(t, project, generation, "Condition", "cond-w", "clinic/a")

	poolW := s.openSingleConnPool(t, "aborting writer")
	poolR := s.openSingleConnPool(t, "reader")
	pidW, pidR := backendPID(t, poolW), backendPID(t, poolR)
	if pidW == pidR {
		t.Fatalf("reader and writer share backend pid %d", pidW)
	}
	adapterRepo, err := NewRelationshipEvidenceRepository(poolR, s.schema)
	if err != nil {
		s.fail("aggregation repository", err)
	}
	store, err := NewRelationshipEvidenceStore(adapterRepo)
	if err != nil {
		s.fail("evidence store", err)
	}

	witness := func(count int64) []catalog.RelationshipObservation {
		if count == 0 {
			return []catalog.RelationshipObservation{}
		}
		return []catalog.RelationshipObservation{
			observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", count, "Patient", "Condition", "INBOUND"),
		}
	}
	readValues := func(step string) []catalog.RelationshipObservation {
		result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
			catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
		if err != nil {
			t.Fatalf("%s read: %v", step, err)
		}
		return result.Values
	}

	const rounds = 12
	insertSQL := "INSERT INTO " + s.edgeTable() +
		" (project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)"
	for round := 1; round <= rounds; round++ {
		wconn, err := poolW.Acquire(s.ctx)
		if err != nil {
			t.Fatalf("round %d acquire writer: %v", round, err)
		}
		tx, err := wconn.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			wconn.Release()
			t.Fatalf("round %d begin: %v", round, err)
		}
		if _, err := tx.Exec(s.ctx, insertSQL, project, generation, "clinic/a", "subject_Patient",
			"Condition", "cond-w", "Patient", "pat-w", "", int64(round)); err != nil {
			_ = tx.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("round %d staged insert: %v", round, err)
		}
		// Mid-flight: the row exists on the writer backend (same pid the
		// probe saw, read through the open transaction) but must be
		// invisible to the reader's aggregate.
		var inflightPID int32
		if err := tx.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&inflightPID); err != nil {
			_ = tx.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("round %d in-flight pid: %v", round, err)
		}
		if inflightPID != pidW {
			_ = tx.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("writer backend changed: %d != %d", inflightPID, pidW)
		}
		var readerPID int32
		if err := poolR.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&readerPID); err != nil {
			_ = tx.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("round %d reader pid: %v", round, err)
		}
		if readerPID == inflightPID {
			_ = tx.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("reader and writer share backend pid %d mid-flight", readerPID)
		}
		requireValuesByteIdentical(t, readValues("mid-flight"), witness(int64(round-1)))

		if err := tx.Rollback(s.ctx); err != nil {
			wconn.Release()
			t.Fatalf("round %d rollback: %v", round, err)
		}
		// Post-abort convergence: byte-identical to the pre-round witness.
		requireValuesByteIdentical(t, readValues("post-abort"), witness(int64(round-1)))

		tx2, err := wconn.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			wconn.Release()
			t.Fatalf("round %d begin commit-tx: %v", round, err)
		}
		if _, err := tx2.Exec(s.ctx, insertSQL, project, generation, "clinic/a", "subject_Patient",
			"Condition", "cond-w", "Patient", "pat-w", "", int64(round)); err != nil {
			_ = tx2.Rollback(s.ctx)
			wconn.Release()
			t.Fatalf("round %d committed insert: %v", round, err)
		}
		if err := tx2.Commit(s.ctx); err != nil {
			wconn.Release()
			t.Fatalf("round %d commit: %v", round, err)
		}
		wconn.Release()

		// After commit the row participates exactly once.
		requireValuesByteIdentical(t, readValues("post-commit"), witness(int64(round)))
	}

	// Final convergence witnesses: adapter agrees with direct SQL and the
	// golden digest of the 12-row fixture.
	if got := s.witnessRowCount(t, project, generation, "clinic/a", "Condition", "subject_Patient", "Patient"); got != rounds {
		t.Fatalf("direct SQL row count = %d, want %d", got, rounds)
	}
	final, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final.Digest != relationshipAbortRaceDigest {
		t.Fatalf("abort-race final digest = %q, want golden %q", final.Digest, relationshipAbortRaceDigest)
	}
}

// TestRelationshipEvidenceIdentityGuardDropNonVacuity proves the claimed
// database guard is real: with fhir_edge_occurrence_identity_ux in place a
// fully identical occurrence is refused by that exact named constraint;
// after a live DROP CONSTRAINT the identical insert is accepted and the
// aggregate provably double-counts; restoring the constraint returns the
// exact refusal. If the aggregation ever counted DISTINCT endpoints, the
// guard-drop leg would go unnoticed — this test is what keeps the exact
// COUNT(*) claim honest at the database level.
func TestRelationshipEvidenceIdentityGuardDropNonVacuity(t *testing.T) {
	s := newRelationshipEvidenceService(t)
	store := s.newStore(t)
	const project, generation = "pgcat03-guard", "g1"

	s.seedResource(t, project, generation, "Condition", "cond-g", "clinic/a")
	s.seedOccurrence(t, project, generation, "clinic/a", "Condition", "cond-g", "subject_Patient", "Patient", "pat-g", "", 0)

	duplicateSQL := "INSERT INTO " + s.edgeTable() +
		" (project, dataset_generation, auth_resource_path, label," +
		" source_resource_type, source_logical_id, target_resource_type, target_logical_id," +
		" source_path, source_ordinal) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)"
	duplicateArgs := []any{project, generation, "clinic/a", "subject_Patient", "Condition", "cond-g", "Patient", "pat-g", "", int64(0)}

	// Positive control: the guard refuses the identical occurrence, naming
	// the exact constraint.
	_, err := s.iso.Exec(s.ctx, duplicateSQL, duplicateArgs...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "fhir_edge_occurrence_identity_ux" {
		t.Fatalf("duplicate insert with guard active: %v, want 23505 from fhir_edge_occurrence_identity_ux", err)
	}

	// Live guard drop: the constraint must really be there to drop.
	if _, err := s.iso.Exec(s.ctx, "ALTER TABLE "+s.edgeTable()+
		" DROP CONSTRAINT fhir_edge_occurrence_identity_ux"); err != nil {
		s.fail("drop identity guard", err)
	}
	if _, err := s.iso.Exec(s.ctx, duplicateSQL, duplicateArgs...); err != nil {
		t.Fatalf("duplicate insert after guard drop was refused (%v); the guard was not what prevented it", err)
	}

	// Without the guard the duplicate participates: the aggregate doubles,
	// proving the exact-count guarantee depends on the live constraint.
	result, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("guard-dropped read: %v", err)
	}
	requireValuesByteIdentical(t, result.Values, []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 2, "Patient", "Condition", "INBOUND"),
	})

	// Restore the named guard. The guard-free duplicate must be removed
	// first (ctid-addressed single row: the two rows are fully identical,
	// a predicate delete would take both), then the identical insert is
	// refused again by the same name and the catalog confirms the
	// constraint is live.
	if _, err := s.iso.Exec(s.ctx, "DELETE FROM "+s.edgeTable()+
		" WHERE ctid = (SELECT ctid FROM "+s.edgeTable()+
		" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3 AND label = $4"+
		" AND source_resource_type = $5 AND source_logical_id = $6 AND target_resource_type = $7"+
		" AND target_logical_id = $8 AND source_path = $9 AND source_ordinal = $10 LIMIT 1)",
		project, generation, "clinic/a", "subject_Patient", "Condition", "cond-g", "Patient", "pat-g", "", int64(0)); err != nil {
		s.fail("remove guard-free duplicate", err)
	}
	if _, err := s.iso.Exec(s.ctx, "ALTER TABLE "+s.edgeTable()+
		" ADD CONSTRAINT fhir_edge_occurrence_identity_ux UNIQUE (project, dataset_generation,"+
		" source_resource_type, source_logical_id, label, target_resource_type, target_logical_id,"+
		" source_path, source_ordinal)"); err != nil {
		s.fail("restore identity guard", err)
	}
	_, err = s.iso.Exec(s.ctx, duplicateSQL, duplicateArgs...)
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "fhir_edge_occurrence_identity_ux" {
		t.Fatalf("duplicate insert after guard restore: %v, want 23505 from fhir_edge_occurrence_identity_ux", err)
	}
	var live int
	if err := s.iso.QueryRow(s.ctx,
		"SELECT COUNT(*) FROM pg_constraint WHERE conrelid = '"+s.edgeTable()+"'::regclass AND conname = 'fhir_edge_occurrence_identity_ux'").Scan(&live); err != nil {
		s.fail("guard catalog witness", err)
	}
	if live != 1 {
		t.Fatalf("restored guard catalog witness = %d, want 1", live)
	}
	restored, err := store.DiscoverRelationshipObservations(s.ctx, relationshipOpts(project, generation,
		catalog.ExplicitAuthResourcePathsUnrestricted(true), nil))
	if err != nil {
		t.Fatalf("restored-guard read: %v", err)
	}
	requireValuesByteIdentical(t, restored.Values, []catalog.RelationshipObservation{
		observation(project, generation, "clinic/a", "Condition", "subject_Patient", "Patient", 1, "Patient", "Condition", "INBOUND"),
	})
}

// TestZZZeroLeftoverRelationshipEvidenceFixtures is the ownership-scoped
// zero-leftover audit. It checks, by exact name only, every database (and
// the schema that lived inside it) this file verified into existence, and
// that the verify_pgcat03_* pg_database count equals the baseline captured
// at process start. It performs no global scan of foreign namespaces, so
// concurrent sibling workers sweeping their own prefixes neither race this
// audit nor trip it.
func TestZZZeroLeftoverRelationshipEvidenceFixtures(t *testing.T) {
	raw := relationshipTestPostgresURL(t)
	relationshipOwnedMu.Lock()
	owned := make(map[string]string, len(relationshipOwnedByName))
	for db, schema := range relationshipOwnedByName {
		owned[db] = schema
	}
	created := relationshipCreatedTotal
	relationshipOwnedMu.Unlock()
	if created == 0 && relationshipBaselineDBs == 0 {
		t.Skip("this process created no verify_pgcat03_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, relationshipFixturePrefix) {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
			if schema != "" {
				u, err := url.Parse(raw)
				if err == nil {
					if q := u.Query(); q.Get("dbname") != "" {
						q.Del("dbname")
						u.RawQuery = q.Encode()
					}
					u.Path = "/" + db
					if probe, err := pgxpool.New(ctx, u.String()); err == nil {
						var schemaCount int
						if err := probe.QueryRow(ctx, "SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
							t.Errorf("audit owned schema %s in %s: %v", schema, db, err)
						} else if schemaCount != 0 {
							t.Errorf("leftover owned schema %s in leftover database %s", schema, db)
						}
						probe.Close()
					}
				}
			}
		}
	}

	if relationshipBaselineDBs < 0 {
		t.Errorf("pg_database baseline was not captured at process start (%s); exact-name audit above still ran", relationshipBaselineError)
		return
	}
	var liveCount int
	if err := admin.QueryRow(ctx,
		"SELECT COUNT(*) FROM pg_database WHERE datname ~ $1", relationshipFixtureNamePattern.String()).Scan(&liveCount); err != nil {
		t.Errorf("audit %s namespace: %v", relationshipFixturePrefix, err)
		return
	}
	if liveCount != relationshipBaselineDBs {
		t.Errorf("%s pg_database count = %d, want baseline %d", relationshipFixturePrefix, liveCount, relationshipBaselineDBs)
	}
}

// Golden digests, each pinned from a live PostgreSQL 18 run of this file
// and cross-checked against the byte-identical literal rows above.
const (
	relationshipExactCountsDigest = "sha256:762e10da27eea1702d9951611c95c4d772e9a1ed999d5b0b2db5c8b360749815"
	relationshipAuthModesDigest   = "sha256:74b528be7584396bba3341b76b4dd13cb63dc374b36916a6e8b68e8a9258f2e1"
	relationshipDirectionDigest   = "sha256:9b0305fa63d4f4be3cc1524da0d079e403f3090144e8ff0d793eb5ea92bd1cce"
	relationshipAbortRaceDigest   = "sha256:8f2f7979f86364ecebed93778191aeb9bf241b2c10da097d9fe5f132ce0cde17"
)
