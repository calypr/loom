package postgres

// Real PostgreSQL 18 integration for pg-store-03. Everything here runs
// against the service named by LOOM_TEST_POSTGRES_URL: each test creates one
// uniquely-named isolated database (pgstore03_it_*) plus one owned schema
// (pgstore03_it_*) inside it and drops both in its cleanups, so the suite
// leaves zero leftovers. Expected rows are asserted literally, through the
// test's own witness pool. A skipped test proves nothing: the CI runner
// fails on skips. CAS schedule tests run serially (no t.Parallel anywhere).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgstore03ResourceNamePattern = regexp.MustCompile(`^pgstore03_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// pgService owns one isolated database and one owned schema inside it, both
// named pgstore03_it_*, created for exactly one test and removed by that
// test's cleanups. Nothing outside these two resources is touched.
type pgService struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database (witness pool)
	dsn    string        // isolated database DSN (credentials scrubbed on failure)
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newPGService(t *testing.T) *pgService {
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

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("pgstore03_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore03ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		// Drop with a fresh bounded context: cleanup must be able to
		// converge and report a failure even when the test context has
		// burned its budget.
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

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgstore03_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore03ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		// Drop with a fresh bounded context (see the database cleanup above).
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := iso.Exec(cleanupCtx,
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	return &pgService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

// openClient opens one readiness-checked pg-store-01 pool against the
// isolated database.
func (s *pgService) openClient(t *testing.T) *storepostgres.Client {
	t.Helper()
	return s.openClientWithBounds(t, 2, 4)
}

func (s *pgService) openClientWithBounds(t *testing.T, minConns, maxConns int32) *storepostgres.Client {
	t.Helper()
	client, err := storepostgres.Open(s.ctx, storepostgres.Config{
		DSN:            s.dsn,
		MaxConns:       maxConns,
		MinConns:       minConns,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

// mustMigrate applies the embedded history (including migration 000003)
// through the production runner and proves this ticket's row is recorded
// literally: version 3, this name, and the checksum of the embedded SQL.
func (s *pgService) mustMigrate(t *testing.T, client *storepostgres.Client) {
	t.Helper()
	result, err := storepostgres.MigrateSchema(s.ctx, client, s.schema)
	if err != nil {
		s.fail("MigrateSchema", err)
	}
	embedded, err := storepostgres.EmbeddedMigrations()
	if err != nil {
		s.fail("EmbeddedMigrations", err)
	}
	var want *storepostgres.Migration
	for i := range embedded {
		if embedded[i].Version == 3 {
			want = &embedded[i]
		}
	}
	if want == nil || want.Name != "dataset_generation_manifest" {
		t.Fatalf("embedded history has no migration 3 named dataset_generation_manifest: %+v", embedded)
	}
	sum := sha256.Sum256([]byte(want.SQL))
	if want.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatalf("migration 3 checksum does not match its SQL: %s", want.Checksum)
	}
	var found *storepostgres.LedgerRecord
	for i := range result.Applied {
		if result.Applied[i].Version == 3 {
			found = &result.Applied[i]
		}
	}
	if found == nil {
		t.Fatalf("ledger lacks migration 3: %+v", result.Applied)
	}
	if found.Name != want.Name || found.Checksum != want.Checksum {
		t.Fatalf("ledger row for migration 3 = %+v, want name %q checksum %q", *found, want.Name, want.Checksum)
	}
	for _, table := range []string{"dataset_generation_manifest", "dataset_active_generation"} {
		var regclass *string
		if err := s.iso.QueryRow(s.ctx, `SELECT to_regclass($1)::text`, s.schema+"."+table).Scan(&regclass); err != nil {
			s.fail("witness to_regclass "+table, err)
		}
		if regclass == nil || *regclass == "" {
			t.Fatalf("table %s was not created by migration 3", table)
		}
	}
}

// openStore migrates through the production runner (once per service) and
// returns a live store over a fresh readiness-checked pool. Reopening a
// store means closing the client and calling openStore again: a new pool
// means new physical connections, so readback after reopen is honest.
func (s *pgService) openStore(t *testing.T) (*Store, *storepostgres.Client) {
	t.Helper()
	client := s.openClient(t)
	s.mustMigrate(t, client)
	store, err := New(client, s.schema)
	if err != nil {
		s.fail("New store", err)
	}
	return store, client
}

// ---------------------------------------------------------------------------
// literal row witnesses (test's own pool, never the store's connections)
// ---------------------------------------------------------------------------

func (s *pgService) rawExec(t *testing.T, sql string, args ...any) pgconn.CommandTag {
	t.Helper()
	tag, err := s.iso.Exec(s.ctx, sql, args...)
	if err != nil {
		s.fail("raw exec", err)
	}
	return tag
}

func (s *pgService) dumpManifestRow(t *testing.T, project, generation string) string {
	t.Helper()
	var state, identity string
	err := s.iso.QueryRow(s.ctx,
		`SELECT state, schema_identity::text FROM "`+s.schema+`".dataset_generation_manifest WHERE project = $1 AND generation = $2`,
		project, generation).Scan(&state, &identity)
	if err != nil {
		return fmt.Sprintf("<no row: %s>", s.scrub(err))
	}
	return fmt.Sprintf("state=%s identity=%s", state, identity)
}

// assertManifestRowLiteral checks one manifest row against literal expected
// values: exact state text and exact schema identity document (jsonb
// equality against the canonical marshal of the expected snapshot).
func (s *pgService) assertManifestRowLiteral(t *testing.T, ref publication.Ref, wantState string, wantIdentity publication.SchemaSnapshot) {
	t.Helper()
	identity, err := json.Marshal(wantIdentity)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	err = s.iso.QueryRow(s.ctx,
		`SELECT true FROM "`+s.schema+`".dataset_generation_manifest
WHERE project = $1 AND generation = $2 AND state = $3 AND schema_identity = $4::jsonb`,
		ref.Project, ref.Generation, wantState, string(identity)).Scan(&found)
	if err != nil || !found {
		t.Fatalf("manifest row %s/%s does not match literal expectation state=%s identity=%s (actual: %s; err=%v)",
			ref.Project, ref.Generation, wantState, identity,
			s.dumpManifestRow(t, ref.Project, ref.Generation), s.scrub(err))
	}
}

type pointerWitness struct {
	generation string
	ctid       string
	updatedAt  string
	exists     bool
}

func (s *pgService) witnessPointer(t *testing.T, project string) pointerWitness {
	t.Helper()
	var w pointerWitness
	err := s.iso.QueryRow(s.ctx,
		`SELECT generation, ctid::text, updated_at::text FROM "`+s.schema+`".dataset_active_generation WHERE project = $1`,
		project).Scan(&w.generation, &w.ctid, &w.updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return w
	}
	if err != nil {
		s.fail("witness pointer", err)
	}
	w.exists = true
	return w
}

func (s *pgService) assertPointerRowLiteral(t *testing.T, project, wantGeneration string) {
	t.Helper()
	w := s.witnessPointer(t, project)
	if wantGeneration == "" {
		if w.exists {
			t.Fatalf("expected no active-generation pointer for %s, found generation=%s", project, w.generation)
		}
		return
	}
	if !w.exists || w.generation != wantGeneration {
		t.Fatalf("active-generation pointer for %s = %+v, want generation=%s", project, w, wantGeneration)
	}
}

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func mustRef(t *testing.T, project, generation string) publication.Ref {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("NewRef(%s, %s): %v", project, generation, err)
	}
	return ref
}

func mustSnapshot(t *testing.T, generation, digest string) publication.SchemaSnapshot {
	t.Helper()
	schema, err := publication.NewSchemaSnapshot("urn:pgstore03-"+generation, "R5", digest, []string{"Observation", "Patient"})
	if err != nil {
		t.Fatalf("NewSchemaSnapshot(%s): %v", generation, err)
	}
	return schema
}

// mustPersistStaged creates a LOADING manifest and transitions it to STAGED
// through the production store, without touching the active pointer.
func (s *pgService) mustPersistStaged(t *testing.T, store *Store, project, generation, digest string) publication.Manifest {
	t.Helper()
	ref := mustRef(t, project, generation)
	schema := mustSnapshot(t, generation, digest)
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest(%s/%s): %v", project, generation, err)
	}
	if _, err := store.CreateManifest(s.ctx, loading); err != nil {
		t.Fatalf("CreateManifest(%s/%s): %v", project, generation, err)
	}
	staged, err := store.TransitionManifest(s.ctx, loading, publication.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest(%s/%s -> STAGED): %v", project, generation, err)
	}
	if !staged.IsStaged() {
		t.Fatalf("manifest %s/%s not staged after transition: %s", project, generation, staged.State)
	}
	s.assertManifestRowLiteral(t, ref, string(publication.StateStaged), schema)
	return staged
}

const (
	digestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	digestC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	digestF = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
)

// ---------------------------------------------------------------------------
// lifecycle: literal persisted rows, identity bytes, typed conflicts
// ---------------------------------------------------------------------------

func TestManifestLifecyclePersistsLiteralRows(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	ref := mustRef(t, "pgstore03-project", "gen-1")
	schema := mustSnapshot(t, "gen-1", digestA)
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateManifest(s.ctx, loading)
	if err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	if !reflect.DeepEqual(created, loading) {
		t.Fatalf("CreateManifest returned %+v, want %+v", created, loading)
	}
	s.assertManifestRowLiteral(t, ref, "LOADING", schema)

	// ReadManifest returns the persisted manifest; the schema identity
	// bytes round-trip exactly.
	read, err := store.ReadManifest(s.ctx, ref)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if !reflect.DeepEqual(read, loading) {
		t.Fatalf("ReadManifest = %+v, want %+v", read, loading)
	}
	identityBytes, err := json.Marshal(read.SchemaIdentity)
	if err != nil {
		t.Fatal(err)
	}
	const wantIdentity = `{"schemaId":"urn:pgstore03-gen-1","fhirVersion":"R5","schemaSha256":"` + digestA + `","generatedResourceTypes":["Observation","Patient"]}`
	if string(identityBytes) != wantIdentity {
		t.Fatalf("schema identity bytes drifted: %s want %s", identityBytes, wantIdentity)
	}

	staged, err := store.TransitionManifest(s.ctx, loading, publication.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest: %v", err)
	}
	if !reflect.DeepEqual(staged, mustStagedCopy(loading)) {
		t.Fatalf("TransitionManifest = %+v", staged)
	}
	s.assertManifestRowLiteral(t, ref, "STAGED", schema)

	// READY is the retained legacy spelling of STAGED.
	ref2 := mustRef(t, "pgstore03-project", "gen-ready")
	schema2 := mustSnapshot(t, "gen-ready", digestB)
	loading2, err := publication.NewManifest(ref2, schema2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManifest(s.ctx, loading2); err != nil {
		t.Fatalf("CreateManifest gen-ready: %v", err)
	}
	if _, err := store.TransitionManifest(s.ctx, loading2, publication.StateReady); err != nil {
		t.Fatalf("TransitionManifest to READY: %v", err)
	}
	s.assertManifestRowLiteral(t, ref2, "READY", schema2)
}

func mustStagedCopy(m publication.Manifest) publication.Manifest {
	m.State = publication.StateStaged
	return m
}

func TestCreateManifestDuplicateAndInvalid(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	ref := mustRef(t, "pgstore03-project", "gen-dup")
	schema := mustSnapshot(t, "gen-dup", digestA)
	loading, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateManifest(s.ctx, loading); err != nil {
		t.Fatalf("first CreateManifest: %v", err)
	}

	// Same generation, same bytes: still a conflict, never an overwrite.
	if _, err := store.CreateManifest(s.ctx, loading); !errors.Is(err, ErrManifestAlreadyExists) {
		t.Fatalf("duplicate create: %v, want ErrManifestAlreadyExists", err)
	}
	// Same generation, different identity bytes: also a conflict; the
	// CAS is the generation identity, not the content.
	other := loading
	other.SchemaIdentity = mustSnapshot(t, "gen-dup", digestB)
	if _, err := store.CreateManifest(s.ctx, other); !errors.Is(err, ErrManifestAlreadyExists) {
		t.Fatalf("duplicate create with different identity: %v, want ErrManifestAlreadyExists", err)
	}
	s.assertManifestRowLiteral(t, ref, "LOADING", schema)

	// Only LOADING may be created.
	staged := loading
	staged.State = publication.StateStaged
	if _, err := store.CreateManifest(s.ctx, staged); !errors.Is(err, publication.ErrInvalidTransition) {
		t.Fatalf("create staged: %v, want ErrInvalidTransition", err)
	}
	// Invalid manifests are refused before any statement.
	bad := loading
	bad.SchemaIdentity.SchemaSHA256 = "not-a-digest"
	if _, err := store.CreateManifest(s.ctx, bad); !errors.Is(err, publication.ErrInvalidManifest) {
		t.Fatalf("create with bad digest: %v, want ErrInvalidManifest", err)
	}
	// Missing manifest reads as the public not-found sentinel.
	missing := mustRef(t, "pgstore03-project", "gen-none")
	if _, err := store.ReadManifest(s.ctx, missing); !errors.Is(err, publication.ErrManifestNotFound) {
		t.Fatalf("ReadManifest missing: %v, want ErrManifestNotFound", err)
	}
}

func TestTransitionManifestTypedConflictAndIllegalEdges(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	loading := func(generation, digest string) publication.Manifest {
		m, err := publication.NewManifest(mustRef(t, "pgstore03-project", generation), mustSnapshot(t, generation, digest))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// CAS mismatch: the caller's copy says LOADING but the row is STAGED.
	m := loading("gen-cas", digestA)
	if _, err := store.CreateManifest(s.ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionManifest(s.ctx, m, publication.StateStaged); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransitionManifest(s.ctx, m, publication.StateFailed); !errors.Is(err, ErrManifestTransitionConflict) {
		t.Fatalf("stale-state transition: %v, want ErrManifestTransitionConflict", err)
	}
	s.assertManifestRowLiteral(t, m.Dataset, "STAGED", m.SchemaIdentity)

	// CAS mismatch on immutable identity: correct state, edited identity.
	edited := m
	edited.SchemaIdentity = mustSnapshot(t, "gen-cas", digestF)
	if _, err := store.TransitionManifest(s.ctx, edited, publication.StateFailed); !errors.Is(err, ErrManifestTransitionConflict) {
		t.Fatalf("edited-identity transition: %v, want ErrManifestTransitionConflict", err)
	}
	s.assertManifestRowLiteral(t, m.Dataset, "STAGED", m.SchemaIdentity)

	// Illegal edges never reach the database.
	if _, err := store.TransitionManifest(s.ctx, m, publication.StateLoading); !errors.Is(err, publication.ErrInvalidTransition) {
		t.Fatalf("STAGED -> LOADING: %v, want ErrInvalidTransition", err)
	}
	failedSource := mustStagedCopy(m)
	failedSource.State = publication.StateFailed
	if _, err := store.TransitionManifest(s.ctx, failedSource, publication.StateStaged); !errors.Is(err, publication.ErrInvalidTransition) {
		t.Fatalf("FAILED -> STAGED: %v, want ErrInvalidTransition", err)
	}

	// LOADING -> FAILED works through the CAS path.
	doomed := loading("gen-fail", digestC)
	if _, err := store.CreateManifest(s.ctx, doomed); err != nil {
		t.Fatal(err)
	}
	failed, err := store.TransitionManifest(s.ctx, doomed, publication.StateFailed)
	if err != nil {
		t.Fatalf("LOADING -> FAILED: %v", err)
	}
	if failed.State != publication.StateFailed {
		t.Fatalf("failed manifest state = %s", failed.State)
	}
	s.assertManifestRowLiteral(t, doomed.Dataset, "FAILED", doomed.SchemaIdentity)
}

// ---------------------------------------------------------------------------
// activation CAS
// ---------------------------------------------------------------------------

func TestActivateFirstPointerAndIdempotentReActivation(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	const project = "pgstore03-project"
	staged := s.mustPersistStaged(t, store, project, "gen-1", digestA)

	if _, err := store.ResolveActiveManifest(s.ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("resolve before activation: %v, want ErrNoActiveGeneration", err)
	}
	s.assertPointerRowLiteral(t, project, "")

	if err := store.ActivateGeneration(s.ctx, staged, nil); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-1")

	// Repeated activation of the already-active exact manifest is
	// idempotent: success with zero writes (ctid and updated_at are
	// untouched, so no UPDATE ever ran).
	before := s.witnessPointer(t, project)
	for i := 0; i < 2; i++ {
		if err := store.ActivateGeneration(s.ctx, staged, nil); err != nil {
			t.Fatalf("idempotent activation %d: %v", i, err)
		}
	}
	after := s.witnessPointer(t, project)
	if after != before {
		t.Fatalf("idempotent re-activation rewrote the pointer row: before=%+v after=%+v", before, after)
	}

	resolved, err := store.ResolveActiveManifest(s.ctx, project)
	if err != nil {
		t.Fatalf("resolve after activation: %v", err)
	}
	if !reflect.DeepEqual(resolved, staged) {
		t.Fatalf("ResolveActiveManifest = %+v, want %+v", resolved, staged)
	}

	// The public resolver accepts the PG store as ActiveResolver.
	resolvedViaResolver, err := publication.ResolveActive(s.ctx, store, project)
	if err != nil || !reflect.DeepEqual(resolvedViaResolver, staged) {
		t.Fatalf("ResolveActive = %+v, %v", resolvedViaResolver, err)
	}

	// Pointer rows are per project: another project stays unset.
	other := s.mustPersistStaged(t, store, "pgstore03-other", "gen-1", digestB)
	if err := store.ActivateGeneration(s.ctx, other, nil); err != nil {
		t.Fatalf("activate other project: %v", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-1")
	s.assertPointerRowLiteral(t, "pgstore03-other", "gen-1")
}

func TestActivateRequiresStagedAndPersistedCandidate(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	const project = "pgstore03-project"

	// LOADING candidate: typed not-ready, no pointer created.
	loadingManifest := func(generation, digest string) publication.Manifest {
		m, err := publication.NewManifest(mustRef(t, project, generation), mustSnapshot(t, generation, digest))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	if err := store.ActivateGeneration(s.ctx, loadingManifest("gen-loading", digestA), nil); !errors.Is(err, publication.ErrGenerationNotReady) {
		t.Fatalf("activate LOADING: %v, want ErrGenerationNotReady", err)
	}
	s.assertPointerRowLiteral(t, project, "")

	// A manifest persisted as FAILED cannot become active.
	failedSource := loadingManifest("gen-failed", digestB)
	if _, err := store.CreateManifest(s.ctx, failedSource); err != nil {
		t.Fatal(err)
	}
	failed, err := store.TransitionManifest(s.ctx, failedSource, publication.StateFailed)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateGeneration(s.ctx, failed, nil); !errors.Is(err, publication.ErrGenerationNotReady) {
		t.Fatalf("activate FAILED: %v, want ErrGenerationNotReady", err)
	}
	s.assertPointerRowLiteral(t, project, "")

	// Candidate that was never persisted: typed conflict.
	ghost := loadingManifest("gen-ghost", digestC)
	ghost.State = publication.StateStaged
	if err := store.ActivateGeneration(s.ctx, ghost, nil); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate unpersisted: %v, want ErrActivationConflict", err)
	}

	// Staged candidate whose row moved to FAILED after the caller read it
	// (corruption path exercised with a direct UPDATE, then reactivated
	// with the stale staged value): typed conflict, pointer untouched.
	staged := s.mustPersistStaged(t, store, project, "gen-corrupt", digestA)
	s.rawExec(t, `UPDATE "`+s.schema+`".dataset_generation_manifest SET state = 'FAILED' WHERE project = $1 AND generation = $2`,
		project, "gen-corrupt")
	if err := store.ActivateGeneration(s.ctx, staged, nil); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate stale staged read: %v, want ErrActivationConflict", err)
	}
	s.assertPointerRowLiteral(t, project, "")

	// Staged candidate whose identity bytes were edited after the read.
	staged2 := s.mustPersistStaged(t, store, project, "gen-identity", digestB)
	tampered := staged2
	tampered.SchemaIdentity.SchemaSHA256 = digestF
	if err := store.ActivateGeneration(s.ctx, tampered, nil); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate edited identity: %v, want ErrActivationConflict", err)
	}
	s.assertPointerRowLiteral(t, project, "")

	// Expected Ref from a different project is an invalid argument.
	if _, err := store.ResolveActiveManifest(s.ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("precondition: %v", err)
	}
	wrongProject := mustRef(t, "pgstore03-other", "gen-identity")
	if err := store.ActivateGeneration(s.ctx, staged2, &wrongProject); !errors.Is(err, publication.ErrInvalidActiveGeneration) {
		t.Fatalf("cross-project expected: %v, want ErrInvalidActiveGeneration", err)
	}
}

func TestActivateCompareAndSwapAgainstPointerValue(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	const project = "pgstore03-project"
	g0 := s.mustPersistStaged(t, store, project, "gen-0", digestA)
	g1 := s.mustPersistStaged(t, store, project, "gen-1", digestB)
	g2 := s.mustPersistStaged(t, store, project, "gen-2", digestC)

	// First activation: expected nil (no active generation).
	if err := store.ActivateGeneration(s.ctx, g0, nil); err != nil {
		t.Fatalf("activate gen-0: %v", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-0")

	// Wrong expected value: typed conflict, pointer untouched.
	wrongExpected := mustRef(t, project, "gen-9")
	if err := store.ActivateGeneration(s.ctx, g1, &wrongExpected); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate with wrong expected: %v, want ErrActivationConflict", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-0")

	// Expected nil although gen-0 is active: conflict.
	if err := store.ActivateGeneration(s.ctx, g1, nil); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate expecting no pointer: %v, want ErrActivationConflict", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-0")

	// Correct expected value wins.
	expectedG0 := g0.Dataset
	if err := store.ActivateGeneration(s.ctx, g1, &expectedG0); err != nil {
		t.Fatalf("activate gen-1 with expected gen-0: %v", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-1")

	// The pointer has moved, so the same expected value is now stale:
	// the loser of the schedule gets the typed conflict.
	if err := store.ActivateGeneration(s.ctx, g2, &expectedG0); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("activate gen-2 with stale expected: %v, want ErrActivationConflict", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-1")

	// And again with the correct expected value: winner.
	expectedG1 := g1.Dataset
	if err := store.ActivateGeneration(s.ctx, g2, &expectedG1); err != nil {
		t.Fatalf("activate gen-2 with expected gen-1: %v", err)
	}
	s.assertPointerRowLiteral(t, project, "gen-2")
}

// TestConcurrentActivationsSameExpectedYieldOneWinner is the CAS schedule
// test: two concurrent activations present the same expected generation.
// Exactly one wins and the loser receives the typed conflict. Runs serially
// with respect to every other test (the suite never calls t.Parallel).
func TestConcurrentActivationsSameExpectedYieldOneWinner(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	const project = "pgstore03-project"
	g0 := s.mustPersistStaged(t, store, project, "gen-0", digestA)
	if err := store.ActivateGeneration(s.ctx, g0, nil); err != nil {
		t.Fatalf("baseline activation: %v", err)
	}
	g1 := s.mustPersistStaged(t, store, project, "gen-1", digestB)
	g2 := s.mustPersistStaged(t, store, project, "gen-2", digestC)

	candidates := []publication.Manifest{g1, g2}
	results := make([]error, len(candidates))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			expected := g0.Dataset
			results[i] = store.ActivateGeneration(s.ctx, candidates[i], &expected)
		}(i)
	}
	close(start)
	wg.Wait()

	winner, loser := -1, -1
	for i, err := range results {
		t.Logf("activation %d (candidate %s): err=%v", i, candidates[i].Dataset.Generation, err)
		if err == nil {
			if winner != -1 {
				t.Fatalf("both concurrent activations won: %v", results)
			}
			winner = i
			continue
		}
		if !errors.Is(err, ErrActivationConflict) {
			t.Fatalf("activation %d failed with untyped error: %v", i, err)
		}
		if loser != -1 {
			t.Fatalf("both concurrent activations lost: %v", results)
		}
		loser = i
	}
	if winner == -1 || loser == -1 {
		t.Fatalf("expected exactly one winner and one typed-conflict loser, got %v", results)
	}
	s.assertPointerRowLiteral(t, project, candidates[winner].Dataset.Generation)

	// The loser may re-read the pointer and converge: with the fresh
	// expected value its activation wins.
	fresh := candidates[winner].Dataset
	if err := store.ActivateGeneration(s.ctx, candidates[loser], &fresh); err != nil {
		t.Fatalf("loser re-activation with fresh expected: %v", err)
	}
	s.assertPointerRowLiteral(t, project, candidates[loser].Dataset.Generation)
}

// ---------------------------------------------------------------------------
// failure before commit
// ---------------------------------------------------------------------------

func TestInjectedFailureBeforeCommitLeavesManifestAndPointerUnchanged(t *testing.T) {
	s := newPGService(t)
	store, client := s.openStore(t)

	const project = "pgstore03-project"
	g0 := s.mustPersistStaged(t, store, project, "gen-0", digestA)
	g1 := s.mustPersistStaged(t, store, project, "gen-1", digestB)
	if err := store.ActivateGeneration(s.ctx, g0, nil); err != nil {
		t.Fatalf("baseline activation: %v", err)
	}
	pointerBefore := s.witnessPointer(t, project)

	// Injected failure before COMMIT on the activation path.
	store.commitFault = func(string) error { return errors.New("injected commit failure") }
	if err := store.ActivateGeneration(s.ctx, g1, &g0.Dataset); err == nil {
		t.Fatal("activation with injected commit fault returned nil")
	} else if errors.Is(err, ErrActivationConflict) {
		t.Fatalf("commit fault misclassified as CAS conflict: %v", err)
	}
	store.commitFault = nil

	// The pointer row and both manifest rows are byte-identical.
	after := s.witnessPointer(t, project)
	if after != pointerBefore {
		t.Fatalf("faulted activation moved the pointer: before=%+v after=%+v", pointerBefore, after)
	}
	s.assertPointerRowLiteral(t, project, "gen-0")
	s.assertManifestRowLiteral(t, g0.Dataset, "STAGED", g0.SchemaIdentity)
	s.assertManifestRowLiteral(t, g1.Dataset, "STAGED", g1.SchemaIdentity)

	// Same on the transition path: a faulted LOADING -> STAGED leaves the
	// row LOADING.
	doomed := func() publication.Manifest {
		m, err := publication.NewManifest(mustRef(t, project, "gen-faulty"), mustSnapshot(t, "gen-faulty", digestC))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}()
	if _, err := store.CreateManifest(s.ctx, doomed); err != nil {
		t.Fatal(err)
	}
	store.commitFault = func(string) error { return errors.New("injected commit failure") }
	if _, err := store.TransitionManifest(s.ctx, doomed, publication.StateStaged); err == nil {
		t.Fatal("transition with injected commit fault returned nil")
	}
	store.commitFault = nil
	s.assertManifestRowLiteral(t, doomed.Dataset, "LOADING", doomed.SchemaIdentity)

	// Readback after reopen: a brand-new pool (new physical connections)
	// returns the committed values, not the faulted writes.
	client.Close()
	reopened, _ := s.openStore(t)
	resolved, err := reopened.ResolveActiveManifest(s.ctx, project)
	if err != nil || !reflect.DeepEqual(resolved, g0) {
		t.Fatalf("resolve after reopen = %+v, %v; want %+v", resolved, err, g0)
	}
	if read, err := reopened.ReadManifest(s.ctx, g1.Dataset); err != nil || !reflect.DeepEqual(read, g1) {
		t.Fatalf("read gen-1 after reopen = %+v, %v", read, err)
	}
	if read, err := reopened.ReadManifest(s.ctx, doomed.Dataset); err != nil || read.State != publication.StateLoading {
		t.Fatalf("read faulted manifest after reopen = %+v, %v; want LOADING", read, err)
	}
}

// ---------------------------------------------------------------------------
// negative schema fixtures: SQL NULL and invalid states are refused
// ---------------------------------------------------------------------------

func TestSchemaConstraintsRejectInvalidRows(t *testing.T) {
	s := newPGService(t)
	store, _ := s.openStore(t)

	// Seed one valid manifest for the FK/pointer fixtures.
	staged := s.mustPersistStaged(t, store, "pgstore03-project", "gen-fk", digestA)

	type negative struct {
		name      string
		sql       string
		args      []any
		wantState string
	}
	identity := `{"schemaSha256":"` + digestA + `","generatedResourceTypes":["Patient"]}`
	cases := []negative{
		{
			name:      "public state names only",
			sql:       `INSERT INTO "%s".dataset_generation_manifest (project, generation, state, schema_identity) VALUES ('p', 'g', 'BOGUS', $1::jsonb)`,
			args:      []any{identity},
			wantState: "23514",
		},
		{
			name:      "state is never SQL NULL",
			sql:       `INSERT INTO "%s".dataset_generation_manifest (project, generation, state, schema_identity) VALUES ('p', 'g', NULL, $1::jsonb)`,
			args:      []any{identity},
			wantState: "23502",
		},
		{
			name:      "schema identity is never SQL NULL",
			sql:       `INSERT INTO "%s".dataset_generation_manifest (project, generation, state, schema_identity) VALUES ('p', 'g', 'LOADING', NULL)`,
			args:      nil,
			wantState: "23502",
		},
		{
			name:      "schema identity must be a JSON object, not JSON null",
			sql:       `INSERT INTO "%s".dataset_generation_manifest (project, generation, state, schema_identity) VALUES ('p', 'g', 'LOADING', 'null'::jsonb)`,
			args:      nil,
			wantState: "23514",
		},
		{
			name:      "pointer requires an existing manifest row",
			sql:       `INSERT INTO "%s".dataset_active_generation (project, generation) VALUES ('p', 'missing')`,
			args:      nil,
			wantState: "23503",
		},
		{
			name:      "pointer generation is never SQL NULL",
			sql:       `INSERT INTO "%s".dataset_active_generation (project, generation) VALUES ('p', NULL)`,
			args:      nil,
			wantState: "23502",
		},
	}
	for _, c := range cases {
		_, err := s.iso.Exec(s.ctx, fmt.Sprintf(c.sql, s.schema), c.args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != c.wantState {
			t.Errorf("%s: err = %v, want SQLSTATE %s", c.name, s.scrub(err), c.wantState)
		}
	}

	// Duplicate pointer rows for one project are impossible.
	if err := store.ActivateGeneration(s.ctx, staged, nil); err != nil {
		t.Fatalf("activate for FK fixture: %v", err)
	}
	_, err := s.iso.Exec(s.ctx,
		`INSERT INTO "`+s.schema+`".dataset_active_generation (project, generation) VALUES ($1, $2)`,
		"pgstore03-project", "gen-fk")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate pointer insert: err = %v, want SQLSTATE 23505", s.scrub(err))
	}
}

// ---------------------------------------------------------------------------
// zero leftovers: the suite must not leave any pgstore03_it_* resource
// ---------------------------------------------------------------------------

func TestZZNoLeftoverOwnedResources(t *testing.T) {
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
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'pgstore03\_it\_%' ESCAPE '\'`).Scan(&leftoverDBs); err != nil {
		t.Fatalf("count leftover databases: %v", err)
	}
	if leftoverDBs != 0 {
		var names []string
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgstore03\_it\_%' ESCAPE '\'`)
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
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgstore03\_it\_%' ESCAPE '\'`).Scan(&leftoverSchemas); err != nil {
		t.Fatalf("count leftover schemas: %v", err)
	}
	// This second probe guards a different leak than the database check:
	// owned schemas are only ever created inside the isolated databases and
	// vanish when those databases are dropped, so a hit here can only mean a
	// pgstore03_it_* schema leaked into the shared service database itself.
	if leftoverSchemas != 0 {
		t.Fatalf("%d owned schemas left behind in the service database", leftoverSchemas)
	}
}
