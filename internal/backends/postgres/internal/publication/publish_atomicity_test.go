package postgres

// Real-PostgreSQL 18 acceptance tests for the atomic publish commit
// (pg-publication-atomic-visibility-v1; check-2:
// go test -count=1 -run ^TestPublishExecutionAtomicity$ ./internal/backends/postgres/internal/publication/).
//
// These tests are the PostgreSQL counterpart of the Arango
// publish_atomicity_e2e_test.go F-B16 evidence: the execution state
// transition and the logical reader pointer move must become externally
// visible together, never half. The promise under test is the publish.go
// contract:
//
//	A. Commit-boundary visibility: while the publish transaction is open
//	   (after every visibility write has applied), readers on a second real
//	   PG connection still see the OLD pointer and the OLD execution state;
//	   after COMMIT they see the pointer naming the execution and the full
//	   exact execution. Two real connections with distinct pg_backend_pid,
//	   at least ten rounds per racing surface.
//	B. Injected transaction failure -- a crash in the window after the
//	   pointer CAS and execution UPDATE have applied but before COMMIT --
//	   exposes neither the new execution visibility nor the pointer: both
//	   rows stay byte-identical to their pre-attempt bytes, and a clean
//	   retry converges (post-abort convergence).
//	C. Republishing the same execution is idempotent: the second call is a
//	   converged no-op with byte-identical rows and the exact convergence
//	   statement set; a drifted retry is refused with the named conflict.
//	D. Concurrent publishers on two real connections converge to exactly
//	   one winner; every stale loser is refused with the exact named
//	   publication.ErrBundlePointerConflict (and not any sibling error)
//	   with its own rows untouched.
//	E. ClickHouse operations never run inside the PG transaction: the
//	   transaction's statement stream is exactly the golden catalog
//	   literals (witnessed at the driver), the backend is witnessed idle in
//	   transaction on the final catalog UPDATE from a second connection,
//	   no backend anywhere runs a ClickHouse-shaped statement during the
//	   window, and the method signature carries no callback that could
//	   smuggle one in.
//	F. Sensitivity control (Arango arm A): replaying the pre-fix two-leg
//	   shape (execution UPDATE applied outside the publish transaction,
//	   pointer leg stopped) MUST expose the split state -- the witnesses in
//	   A/B/D genuinely see a half-publish when one exists.
//
// Every fixture database and schema is uniquely owned as verify_pubvis_it_*
// and dropped by the test that created it. The TestZZ audit is
// ownership-scoped for exact-name checks plus a pg_database==baseline
// equality over the process-lifetime baseline; concurrent sibling suites
// (pgpubcat01_it_*, pgleases01_it_*) own their prefixes and their own
// audits, so their transient databases are noted, not counted against this
// file. A skipped required case is a failure for this ticket's acceptance,
// so the check is run with LOOM_TEST_POSTGRES_URL configured; the gate
// exists only to keep unrelated CI lanes green.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/jackc/pgx/v5/pgxpool"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/publication"
)

var pubvisResourceNamePattern = regexp.MustCompile(`^verify_pubvis_it_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every database this file verified into existence is
// recorded here and TestZZ asserts each exact name is gone.
var (
	pubvisOwnedMu sync.Mutex
	pubvisOwned   = map[string]string{} // database -> owned schema
)

func pubvisRegisterOwned(db, schema string) {
	pubvisOwnedMu.Lock()
	defer pubvisOwnedMu.Unlock()
	pubvisOwned[db] = schema
}

// pg_database baseline captured before any test runs, for the
// pg_database==baseline equality in the zero-leftover audit.
var (
	pubvisBaselineOnce sync.Once
	pubvisBaseline     map[string]bool
	pubvisBaselineErr  error
)

func capturePubvisBaseline() {
	pubvisBaselineOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
		if raw == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			pubvisBaselineErr = fmt.Errorf("baseline connect: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database")
		if err != nil {
			pubvisBaselineErr = fmt.Errorf("baseline query: %w", err)
			return
		}
		defer rows.Close()
		pubvisBaseline = map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				pubvisBaselineErr = fmt.Errorf("baseline scan: %w", err)
				return
			}
			pubvisBaseline[name] = true
		}
		if err := rows.Err(); err != nil {
			pubvisBaselineErr = fmt.Errorf("baseline rows: %w", err)
		}
	})
}

func init() { capturePubvisBaseline() }

// auditPublishVisibilityLeftovers is the zero-leftover audit: (1) every
// exact database name this file created is gone, and (2) the pg_database
// set still equals the process baseline -- new databases outside the
// documented sibling-suite prefixes are failures.
func auditPublishVisibilityLeftovers() error {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")) == "" {
		return nil
	}
	capturePubvisBaseline()
	if pubvisBaselineErr != nil {
		return pubvisBaselineErr
	}
	pubvisOwnedMu.Lock()
	owned := make(map[string]string, len(pubvisOwned))
	for db, schema := range pubvisOwned {
		owned[db] = schema
	}
	pubvisOwnedMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")))
	if err != nil {
		return fmt.Errorf("audit connect: %w", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "verify_pubvis_it_") {
			return fmt.Errorf("ownership registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			return fmt.Errorf("audit owned database %s: %w", db, err)
		}
		if count != 0 {
			return fmt.Errorf("leftover owned database %s (owned schema %s) after all cleanups", db, schema)
		}
	}

	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database")
	if err != nil {
		return fmt.Errorf("audit current databases: %w", err)
	}
	defer rows.Close()
	current := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("audit scan: %w", err)
		}
		current[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("audit rows: %w", err)
	}
	for name := range current {
		if pubvisBaseline[name] {
			continue
		}
		// Sibling suites own their prefixes and their own zero-leftover
		// audits in this same package; their transient databases are
		// documented, not counted against this file.
		if strings.HasPrefix(name, "pgpubcat01_it_") || strings.HasPrefix(name, "pgleases01_it_") {
			continue
		}
		return fmt.Errorf("pg_database drifted from the baseline: unexpected database %q", name)
	}
	for name := range pubvisBaseline {
		if !current[name] {
			return fmt.Errorf("pg_database drifted from the baseline: baseline database %q vanished", name)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Fixture service: one owned database, one owned schema, three pools pinned
// to one physical connection each -- publisher A, publisher B, reader -- so
// every racing surface provably runs on distinct real PG backends.
// ---------------------------------------------------------------------------

type pubvisService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	pub    *pgxpool.Pool // publisher A (one backend)
	pubB   *pgxpool.Pool // publisher B / stale (one backend)
	reader *pgxpool.Pool // readers and SQL witnesses (one backend)
	schema string
	regA   *Registry
	regB   *Registry
	regR   *Registry
	scrub  func(any) string
	fail   func(step string, err error)
}

func newPubvisService(t *testing.T) *pubvisService {
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
	fail := func(step string, err error) { t.Fatalf("%s: %s", step, scrub(err)) }

	capturePubvisBaseline()
	if pubvisBaselineErr != nil {
		fail("capture pg_database baseline", pubvisBaselineErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("verify_pubvis_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pubvisResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pubvisRegisterOwned(dbName, "")
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
		fail("connect publisher pool A", err)
	}
	t.Cleanup(poolA.Close)
	poolB, err := newOwnedPool(ctx, dsn)
	if err != nil {
		fail("connect publisher pool B", err)
	}
	t.Cleanup(poolB.Close)
	poolR, err := newOwnedPool(ctx, dsn)
	if err != nil {
		fail("connect reader pool", err)
	}
	t.Cleanup(poolR.Close)

	schema := fmt.Sprintf("verify_pubvis_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pubvisResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := poolR.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	pubvisRegisterOwned(dbName, schema)
	t.Cleanup(func() {
		if _, err := poolR.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &pubvisService{ctx: ctx, admin: admin, pub: poolA, pubB: poolB, reader: poolR, schema: schema, scrub: scrub, fail: fail}
	s.migrate(t)
	s.regA = s.registry(t, poolA)
	s.regB = s.registry(t, poolB)
	s.regR = s.registry(t, poolR)
	return s
}

func (s *pubvisService) registry(t *testing.T, pool *pgxpool.Pool) *Registry {
	t.Helper()
	registry, err := New(pool, s.schema)
	if err != nil {
		s.fail("build registry", err)
	}
	return registry
}

// migrate applies exactly the ledger bootstrap plus the 000004 publication
// bundle catalog from this package's authoritative owner embed (migrate.go)
// through the real pg-store-02 runner. The publish path adds no DDL --
// NewMigrationRunner sorts the owner history and tolerates the 1..4 version
// gap (internal/backends/postgres/internal/store/migrate.go), the proven composition the
// sibling suites already apply.
func (s *pubvisService) migrate(t *testing.T) {
	t.Helper()
	history := mustOwnerHistorySubset(t, 1, 4)
	runner, err := storepostgres.NewMigrationRunner(s.reader, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate catalog", err)
	}
	if result.LatestVersion != 4 || len(result.Applied) != 2 {
		t.Fatalf("applied history = %+v, want ledger + publication_bundle_catalog", result.Applied)
	}
}

// ---------------------------------------------------------------------------
// Witnesses on dedicated pools.
// ---------------------------------------------------------------------------

func (s *pubvisService) wText(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var value string
	if err := pool.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *pubvisService) wBool(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) bool {
	t.Helper()
	var value bool
	if err := pool.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

func (s *pubvisService) wInt64(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := pool.QueryRow(s.ctx, sql, args...).Scan(&value); err != nil {
		s.fail("witness "+sql, err)
	}
	return value
}

// backendPID asks a one-connection pool which real PG backend it owns.
func (s *pubvisService) backendPID(t *testing.T, pool *pgxpool.Pool, role string) uint32 {
	t.Helper()
	raw := s.wText(t, pool, "SELECT pg_backend_pid()::text")
	var pid uint32
	if _, err := fmt.Sscanf(raw, "%d", &pid); err != nil {
		s.fail("parse pg_backend_pid "+role, err)
	}
	if pid == 0 {
		t.Fatalf("%s backend pid is zero", role)
	}
	return pid
}

// pointerRow returns the literal pointer row for a name as stable witness
// strings, or nil when no row exists.
type pubvisPointerRow struct {
	PointerKey string
	NameHex    string
	Execution  string
	UpdatedAt  string
}

func (s *pubvisService) pointerRow(t *testing.T, name string) *pubvisPointerRow {
	t.Helper()
	var row pubvisPointerRow
	err := s.reader.QueryRow(s.ctx,
		"SELECT pointer_key, encode(name_bytes, 'hex'), execution_id, "+
			`to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') `+
			"FROM "+s.qualified(BundlePointersTable)+" WHERE pointer_key = $1",
		pointerKey(name)).Scan(&row.PointerKey, &row.NameHex, &row.Execution, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		s.fail("pointer row witness", err)
	}
	return &row
}

