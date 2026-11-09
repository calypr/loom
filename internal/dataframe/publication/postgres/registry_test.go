package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/dataframe/publication"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
)

// Real PostgreSQL 18 integration (service-gated by LOOM_TEST_POSTGRES_URL).
// Every acceptance assertion below runs against a real server: the tests
// create one uniquely owned pgpubcat01_it_* database and one owned schema
// per test, apply the catalog migration through the real pg-store-02 runner,
// and assert literal expected rows. A skipped required case is a failure for
// this ticket's acceptance, so the check is run with the service configured;
// the gate exists only to keep unrelated CI lanes green.

var pgpubcat01ResourceNamePattern = regexp.MustCompile(`^pgpubcat01_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 publication catalog integration tests")
	}
	return raw
}

func randomHex() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// catalogService owns one isolated database and one owned schema inside it,
// both named pgpubcat01_it_*, plus the migrated publication bundle catalog
// and a registry bound to that schema. Nothing outside these two resources
// is touched; TestMain audits that none survive.
type catalogService struct {
	ctx      context.Context
	admin    *pgxpool.Pool // service database
	iso      *pgxpool.Pool // isolated database
	schema   string
	registry *Registry
	scrub    func(any) string
	fail     func(step string, err error)
}

func newCatalogService(t *testing.T) *catalogService {
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

	dbName := fmt.Sprintf("pgpubcat01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgpubcat01ResourceNamePattern.MatchString(dbName) {
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

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("pgpubcat01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgpubcat01ResourceNamePattern.MatchString(schema) {
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

	s := &catalogService{ctx: ctx, admin: admin, iso: iso, schema: schema, scrub: scrub, fail: fail}
	s.migrate(t)
	registry, err := New(iso, schema)
	if err != nil {
		fail("build registry", err)
	}
	s.registry = registry
	return s
}

// migrate applies the full history -- the embedded ledger bootstrap plus the
// ticket's tests/migrations/000004_publication_bundle_catalog.sql -- through
// the real pg-store-02 runner, and asserts the ledger literally records both
// rows.
func (s *catalogService) migrate(t *testing.T) {
	t.Helper()
	embedded, err := storepostgres.EmbeddedMigrations()
	if err != nil {
		s.fail("embedded migrations", err)
	}
	catalog, err := storepostgres.LoadMigrations(os.DirFS("../../../../"), "tests/migrations")
	if err != nil {
		s.fail("load tests/migrations", err)
	}
	if len(catalog) != 1 || catalog[0].Version != 4 || catalog[0].Name != "publication_bundle_catalog" {
		t.Fatalf("tests/migrations history = %+v, want exactly version 4 publication_bundle_catalog", catalog)
	}
	runner, err := storepostgres.NewMigrationRunner(s.iso, s.schema, append(append([]storepostgres.Migration(nil), embedded...), catalog...))
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate catalog", err)
	}
	if result.LatestVersion != 4 {
		t.Fatalf("latest migration version = %d, want 4", result.LatestVersion)
	}
	wantLedger := []storepostgres.LedgerRecord{
		{Version: 1, Name: "ledger"},
		{Version: 4, Name: "publication_bundle_catalog"},
	}
	if len(result.Applied) != len(wantLedger) {
		t.Fatalf("applied history = %+v, want %+v", result.Applied, wantLedger)
	}
	for i, want := range wantLedger {
		got := result.Applied[i]
		if got.Version != want.Version || got.Name != want.Name {
			t.Fatalf("applied[%d] = {Version:%d Name:%q}, want {Version:%d Name:%q}",
				i, got.Version, got.Name, want.Version, want.Name)
		}
	}
}

// mustExec runs a statement and fails the test on error.
func (s *catalogService) mustExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("exec "+sql, err)
	}
}

// execCount runs a count query and returns the value.
func (s *catalogService) execCount(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&n); err != nil {
		s.fail("count "+sql, err)
	}
	return n
}

// witnessText runs a single-text-value query, returning ok=false only for
// zero rows.
func (s *catalogService) witnessText(t *testing.T, sql string, args ...any) (string, bool) {
	t.Helper()
	var value string
	err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		s.fail("witness "+sql, err)
	}
	return value, true
}

// mustWitnessText fails when the query returns zero rows.
func (s *catalogService) mustWitnessText(t *testing.T, sql string, args ...any) string {
	t.Helper()
	value, ok := s.witnessText(t, sql, args...)
	if !ok {
		t.Fatalf("witness query returned no rows: %s", sql)
	}
	return value
}

// mustWitnessBool fails when the query returns zero rows.
func (s *catalogService) mustWitnessBool(t *testing.T, sql string, args ...any) bool {
	t.Helper()
	var value bool
	err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&value)
	if err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

// mustWitnessInt64 fails when the query returns zero rows.
func (s *catalogService) mustWitnessInt64(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var value int64
	err := s.iso.QueryRow(s.ctx, sql, args...).Scan(&value)
	if err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *catalogService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

// acquireLease claims a lease or dies.
func (s *catalogService) acquireLease(t *testing.T, key, owner string, expires time.Time) {
	t.Helper()
	claimed, err := s.registry.AcquireBundleLease(s.ctx, key, owner, expires)
	if err != nil || !claimed {
		s.fail(fmt.Sprintf("acquire lease %s/%s", key, owner), fmt.Errorf("claimed=%v err=%w", claimed, err))
	}
}

// saveExecution saves under a fence or dies.
func (s *catalogService) saveExecution(t *testing.T, execution publication.BundleExecution, fenceOwner string) {
	t.Helper()
	if err := s.registry.SaveExecution(s.ctx, execution, fenceOwner); err != nil {
		s.fail("save execution "+execution.ID, err)
	}
}

// baseExecution builds a fully populated execution for the standard scope
// project=alpha generation=gen-1 recipe=panel translation=v1.
func baseExecution(id, key string, state publication.BundleState, stamps executionStamps) publication.BundleExecution {
	createdAt := stamps.created
	updatedAt := stamps.updated
	execution := publication.BundleExecution{
		ID:  id,
		Key: key,
		BundleIdentity: publication.BundleIdentity{
			Name:               "panel",
			TranslationVersion: "v1",
			Project:            "alpha",
			DatasetGeneration:  "gen-1",
			RecipeDigest:       "sha256:recipe",
			SchemaDigest:       "sha256:schema",
			ScopeDigest:        "sha256:scope",
			EngineVersion:      "loom-engine-3",
			AuthScopeMode:      "project",
			AuthResourcePaths:  []string{"fhir/Patient", "fhir/Observation"},
		},
		State:     state,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
		ReadyAt:   stamps.ready,
	}
	if stamps.published != nil {
		execution.PublishedAt = stamps.published
	}
	if state.Successful() || state == publication.BundleRunning {
		execution.Outputs = []publication.BundleOutputRecord{
			{
				Name:          "out-a",
				PhysicalTable: "phys_alpha_out_a",
				Selector:      execution.Selector("out-a"),
				Columns: []publication.PhysicalColumn{
					{ID: "c1", Name: "subject", SemanticPath: "Patient.id", ClickHouse: "String", LogicalType: "ResourceIdentifier", Provenance: publication.ColumnExplicit, LoomOwned: true},
					{ID: "c2", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "Quantity", Nullable: true, Repeated: true},
				},
				RowCount:  1234,
				ByteCount: 567890,
				State:     state.Canonical(),
				SourceRow: &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "subject"},
			},
			{
				Name:          "out-b",
				PhysicalTable: "phys_alpha_out_b",
				Selector:      execution.Selector("out-b"),
				RowCount:      7,
				State:         state.Canonical(),
			},
		}
		if state.Successful() {
			verified := updatedAt
			for i := range execution.Outputs {
				execution.Outputs[i].VerifiedAt = &verified
			}
		}
	}
	return execution
}

type executionStamps struct {
	created   time.Time
	updated   time.Time
	ready     *time.Time
	published *time.Time
}

func stamps(created, updated, ready, published time.Time) executionStamps {
	st := executionStamps{created: created, updated: updated}
	if !ready.IsZero() {
		st.ready = &ready
	}
	if !published.IsZero() {
		st.published = &published
	}
	return st
}

var (
	baseTime    = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	createdTime = baseTime
	updatedTime = baseTime.Add(time.Minute)
	readyTime   = baseTime.Add(90 * time.Second)
	pubTime     = baseTime.Add(2 * time.Minute)
)

// ---------------------------------------------------------------------------
// Construction contract (no server needed).
// ---------------------------------------------------------------------------

func TestNewRejectsNilPoolAndUnsafeSchemas(t *testing.T) {
	if _, err := New(nil, "ok_schema"); err == nil {
		t.Fatal("New(nil pool) = nil error, want rejection")
	}
	for _, schema := range []string{"", "Public", "a;drop", "0starts_with_digit", "has space", "tab\tname", strings.Repeat("a", 64)} {
		_, err := New(neverUsedPool{}, schema)
		if !errors.Is(err, ErrInvalidSchema) {
			t.Fatalf("New(schema %q) = %v, want ErrInvalidSchema", schema, err)
		}
	}
}

type neverUsedPool struct{}

func (neverUsedPool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("never used")
}
func (neverUsedPool) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("never used")
}
func (neverUsedPool) QueryRow(context.Context, string, ...any) pgx.Row {
	return nil
}
func (neverUsedPool) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("never used") }

func TestPointerKeyDigestDiscipline(t *testing.T) {
	name := "HTAN_INT-BForePC\x00\x00aced-meta-default"
	first := pointerKey(name)
	second := pointerKey(name)
	if first != second {
		t.Fatalf("pointer key is not deterministic: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("pointer key is not a digest-safe hex key: %q", first)
	}
	if first == pointerKey(name+"\x00") {
		t.Fatal("distinct names collide on pointer key")
	}
}

// ---------------------------------------------------------------------------
// Metadata-only schema (ClickHouse remains the publication data plane).
// ---------------------------------------------------------------------------

func TestCatalogSchemaHoldsMetadataOnly(t *testing.T) {
	s := newCatalogService(t)
	rows, err := s.iso.Query(s.ctx,
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
		"schema_migrations",
	}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("catalog schema tables = %#v, want exactly %#v (metadata only; no row-store tables)", tables, want)
	}
}

// ---------------------------------------------------------------------------
// Save/read fidelity and columns.
// ---------------------------------------------------------------------------

func TestSaveExecutionRoundTripAndLiteralColumns(t *testing.T) {
	s := newCatalogService(t)
	s.acquireLease(t, "bundle-key", "publisher-a", time.Now().UTC().Add(time.Minute))
	execution := baseExecution("exec-full", "bundle-key", publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	s.saveExecution(t, execution, "publisher-a")

	got, err := s.registry.GetExecution(s.ctx, "exec-full")
	if err != nil {
		s.fail("get execution", err)
	}
	// Payload fidelity: the decoded execution must re-marshal byte-identically.
	wantJSON := mustMarshal(t, execution)
	gotJSON := mustMarshal(t, got)
	if wantJSON != gotJSON {
		t.Fatalf("round-tripped execution differs:\n got %s\nwant %s", gotJSON, wantJSON)
	}

	// Literal scope columns, ordered SQL parameters written by the registry.
	var (
		gotKey, gotProject, gotGeneration, gotRecipe, gotTV, gotState string
		gotCreated, gotUpdated                                        time.Time
		gotReady, gotPublished                                        *time.Time
	)
	err = s.iso.QueryRow(s.ctx,
		"SELECT execution_key, project, dataset_generation, recipe_name, translation_version, state,"+
			" created_at, updated_at, ready_at, published_at FROM "+s.qualified(BundleExecutionsTable)+
			" WHERE id = $1", "exec-full").
		Scan(&gotKey, &gotProject, &gotGeneration, &gotRecipe, &gotTV,
			&gotState, &gotCreated, &gotUpdated, &gotReady, &gotPublished)
	if err != nil {
		s.fail("witness columns", err)
	}
	if gotKey != "bundle-key" || gotProject != "alpha" || gotGeneration != "gen-1" ||
		gotRecipe != "panel" || gotTV != "v1" || gotState != "RUNNING" {
		t.Fatalf("stored scope columns = {%s %s %s %s %s %s}, want {bundle-key alpha gen-1 panel v1 RUNNING}",
			gotKey, gotProject, gotGeneration, gotRecipe, gotTV, gotState)
	}
	if !gotCreated.Equal(createdTime) || !gotUpdated.Equal(updatedTime) {
		t.Fatalf("stored stamps = %v/%v, want %v/%v", gotCreated, gotUpdated, createdTime, updatedTime)
	}
	if gotReady == nil || !gotReady.Equal(readyTime) || gotPublished != nil {
		t.Fatalf("stored nullable stamps = ready %v published %v, want ready %v published NULL",
			gotReady, gotPublished, readyTime)
	}
	// The payload stores the execution as given; the state column matches it.
	if payloadState := s.mustWitnessText(t,
		"SELECT payload->>'state' FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-full"); payloadState != "RUNNING" {
		t.Fatalf("payload state = %q, want RUNNING", payloadState)
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(data)
}

// ---------------------------------------------------------------------------
// Lease fencing.
// ---------------------------------------------------------------------------

func TestSaveExecutionRequiresActiveFence(t *testing.T) {
	s := newCatalogService(t)
	execution := baseExecution("exec-fence", "bundle-key", publication.BundleRunning, stamps(createdTime, updatedTime, time.Time{}, time.Time{}))

	// Empty identity, key, or fence owner are lost fences, no server query.
	noID := execution
	noID.ID = ""
	noKey := execution
	noKey.Key = ""
	for name, candidate := range map[string]publication.BundleExecution{"empty id": noID, "empty key": noKey} {
		if err := s.registry.SaveExecution(s.ctx, candidate, "publisher-a"); !errors.Is(err, publication.ErrBundleLeaseLost) {
			t.Fatalf("SaveExecution(%s) = %v, want ErrBundleLeaseLost", name, err)
		}
	}
	if err := s.registry.SaveExecution(s.ctx, execution, ""); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution(empty owner) = %v, want ErrBundleLeaseLost", err)
	}
	// No lease at all: the write is refused and nothing is stored.
	if err := s.registry.SaveExecution(s.ctx, execution, "publisher-a"); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution(without lease) = %v, want ErrBundleLeaseLost", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleExecutionsTable)); n != 0 {
		t.Fatalf("executions stored after refused saves = %d, want 0", n)
	}

	// A live lease saves; after the owner's own lease expires and another
	// owner takes over, the old owner's checkpoint is refused and the new
	// owner's record survives untouched.
	s.acquireLease(t, "bundle-key", "publisher-a", time.Now().UTC().Add(time.Minute))
	s.saveExecution(t, execution, "publisher-a")
	newer := execution
	newer.Error = "owned by publisher-b"
	// The same owner legally re-claims with a past expiry: the fence is now
	// expired, so publisher-b can take over.
	s.acquireLease(t, "bundle-key", "publisher-a", time.Now().UTC().Add(-time.Second))
	s.acquireLease(t, "bundle-key", "publisher-b", time.Now().UTC().Add(time.Hour))
	if err := s.registry.SaveExecution(s.ctx, newer, "publisher-a"); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution after takeover = %v, want ErrBundleLeaseLost", err)
	}
	if payload := s.mustWitnessText(t,
		"SELECT COALESCE(payload->>'error', '') FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-fence"); payload != "" {
		t.Fatalf("stale owner mutated payload error = %q, want untouched", payload)
	}
	s.saveExecution(t, newer, "publisher-b")
	if payload := s.mustWitnessText(t,
		"SELECT COALESCE(payload->>'error', '') FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-fence"); payload != "owned by publisher-b" {
		t.Fatalf("payload error = %q, want owned by publisher-b", payload)
	}
}

func TestLeaseLifecycleAndRepeatedOwnerCorrelation(t *testing.T) {
	s := newCatalogService(t)
	leaseTable := s.qualified(BundleLeasesTable)
	epoch := func() int64 {
		return s.mustWitnessInt64(t, "SELECT epoch FROM "+leaseTable+" WHERE key = $1", "bundle-l")
	}
	owner := func() string {
		return s.mustWitnessText(t, "SELECT owner_id FROM "+leaseTable+" WHERE key = $1", "bundle-l")
	}

	claimed, err := s.registry.AcquireBundleLease(s.ctx, "bundle-l", "owner-a", time.Now().UTC().Add(time.Hour))
	if err != nil || !claimed {
		s.fail("first acquire", fmt.Errorf("claimed=%v err=%w", claimed, err))
	}
	if epoch() != 1 || owner() != "owner-a" {
		t.Fatalf("after first acquire: epoch=%d owner=%q, want 1 owner-a", epoch(), owner())
	}

	// A live lease refuses a different owner and a foreign renew/release.
	claimed, err = s.registry.AcquireBundleLease(s.ctx, "bundle-l", "owner-b", time.Now().UTC().Add(time.Hour))
	if err != nil || claimed {
		t.Fatalf("acquire held lease = %v, %v; want false, nil", claimed, err)
	}
	if renewed, err := s.registry.RenewBundleLease(s.ctx, "bundle-l", "owner-b", time.Now().UTC().Add(2*time.Hour)); err != nil || renewed {
		t.Fatalf("foreign renew = %v, %v; want false, nil", renewed, err)
	}
	if err := s.registry.ReleaseBundleLease(s.ctx, "bundle-l", "owner-b"); err != nil {
		s.fail("foreign release", err)
	}
	if owner() != "owner-a" {
		t.Fatalf("foreign release removed the fence: owner = %q", owner())
	}
	if renewed, err := s.registry.RenewBundleLease(s.ctx, "bundle-l", "owner-a", time.Now().UTC().Add(2*time.Hour)); err != nil || !renewed {
		t.Fatalf("owner renew = %v, %v; want true, nil", renewed, err)
	}

	// The owner releases cleanly.
	if err := s.registry.ReleaseBundleLease(s.ctx, "bundle-l", "owner-a"); err != nil {
		s.fail("owner release", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+leaseTable+" WHERE key = $1", "bundle-l"); n != 0 {
		t.Fatalf("lease rows after owner release = %d, want 0", n)
	}

	// Expired leases are claimable by anyone, and a same-owner reclaim of an
	// expired lease is allowed; each grant bumps epoch so witnesses can
	// correlate the sequence of ownership grants on one key.
	s.acquireLease(t, "bundle-l", "owner-a", time.Now().UTC().Add(time.Hour))
	if epoch() != 1 {
		t.Fatalf("re-grant after release epoch = %d, want 1", epoch())
	}
	s.acquireLease(t, "bundle-l", "owner-a", time.Now().UTC().Add(-time.Minute))
	if epoch() != 2 || owner() != "owner-a" {
		t.Fatalf("same-owner expired reclaim: epoch=%d owner=%q, want 2 owner-a", epoch(), owner())
	}
	claimed, err = s.registry.AcquireBundleLease(s.ctx, "bundle-l", "owner-b", time.Now().UTC().Add(time.Hour))
	if err != nil || !claimed {
		t.Fatalf("claim after expiry = %v, %v; want true, nil", claimed, err)
	}
	if epoch() != 3 || owner() != "owner-b" {
		t.Fatalf("takeover after expiry: epoch=%d owner=%q, want 3 owner-b", epoch(), owner())
	}
}

// ---------------------------------------------------------------------------
// Pointers: exact CAS, NUL-safe names, collision behavior.
// ---------------------------------------------------------------------------

func TestPointerCompareAndSwapLiteralRows(t *testing.T) {
	s := newCatalogService(t)
	name := "5:alpha8:gen-15:panel2:v1"
	key := pointerKey(name)

	if err := s.registry.CompareAndSwapPointer(s.ctx, name, "", "exec-1"); err != nil {
		s.fail("initial CAS", err)
	}
	pointer, err := s.registry.GetPointer(s.ctx, name)
	if err != nil {
		s.fail("get pointer", err)
	}
	if pointer.Name != name || pointer.ExecutionID != "exec-1" {
		t.Fatalf("pointer = %+v, want name %q exec-1", pointer, name)
	}

	// A stale expected value conflicts and leaves the literal row untouched.
	if err := s.registry.CompareAndSwapPointer(s.ctx, name, "exec-stale", "exec-2"); !errors.Is(err, publication.ErrBundlePointerConflict) {
		t.Fatalf("stale CAS = %v, want ErrBundlePointerConflict", err)
	}
	if got := s.mustWitnessText(t,
		"SELECT execution_id FROM "+s.qualified(BundlePointersTable)+" WHERE pointer_key = $1", key); got != "exec-1" {
		t.Fatalf("execution_id after stale CAS = %q, want exec-1", got)
	}

	// The current owner's expected value wins.
	if err := s.registry.CompareAndSwapPointer(s.ctx, name, "exec-1", "exec-2"); err != nil {
		s.fail("owner CAS", err)
	}
	if got := s.mustWitnessText(t,
		"SELECT execution_id FROM "+s.qualified(BundlePointersTable)+" WHERE pointer_key = $1", key); got != "exec-2" {
		t.Fatalf("execution_id after owner CAS = %q, want exec-2", got)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundlePointersTable)+" WHERE pointer_key = $1", key); n != 1 {
		t.Fatalf("pointer rows = %d, want 1", n)
	}
}

func TestPointerNamesWithNULRoundTripAndNoCollisionFold(t *testing.T) {
	s := newCatalogService(t)

	// The Repair-5 class: identities whose bare-join names would fold still
	// have distinct PointerName() values, distinct keys, and independent
	// pointers. Domain results must be unchanged by the PG backend.
	foldA := publication.BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a\x00b"}
	foldB := publication.BundleIdentity{Project: "p", DatasetGeneration: "g", Name: "a", TranslationVersion: "b"}
	if foldA.PointerName() == foldB.PointerName() {
		t.Fatal("folded identities share a PointerName; domain encoding regressed")
	}
	if foldA.Key() == foldB.Key() {
		t.Fatal("folded identities share a Key; domain encoding regressed")
	}

	nulName := "HTAN_INT-BForePC\x00\x00aced-meta-default"
	if err := s.registry.CompareAndSwapPointer(s.ctx, nulName, "", "exec-nul"); err != nil {
		s.fail("NUL-name CAS", err)
	}
	pointer, err := s.registry.GetPointer(s.ctx, nulName)
	if err != nil {
		s.fail("get NUL pointer", err)
	}
	if pointer.Name != nulName || pointer.ExecutionID != "exec-nul" {
		t.Fatalf("NUL pointer = %+v, want exact byte round trip of %q", pointer, nulName)
	}
	if raw := s.mustWitnessText(t,
		"SELECT encode(name_bytes, 'hex') FROM "+s.qualified(BundlePointersTable)+" WHERE pointer_key = $1", pointerKey(nulName)); raw != hex.EncodeToString([]byte(nulName)) {
		t.Fatalf("stored name_bytes = %s, want %s", raw, hex.EncodeToString([]byte(nulName)))
	}

	// Both folded pointers coexist as distinct rows pointing at distinct
	// executions -- publishing the second never repoints the first.
	if err := s.registry.CompareAndSwapPointer(s.ctx, foldA.PointerName(), "", "exec-a"); err != nil {
		s.fail("fold A CAS", err)
	}
	if err := s.registry.CompareAndSwapPointer(s.ctx, foldB.PointerName(), "", "exec-b"); err != nil {
		s.fail("fold B CAS", err)
	}
	first, err := s.registry.GetPointer(s.ctx, foldA.PointerName())
	if err != nil || first.ExecutionID != "exec-a" {
		t.Fatalf("fold A pointer = %+v, %v; want exec-a", first, err)
	}
	second, err := s.registry.GetPointer(s.ctx, foldB.PointerName())
	if err != nil || second.ExecutionID != "exec-b" {
		t.Fatalf("fold B pointer = %+v, %v; want exec-b", second, err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundlePointersTable)); n != 3 {
		t.Fatalf("pointer rows = %d, want 3", n)
	}
}

// ---------------------------------------------------------------------------
// Atomic publish under the lease fence.
// ---------------------------------------------------------------------------

func TestPublishExecutionCommitsPointerAndStateAtomically(t *testing.T) {
	s := newCatalogService(t)
	running := baseExecution("exec-pub", "bundle-pub", publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	s.acquireLease(t, "bundle-pub", "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, running, "publisher-a")

	name := "5:alpha8:gen-15:panel2:v1"
	published := running
	published.OwnerID = "publisher-a"
	published.State = publication.BundlePublished
	published.UpdatedAt = pubTime
	published.PublishedAt = &pubTime
	if err := s.registry.PublishExecution(s.ctx, name, "", published); err != nil {
		s.fail("publish", err)
	}

	got, err := s.registry.GetExecution(s.ctx, "exec-pub")
	if err != nil {
		s.fail("get published", err)
	}
	if got.State != publication.BundlePublished || got.PublishedAt == nil || !got.PublishedAt.Equal(pubTime) {
		t.Fatalf("published execution = state %q publishedAt %v, want PUBLISHED at %s", got.State, got.PublishedAt, pubTime)
	}
	pointer, err := s.registry.GetPointer(s.ctx, name)
	if err != nil {
		s.fail("get pointer", err)
	}
	if pointer.ExecutionID != "exec-pub" || !pointer.UpdatedAt.Equal(pubTime) {
		t.Fatalf("pointer = %+v, want exec-pub updated at %s", pointer, pubTime)
	}
}

func TestPublishExecutionRollsBackEverythingOnConflict(t *testing.T) {
	s := newCatalogService(t)
	name := "5:alpha8:gen-15:panel2:v1"

	// Expired fence: conflict, and neither the pointer nor the execution
	// moves. The registry never falls back to an unfenced write.
	expired := baseExecution("exec-exp", "bundle-exp", publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	s.acquireLease(t, "bundle-exp", "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, expired, "publisher-a")
	// Same owner re-claims with a past expiry: the grant is legal, the fence
	// is now expired for writers.
	s.acquireLease(t, "bundle-exp", "publisher-a", time.Now().UTC().Add(-time.Second))
	published := expired
	published.OwnerID = "publisher-a"
	published.State = publication.BundlePublished
	if err := s.registry.PublishExecution(s.ctx, name, "", published); !errors.Is(err, publication.ErrBundlePointerConflict) {
		t.Fatalf("publish under expired lease = %v, want ErrBundlePointerConflict", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundlePointersTable)); n != 0 {
		t.Fatalf("pointer rows after expired-fence publish = %d, want 0", n)
	}
	if state := s.mustWitnessText(t,
		"SELECT payload->>'state' FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-exp"); state != "RUNNING" {
		t.Fatalf("execution state after refused publish = %q, want RUNNING (rolled back)", state)
	}

	// Foreign owner cannot publish.
	foreign := baseExecution("exec-foreign", "bundle-foreign", publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	s.acquireLease(t, "bundle-foreign", "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, foreign, "publisher-a")
	foreignPublished := foreign
	foreignPublished.OwnerID = "publisher-b"
	if err := s.registry.PublishExecution(s.ctx, name, "", foreignPublished); !errors.Is(err, publication.ErrBundlePointerConflict) {
		t.Fatalf("publish by non-owner = %v, want ErrBundlePointerConflict", err)
	}

	// Missing execution row: conflict, pointer untouched.
	missing := foreignPublished
	missing.OwnerID = "publisher-a"
	missing.ID = "exec-never-saved"
	if err := s.registry.PublishExecution(s.ctx, name, "", missing); !errors.Is(err, publication.ErrBundlePointerConflict) {
		t.Fatalf("publish missing execution = %v, want ErrBundlePointerConflict", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundlePointersTable)); n != 0 {
		t.Fatalf("pointer rows after refused publishes = %d, want 0", n)
	}

	// A correct publish first succeeds; then publishing with a stale expected
	// value conflicts and the pointer keeps naming the winner.
	ok := foreign
	ok.OwnerID = "publisher-a"
	ok.State = publication.BundlePublished
	ok.PublishedAt = &pubTime
	if err := s.registry.PublishExecution(s.ctx, name, "", ok); err != nil {
		s.fail("baseline publish", err)
	}
	stale := ok
	stale.ID = "exec-stale"
	s.acquireLease(t, "bundle-stale", "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, stale, "publisher-a")
	stale.OwnerID = "publisher-a"
	if err := s.registry.PublishExecution(s.ctx, name, "exec-never-current", stale); !errors.Is(err, publication.ErrBundlePointerConflict) {
		t.Fatalf("publish with stale expected = %v, want ErrBundlePointerConflict", err)
	}
	pointer, err := s.registry.GetPointer(s.ctx, name)
	if err != nil || pointer.ExecutionID != "exec-foreign" {
		t.Fatalf("pointer after stale publish = %+v, %v; want exec-foreign", pointer, err)
	}
}

func TestPublishExecutionConcurrentCASOneWinner(t *testing.T) {
	s := newCatalogService(t)
	name := "5:alpha8:gen-15:panel2:v1"
	key := "bundle-race"
	candidateA := baseExecution("exec-race-a", key, publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	candidateB := baseExecution("exec-race-b", key, publication.BundleRunning, stamps(createdTime, updatedTime, readyTime, time.Time{}))
	s.acquireLease(t, key, "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, candidateA, "publisher-a")
	s.saveExecution(t, candidateB, "publisher-a")
	for _, e := range []*publication.BundleExecution{&candidateA, &candidateB} {
		e.OwnerID = "publisher-a"
		e.State = publication.BundlePublished
		e.PublishedAt = &pubTime
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	outcomes := map[string]error{}
	for _, execution := range []publication.BundleExecution{candidateA, candidateB} {
		wg.Add(1)
		go func(execution publication.BundleExecution) {
			defer wg.Done()
			<-start
			err := s.registry.PublishExecution(s.ctx, name, "", execution)
			mu.Lock()
			outcomes[execution.ID] = err
			mu.Unlock()
		}(execution)
	}
	close(start)
	wg.Wait()

	winners := []string{}
	for id, err := range outcomes {
		if len(outcomes) != 2 {
			t.Fatalf("outcomes = %d, want 2", len(outcomes))
		}
		if err == nil {
			winners = append(winners, id)
			continue
		}
		if !errors.Is(err, publication.ErrBundlePointerConflict) {
			t.Fatalf("loser outcome for %s = %v, want ErrBundlePointerConflict", id, err)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %#v, want exactly one of exec-race-a/exec-race-b", winners)
	}
	pointer, err := s.registry.GetPointer(s.ctx, name)
	if err != nil {
		s.fail("get race pointer", err)
	}
	if pointer.ExecutionID != winners[0] {
		t.Fatalf("pointer names %q, want the winner %q", pointer.ExecutionID, winners[0])
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundlePointersTable)); n != 1 {
		t.Fatalf("pointer rows after race = %d, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// Exact lookup: project+generation+selector scope, no name-only fallback.
// ---------------------------------------------------------------------------

func TestExactLookupIsFullyScoped(t *testing.T) {
	s := newCatalogService(t)
	selector := publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-a"}

	newer := baseExecution("exec-exact-new", "bundle-new", publication.BundlePublished, stamps(createdTime, updatedTime, readyTime, pubTime))
	older := baseExecution("exec-exact-old", "bundle-old", publication.BundlePublished, stamps(createdTime.Add(-time.Hour), updatedTime.Add(-time.Hour), readyTime.Add(-time.Hour), pubTime.Add(-time.Hour)))
	sibling := baseExecution("exec-exact-beta", "bundle-beta", publication.BundlePublished, stamps(createdTime, updatedTime, readyTime, pubTime))
	sibling.Project = "beta"
	otherGeneration := baseExecution("exec-exact-gen2", "bundle-gen2", publication.BundlePublished, stamps(createdTime, updatedTime, readyTime, pubTime))
	otherGeneration.DatasetGeneration = "gen-2"
	otherTranslation := baseExecution("exec-exact-v2", "bundle-v2", publication.BundlePublished, stamps(createdTime, updatedTime, readyTime, pubTime))
	otherTranslation.TranslationVersion = "v2"
	runningTwin := baseExecution("exec-exact-run", "bundle-run", publication.BundleRunning, stamps(createdTime.Add(time.Hour), updatedTime.Add(time.Hour), readyTime.Add(time.Hour), time.Time{}))
	runningTwin.Outputs = append(runningTwin.Outputs, publication.BundleOutputRecord{
		Name: "out-run", PhysicalTable: "phys_alpha_out_run",
		Selector: runningTwin.Selector("out-run"), RowCount: 42, State: publication.BundleRunning,
	})

	for _, execution := range []publication.BundleExecution{newer, older, sibling, otherGeneration, otherTranslation, runningTwin} {
		keyOwner := execution.Key
		s.acquireLease(t, keyOwner, "publisher-a", time.Now().UTC().Add(time.Hour))
		s.saveExecution(t, execution, "publisher-a")
	}

	// Exact hit resolves to the newest published_at match with literal
	// output metadata.
	execution, output, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", selector)
	if err != nil {
		s.fail("exact lookup", err)
	}
	if execution.ID != "exec-exact-new" {
		t.Fatalf("exact lookup returned %q, want exec-exact-new (newest published_at)", execution.ID)
	}
	wantOutput := publication.BundleOutputRecord{
		Name: "out-a", PhysicalTable: "phys_alpha_out_a",
		Selector: publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-a"},
		Columns:  newer.Outputs[0].Columns,
		RowCount: 1234, ByteCount: 567890, State: publication.BundlePublished,
		VerifiedAt: newer.Outputs[0].VerifiedAt,
		SourceRow:  &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "subject"},
	}
	if gotJSON, wantJSON := mustMarshal(t, output), mustMarshal(t, wantOutput); gotJSON != wantJSON {
		t.Fatalf("exact output =\n %s\nwant\n %s", gotJSON, wantJSON)
	}

	// Positive exact-scope resolution: each sibling row is found only under
	// its own full scope. A name-only latest fallback would answer the
	// beta/generation-2/v2 queries with the newest "panel"/"out-a" row
	// (exec-exact-new); the registry must not.
	type hit struct {
		project    string
		generation string
		selector   publication.DataframeSelector
		wantID     string
	}
	for _, h := range []hit{
		{"beta", "gen-1", selector, "exec-exact-beta"},
		{"alpha", "gen-2", selector, "exec-exact-gen2"},
		{"alpha", "gen-1", publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v2", Output: "out-a"}, "exec-exact-v2"},
		{"alpha", "gen-1", publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-b"}, "exec-exact-new"},
	} {
		got, _, err := s.registry.FindExecutionBySelector(s.ctx, h.project, h.generation, h.selector)
		if err != nil {
			t.Fatalf("exact hit %s: %v", h.wantID, err)
		}
		if got.ID != h.wantID {
			t.Fatalf("exact lookup (%s,%s,%+v) = %q, want %q", h.project, h.generation, h.selector, got.ID, h.wantID)
		}
	}

	// Every scope component is required: with any component changed to a
	// value no row carries, the lookup misses even though name+output still
	// match somewhere in the collection. This is the no-name-only-latest-
	// fallback acceptance.
	type miss struct {
		name       string
		project    string
		generation string
		selector   publication.DataframeSelector
	}
	misses := []miss{
		{"unknown project", "gamma", "gen-1", selector},
		{"swapped scope pair", "beta", "gen-2", selector},
		{"unknown generation", "alpha", "gen-9", selector},
		{"unknown translation version", "alpha", "gen-1", publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v9", Output: "out-a"}},
		{"unknown output", "alpha", "gen-1", publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-z"}},
		{"case-sensitive recipe", "alpha", "gen-1", publication.DataframeSelector{Recipe: "Panel", TranslationVersion: "v1", Output: "out-a"}},
	}
	for _, m := range misses {
		if _, _, err := s.registry.FindExecutionBySelector(s.ctx, m.project, m.generation, m.selector); !errors.Is(err, publication.ErrBundleNotFound) {
			t.Fatalf("%s: FindExecutionBySelector = %v, want ErrBundleNotFound", m.name, err)
		}
	}

	// The non-published twin carries the newest stamps but is invisible: a
	// name-only latest fallback over all states would answer with it.
	runSelector := publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-run"}
	if _, _, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", runSelector); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("running twin visible before publish: %v", err)
	}
	publishedRun := runningTwin
	publishedRun.OwnerID = "publisher-a"
	publishedRun.State = publication.BundlePublished
	olderPublished := pubTime.Add(-time.Hour)
	publishedRun.PublishedAt = &olderPublished
	for i := range publishedRun.Outputs {
		publishedRun.Outputs[i].State = publication.BundlePublished
		publishedRun.Outputs[i].VerifiedAt = &olderPublished
	}
	if err := s.registry.PublishExecution(s.ctx, "pointer-run", "", publishedRun); err != nil {
		s.fail("publish run twin", err)
	}
	if execution, output, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", runSelector); err != nil ||
		execution.ID != "exec-exact-run" || output.RowCount != 42 || output.PhysicalTable != "phys_alpha_out_run" {
		t.Fatalf("run twin after publish = %q/%+v, %v; want exec-exact-run with out-run metadata", execution.ID, output, err)
	}
	// The out-a scope still resolves to the newest published match.
	if execution, _, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", selector); err != nil || execution.ID != "exec-exact-new" {
		t.Fatalf("newest after run twin published = %q, %v; want exec-exact-new", execution.ID, err)
	}
}

func TestExactLookupOrdersNullStampsDeterministically(t *testing.T) {
	s := newCatalogService(t)
	selector := publication.DataframeSelector{Recipe: "panel", TranslationVersion: "v1", Output: "out-a"}

	// Both rows lack published_at (SQL NULL): ordering falls to ready_at,
	// then updated_at, then id. Literal expected row: exec-null-late.
	first := baseExecution("exec-null-early", "bundle-null-1", publication.BundlePublished, stamps(createdTime, updatedTime, time.Time{}, time.Time{}))
	second := baseExecution("exec-null-late", "bundle-null-2", publication.BundlePublished, stamps(createdTime, updatedTime.Add(time.Minute), time.Time{}, time.Time{}))
	for _, execution := range []publication.BundleExecution{first, second} {
		s.acquireLease(t, execution.Key, "publisher-a", time.Now().UTC().Add(time.Hour))
		s.saveExecution(t, execution, "publisher-a")
	}
	execution, _, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", selector)
	if err != nil {
		s.fail("null-stamp lookup", err)
	}
	if execution.ID != "exec-null-late" {
		t.Fatalf("null-stamp lookup = %q, want exec-null-late", execution.ID)
	}
}

func TestExactLookupRejectsInvalidSelector(t *testing.T) {
	s := newCatalogService(t)
	for name, selector := range map[string]publication.DataframeSelector{
		"blank recipe":       {Recipe: " ", TranslationVersion: "v1", Output: "out-a"},
		"padded translation": {Recipe: "panel", TranslationVersion: " v1", Output: "out-a"},
		"blank output":       {Recipe: "panel", TranslationVersion: "v1", Output: ""},
	} {
		_, _, err := s.registry.FindExecutionBySelector(s.ctx, "alpha", "gen-1", selector)
		if err == nil || !strings.Contains(err.Error(), "is required and must not have surrounding whitespace") {
			t.Fatalf("%s: FindExecutionBySelector = %v, want the selector validation error", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Legacy payload canonicalization on read, never on write.
// ---------------------------------------------------------------------------

const legacyQueuedPayload = `{"id":"legacy-queued","key":"legacy-key","name":"legacy-recipe","translationVersion":"v9","project":"p","datasetGeneration":"g","state":"PENDING","outputs":[{"name":"out","physicalTable":"phys_legacy","state":"LOADING","rowCount":3}],"createdAt":"2026-11-09T10:00:00Z","updatedAt":"2026-11-09T10:05:00Z","readyAt":"2026-11-09T10:06:00Z"}`

const legacyReadyPayload = `{"id":"legacy-ready","key":"legacy-key","name":"legacy-recipe","translationVersion":"v9","project":"p","datasetGeneration":"g","state":"READY","outputs":[{"name":"out","physicalTable":"phys_legacy","state":"READY","rowCount":3}],"createdAt":"2026-11-09T10:01:00Z","updatedAt":"2026-11-09T10:07:00Z","readyAt":"2026-11-09T10:08:00Z"}`

func insertLegacyRow(t *testing.T, s *catalogService, id, key, recipe, tv, state, payload string, created, updated time.Time, ready *time.Time) {
	t.Helper()
	s.mustExec(t,
		"INSERT INTO "+s.qualified(BundleExecutionsTable)+
			" (id, execution_key, project, dataset_generation, recipe_name, translation_version,"+
			" state, created_at, updated_at, ready_at, published_at, payload)"+
			" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)",
		id, key, "p", "g", recipe, tv, state, created, updated, ready, nil, payload)
}

func TestLegacyCanonicalizationOnReadDoesNotMutateHistory(t *testing.T) {
	s := newCatalogService(t)
	legacyCreated := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	legacyUpdatedQueued := time.Date(2026, 11, 9, 10, 5, 0, 0, time.UTC)
	legacyReadyQueued := time.Date(2026, 11, 9, 10, 6, 0, 0, time.UTC)
	legacyUpdatedReady := time.Date(2026, 11, 9, 10, 7, 0, 0, time.UTC)
	legacyReadyReady := time.Date(2026, 11, 9, 10, 8, 0, 0, time.UTC)
	insertLegacyRow(t, s, "legacy-queued", "legacy-key", "legacy-recipe", "v9", "PENDING", legacyQueuedPayload, legacyCreated, legacyUpdatedQueued, &legacyReadyQueued)
	insertLegacyRow(t, s, "legacy-ready", "legacy-key", "legacy-recipe", "v9", "READY", legacyReadyPayload, legacyCreated.Add(time.Minute), legacyUpdatedReady, &legacyReadyReady)

	// GetExecution canonicalizes the queued legacy row: PENDING→QUEUED,
	// LOADING→RUNNING, output selector derived from the identity, and no
	// invented timestamps.
	queued, err := s.registry.GetExecution(s.ctx, "legacy-queued")
	if err != nil {
		s.fail("get legacy queued", err)
	}
	if queued.State != publication.BundleQueued || queued.Outputs[0].State != publication.BundleRunning {
		t.Fatalf("legacy queued states = %q/%q, want QUEUED/RUNNING", queued.State, queued.Outputs[0].State)
	}
	wantSelector := publication.DataframeSelector{Recipe: "legacy-recipe", TranslationVersion: "v9", Output: "out"}
	if queued.Outputs[0].Selector != wantSelector {
		t.Fatalf("legacy derived selector = %+v, want %+v", queued.Outputs[0].Selector, wantSelector)
	}
	if queued.Outputs[0].VerifiedAt != nil || queued.PublishedAt != nil {
		t.Fatalf("legacy queued invented stamps: verified=%v published=%v, want nil", queued.Outputs[0].VerifiedAt, queued.PublishedAt)
	}

	// The READY compat row backfills publishedAt/verifiedAt from readyAt on
	// the returned copy.
	readyExec, err := s.registry.GetExecution(s.ctx, "legacy-ready")
	if err != nil {
		s.fail("get legacy ready", err)
	}
	if readyExec.State != publication.BundlePublished || readyExec.PublishedAt == nil || !readyExec.PublishedAt.Equal(legacyReadyReady) {
		t.Fatalf("legacy ready = %q publishedAt %v, want PUBLISHED at %s", readyExec.State, readyExec.PublishedAt, legacyReadyReady)
	}
	if readyExec.Outputs[0].VerifiedAt == nil || !readyExec.Outputs[0].VerifiedAt.Equal(legacyReadyReady) {
		t.Fatalf("legacy ready verifiedAt = %v, want %s", readyExec.Outputs[0].VerifiedAt, legacyReadyReady)
	}

	// Compat states remain reachable through the synonym expansions.
	list, err := s.registry.ListExecutions(s.ctx, publication.BundleQueued, baseTime)
	if err != nil {
		s.fail("list queued", err)
	}
	if len(list) != 1 || list[0].ID != "legacy-queued" {
		t.Fatalf("queued list = %+v, want only legacy-queued", list)
	}
	if _, err := s.registry.ListExecutions(s.ctx, publication.BundlePending, baseTime); err != nil {
		s.fail("list by compat state", err)
	}

	// FindExecutionByKey sees both legacy rows and returns the newest
	// created_at among the shared key.
	byKey, err := s.registry.FindExecutionByKey(s.ctx, "legacy-key")
	if err != nil {
		s.fail("find by key", err)
	}
	if byKey.ID != "legacy-ready" {
		t.Fatalf("FindExecutionByKey = %q, want legacy-ready (newer created_at)", byKey.ID)
	}

	// The exact lookup finds the compat READY row on full scope equality.
	if execution, _, err := s.registry.FindExecutionBySelector(s.ctx, "p", "g",
		publication.DataframeSelector{Recipe: "legacy-recipe", TranslationVersion: "v9", Output: "out"}); err != nil || execution.ID != "legacy-ready" {
		t.Fatalf("legacy exact lookup = %q, %v; want legacy-ready", execution.ID, err)
	}

	// Paging sees the compat PENDING row through the QUEUED expansion.
	visited := []string{}
	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleQueued, baseTime, 10, func(page []publication.BundleExecution) error {
		for _, execution := range page {
			visited = append(visited, execution.ID)
		}
		return nil
	}); err != nil {
		s.fail("visit queued", err)
	}
	if len(visited) != 1 || visited[0] != "legacy-queued" {
		t.Fatalf("visited queued executions = %#v, want [legacy-queued] via the PENDING synonym", visited)
	}

	// The decisive acceptance check: after every read path above, neither
	// historical payload changed value. jsonb normalizes lexical JSON form
	// (whitespace/key order) on storage, so the guarantee is asserted as
	// jsonb equality against the inserted literal plus the specific fields
	// canonicalization must not have written: the legacy state is untouched,
	// and no verifiedAt/publishedAt was backfilled into the stored row.
	for id, wantPayload := range map[string]string{"legacy-queued": legacyQueuedPayload, "legacy-ready": legacyReadyPayload} {
		if !s.mustWitnessBool(t,
			"SELECT payload = $2::jsonb FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1",
			id, wantPayload) {
			got := s.mustWitnessText(t,
				"SELECT payload::text FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", id)
			t.Fatalf("payload for %s changed value under reads:\n got %s\nwant %s", id, got, wantPayload)
		}
	}
	if s.mustWitnessBool(t,
		"SELECT (payload ? 'publishedAt') OR (payload->'outputs'->0 ? 'verifiedAt') FROM "+
			s.qualified(BundleExecutionsTable)+" WHERE id = $1", "legacy-queued") {
		t.Fatal("read canonicalization fabricated publishedAt/verifiedAt into the stored legacy row")
	}
	// The legacy state columns are also untouched, not "helpfully"
	// canonicalized into the row.
	if state := s.mustWitnessText(t,
		"SELECT state FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "legacy-queued"); state != "PENDING" {
		t.Fatalf("legacy state column = %q, want stored PENDING", state)
	}
}

// ---------------------------------------------------------------------------
// Missing vs JSON-null vs SQL-NULL.
// ---------------------------------------------------------------------------

func TestMissingJSONNullAndSQLNullStayDistinct(t *testing.T) {
	s := newCatalogService(t)

	// Saved row with nil stamps: the columns are SQL NULL and the payload
	// omits the fields entirely (missing).
	saved := baseExecution("exec-nulls", "bundle-nulls", publication.BundleRunning, stamps(createdTime, updatedTime, time.Time{}, time.Time{}))
	saved.Outputs = nil
	s.acquireLease(t, "bundle-nulls", "publisher-a", time.Now().UTC().Add(time.Hour))
	s.saveExecution(t, saved, "publisher-a")
	if s.mustWitnessBool(t,
		"SELECT ready_at IS NULL AND published_at IS NULL FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-nulls") {
		// expected
	} else {
		t.Fatal("nil stamps must land as SQL NULL columns")
	}
	if s.mustWitnessBool(t,
		"SELECT NOT (payload ? 'readyAt') AND NOT (payload ? 'publishedAt') FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-nulls") {
		// expected
	} else {
		t.Fatal(`saved payload must omit absent stamp fields (no "readyAt":null)`)
	}

	// A historical row with explicit JSON null keeps that distinction: the
	// field is present ('?' true), jsonb_typeof is 'null', the columns are
	// still SQL NULL, and the read decodes nil pointers -- three distinct
	// representations preserved across the store boundary.
	jsonNullPayload := `{"id":"legacy-jsonnull","key":"legacy-jsonnull","name":"legacy-recipe","translationVersion":"v9","project":"p","datasetGeneration":"g","state":"QUEUED","outputs":[],"createdAt":"2026-11-09T10:00:00Z","updatedAt":"2026-11-09T10:05:00Z","readyAt":null,"publishedAt":null}`
	insertLegacyRow(t, s, "legacy-jsonnull", "legacy-jsonnull", "legacy-recipe", "v9", "QUEUED", jsonNullPayload,
		time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 10, 5, 0, 0, time.UTC), nil)
	if s.mustWitnessBool(t,
		"SELECT ready_at IS NULL AND published_at IS NULL"+
			" AND jsonb_typeof(payload->'readyAt') = 'null' AND jsonb_typeof(payload->'publishedAt') = 'null'"+
			" AND (payload ? 'readyAt') AND (payload ? 'publishedAt') FROM "+
			s.qualified(BundleExecutionsTable)+" WHERE id = $1", "legacy-jsonnull") {
		// expected
	} else {
		t.Fatal(`explicit JSON null must survive as JSON null with SQL NULL columns`)
	}
	execution, err := s.registry.GetExecution(s.ctx, "legacy-jsonnull")
	if err != nil {
		s.fail("get json-null row", err)
	}
	if execution.ReadyAt != nil || execution.PublishedAt != nil {
		t.Fatalf("JSON null decoded as %v/%v, want nil pointers", execution.ReadyAt, execution.PublishedAt)
	}
	if !s.mustWitnessBool(t,
		"SELECT payload = $2::jsonb FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1",
		"legacy-jsonnull", jsonNullPayload) {
		got := s.mustWitnessText(t,
			"SELECT payload::text FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "legacy-jsonnull")
		t.Fatalf("JSON null payload mutated value:\n got %s\nwant %s", got, jsonNullPayload)
	}
}

// ---------------------------------------------------------------------------
// Unsupported inputs fail explicitly; no silent fallback.
// ---------------------------------------------------------------------------

func TestNULInTextScopeColumnsFailsExplicitly(t *testing.T) {
	s := newCatalogService(t)
	execution := baseExecution("exec-nul-project", "bundle-nul", publication.BundleRunning, stamps(createdTime, updatedTime, time.Time{}, time.Time{}))
	execution.Project = "al\x00pha"
	s.acquireLease(t, "bundle-nul", "publisher-a", time.Now().UTC().Add(time.Hour))

	err := s.registry.SaveExecution(s.ctx, execution, "publisher-a")
	if err == nil {
		t.Fatal("SaveExecution with NUL in a text column succeeded; PostgreSQL text cannot hold NUL and the registry must surface that")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22021" {
		t.Fatalf("SaveExecution error = %v, want explicit 22021 invalid_text_representation (no silent fallback)", err)
	}
	if n := s.execCount(t, "SELECT count(*) FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", "exec-nul-project"); n != 0 {
		t.Fatalf("rejected save left %d rows, want 0", n)
	}
}

func TestReadsAndWritesOnMissingRowsReportNotFound(t *testing.T) {
	s := newCatalogService(t)
	if _, err := s.registry.GetExecution(s.ctx, "no-such-execution"); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("GetExecution(missing) = %v, want ErrBundleNotFound", err)
	}
	if _, err := s.registry.FindExecutionByKey(s.ctx, "no-such-key"); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("FindExecutionByKey(missing) = %v, want ErrBundleNotFound", err)
	}
	if _, err := s.registry.GetPointer(s.ctx, "no-such-pointer"); !errors.Is(err, publication.ErrBundleNotFound) {
		t.Fatalf("GetPointer(missing) = %v, want ErrBundleNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// Scans and stable paging.
// ---------------------------------------------------------------------------

func TestListExecutionsEmptyIsNonNilSlice(t *testing.T) {
	s := newCatalogService(t)
	list, err := s.registry.ListExecutions(s.ctx, publication.BundlePublished, time.Now().UTC().Add(time.Hour))
	if err != nil {
		s.fail("list empty", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("empty ListExecutions = %#v, want non-nil empty slice (Arango parity)", list)
	}
}

func TestVisitExecutionPagesStableContinuationAndGuards(t *testing.T) {
	s := newCatalogService(t)
	// Five RUNNING rows whose ids sort opposite to updated_at so ordering is
	// proven by the scan columns, not insertion order.
	type row struct {
		id      string
		updated time.Time
	}
	rows := []row{
		{"exec-page-e", baseTime.Add(4 * time.Minute)},
		{"exec-page-a", baseTime.Add(5 * time.Minute)},
		{"exec-page-c", baseTime.Add(6 * time.Minute)},
		{"exec-page-b", baseTime.Add(7 * time.Minute)},
		{"exec-page-d", baseTime.Add(8 * time.Minute)},
	}
	for _, r := range rows {
		execution := baseExecution(r.id, "bundle-page", publication.BundleRunning, stamps(createdTime, r.updated, time.Time{}, time.Time{}))
		execution.Outputs = nil
		s.acquireLease(t, "bundle-page", "publisher-a", time.Now().UTC().Add(time.Hour))
		s.saveExecution(t, execution, "publisher-a")
	}
	before := baseTime.Add(time.Hour)

	var pages [][]string
	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleRunning, before, 2, func(page []publication.BundleExecution) error {
		ids := []string{}
		for _, execution := range page {
			ids = append(ids, execution.ID)
		}
		pages = append(pages, ids)
		return nil
	}); err != nil {
		s.fail("visit pages", err)
	}
	wantPages := [][]string{
		{"exec-page-e", "exec-page-a"},
		{"exec-page-c", "exec-page-b"},
		{"exec-page-d"},
	}
	if !reflect.DeepEqual(pages, wantPages) {
		t.Fatalf("pages = %#v, want %#v", pages, wantPages)
	}

	// The before cutoff is exclusive and honored across pages: the earliest
	// update sits exactly at the cutoff and is excluded, so nothing visits.
	count := 0
	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleRunning, rows[0].updated, 2, func(page []publication.BundleExecution) error {
		count += len(page)
		return nil
	}); err != nil {
		s.fail("visit before cutoff", err)
	}
	if count != 0 {
		t.Fatalf("rows before cutoff = %d, want 0 (all updates are later)", count)
	}

	// A failing visitor stops the walk immediately without further queries.
	visits := 0
	stopErr := errors.New("stop the sweep")
	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleRunning, before, 2, func(page []publication.BundleExecution) error {
		visits++
		return stopErr
	}); !errors.Is(err, stopErr) {
		t.Fatalf("VisitExecutionPages visitor error = %v, want the visitor error itself", err)
	}
	if visits != 1 {
		t.Fatalf("visitor calls after error = %d, want 1 (stop on first error)", visits)
	}

	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleRunning, before, 0, func([]publication.BundleExecution) error { return nil }); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("VisitExecutionPages(pageSize 0) = %v, want positive-size error", err)
	}
	if err := s.registry.VisitExecutionPages(s.ctx, publication.BundleRunning, before, 2, nil); err == nil || !strings.Contains(err.Error(), "visitor is required") {
		t.Fatalf("VisitExecutionPages(nil visitor) = %v, want required-visitor error", err)
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit: no pgpubcat01_it_* database or schema may survive.
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	code := m.Run()
	if err := auditZeroLeftovers(); err != nil {
		fmt.Fprintf(os.Stderr, "zero-leftover audit failed: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func auditZeroLeftovers() error {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		// No service was used; nothing can have been created.
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		return fmt.Errorf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgpubcat01_it_%'").Scan(&databases); err != nil {
		return fmt.Errorf("count databases: %v", err)
	}
	if databases != 0 {
		return fmt.Errorf("%d pgpubcat01_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgpubcat01_it_%'").Scan(&schemas); err != nil {
		return fmt.Errorf("count schemas: %v", err)
	}
	if schemas != 0 {
		return fmt.Errorf("%d pgpubcat01_it_* schemas still exist in the service database", schemas)
	}
	return nil
}