// pointerValue reads the pointer row and fails when it is absent.
func (s *pubvisService) pointerValue(t *testing.T, name string) pubvisPointerRow {
	t.Helper()
	row := s.pointerRow(t, name)
	if row == nil {
		t.Fatalf("pointer row vanished for %q", name)
	}
	return *row
}

// payloadText returns the stored payload's jsonb text bytes for one
// execution -- the byte witness compared across operations.
func (s *pubvisService) payloadText(t *testing.T, id string) string {
	t.Helper()
	return s.wText(t, s.reader,
		"SELECT payload::text FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", id)
}

func pubvisSHA256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *pubvisService) qualified(table string) string {
	return pgx.Identifier{s.schema, table}.Sanitize()
}

// activity returns (state, last query) for one backend as seen from the
// reader connection -- a real cross-connection witness of what the publish
// backend is doing during the gate window.
func (s *pubvisService) activity(t *testing.T, pid uint32) (string, string) {
	t.Helper()
	var state, query string
	if err := s.reader.QueryRow(s.ctx,
		"SELECT state, query FROM pg_stat_activity WHERE pid = $1", pid).Scan(&state, &query); err != nil {
		s.fail("pg_stat_activity witness", err)
	}
	return state, query
}

// ---------------------------------------------------------------------------
// Canonical fixture data. All timestamps carry non-zero, non-microsecond-
// trailing nanoseconds so an RFC3339Nano round trip is a real precision
// test; digests and names are literal fixtures.
// ---------------------------------------------------------------------------

const (
	pubvisOwnerA   = "pubvis-owner-a"
	pubvisOwnerB   = "pubvis-owner-b"
	pubvisLeaseEnd = "2099-01-01T00:00:00Z"
)

var (
	pubvisCreated     = time.Date(2026, 11, 9, 10, 0, 0, 123456789, time.UTC)
	pubvisRunUpdated  = time.Date(2026, 11, 9, 10, 0, 30, 987654321, time.UTC)
	pubvisReady       = time.Date(2026, 11, 9, 10, 0, 45, 555555555, time.UTC)
	pubvisPublishedAt = time.Date(2026, 11, 9, 10, 2, 0, 112233445, time.UTC)
	pubvisLeaseUntil  = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
)

func pubvisIdentity() publication.BundleIdentity {
	return publication.BundleIdentity{
		Name:               "pa:nel",
		TranslationVersion: "9:v",
		Project:            "pubvis-project",
		DatasetGeneration:  "gen-9",
		RecipeDigest:       "sha256:recipe-pubvis",
		SchemaDigest:       "sha256:schema-pubvis",
		ReceiptID:          "receipt-pubvis-1",
		ScopeDigest:        "sha256:scope-pubvis",
		EngineVersion:      "loom-engine-pubvis",
		AuthScopeMode:      "project",
		// Deliberately unsorted: the payload keeps what the caller stored,
		// and the pointer-name encoding must stay injective around the
		// embedded colons.
		AuthResourcePaths: []string{"fhir/Observation", "fhir/Patient"},
	}
}

// pubvisExecution builds one canonical execution for an id. published=false
// yields the RUNNING pre-publish shape; published=true carries the
// PUBLISHED state, PublishedAt, and per-output VerifiedAt.
func pubvisExecution(id string, published bool, owner string) publication.BundleExecution {
	identity := pubvisIdentity()
	updatedAt := pubvisRunUpdated
	if published {
		updatedAt = pubvisPublishedAt
	}
	execution := publication.BundleExecution{
		ID:             id,
		Key:            "bundle-" + id,
		BundleIdentity: identity,
		State:          publication.BundleRunning,
		CreatedAt:      pubvisCreated,
		UpdatedAt:      updatedAt,
		ReadyAt:        &pubvisReady,
		OwnerID:        owner,
	}
	if published {
		execution.State = publication.BundlePublished
		execution.PublishedAt = &pubvisPublishedAt
	}
	state := execution.State
	buildOutput := func(name, physical string, rows, bytes int64) publication.BundleOutputRecord {
		record := publication.BundleOutputRecord{
			Name:          name,
			PhysicalTable: physical,
			Selector:      execution.Selector(name),
			RowCount:      rows,
			ByteCount:     bytes,
			State:         state,
		}
		if published {
			verified := pubvisPublishedAt
			record.VerifiedAt = &verified
		}
		return record
	}
	outA := buildOutput("out-a", "phys_pubvis_out_a", 1234, 567890)
	outA.Columns = []publication.PhysicalColumn{
		{ID: "c1", Name: "subject", SemanticPath: "Patient.id", ClickHouse: "String", LogicalType: "ResourceIdentifier", Provenance: publication.ColumnExplicit, LoomOwned: true},
		{ID: "c2", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "Quantity", Nullable: true, Repeated: true},
	}
	outA.SourceRow = &publication.SourceRowMetadata{ResourceType: "Patient", IDColumn: "subject"}
	outB := buildOutput("out-b", "phys_pubvis_out_b", 7, 4200)
	execution.Outputs = []publication.BundleOutputRecord{outA, outB}
	return execution
}

// pubvisPointerName is the canonical chain pointer name for an id -- the
// untouched domain encoding of the canonical identity.
func pubvisPointerName() string { return pubvisIdentity().PointerName() }

// pubvisPayloadGoldenSHA256 holds the golden sha256 of the jsonb-normalized
// payload text for every canonical PUBLISHED execution id used below. The
// literals were pinned once from the production write path; any payload
// drift trips the literal, not a re-marshaled proxy.
var pubvisPayloadGoldenSHA256 = map[string]string{
	"pubvis-exec-c":       "86887eb9943381092f7188d1b8413c7cd978ef0f26f602c1fd16f6f28f934ce6",
	"pubvis-exec-e":       "1fbe8e253ce9d56d1ceaeeb874b62f706d6b83c5868ee34e5ac516ca00fe1f6b",
	"pubvis-exec-a1":      "7e6f2d0605f000aa45092794c84c22a3ed07974a884c825382d5d47616021e3b",
	"pubvis-exec-a2":      "4952b494ccc0411aa6a75ec11b6d73537e2987fd18de6579b8ae557ff30f5c22",
	"pubvis-exec-a3":      "eccaa7c904afd898ce25f2250607012c73794bc1cca71d94149852811839506d",
	"pubvis-exec-a4":      "a0e1c1827a9ff03a29f9c22c6161f7094988d451279ced1520009d0b66aee16b",
	"pubvis-exec-a5":      "d6e0b2e63ae2f3bce0b7da2caf7dc52b9a7e3e8fd854879f660d13a37cfb0be6",
	"pubvis-exec-a6":      "37df874d48575881ac18b6cccc289ecccb1caff3aa58054c6a218e24295ba353",
	"pubvis-exec-a7":      "1e63f2ca86e26d761940df49ead0790f0091967e13654fb02f965f95cf7f8dc5",
	"pubvis-exec-a8":      "8634ca3c5716638f6c47ff4e183ab5f4caf1b0206d84554c4418ac648f25418d",
	"pubvis-exec-a9":      "8e69135790e96fb4dcff6acbd9f6150b6012a32b885cdc983579bf8cc5d5e66b",
	"pubvis-exec-a10":     "def4c3ae5d374060ac8736ee4107bb364ef11f0db7c1ba4fe2989b8c053eb7c5",
	"pubvis-exec-a11":     "8e4be67e86be743a7c3f6c97f4da9689f7cf080a94eaf07113d28118b946d4b7",
	"pubvis-exec-a12":     "787e8776fcce894b126a9390c9291b3e686f81b2ef4c5c983d0454aa12e133c8",
	"pubvis-exec-b1":      "1c4e607f4c9fa889716cba380e0fa4210d4499587e15b3f3dc9cccbdbf0ef2c6",
	"pubvis-exec-b2":      "385596457a9ba6c88d57f76ee4135d8b1bcd924ef97b6e3126697b0d89eaac8f",
	"pubvis-exec-b3":      "3796e938f6cc0b28ea8c59e13843482c60f251908c074251afaf6924158b3664",
	"pubvis-exec-d-r01-a": "8da08c6f92cbdbfc0a3f53a8fadb068b44080a3f3fbbe38bb65367de615881c8",
	"pubvis-exec-d-r01-b": "d650cc34b4bb1d13ee79802d804f8a9b65cf9509825a3667b16fd5827246c346",
	"pubvis-exec-d-r02-a": "f546f7824de877ff6ed622bbf7a0a6665759d4a67c485f9d276c36eed3d4c1c3",
	"pubvis-exec-d-r02-b": "5bf186433d3bc4358f88b04a506838c2f57857239b8838dbf1b5812df3ffda52",
	"pubvis-exec-d-r03-a": "d4b838da0f93a0de5559fa9a8381f82c904cf5af88d20e84458b8aab47a704ea",
	"pubvis-exec-d-r03-b": "8c98b91fd65e62c74e0d28169d2f990ed46799eff831211454ecf6f1d0d7d024",
	"pubvis-exec-d-r04-a": "d6ae7732cb28c360872518977de7dada5dad563c216942a3cf31ac1b5a27fb0b",
	"pubvis-exec-d-r04-b": "a732ad412e6d4ff130e9253685f94cda13e9a53c7acf28fdc9450c89457fdf0f",
	"pubvis-exec-d-r05-a": "79ee74d1088eddc9eda84fc6956223bc0e350672578cedf188630c2926959ded",
	"pubvis-exec-d-r05-b": "dce5589bb3b5b97edf14efa81b435778871b6c464fb424b8ffe53a9f76ea4059",
	"pubvis-exec-d-r06-a": "6e3a46f762070040617809991c07d797db7750d929594d7b800f670dbd18d398",
	"pubvis-exec-d-r06-b": "20a4c962bdd6e93c2a28e8524e7ef91ea3dc9487a59336a1847ffeea1a7efac1",
	"pubvis-exec-d-r07-a": "239dab003d9159f9fcba733cba8db936a6cf7230372a9a0aab2482582e4bd9c6",
	"pubvis-exec-d-r07-b": "f315246bf2489e9e4af3a3ed977d1b2269dabdce4ac3affaee22a68e8d17c5a8",
	"pubvis-exec-d-r08-a": "b448df7ab59c4e11476bc44746731c9f009092163f74f0f70fca630283996b42",
	"pubvis-exec-d-r08-b": "45d13ce4c82ab1ac17ba278b8b3cf8a9c252286d8b6aeb0ec3063ab9c6192d72",
	"pubvis-exec-d-r09-a": "d07aee22f1b2002c908b47ee4e494f90cc5e17d24746b92fe1caf6ee7237884c",
	"pubvis-exec-d-r09-b": "4b504423b063e03e84581ef582852dfbe33479bd140e66482b649537810deab8",
	"pubvis-exec-d-r10-a": "9b70eb587481cd1c07d2e7e69db2c9be529b27649cedfcd72051b9da47614388",
	"pubvis-exec-d-r10-b": "ad22c26f50014d80ef24720f9d8eec4d898e7dfae01a5be227cc68cf76105207",
	"pubvis-exec-d-r11-a": "b11d5fe6eb7f2a545da01a7cc594d5586945db7aa94403055d1e97e36acfe258",
	"pubvis-exec-d-r11-b": "e41cf9e81d0cbdc5e3da0a1bcde14c5f0433e68fbe30ccbdfc0ac1008bc74b59",
	"pubvis-exec-d-r12-a": "45ba181aafc8ca1aeb41916afd4cbb4d9191aa8834bb6b6b3b225f39df7c8173",
	"pubvis-exec-d-r12-b": "b47944c243ce7ea639d415fcbb060ab4e6175059130950b338c57d01ffeb3a83",
	"pubvis-exec-d-s01-a": "73e1555e6ce658e4f7f44ca484993d6345b0e934e0a771d2e80e7e920727fce1",
	"pubvis-exec-d-s01-b": "1287f1a519c1be245f73815b89e510d34f2d162edd5e8fd57cfdcc2d950af008",
	"pubvis-exec-d-s02-a": "fd6d82ba3c0f6586aa3240a9a73019e3056f39ad948412924e87e9f82a3ee1ee",
	"pubvis-exec-d-s02-b": "56c58b81a2d89db7128bbd99b5f57efb069fc5ed7dd6d3f6f763c5107f2fdb5b",
	"pubvis-exec-d-s03-a": "02f39fdda136ea21a1c59500674be16f6f86418e8c1dd8eafd6633b168976ac2",
	"pubvis-exec-d-s03-b": "2c3e77ab1a129c4eaf40a0bce682b234f8188a4aabfa47bcd14d6cd47a88a5e2",
	"pubvis-exec-d-s04-a": "20d47c404f0e6cf80e8c4292ed490d20bdda26a577d7b0ffdc49e10b68c91f31",
	"pubvis-exec-d-s04-b": "e301115f4f7b8cd8917deec689662b8e21924864f0fd4d458a6082243a4ea215",
	"pubvis-exec-d-s05-a": "13e56112bf5e904f40c04a800e8a7d6a09b634f83376d8a9b2e05d75f3a819ff",
	"pubvis-exec-d-s05-b": "75ee1b8cb6fedd26934af686fe0521dece6f04eb10f784f8f2dd4fb99678b358",
	"pubvis-exec-d-s06-a": "728926b87c14895d0efd2e92b6606eaac1faa957fc252da4e064eff4a9fc6c5a",
	"pubvis-exec-d-s06-b": "12f094813346d4a949406edcbc8a1862290870ba22779339c6401fe0d4cafc62",
	"pubvis-exec-d-s07-a": "6dea14edc91ce2502ae8223d4fd291e5115d34caacebfe507f9bbfd2900547b7",
	"pubvis-exec-d-s07-b": "c452d0d6022891ecb32adcf29d93c2ba32c1e1087066595bc5aa1117e66deeb2",
	"pubvis-exec-d-s08-a": "d3a34af0f8d4e046bf9a138863e08b44695d6d37c09891e1cb39002ded3adf29",
	"pubvis-exec-d-s08-b": "7ca472316f45b25b9c737e14bcfe05fc1b79d717a1e81706a7d07854f22b085a",
	"pubvis-exec-d-s09-a": "aa599df3947a7a5e70ff5706738362659ac5e6f7d30b90d10e18b5f27c09f6b8",
	"pubvis-exec-d-s09-b": "01e226c23fa91f4061d5bb736a8e986ddf88b28829cc237b69b80aacc0339528",
	"pubvis-exec-d-s10-a": "63d3d533bec74ce87d8b16b020e1468d091110abef4b4d4de7c5eafc4c81bdf6",
	"pubvis-exec-d-s10-b": "fab861cad1a8be4fd57f18002166ef5a5b89d725eb0d1426e2e952a7f808e470",
	"pubvis-exec-d-s11-a": "135716f27fca7695a8f535979b93d89426207003c609a76128ae7b00515ad0af",
	"pubvis-exec-d-s11-b": "6170e40cd3ae993091f25bcfef93509fb5b1fafab49cfa60a165729295a1aff2",
	"pubvis-exec-d-s12-a": "acb66f303fd581f82512d8d60415b71eda822a83584a77f48286e03e8a42425e",
	"pubvis-exec-d-s12-b": "14334971ccc4dfd4d6fb890f4a2c64f095286d6a4352e4c3b03dcc8936c8864d",
}

func pubvisWantPayloadSHA(t *testing.T, id string) string {
	t.Helper()
	want, ok := pubvisPayloadGoldenSHA256[id]
	if !ok {
		t.Fatalf("no golden payload sha256 registered for %q", id)
	}
	return want
}

// ---------------------------------------------------------------------------
// Production-entry-point seeding helpers (registry API only).
// ---------------------------------------------------------------------------

func (s *pubvisService) seed(t *testing.T, reg *Registry, owner string, id string) publication.BundleExecution {
	t.Helper()
	execution := pubvisExecution(id, false, owner)
	claimed, err := reg.AcquireBundleLease(s.ctx, execution.Key, owner, pubvisLeaseUntil)
	if err != nil || !claimed {
		s.fail("acquire lease "+execution.Key, fmt.Errorf("claimed=%v err=%w", claimed, err))
	}
	if err := reg.SaveExecution(s.ctx, execution, owner); err != nil {
		s.fail("save execution "+id, err)
	}
	return execution
}

func (s *pubvisService) publishClean(t *testing.T, reg *Registry, name, expected, id, owner string) publication.BundleExecution {
	t.Helper()
	published := pubvisExecution(id, true, owner)
	if err := reg.PublishExecution(s.ctx, name, expected, published); err != nil {
		s.fail("clean publish "+id, err)
	}
	return published
}

// ---------------------------------------------------------------------------
// Publish gate: the test seam inside the publish transaction.
// ---------------------------------------------------------------------------

type pubvisGate struct {
	arrived chan publishAttempt
	release chan error
}

func newPubvisGate() *pubvisGate {
	return &pubvisGate{arrived: make(chan publishAttempt, 1), release: make(chan error, 1)}
}

func pubvisArmGate(t *testing.T, gate *pubvisGate) {
	t.Helper()
	// One-shot engagement: the hook rendezvous with the FIRST publish that
	// reaches it and passes every later call through, so subsequent
	// publishes in the same subtest are never captured by a stale hook.
	var once sync.Once
	fn := func(attempt publishAttempt) error {
		engaged := false
		once.Do(func() { engaged = true })
		if !engaged {
			return nil
		}
		attempt.Statements = append([]string(nil), attempt.Statements...)
		gate.arrived <- attempt
		return <-gate.release
	}
	previous := publishGate.Swap(&fn)
	t.Cleanup(func() {
		publishGate.Store(previous)
		// Never leave a publish parked when the test has already failed:
		// unblock a still-waiting hook exactly once if nobody else did.
		select {
		case gate.release <- nil:
		default:
		}
		select {
		case <-gate.arrived:
		default:
		}
	})
}

func pubvisWaitAttempt(t *testing.T, gate *pubvisGate) publishAttempt {
	t.Helper()
	select {
	case attempt := <-gate.arrived:
		return attempt
	case <-time.After(30 * time.Second):
		t.Fatal("publish gate never fired within 30s: the gate call is missing or publish returned early")
		return publishAttempt{}
	}
}

var errPubvisInjected = errors.New("injected publish transaction failure")

// ---------------------------------------------------------------------------
// Golden statement literals. These mirror, byte for byte, the catalog
// statements publish.go must hand to the driver; the witness compares the
// recorded statement stream against these literals.
// ---------------------------------------------------------------------------

func pubvisGoldenFence(fqLeases string) string {
	return "SELECT true FROM " + fqLeases + " WHERE key = $1 AND owner_id = $2 AND expires_at >= $3 FOR UPDATE"
}

func pubvisGoldenLock(fqExec string) string {
	return "SELECT true FROM " + fqExec + " WHERE id = $1 FOR UPDATE"
}

func pubvisGoldenCAS(fqPointers string) string {
	return "INSERT INTO " + fqPointers + " (pointer_key, name_bytes, execution_id, updated_at)" +
		" VALUES ($1, $2, $3, $4)" +
		" ON CONFLICT (pointer_key) DO UPDATE SET execution_id = $3, updated_at = $4" +
		" WHERE " + BundlePointersTable + ".execution_id = $5"
}

func pubvisGoldenUpdate(fqExec string) string {
	return "UPDATE " + fqExec + " SET execution_key = $2, project = $3, dataset_generation = $4, recipe_name = $5, " +
		"translation_version = $6, state = $7, created_at = $8, updated_at = $9, " +
		"ready_at = $10, published_at = $11, payload = $12 WHERE id = $1"
}

func pubvisGoldenPointerRead(fqPointers string) string {
	return "SELECT execution_id FROM " + fqPointers + " WHERE pointer_key = $1"
}

func pubvisGoldenPayloadRead(fqExec string) string {
	return "SELECT payload = $2::jsonb FROM " + fqExec + " WHERE id = $1"
}

func (s *pubvisService) goldenPublishStatements(fqLeases, fqExec, fqPointers string) []string {
	return []string{
		pubvisGoldenFence(fqLeases),
		pubvisGoldenLock(fqExec),
		pubvisGoldenCAS(fqPointers),
		pubvisGoldenUpdate(fqExec),
	}
}

// goldenConvergedStatements is the NO-OP statement set: the three guards
// plus the two convergence reads -- the execution UPDATE is never issued
// on the converged path.
func (s *pubvisService) goldenConvergedStatements(fqLeases, fqExec, fqPointers string) []string {
	return []string{
		pubvisGoldenFence(fqLeases),
		pubvisGoldenLock(fqExec),
		pubvisGoldenCAS(fqPointers),
		pubvisGoldenPointerRead(fqPointers),
		pubvisGoldenPayloadRead(fqExec),
	}
}

func pubvisEqualStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d statements recorded %q, want %d %q", label, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: statement[%d] drifted\n got: %q\nwant: %q", label, i, got[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Full exact execution comparison against the published fixture.
// ---------------------------------------------------------------------------

func pubvisExpectExactPublished(t *testing.T, s *pubvisService, want publication.BundleExecution) {
	t.Helper()
	got, err := s.regR.GetExecution(s.ctx, want.ID)
	if err != nil {
		s.fail("get published execution "+want.ID, err)
	}
	check := func(field string, gotV, wantV any) {
		t.Helper()
		if !reflect.DeepEqual(gotV, wantV) {
			t.Fatalf("published execution %s.%s = %#v, want %#v", want.ID, field, gotV, wantV)
		}
	}
	check("ID", got.ID, want.ID)
	check("Key", got.Key, want.Key)
	check("State", got.State, want.State)
	check("Project", got.Project, want.Project)
	check("DatasetGeneration", got.DatasetGeneration, want.DatasetGeneration)
	check("Name", got.Name, want.Name)
	check("TranslationVersion", got.TranslationVersion, want.TranslationVersion)
	check("RecipeDigest", got.RecipeDigest, want.RecipeDigest)
	check("SchemaDigest", got.SchemaDigest, want.SchemaDigest)
	check("ScopeDigest", got.ScopeDigest, want.ScopeDigest)
	check("ReceiptID", got.ReceiptID, want.ReceiptID)
	check("EngineVersion", got.EngineVersion, want.EngineVersion)
	check("AuthScopeMode", got.AuthScopeMode, want.AuthScopeMode)
	check("AuthResourcePaths", got.AuthResourcePaths, want.AuthResourcePaths)
	check("OwnerID", got.OwnerID, want.OwnerID)
	check("Error", got.Error, want.Error)
	check("FailureCode", got.FailureCode, want.FailureCode)
	check("CreatedAt", got.CreatedAt.Equal(want.CreatedAt), true)
	check("UpdatedAt", got.UpdatedAt.Equal(want.UpdatedAt), true)
	check("ReadyAt set", got.ReadyAt != nil && got.ReadyAt.Equal(*want.ReadyAt), true)
	check("PublishedAt set", got.PublishedAt != nil && got.PublishedAt.Equal(*want.PublishedAt), true)
	if len(got.Outputs) != len(want.Outputs) {
		t.Fatalf("published outputs = %d, want %d", len(got.Outputs), len(want.Outputs))
	}
	for i := range want.Outputs {
		w, g := want.Outputs[i], got.Outputs[i]
		check(fmt.Sprintf("outputs[%d].Name", i), g.Name, w.Name)
		check(fmt.Sprintf("outputs[%d].PhysicalTable", i), g.PhysicalTable, w.PhysicalTable)
		check(fmt.Sprintf("outputs[%d].RowCount", i), g.RowCount, w.RowCount)
		check(fmt.Sprintf("outputs[%d].ByteCount", i), g.ByteCount, w.ByteCount)
		check(fmt.Sprintf("outputs[%d].State", i), g.State, w.State)
		check(fmt.Sprintf("outputs[%d].Selector", i), g.Selector, w.Selector)
		check(fmt.Sprintf("outputs[%d].Columns", i), g.Columns, w.Columns)
		check(fmt.Sprintf("outputs[%d].SourceRow", i), g.SourceRow, w.SourceRow)
		check(fmt.Sprintf("outputs[%d].VerifiedAt set", i), g.VerifiedAt != nil && g.VerifiedAt.Equal(*w.VerifiedAt), true)
	}
}

// ---------------------------------------------------------------------------
// The acceptance test.
// ---------------------------------------------------------------------------

func TestPublishExecutionAtomicity(t *testing.T) {
	t.Run("A_reader_sees_old_pointer_until_commit", func(t *testing.T) {
		s := newPubvisService(t)
		name := pubvisPointerName()
		fqLeases, fqExec, fqPointers := s.qualified(BundleLeasesTable), s.qualified(BundleExecutionsTable), s.qualified(BundlePointersTable)

		// The pre-chain published execution gives readers an OLD pointer
		// value to keep seeing while the next publish is open.
		s.seed(t, s.regA, pubvisOwnerA, "pubvis-exec-a0")
		a0 := s.publishClean(t, s.regA, name, "", "pubvis-exec-a0", pubvisOwnerA)
		pubvisExpectExactPublished(t, s, a0)

		const rounds = 12
		for round := 1; round <= rounds; round++ {
			id := fmt.Sprintf("pubvis-exec-a%d", round)
			previous := fmt.Sprintf("pubvis-exec-a%d", round-1)
			s.seed(t, s.regA, pubvisOwnerA, id)
			published := pubvisExecution(id, true, pubvisOwnerA)

			// Capture the two backend pids BEFORE the publish opens its
			// transaction: the publisher pool carries exactly one physical
			// connection, so querying it mid-transaction would queue.
			pubPID := s.backendPID(t, s.pub, "publisher")
			readPID := s.backendPID(t, s.reader, "reader")
			if pubPID == readPID {
				t.Fatalf("publisher and reader share backend pid %d, want two real PG connections", pubPID)
			}

			gate := newPubvisGate()
			pubvisArmGate(t, gate)
			done := make(chan error, 1)
			go func() { done <- s.regA.PublishExecution(s.ctx, name, previous, published) }()

			attempt := pubvisWaitAttempt(t, gate)
			if attempt.BackendPID != pubPID {
				t.Fatalf("round %d: publish transaction backend pid = %d, want the publisher pool backend %d", round, attempt.BackendPID, pubPID)
			}
			if attempt.BackendPID == readPID {
				t.Fatalf("round %d: publish shares backend %d with the reader", round, readPID)
			}
			pubvisEqualStrings(t, fmt.Sprintf("round %d statements", round), attempt.Statements,
				s.goldenPublishStatements(fqLeases, fqExec, fqPointers))

			// WHILE THE TRANSACTION IS OPEN: readers still see the old
			// pointer and the old execution state.
			pointer, err := s.regR.GetPointer(s.ctx, name)
			if err != nil || pointer.ExecutionID != previous {
				t.Fatalf("round %d: reader saw pointer %+v err=%v during publish, want %q", round, pointer, err, previous)
			}
			reading, err := s.regR.GetExecution(s.ctx, id)
			if err != nil || reading.State != publication.BundleRunning {
				t.Fatalf("round %d: reader saw execution state %q err=%v during publish, want RUNNING", round, reading.State, err)
			}
			if row := s.pointerRow(t, name); row == nil || row.Execution != previous {
				t.Fatalf("round %d: SQL witness of pointer during publish = %+v, want %q", round, row, previous)
			}
			if state := s.wText(t, s.reader,
				"SELECT payload->>'state' FROM "+fqExec+" WHERE id = $1", id); state != "RUNNING" {
				t.Fatalf("round %d: SQL witness of execution during publish = %q, want RUNNING", round, state)
			}
			// The publish backend is genuinely parked mid-transaction on
			// the catalog UPDATE -- not executing anything else.
			txState, lastQuery := s.activity(t, attempt.BackendPID)
			if txState != "idle in transaction" {
				t.Fatalf("round %d: publish backend state = %q, want idle in transaction", round, txState)
			}
			if lastQuery != pubvisGoldenUpdate(fqExec) {
				t.Fatalf("round %d: publish backend last query drifted from the golden UPDATE:\n got: %q", round, lastQuery)
			}

			gate.release <- nil
			if err := <-done; err != nil {
				s.fail(fmt.Sprintf("round %d publish", round), err)
			}

			// AFTER COMMIT: the pointer names the execution and the full
			// exact execution is visible in the same reader surface.
			pointer, err = s.regR.GetPointer(s.ctx, name)
			if err != nil || pointer.ExecutionID != id {
				t.Fatalf("round %d: reader saw pointer %+v err=%v after commit, want %q", round, pointer, err, id)
			}
			pubvisExpectExactPublished(t, s, published)
			if got := pubvisSHA256Hex(s.payloadText(t, id)); got != pubvisWantPayloadSHA(t, id) {
				t.Fatalf("round %d: committed payload digest = %s, want golden %s", round, got, pubvisWantPayloadSHA(t, id))
			}
			row := s.pointerRow(t, name)
			if row == nil {
				t.Fatalf("round %d: pointer row vanished after commit", round)
			}
			if row.Execution != id {
				t.Fatalf("round %d: pointer row execution = %q, want %q", round, row.Execution, id)
			}
			t.Logf("OBS A round %d: old pointer held until COMMIT; full exact execution after (pids %d/%d)", round, attempt.BackendPID, readPID)
		}

		// Canonical pointer literals after the chain: golden digest key,
		// byte-exact NUL-free bytea name, single row.
		row := s.pointerRow(t, name)
		wantName := pubvisPointerName()
		sum := sha256.Sum256([]byte(wantName))
		if row == nil || row.PointerKey != hex.EncodeToString(sum[:]) || row.NameHex != hex.EncodeToString([]byte(wantName)) {
			t.Fatalf("pointer literals = %+v, want key %s name hex %s", row, hex.EncodeToString(sum[:]), hex.EncodeToString([]byte(wantName)))
		}
		if n := s.wInt64(t, s.reader, "SELECT count(*) FROM "+fqPointers); n != 1 {
			t.Fatalf("pointer rows = %d, want exactly 1 after the whole chain", n)
		}
	})

	t.Run("B_injected_failure_exposes_neither_half", func(t *testing.T) {
		s := newPubvisService(t)
		name := pubvisPointerName()
		s.seed(t, s.regA, pubvisOwnerA, "pubvis-exec-b0")
		s.publishClean(t, s.regA, name, "", "pubvis-exec-b0", pubvisOwnerA)

		const rounds = 3
		for round := 1; round <= rounds; round++ {
			id := fmt.Sprintf("pubvis-exec-b%d", round)
			s.seed(t, s.regA, pubvisOwnerA, id)
			published := pubvisExecution(id, true, pubvisOwnerA)

			// Pre-attempt byte witnesses.
			prePointer := s.pointerRow(t, name)
			prePayload := s.payloadText(t, id)
			if pubvisSHA256Hex(prePayload) == pubvisWantPayloadSHA(t, id) {
				t.Fatalf("round %d: fixture already carries the published payload", round)
			}

			gate := newPubvisGate()
			pubvisArmGate(t, gate)
			done := make(chan error, 1)
			go func() { done <- s.regA.PublishExecution(s.ctx, name, "pubvis-exec-b0", published) }()

			attempt := pubvisWaitAttempt(t, gate)
			// Even while the aborted attempt is open, readers see only the
			// old world.
			pointer, err := s.regR.GetPointer(s.ctx, name)
			if err != nil || pointer.ExecutionID != "pubvis-exec-b0" {
				t.Fatalf("round %d: reader saw pointer %+v err=%v during the doomed publish", round, pointer, err)
			}
			gate.release <- errPubvisInjected
			err = <-done
			if !errors.Is(err, errPubvisInjected) {
				t.Fatalf("round %d: injected failure did not surface, got %v", round, err)
			}

			// NEITHER half may be exposed: pointer and execution rows stay
			// byte-identical to their pre-attempt bytes.
			postPointer := s.pointerRow(t, name)
			if postPointer == nil || *postPointer != *prePointer {
				t.Fatalf("round %d: pointer row drifted across the abort: %+v vs %+v", round, postPointer, prePointer)
			}
			postPayload := s.payloadText(t, id)
			if postPayload != prePayload {
				t.Fatalf("round %d: execution payload drifted across the abort (first difference at byte %d)",
					round, firstDiff(prePayload, postPayload))
			}
			if state := s.wText(t, s.reader,
				"SELECT payload->>'state' FROM "+s.qualified(BundleExecutionsTable)+" WHERE id = $1", id); state != "RUNNING" {
				t.Fatalf("round %d: aborted execution state = %q, want RUNNING", round, state)
			}
			t.Logf("OBS B round %d: crash window after statements pid=%d rolled back; rows byte-identical", round, attempt.BackendPID)
		}

		// Post-abort convergence: the clean retry of the last aborted
		// publish lands both halves together, byte-exact.
		const finalID = "pubvis-exec-b3"
		published := pubvisExecution(finalID, true, pubvisOwnerA)
		if err := s.regA.PublishExecution(s.ctx, name, "pubvis-exec-b0", published); err != nil {
			s.fail("post-abort convergence publish", err)
		}
		pubvisExpectExactPublished(t, s, published)
		if got := pubvisSHA256Hex(s.payloadText(t, finalID)); got != pubvisWantPayloadSHA(t, finalID) {
			t.Fatalf("post-abort payload digest = %s, want golden %s", got, pubvisWantPayloadSHA(t, finalID))
		}
		row := s.pointerRow(t, name)
		if row == nil || row.Execution != finalID {
			t.Fatalf("post-abort pointer row = %+v, want %q", row, finalID)
		}

		// Absent-pointer abort: a first publish whose pointer row never
		// existed must leave NO pointer row at all after the abort.
		absentID := "pubvis-exec-b9"
		s.seed(t, s.regA, pubvisOwnerA, absentID)
		absentName := pubvisPointerName() + "|absent"
		gate := newPubvisGate()
		pubvisArmGate(t, gate)
		done := make(chan error, 1)
		go func() {
			done <- s.regA.PublishExecution(s.ctx, absentName, "", pubvisExecution(absentID, true, pubvisOwnerA))
		}()
		pubvisWaitAttempt(t, gate)
		gate.release <- errPubvisInjected
		if err := <-done; !errors.Is(err, errPubvisInjected) {
			t.Fatalf("absent-pointer abort did not surface the injection, got %v", err)
		}
		if row := s.pointerRow(t, absentName); row != nil {
			t.Fatalf("aborted first publish left a pointer row behind: %+v", row)
		}
		if _, err := s.regR.GetPointer(s.ctx, absentName); !errors.Is(err, publication.ErrBundleNotFound) {
			t.Fatalf("aborted first publish pointer readable: %v, want ErrBundleNotFound", err)
		}
		// Recovery from the absent-pointer abort publishes cleanly.
		if err := s.regA.PublishExecution(s.ctx, absentName, "", pubvisExecution(absentID, true, pubvisOwnerA)); err != nil {
			s.fail("recovery publish after absent-pointer abort", err)
		}
		row = s.pointerRow(t, absentName)
		if row == nil || row.Execution != absentID {
			t.Fatalf("recovery pointer row = %+v, want %q", row, absentID)
		}
	})

	t.Run("C_same_execution_retry_is_idempotent", func(t *testing.T) {
		s := newPubvisService(t)
		name := pubvisPointerName()
		const id = "pubvis-exec-c"
		fqLeases, fqExec, fqPointers := s.qualified(BundleLeasesTable), s.qualified(BundleExecutionsTable), s.qualified(BundlePointersTable)
		s.seed(t, s.regA, pubvisOwnerA, id)
		published := pubvisExecution(id, true, pubvisOwnerA)

		// First publish: ordinary path with the golden statement set.
		gate := newPubvisGate()
		pubvisArmGate(t, gate)
		done := make(chan error, 1)
		go func() { done <- s.regA.PublishExecution(s.ctx, name, "", published) }()
		attempt := pubvisWaitAttempt(t, gate)
		if attempt.Converged {
			t.Fatal("first publish took the converged path before anything was published")
		}
		pubvisEqualStrings(t, "first publish", attempt.Statements, s.goldenPublishStatements(fqLeases, fqExec, fqPointers))
		gate.release <- nil
		if err := <-done; err != nil {
			s.fail("first publish", err)
		}

		// Byte witnesses across the retry.
		pointerBefore := s.pointerValue(t, name)
		payloadBefore := s.payloadText(t, id)
		epochBefore := s.wInt64(t, s.reader,
			"SELECT epoch FROM "+fqLeases+" WHERE key = $1", "bundle-"+id)
		updatedAtLiteral := pointerBefore.UpdatedAt
		if updatedAtLiteral != "2026-11-09T10:02:00.112233Z" {
			t.Fatalf("pointer updated_at literal = %q, want the published stamp to microsecond precision", updatedAtLiteral)
		}

		// Identical retry: converged no-op success.
		gate2 := newPubvisGate()
		pubvisArmGate(t, gate2)
		done = make(chan error, 1)
		go func() { done <- s.regA.PublishExecution(s.ctx, name, "", published) }()
		retry := pubvisWaitAttempt(t, gate2)
		if !retry.Converged {
			t.Fatal("identical retry did not take the converged path")
		}
		pubvisEqualStrings(t, "converged retry", retry.Statements, s.goldenConvergedStatements(fqLeases, fqExec, fqPointers))
		gate2.release <- nil
		if err := <-done; err != nil {
			t.Fatalf("identical retry = %v, want nil (idempotent)", err)
		}
		if after := s.pointerValue(t, name); after != pointerBefore {
			t.Fatalf("retry mutated the pointer row: %+v vs %+v", after, pointerBefore)
		}
		if after := s.payloadText(t, id); after != payloadBefore {
			t.Fatalf("retry mutated the payload bytes (first difference at byte %d)", firstDiff(payloadBefore, after))
		}
		if epoch := s.wInt64(t, s.reader, "SELECT epoch FROM "+fqLeases+" WHERE key = $1", "bundle-"+id); epoch != epochBefore {
			t.Fatalf("retry bumped the lease epoch to %d, want it untouched at %d", epoch, epochBefore)
		}

		// A drifted retry is refused with the named conflict -- the
		// converged path only forgives the EXACT execution.
		drifted := published
		drifted.PublishedAt = nil
		driftedErr := s.regA.PublishExecution(s.ctx, name, "", drifted)
		if !errors.Is(driftedErr, publication.ErrBundlePointerConflict) {
			t.Fatalf("drifted retry = %v, want ErrBundlePointerConflict", driftedErr)
		}
		if errors.Is(driftedErr, publication.ErrBundleLeaseLost) || errors.Is(driftedErr, publication.ErrBundleNotFound) {
			t.Fatal("drifted retry reported a sibling typed error, want exactly ErrBundlePointerConflict")
		}
		if after := s.pointerValue(t, name); after != pointerBefore {
			t.Fatalf("refused drifted retry mutated the pointer row: %+v vs %+v", after, pointerBefore)
		}
		if after := s.payloadText(t, id); after != payloadBefore {
			t.Fatalf("refused drifted retry mutated the payload (first difference at byte %d)", firstDiff(payloadBefore, after))
		}

		// Concurrency with no new work: the exact retry still converges
		// after the refusals.
		if err := s.regA.PublishExecution(s.ctx, name, "", published); err != nil {
			t.Fatalf("exact retry after refusals = %v, want nil", err)
		}
		if got := pubvisSHA256Hex(s.payloadText(t, id)); got != pubvisWantPayloadSHA(t, id) {
			t.Fatalf("canonical payload digest = %s, want golden %s", got, pubvisWantPayloadSHA(t, id))
		}
	})

	t.Run("D_concurrent_publishers_one_typed_winner", func(t *testing.T) {
		s := newPubvisService(t)
		fqPointers := s.qualified(BundlePointersTable)
		fqExec := s.qualified(BundleExecutionsTable)

		runRound := func(tag string, staleLease bool) {
			t.Helper()
			name := pubvisPointerName() + "|" + tag
			idA, idB := "pubvis-exec-d-"+tag+"-a", "pubvis-exec-d-"+tag+"-b"
			s.seed(t, s.regA, pubvisOwnerA, idA)
			s.seed(t, s.regB, pubvisOwnerB, idB)
			if staleLease {
				// Same-owner reclaim with a past expiry: B's fence is now
				// expired, making B the literal stale publisher.
				claimed, err := s.regB.AcquireBundleLease(s.ctx, "bundle-"+idB, pubvisOwnerB, time.Now().UTC().Add(-time.Minute))
				if err != nil || !claimed {
					s.fail("expire stale lease", fmt.Errorf("claimed=%v err=%w", claimed, err))
				}
			}
			pubA, pubB := pubvisExecution(idA, true, pubvisOwnerA), pubvisExecution(idB, true, pubvisOwnerB)

			pidA := s.backendPID(t, s.pub, "publisher A")
			pidB := s.backendPID(t, s.pubB, "publisher B")
			if pidA == pidB {
				t.Fatalf("round %s: publishers share backend pid %d, want two real PG connections", tag, pidA)
			}

			start := make(chan struct{})
			var wg sync.WaitGroup
			var errA, errB error
			wg.Add(2)
			go func() { defer wg.Done(); <-start; errA = s.regA.PublishExecution(s.ctx, name, "", pubA) }()
			go func() { defer wg.Done(); <-start; errB = s.regB.PublishExecution(s.ctx, name, "", pubB) }()
			close(start)
			wg.Wait()

			winners, losers := []string{}, []string{}
			for id, err := range map[string]error{idA: errA, idB: errB} {
				switch {
				case err == nil:
					winners = append(winners, id)
				case errors.Is(err, publication.ErrBundlePointerConflict) &&
					!errors.Is(err, publication.ErrBundleLeaseLost) &&
					!errors.Is(err, publication.ErrBundleNotFound):
					losers = append(losers, id)
				default:
					t.Fatalf("round %s: publisher %s failed with %v, want exactly ErrBundlePointerConflict or success", tag, id, err)
				}
			}
			if len(winners) != 1 || len(losers) != 1 {
				t.Fatalf("round %s: winners=%v losers=%v, want exactly one each", tag, winners, losers)
			}
			winner, loser := winners[0], losers[0]
			row := s.pointerRow(t, name)
			if row == nil || row.Execution != winner {
				t.Fatalf("round %s: pointer row = %+v, want the winner %q", tag, row, winner)
			}
			if n := s.wInt64(t, s.reader, "SELECT count(*) FROM "+fqPointers+" WHERE pointer_key = $1", pointerKey(name)); n != 1 {
				t.Fatalf("round %s: pointer rows = %d, want 1", tag, n)
			}
			// The loser's execution row is untouched RUNNING and its
			// payload is byte-identical to the pre-publish fixture.
			if state := s.wText(t, s.reader,
				"SELECT payload->>'state' FROM "+fqExec+" WHERE id = $1", loser); state != "RUNNING" {
				t.Fatalf("round %s: loser execution state = %q, want RUNNING (fully rolled back)", tag, state)
			}
			if pubvisSHA256Hex(s.payloadText(t, loser)) == pubvisWantPayloadSHA(t, loser) {
				t.Fatalf("round %s: loser payload carries the published digest", tag)
			}
			// The winner is fully exact and byte-stable under its own
			// idempotent retry.
			want := pubvisExecution(winner, true, map[string]string{idA: pubvisOwnerA, idB: pubvisOwnerB}[winner])
			pubvisExpectExactPublished(t, s, want)
			regWinner, winnerID := s.regA, idA
			if winner == idB {
				regWinner, winnerID = s.regB, idB
			}
			if staleLease && winnerID == idB {
				t.Fatal("stale-lease round let the expired fence win")
			}
			if err := regWinner.PublishExecution(s.ctx, name, "", want); err != nil {
				t.Fatalf("round %s: winner retry = %v, want the idempotent nil", tag, err)
			}
			if after := s.pointerValue(t, name); after != *row {
				t.Fatalf("round %s: winner retry mutated the pointer %+v vs %+v", tag, after, *row)
			}
			t.Logf("OBS D round %s (staleLease=%v): winner %s pid=%d, loser %s refused with the named conflict",
				tag, staleLease, winner, map[string]uint32{idA: pidA, idB: pidB}[winner], loser)
		}

		for round := 1; round <= 12; round++ {
			runRound(fmt.Sprintf("r%02d", round), false)
		}
		for round := 1; round <= 12; round++ {
			runRound(fmt.Sprintf("s%02d", round), true)
		}
	})

	t.Run("E_no_clickhouse_surface_inside_the_publish_transaction", func(t *testing.T) {
		s := newPubvisService(t)
		name := pubvisPointerName()
		const id = "pubvis-exec-e"
		fqLeases, fqExec, fqPointers := s.qualified(BundleLeasesTable), s.qualified(BundleExecutionsTable), s.qualified(BundlePointersTable)
		s.seed(t, s.regA, pubvisOwnerA, id)
		published := pubvisExecution(id, true, pubvisOwnerA)

		gate := newPubvisGate()
		pubvisArmGate(t, gate)
		done := make(chan error, 1)
		go func() { done <- s.regA.PublishExecution(s.ctx, name, "", published) }()
		attempt := pubvisWaitAttempt(t, gate)

		// (1) Statement stream: exactly the golden catalog literals, in
		// order, nothing else -- no statement can be a ClickHouse
		// operation because the whole set is the catalog contract.
		pubvisEqualStrings(t, "publish statement stream", attempt.Statements,
			s.goldenPublishStatements(fqLeases, fqExec, fqPointers))

		// (2) Cross-connection witness: the publish backend is parked
		// idle in transaction on the final catalog UPDATE.
		txState, lastQuery := s.activity(t, attempt.BackendPID)
		if txState != "idle in transaction" {
			t.Fatalf("publish backend state during the window = %q, want idle in transaction", txState)
		}
		if lastQuery != pubvisGoldenUpdate(fqExec) {
			t.Fatalf("publish backend last query is not the golden catalog UPDATE:\n got: %q", lastQuery)
		}

		// (3) Live sweep from the reader backend: while the publish
		// transaction is open, no client backend anywhere is executing a
		// ClickHouse-shaped statement.
		sweep := s.wInt64(t, s.reader,
			"SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' "+
				"AND pid <> pg_backend_pid() AND query ~* 'clickhouse|ch_send|insert into .*ch_'")
		if sweep != 0 {
			t.Fatalf("%d backends were executing ClickHouse-shaped statements inside the publish window", sweep)
		}

		gate.release <- nil
		if err := <-done; err != nil {
			s.fail("publish after E window", err)
		}
		pubvisExpectExactPublished(t, s, published)

		// (4) Structural guarantee: PublishExecution takes values only --
		// no callback or channel can smuggle a ClickHouse call into the
		// transaction window.
		method, ok := reflect.TypeOf(&Registry{}).MethodByName("PublishExecution")
		if !ok {
			t.Fatal("Registry.PublishExecution missing")
		}
		for i := 0; i < method.Type.NumIn(); i++ {
			switch kind := method.Type.In(i).Kind(); kind {
			case reflect.Func, reflect.Chan:
				t.Fatalf("PublishExecution parameter %d has kind %v: the publish API must take values only", i, kind)
			}
		}
		if method.Type.NumIn() != 5 || method.Type.NumOut() != 1 {
			t.Fatalf("PublishExecution signature shape = (%d in, %d out), want (5 in, 1 out)",
				method.Type.NumIn(), method.Type.NumOut())
		}
		ifaceMethod, ok := reflect.TypeOf((*publication.BundleCatalog)(nil)).Elem().MethodByName("PublishExecution")
		if !ok || ifaceMethod.Type.NumIn() != 4 {
			t.Fatalf("BundleCatalog.PublishExecution interface shape drifted: %+v", ifaceMethod)
		}
	})

	t.Run("F_prefix_split_control_is_observable", func(t *testing.T) {
		// Sensitivity control (the Arango arm-A analogue): replaying the
		// pre-fix two-leg shape -- the execution UPDATE applied outside
		// any publish transaction, pointer leg stopped -- MUST expose
		// PUBLISHED-without-pointer to the same witnesses B and A rely on.
		// If this control ever fails to see the split, the atomicity
		// witnesses above are vacuous.
		s := newPubvisService(t)
		name := pubvisPointerName()
		const id = "pubvis-exec-f"
		fqExec := s.qualified(BundleExecutionsTable)
		s.seed(t, s.regA, pubvisOwnerA, id)
		published := pubvisExecution(id, true, pubvisOwnerA)
		params, err := executionParams(published)
		if err != nil {
			s.fail("build publish params", err)
		}

		// Leg 1 verbatim in the production statement shape, but committed
		// on its own -- the pre-fix product bug.
		leg1 := pubvisGoldenUpdate(fqExec)
		if _, err := s.pub.Exec(s.ctx, leg1, params...); err != nil {
			s.fail("pre-fix leg 1", err)
		}

		state, err := s.regR.GetExecution(s.ctx, id)
		if err != nil || state.State != publication.BundlePublished {
			t.Fatalf("sensitivity failure: pre-fix split execution state = %q err=%v, want PUBLISHED visible", state.State, err)
		}
		if _, err := s.regR.GetPointer(s.ctx, name); !errors.Is(err, publication.ErrBundleNotFound) {
			t.Fatalf("sensitivity failure: probe must not see a pointer in the split state, got %v", err)
		}
		if row := s.pointerRow(t, name); row != nil {
			t.Fatalf("sensitivity failure: pointer row exists in the split state: %+v", row)
		}
		t.Log("OBS F: the pre-fix split IS observable -- witnesses in A/B/D are sensitive to half-publish")

		// The repaired path then lands both halves together from exactly
		// this split state.
		if err := s.regA.PublishExecution(s.ctx, name, "", published); err != nil {
			s.fail("repaired publish from the split state", err)
		}
		row := s.pointerRow(t, name)
		if row == nil || row.Execution != id {
			t.Fatalf("repaired publish pointer row = %+v, want %q", row, id)
		}
		pubvisExpectExactPublished(t, s, published)
	})

	t.Run("G_payload_goldens_are_load_bearing", func(t *testing.T) {
		// Every d-* golden pin is asserted POSITIVELY: publishing the
		// canonical execution through the production write path with its
		// round-D owner binding (a->owner-a, b->owner-b) must store a
		// payload whose sha256 equals the pin. Negative-only usage (D's
		// loser control) can never redden on pin drift -- this closes that
		// gap: a future payload change or a wrong-owner pin fails here by
		// name.
		s := newPubvisService(t)
		pinned := 0
		for _, p := range []string{"r", "s"} {
			for i := 1; i <= 12; i++ {
				tag := fmt.Sprintf("%s%02d", p, i)
				for _, suffix := range []string{"a", "b"} {
					id := "pubvis-exec-d-" + tag + "-" + suffix
					owner := pubvisOwnerA
					if suffix == "b" {
						owner = pubvisOwnerB
					}
					name := pubvisPointerName() + "|g" + tag + suffix
					s.seed(t, s.regA, owner, id)
					published := pubvisExecution(id, true, owner)
					if err := s.regA.PublishExecution(s.ctx, name, "", published); err != nil {
						s.fail("G publish "+id, err)
					}
					if got := pubvisSHA256Hex(s.payloadText(t, id)); got != pubvisWantPayloadSHA(t, id) {
						t.Fatalf("%s: production-published payload digest = %s, want golden %s -- the pin no longer tracks the write path (owner binding or payload drift)",
							id, got, pubvisWantPayloadSHA(t, id))
					}
					pubvisExpectExactPublished(t, s, published)
					pinned++
				}
			}
		}
		t.Logf("OBS G: %d d-* payload golden pins asserted positively against the production write path (owner-correct bindings)", pinned)
	})
}

// firstDiff reports the first byte offset where two witnesses differ, or
// the shorter length when one is a prefix of the other.
func firstDiff(a, b string) int {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

// TestZZZeroLeftoverPublishVisibilityFixtures audits only the exact
// verify_pubvis_it_* resources this file creates. This test MUST stay the
// last test declared in this file: Go runs a package's tests in
// declaration order within each source file, and only tests in this file
// create verify_pubvis_it_* resources, so it executes after every fixture
// this file owns has run and been dropped. TestMain additionally invokes
// the same audit after the whole package (registry_test.go) so the
// required zero-leftover and pg_database==baseline checks still execute
// under the filtered -run of the ticket's check-2.
func TestZZZeroLeftoverPublishVisibilityFixtures(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")) == "" {
		t.Skip("no service configured; nothing could have been created")
	}
	if err := auditPublishVisibilityLeftovers(); err != nil {
		t.Errorf("zero-leftover audit failed: %v", err)
	}
}
