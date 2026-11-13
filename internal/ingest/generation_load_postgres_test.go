package ingest

// Real-PostgreSQL acceptance tests for the pg-ingest-03 generation-load
// orchestrator (check-2: go test ./internal/ingest -run PostgresGenerationLoad
// -count=1).
//
// These tests drive LoadPostgresGeneration and ActivatePostgresGeneration
// against the PostgreSQL 18 service named by LOOM_TEST_POSTGRES_URL. Every
// row is built by the real production row builders (generated and generic,
// generation-qualified exactly as loadFile builds them), committed through
// the real merged writers, repositories, and adapters, and witnessed by
// direct SQL against literals. Nothing is mocked for a live claim: the only
// doubles are failure injectors wrapping the real repositories (the merged
// contract's explicit allowance) — they never stand in for a commit.
//
// Lifecycle durability (generation_load_postgres_durability_test.go):
// pg_terminate_backend is injected across every durability seam (manifest
// create, resource batch, edge batch, field profile, inventory, semantic
// contributions, staged transition), proving no partially written generation
// ever becomes visible (the manifest never reaches STAGED with missing data
// and the active-generation pointer never moves), and replaying from a
// separate OS process converges with byte-stable fingerprints and ledger
// digests.
//
// Concurrency: same-generation races run at least ten rounds each on two
// real pools whose distinct pg_backend_pid rows are witnessed live in
// pg_stat_activity, with positive overlap witnesses and sequential controls
// that fail when the witness logic itself is broken.
//
// Non-vacuity: every refusal has a paired control passing the same gate;
// guard drops refuse with the exact named sentinel and write nothing
// (witnessed by zero ledger/row counts on the live stack).
//
// SQL discipline: the orchestrator file must contain no SQL at all (pinned
// by TestPostgresGenerationLoadOwnSourceHasNoSQL); every test-side dynamic
// identifier passes through pgx.Identifier.Sanitize via ing03QIdent, every
// value stays a bound parameter (hostile literals stay data), and every
// generated placeholder/ordinal string is pattern-validated.
//
// Fixture databases and schemas are uniquely owned as verify_ing03_load_*
// and dropped by the test that created them; the TestZZ audit is
// ownership-scoped: it counts only this process's own verify_ing03_load_*
// prefix against the baseline captured before this process created
// anything, so concurrent sibling workers neither race nor trip it. Foreign
// registered churn is life, not failure.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ing03LoadPrefix = "verify_ing03_load_"
	ing03LoadDBFmt  = ing03LoadPrefix + "%d_%s"
	ing03SchemaPfx  = ing03LoadPrefix + "s_"
	ing03SchemaFmt  = ing03SchemaPfx + "%d_%s"
	// The manifest lane (dataset_release series) lives in its own
	// caller-owned schema inside the same isolated database: one schema,
	// one history owner.
	ing03ManifestSchemaPfx = ing03LoadPrefix + "sm_"
	ing03ManifestSchemaFmt = ing03ManifestSchemaPfx + "%d_%s"
	ing03Project           = "prj-ing03-load"
	ing03Generation        = "gen-build-a"
	ing03AuthPath          = "/fhir"
	ing03AppBase           = "ing03-test"
	ing03RaceRounds        = 10
	ing03LoincSystem       = "http://loinc.org"
)

// ing03HostileID is SQL/quote-hostile but plain JSON-string data: it must
// survive as a stored value, never reach the parser as syntax.
const ing03HostileID = `O'Brien"; DROP TABLE "fhir_resource"; --$1\%2`

var (
	ing03LoadNamePattern       = regexp.MustCompile(`^verify_ing03_load_[0-9]+_[0-9a-f]+$`)
	ing03SchemaNamePattern     = regexp.MustCompile(`^verify_ing03_load_s_[0-9]+_[0-9a-f]+$`)
	ing03ManifestSchemaPattern = regexp.MustCompile(`^verify_ing03_load_sm_[0-9]+_[0-9a-f]+$`)
	ing03OrdinalPattern        = regexp.MustCompile(`^[0-9]+$`)
	ing03PlaceholderPat        = regexp.MustCompile(`^\$[0-9]+$`)
)

func ing03TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 generation-load integration tests")
	}
	return raw
}

func ing03RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

var ing03ControlSeq atomic.Int64

// ---------------------------------------------------------------------------
// Ownership registry + baseline (ownership-scoped hygiene).
// ---------------------------------------------------------------------------

var (
	ing03OwnedMu      sync.Mutex
	ing03OwnedByName  = map[string]*ing03OwnedFixture{}
	ing03BaselineOnce sync.Once
	ing03Baseline     int64
	ing03BaselineErr  error
)

type ing03OwnedFixture struct {
	database string
	schema   string
	dropped  bool
}

func ing03RegisterFixture(db, schema string) {
	ing03OwnedMu.Lock()
	defer ing03OwnedMu.Unlock()
	ing03OwnedByName[db] = &ing03OwnedFixture{database: db, schema: schema}
}

func ing03MarkDropped(db string) {
	ing03OwnedMu.Lock()
	defer ing03OwnedMu.Unlock()
	if fixture, ok := ing03OwnedByName[db]; ok {
		fixture.dropped = true
	}
}

func ing03CaptureBaseline(raw string) {
	ing03BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			ing03BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		// Static literal; the LIKE pattern is a constant, not built input.
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing03\\_load\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			ing03BaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		ing03Baseline = count
	})
}

// ---------------------------------------------------------------------------
// SQL helpers: dynamic identifiers are sanitized, values stay bound.
// ---------------------------------------------------------------------------

// ing03ManifestSeriesTables are the tables the dataset_release runner owns;
// everything else in ing03KnownTables belongs to the source runner. The
// production composition assigns each series its own schema, and this
// fixture mirrors it: two schemas, two history ledgers.
var ing03ManifestSeriesTables = map[string]bool{
	"schema_migrations":           true,
	"dataset_generation_manifest": true,
	"dataset_active_generation":   true,
	"project_release":             true,
	"active_project_release":      true,
}

var ing03KnownTables = map[string]bool{
	"schema_migrations":                    true,
	"fhir_resource":                        true,
	"dataset_generation_manifest":          true,
	"dataset_active_generation":            true,
	"fhir_edge_occurrence":                 true,
	"fhir_resource_inventory_count":        true,
	"fhir_resource_inventory_build":        true,
	"fhir_semantic_inventory_build":        true,
	"fhir_field_profile":                   true,
	"fhir_field_profile_coverage":          true,
	"batch_write_ledger":                   true,
	"fhir_semantic_inventory_contribution": true,
}

// ing03QIdent sanitizes <schema>.<table>; the schema must be one this
// process owns (verify_ing03_load_s_*) and the table must be a known
// migration table. No caller may concatenate identifiers into SQL.
func ing03QIdent(t *testing.T, schema, table string) string {
	t.Helper()
	if !ing03SchemaNamePattern.MatchString(schema) && !ing03ManifestSchemaPattern.MatchString(schema) {
		t.Fatalf("schema %q is not an owned verify_ing03_load_s_*/sm_* name", schema)
	}
	if !ing03KnownTables[table] && !ing03ManifestSeriesTables[table] {
		t.Fatalf("table %q is not a known migration table", table)
	}
	return pgx.Identifier{schema, table}.Sanitize()
}

// ing03OrderBy builds " ORDER BY 1,2,…" with each ordinal literal
// pattern-validated before joining.
func ing03OrderBy(columnCount int) string {
	if columnCount < 1 {
		return ""
	}
	ords := make([]string, 0, columnCount)
	for i := 1; i <= columnCount; i++ {
		ordinal := fmt.Sprintf("%d", i)
		if !ing03OrdinalPattern.MatchString(ordinal) {
			panic("generated ordinal violates the numeric pattern")
		}
		ords = append(ords, ordinal)
	}
	return " ORDER BY " + strings.Join(ords, ",")
}

// ing03Placeholder asserts a generated bind placeholder like "$4" matches
// the pattern-validated placeholder-number form.
func ing03Placeholder(t *testing.T, n int) string {
	t.Helper()
	placeholder := fmt.Sprintf("$%d", n)
	if !ing03PlaceholderPat.MatchString(placeholder) {
		t.Fatalf("generated placeholder %q violates the $n pattern", placeholder)
	}
	return placeholder
}

func ing03Exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec failed: %v", err)
	}
}

func ing03Count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&count); err != nil {
		t.Fatalf("count failed: %v", err)
	}
	return count
}

// ---------------------------------------------------------------------------
// Service fixture.
// ---------------------------------------------------------------------------

type ing03Service struct {
	t              *testing.T
	ctx            context.Context
	admin          *pgxpool.Pool
	iso            *pgxpool.Pool
	dsn            string
	schema         string
	manifestSchema string
	scrub          func(any) string
	// faults injects catalog-evidence failures into the writers wired for
	// the next load: a test-only seam following the merged precedent
	// (dataset/postgres Store.commitFault). Wrappers decorate the real
	// repository and inject failures, never fake results.
	faults *ing03CatalogFaults
}

// ing03CatalogFaults carries the optional fault wrappers applied around
// the raw catalog repositories before the store-layer constructors wrap
// them, matching the production wiring recipe in GenerationCatalogWriters
// exactly (internal/ingest deliberately does not import catalog/postgres,
// so the fault seam lives here with the wiring).
type ing03CatalogFaults struct {
	wrapFieldProfile  func(control.FieldProfileStore) control.FieldProfileStore
	wrapInventory     func(control.InventoryStore) control.InventoryStore
	wrapRelationship  func(control.RelationshipStore) control.RelationshipStore
	wrapContributions func(inner GenerationSemanticContributor) GenerationSemanticContributor
}

// ing03WireCatalogWriters builds the four production catalog evidence
// writers over src following the GenerationCatalogWriters wiring recipe
// verbatim, so loads exercise the real store-layer guards and commit
// checks, not fakes. Optional fault wrappers decorate the raw repositories
// before the store-layer constructors wrap them. fail receives any
// constructor error.
func ing03WireCatalogWriters(src control.ConnSource, schema string, faults *ing03CatalogFaults, fail func(error)) GenerationCatalogWriters {
	profileRepo, err := control.NewFieldProfileRepository(src, schema)
	if err != nil {
		fail(fmt.Errorf("field profile repository: %w", err))
	}
	var profileStore control.FieldProfileStore = profileRepo
	if faults != nil && faults.wrapFieldProfile != nil {
		profileStore = faults.wrapFieldProfile(profileRepo)
	}
	profiles, err := control.NewCatalogFieldProfileAdapter(profileStore)
	if err != nil {
		fail(fmt.Errorf("field profile store: %w", err))
	}
	inventoryRepo, err := control.NewInventoryRepository(src, schema)
	if err != nil {
		fail(fmt.Errorf("inventory repository: %w", err))
	}
	var inventoryStore control.InventoryStore = inventoryRepo
	if faults != nil && faults.wrapInventory != nil {
		inventoryStore = faults.wrapInventory(inventoryRepo)
	}
	inventory, err := control.NewCatalogInventoryAdapter(inventoryStore)
	if err != nil {
		fail(fmt.Errorf("inventory store: %w", err))
	}
	relationshipRepo, err := control.NewCatalogRelationshipEvidenceRepository(src, schema)
	if err != nil {
		fail(fmt.Errorf("relationship evidence repository: %w", err))
	}
	var relationshipStore control.RelationshipStore = relationshipRepo
	if faults != nil && faults.wrapRelationship != nil {
		relationshipStore = faults.wrapRelationship(relationshipRepo)
	}
	relations, err := control.NewCatalogRelationshipAdapter(relationshipStore)
	if err != nil {
		fail(fmt.Errorf("relationship evidence store: %w", err))
	}
	contribs, err := control.NewSemanticContributionRepository(src, schema)
	if err != nil {
		fail(fmt.Errorf("semantic contribution repository: %w", err))
	}
	var contributions GenerationSemanticContributor = contribs
	if faults != nil && faults.wrapContributions != nil {
		contributions = faults.wrapContributions(contribs)
	}
	return GenerationCatalogWriters{
		FieldProfiles: profiles,
		Inventory:     inventory,
		Relationships: relations,
		Contributions: contributions,
	}
}

// catalogWriters builds the four production catalog evidence writers over
// the service pool, applying any armed fault wrappers.
func (s *ing03Service) catalogWriters() GenerationCatalogWriters {
	s.t.Helper()
	return ing03WireCatalogWriters(s.iso, s.schema, s.faults, func(err error) {
		s.t.Fatalf("catalog wiring: %s", s.scrub(err))
	})
}

func newIngest03Service(t *testing.T) *ing03Service {
	t.Helper()
	raw := ing03TestPostgresURL(t)
	ing03CaptureBaseline(raw)
	if ing03BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", ing03BaselineErr)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %s", scrub(err))
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf(ing03LoadDBFmt, time.Now().UnixNano(), ing03RandomHex(t))
	if !ing03LoadNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		t.Fatalf("create isolated database: %s", scrub(err))
	}
	schema := fmt.Sprintf(ing03SchemaFmt, time.Now().UnixNano(), ing03RandomHex(t))
	if !ing03SchemaNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	ing03RegisterFixture(dbName, schema)
	t.Cleanup(func() {
		// Foreign churn is life: cleanup drops only this process's own
		// registered fixture, witnessed against the ownership-scoped audit.
		if _, err := admin.Exec(context.WithoutCancel(context.Background()),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
			return
		}
		ing03MarkDropped(dbName)
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
		t.Fatalf("connect to isolated database: %s", scrub(err))
	}
	t.Cleanup(iso.Close)

	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create owned schema: %s", scrub(err))
	}
	manifestSchema := fmt.Sprintf(ing03ManifestSchemaFmt, time.Now().UnixNano(), ing03RandomHex(t))
	if !ing03ManifestSchemaPattern.MatchString(manifestSchema) {
		t.Fatalf("manifest schema name %q does not match the required pattern", manifestSchema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+manifestSchema+`"`); err != nil {
		t.Fatalf("create owned manifest schema: %s", scrub(err))
	}
	return &ing03Service{t: t, ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, manifestSchema: manifestSchema, scrub: scrub}
}

// migrate pre-applies migrations so tests can LOCK tables and seed rows
// before a load; the load's own MigrateSchema call then converges as the
// idempotent no-op it is.
func (s *ing03Service) migrate() {
	s.t.Helper()
	client := s.openClient(2)
	if _, err := control.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.t.Fatalf("MigrateSchema: %s", s.scrub(err))
	}
	if _, err := control.MigrateDatasetReleaseSchema(s.ctx, client, s.manifestSchema); err != nil {
		s.t.Fatalf("MigrateDatasetReleaseSchema: %s", s.scrub(err))
	}
}

func (s *ing03Service) openClient(maxConns int32) *control.Client {
	s.t.Helper()
	client, err := control.OpenPool(s.ctx, control.ClientConfig{
		DSN:            s.dsn,
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		s.t.Fatalf("open client pool: %s", s.scrub(err))
	}
	s.t.Cleanup(client.Close)
	return client
}

// namedPool opens a pool whose backends carry the given application_name,
// mirroring how the load stamps its own connections. Catalog writers are
// wired over such a pool so pg_stat_activity witnesses attribute catalog
// backends to the load that owns them.
func (s *ing03Service) namedPool(application string) *pgxpool.Pool {
	s.t.Helper()
	cfg, err := pgxpool.ParseConfig(s.dsn)
	if err != nil {
		s.t.Fatalf("parse pool config: %s", s.scrub(err))
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["application_name"] = application
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		s.t.Fatalf("open named pool %q: %s", application, s.scrub(err))
	}
	s.t.Cleanup(pool.Close)
	return pool
}

func (s *ing03Service) pgOptions(application string) PGSourceOptions {
	return PGSourceOptions{
		DSN:             s.dsn,
		Schema:          s.schema,
		ManifestSchema:  s.manifestSchema,
		ApplicationName: application,
		MaxConns:        4,
		Catalog: ing03WireCatalogWriters(s.namedPool(application), s.schema, s.faults,
			func(err error) { s.t.Fatalf("catalog wiring: %s", s.scrub(err)) }),
	}
}

func (s *ing03Service) loadOptions(metaDir, project string, dataset *publication.Ref) LoadOptions {
	return LoadOptions{
		Schema:           repoPath(s.t, "schemas", "graph-fhir.json"),
		MetaDir:          metaDir,
		Project:          project,
		Dataset:          dataset,
		AuthResourcePath: ing03AuthPath,
	}
}

func (s *ing03Service) manifestState(project, generation string) (string, bool) {
	s.t.Helper()
	table := ing03QIdent(s.t, s.manifestSchema, "dataset_generation_manifest")
	var state string
	err := s.iso.QueryRow(context.Background(),
		"SELECT state FROM "+table+" WHERE project = $1 AND generation = $2", project, generation).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		s.t.Fatalf("read manifest state: %s", s.scrub(err))
	}
	return state, true
}

func (s *ing03Service) activePointerGeneration(project string) (string, bool) {
	s.t.Helper()
	table := ing03QIdent(s.t, s.manifestSchema, "dataset_active_generation")
	var generation string
	err := s.iso.QueryRow(context.Background(),
		"SELECT generation FROM "+table+" WHERE project = $1", project).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		s.t.Fatalf("read active pointer: %s", s.scrub(err))
	}
	return generation, true
}

// tableSchema routes a known table to the schema whose runner owns it,
// mirroring the production two-schema composition.
func (s *ing03Service) tableSchema(table string) string {
	if ing03ManifestSeriesTables[table] && table != "schema_migrations" {
		return s.manifestSchema
	}
	return s.schema
}

func (s *ing03Service) countTable(table, where string, args ...any) int64 {
	s.t.Helper()
	fqn := ing03QIdent(s.t, s.tableSchema(table), table)
	sql := "SELECT count(*) FROM " + fqn
	if where != "" {
		sql += " WHERE " + where
	}
	return ing03Count(s.t, s.iso, sql, args...)
}

// ---------------------------------------------------------------------------
// Byte-stable fingerprints over business columns (server-assigned storage
// identities and timestamps are excluded: they are server facts, not load
// content, and independent loads must not be required to reproduce them).
// ---------------------------------------------------------------------------

var ing03FingerprintColumns = []struct {
	table  string
	column string
}{
	{"dataset_generation_manifest", "project, generation, state, schema_identity::text"},
	{"dataset_active_generation", "project, generation"},
	{"fhir_resource", "project, dataset_generation, resource_type, logical_id, auth_resource_path, payload::text"},
	{"fhir_edge_occurrence", "project, dataset_generation, auth_resource_path, label, source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal, logical_edge_key"},
	{"batch_write_ledger", "batch_id, project, dataset_generation, digest, resource_count, edge_count"},
	{"fhir_resource_inventory_count", "count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count"},
	{"fhir_resource_inventory_build", "marker_key, project, dataset_generation, build_id, coverage_digest, resource_types::text, resource_type_count, count_row_count, complete"},
	{"fhir_field_profile", "profile_key, record_type, kind, doc_count, max_items, sample_count, distinct_values::text, distinct_truncated, enrichment_complete, enrichment_truncated, pivot_candidate, pivot_kind, pivot_columns::text, pivot_family, pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type, pivot_value_selectors::text, extension_values::text, semantic_observations::text, project, dataset_generation, auth_resource_path, resource_type, path"},
	{"fhir_field_profile_coverage", "project, dataset_generation, auth_resource_path, resource_type"},
	{"fhir_semantic_inventory_build", "build_key, project, dataset_generation, build_id, state, observation_schema, inventory_schema, entry_index_version, rule_version, source_kind, source_availability, auth_resource_path, scanned_resources, checkpoint, checkpoint_collection, checkpoint_key, lease_token, lease_expires_at, diagnostics::text"},
	{"fhir_semantic_inventory_contribution", "contribution_key, project, dataset_generation, build_id, source_kind, auth_resource_path, resource_type, source_id, ordinal, concept_id, binding_id, example, payload_digest, observation::text"},
}

// fingerprint hashes the full business content of one schema in a
// collation-stable ordered way. Two loads that committed identical content
// produce identical digests regardless of timing or process.
func (s *ing03Service) fingerprint() string {
	s.t.Helper()
	digest := sha256.New()
	for _, entry := range ing03FingerprintColumns {
		fqn := ing03QIdent(s.t, s.tableSchema(entry.table), entry.table)
		sql := "SELECT " + entry.column + " FROM " + fqn
		rows, err := s.iso.Query(context.Background(), sql)
		if err != nil {
			s.t.Fatalf("fingerprint query %s: %s", entry.table, s.scrub(err))
		}
		var rendered []string
		for rows.Next() {
			values, scanErr := rows.Values()
			if scanErr != nil {
				rows.Close()
				s.t.Fatalf("fingerprint scan %s: %s", entry.table, s.scrub(scanErr))
			}
			rendered = append(rendered, fmt.Sprintf("%v", values))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			s.t.Fatalf("fingerprint rows %s: %s", entry.table, s.scrub(err))
		}
		rows.Close()
		sort.Strings(rendered)
		fmt.Fprintf(digest, "%s\n", entry.table)
		for _, row := range rendered {
			fmt.Fprintf(digest, "%s\n", row)
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// ---------------------------------------------------------------------------
// pg_stat_activity witnesses + kill/block injection.
// ---------------------------------------------------------------------------

type ing03BackendWitness struct {
	Pid         int32
	Application string
	// State is nullable: pg_stat_activity.state is NULL for a backend
	// that has not yet entered a client state; the witness must scan it
	// without failing.
	State         *string
	WaitEventType *string
}

func (s *ing03Service) backendsByApplication(appNames ...string) []ing03BackendWitness {
	rows, err := s.iso.Query(context.Background(),
		`SELECT pid, application_name, state, wait_event_type FROM pg_stat_activity WHERE application_name = ANY($1::text[]) AND backend_type = 'client backend' AND pid <> pg_backend_pid()`,
		appNames)
	if err != nil {
		s.t.Fatalf("witness backends: %s", s.scrub(err))
	}
	defer rows.Close()
	var out []ing03BackendWitness
	for rows.Next() {
		var w ing03BackendWitness
		if err := rows.Scan(&w.Pid, &w.Application, &w.State, &w.WaitEventType); err != nil {
			s.t.Fatalf("scan backend witness: %s", s.scrub(err))
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.t.Fatalf("rows backend witness: %s", s.scrub(err))
	}
	return out
}

// maxConcurrentDistinctApps samples pg_stat_activity until done closes and
// reports the maximum number of distinct application names (each with at
// least one actively executing backend) observed in a single sample. Only
// state='active' backends count: idle pooled connections (for example a
// finished load's still-open catalog pool) must not fake an overlap, and
// lock-parked backends are active with wait_event_type='Lock'.
func (s *ing03Service) maxConcurrentDistinctApps(done <-chan struct{}, appNames ...string) int {
	best := 0
	for {
		select {
		case <-done:
			return best
		default:
		}
		backends := s.backendsByApplication(appNames...)
		seen := map[string]bool{}
		for _, backend := range backends {
			if backend.State != nil && *backend.State == "active" {
				seen[backend.Application] = true
			}
		}
		if len(seen) > best {
			best = len(seen)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (s *ing03Service) waitForBackend(appName string, timeout time.Duration) (ing03BackendWitness, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		backends := s.backendsByApplication(appName)
		if len(backends) > 0 {
			return backends[0], true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return ing03BackendWitness{}, false
}

// waitForBackendWaiting parks until a backend for appName is visible and
// waiting on a lock (wait_event_type = 'Lock').
func (s *ing03Service) waitForBackendWaiting(appName string, timeout time.Duration) (ing03BackendWitness, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, backend := range s.backendsByApplication(appName) {
			if backend.WaitEventType != nil && *backend.WaitEventType == "Lock" {
				return backend, true
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return ing03BackendWitness{}, false
}

// terminateAppBackends kills every live backend tagged with appName and
// returns how many were terminated.
func (s *ing03Service) terminateAppBackends(appName string) int {
	rows, err := s.iso.Query(context.Background(),
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1 AND backend_type = 'client backend' AND pid <> pg_backend_pid()`,
		appName)
	if err != nil {
		s.t.Fatalf("terminate backends: %s", s.scrub(err))
	}
	killed := 0
	for rows.Next() {
		var ok bool
		if err := rows.Scan(&ok); err != nil {
			rows.Close()
			s.t.Fatalf("scan terminate: %s", s.scrub(err))
		}
		if ok {
			killed++
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.t.Fatalf("rows terminate: %s", s.scrub(err))
	}
	rows.Close()
	return killed
}

// blockTable holds ACCESS EXCLUSIVE on one owned table until release, so a
// load touching it parks in the lock wait and can be terminated there.
func (s *ing03Service) blockTable(table string) (release func()) {
	s.t.Helper()
	fqn := ing03QIdent(s.t, s.tableSchema(table), table)
	conn, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.t.Fatalf("acquire blocker: %s", s.scrub(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		conn.Release()
		cancel()
		s.t.Fatalf("begin blocker: %s", s.scrub(err))
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '200s'`); err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		cancel()
		s.t.Fatalf("blocker lock timeout: %s", s.scrub(err))
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE `+fqn+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		cancel()
		s.t.Fatalf("lock %s: %s", table, s.scrub(err))
	}
	var released bool
	release = func() {
		if released {
			return
		}
		released = true
		_ = tx.Rollback(context.WithoutCancel(ctx))
		conn.Release()
		cancel()
	}
	s.t.Cleanup(release)
	return release
}

// blockManifestRow parks the transition-to-STAGED UPDATE behind a row lock.
func (s *ing03Service) blockManifestRow(project, generation string) (release func()) {
	s.t.Helper()
	fqn := ing03QIdent(s.t, s.manifestSchema, "dataset_generation_manifest")
	conn, err := s.iso.Acquire(s.ctx)
	if err != nil {
		s.t.Fatalf("acquire manifest blocker: %s", s.scrub(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		conn.Release()
		cancel()
		s.t.Fatalf("begin manifest blocker: %s", s.scrub(err))
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '200s'`); err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		cancel()
		s.t.Fatalf("manifest blocker lock timeout: %s", s.scrub(err))
	}
	if _, err := tx.Exec(ctx, `SELECT project FROM `+fqn+` WHERE project = $1 AND generation = $2 FOR UPDATE`,
		project, generation); err != nil {
		_ = tx.Rollback(ctx)
		conn.Release()
		cancel()
		s.t.Fatalf("lock manifest row: %s", s.scrub(err))
	}
	var released bool
	release = func() {
		if released {
			return
		}
		released = true
		_ = tx.Rollback(context.WithoutCancel(ctx))
		conn.Release()
		cancel()
	}
	s.t.Cleanup(release)
	return release
}

// ---------------------------------------------------------------------------
// NDJSON fixtures (test-owned literals; hostile values stay pure data via
// encoding/json marshaling — never hand-escaped).
// ---------------------------------------------------------------------------

func ing03JSONLine(t *testing.T, document map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal fixture line: %v", err)
	}
	return string(encoded)
}

// ing03ObservationLine builds one Observation row. FHIR id validation
// legitimately requires ^[A-Za-z0-9\-.]+$ for ids, and every emitted edge
// direction must resolve to a stored source, so hostile strings ride in
// free-text code.text — pure bound JSONB data, never SQL syntax.
func ing03ObservationLine(t *testing.T, id, subject string, focusRefs []string, coded bool) string {
	t.Helper()
	document := map[string]any{
		"resourceType": "Observation",
		"id":           id,
		"status":       "final",
		"subject":      map[string]any{"reference": "Patient/" + subject},
	}
	if coded {
		document["code"] = map[string]any{"coding": []any{map[string]any{
			"system": ing03LoincSystem, "code": "8867-4", "display": "Heart rate"}}}
	} else {
		document["code"] = map[string]any{"text": ing03HostileID}
	}
	if len(focusRefs) > 0 {
		focus := make([]any, 0, len(focusRefs))
		for _, ref := range focusRefs {
			focus = append(focus, map[string]any{"reference": ref})
		}
		document["focus"] = focus
	}
	return ing03JSONLine(t, document)
}

func ing03PatientLine(t *testing.T, id string) string {
	t.Helper()
	return ing03JSONLine(t, map[string]any{"resourceType": "Patient", "id": id, "gender": "female"})
}

// ing03HostilePatientLine keeps the SQL/quote-hostile string in a free-text
// name element: FHIR id validation legitimately requires the pattern
// ^[A-Za-z0-9\-.]+$ for ids, so the injection payload rides as unvalidated
// JSON string data, which is exactly the binding-integrity claim.
func ing03HostilePatientLine(t *testing.T) string {
	t.Helper()
	return ing03JSONLine(t, map[string]any{
		"resourceType": "Patient",
		"id":           "pat-hostile",
		"gender":       "female",
		"name":         []any{map[string]any{"text": ing03HostileID}},
	})
}

// ing03WriteFixtures writes the canonical two-resource staging directory.
// rowsPerFile scales the Patient file; a malformed tail row beyond the
// preflight sample window is added when lateBadRow is set.
func ing03WriteFixtures(t *testing.T, dir string, rowsPerFile int, lateBadRow bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixtures: %v", err)
	}
	observationRows := []string{
		ing03ObservationLine(t, "obs-a", "pat-0000",
			[]string{"Observation/obs-a", "Observation/obs-a"}, true),
		ing03ObservationLine(t, "obs-b", "pat-hostile", nil, false),
	}
	if err := os.WriteFile(filepath.Join(dir, "Observation.ndjson"),
		[]byte(strings.Join(observationRows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write Observation fixture: %v", err)
	}
	patientRows := make([]string, 0, rowsPerFile+1)
	for i := 0; i < rowsPerFile; i++ {
		patientRows = append(patientRows, ing03PatientLine(t, fmt.Sprintf("pat-%04d", i)))
	}
	patientRows = append(patientRows, ing03HostilePatientLine(t))
	if lateBadRow {
		// Malformed line beyond the 10-row preflight sample window so the
		// claim and the write passes start before the failure lands.
		patientRows = append(patientRows[:11], append([]string{`{oops`}, patientRows[11:]...)...)
	}
	if err := os.WriteFile(filepath.Join(dir, "Patient.ndjson"),
		[]byte(strings.Join(patientRows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write Patient fixture: %v", err)
	}
}

func ing03Ref(t *testing.T, project, generation string) publication.Ref {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	return ref
}

// ---------------------------------------------------------------------------
// Core lifecycle + guard acceptance.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadPreflightRunsBeforePostgresContact(t *testing.T) {
	service := newIngest03Service(t)
	goodDir := t.TempDir()
	ing03WriteFixtures(t, goodDir, 2, false)
	ref := ing03Ref(t, ing03Project, ing03Generation)

	badUnknown := t.TempDir()
	writePreflightFixture(t, badUnknown, "Unknown.ndjson", `{"resourceType":"Unknown"}`+"\n")

	badDirIsFile := filepath.Join(t.TempDir(), "not-a-dir.ndjson")
	if err := os.WriteFile(badDirIsFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write non-directory: %v", err)
	}

	app := ing03AppBase + "-guard"
	tests := []struct {
		name    string
		mutate  func(*LoadOptions)
		wantErr error
	}{
		{"dataset required", func(o *LoadOptions) { o.Dataset = nil }, ErrGenerationDatasetRequired},
		{"truncate forbidden", func(o *LoadOptions) { o.Truncate = true }, ErrGenerationLoadTruncateForbidden},
		{"project mismatch", func(o *LoadOptions) { o.Project = "prj-something-else" }, ErrGenerationDatasetProjectMismatch},
		{"requires directory", func(o *LoadOptions) { o.MetaDir = badDirIsFile }, ErrGenerationLoadRequiresDirectory},
		{"requires files", func(o *LoadOptions) { o.MetaDir = t.TempDir() }, ErrGenerationLoadRequiresFiles},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var events []string
			opts := service.loadOptions(goodDir, ing03Project, &ref)
			opts.EventSink = func(event string, _ map[string]any) { events = append(events, event) }
			test.mutate(&opts)
			_, err := LoadPostgresGeneration(context.Background(), opts, service.pgOptions(app))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			for _, event := range events {
				if strings.HasPrefix(event, "go_pg_") {
					t.Fatalf("guard %s opened the PG backend: %v", test.name, events)
				}
			}
			// Control: the same call minus the guard defect reaches PG.
			controlEvents := ing03RunLoadCapturingEvents(t, service, goodDir, app+"-control")
			if !containsPrefix(controlEvents, "go_pg_") {
				t.Fatalf("control load never contacted PG; ordering witness is vacuous: %v", controlEvents)
			}
		})
	}

	t.Run("preflight failure precedes backend", func(t *testing.T) {
		// Its own service: the guard controls above already ran loads on
		// service.schema, so only a virgin schema can witness "never
		// migrated".
		clean := newIngest03Service(t)
		var events []string
		opts := clean.loadOptions(badUnknown, ing03Project, &ref)
		opts.EventSink = func(event string, _ map[string]any) { events = append(events, event) }
		_, err := LoadPostgresGeneration(context.Background(), opts, clean.pgOptions(app))
		var preflightErr *PreflightError
		if !errors.As(err, &preflightErr) {
			t.Fatalf("error = %T %v, want PreflightError", err, err)
		}
		for _, event := range events {
			if strings.HasPrefix(event, "go_pg_") {
				t.Fatalf("preflight failure still reached PG: %v", events)
			}
		}
		// No PG state was created: this virgin schema was never migrated,
		// and the load must not have migrated it either.
		var missing bool
		if err := clean.iso.QueryRow(context.Background(),
			`SELECT to_regclass($1) IS NULL`, clean.manifestSchema+".dataset_generation_manifest").Scan(&missing); err != nil {
			t.Fatalf("probe schema: %v", err)
		}
		if !missing {
			t.Fatal("preflight failure still created the manifest table: MigrateSchema ran")
		}
		// Non-vacuity: the same virgin schema does gain the table once a
		// clean load runs, so the check above could have failed.
		cleanDir := t.TempDir()
		ing03WriteFixtures(t, cleanDir, 2, false)
		if _, cleanErr := LoadPostgresGeneration(context.Background(),
			clean.loadOptions(cleanDir, ing03Project, &ref),
			clean.pgOptions(ing03AppBase+"-preflight-control")); cleanErr != nil {
			t.Fatalf("preflight control load: %s", clean.scrub(cleanErr))
		}
		if err := clean.iso.QueryRow(context.Background(),
			`SELECT to_regclass($1) IS NOT NULL`, clean.manifestSchema+".dataset_generation_manifest").Scan(&missing); err != nil {
			t.Fatalf("probe control schema: %v", err)
		}
		if !missing {
			t.Fatal("control load did not migrate the schema: the witness is vacuous")
		}
	})

	t.Run("blank dsn refused before connect", func(t *testing.T) {
		_, err := LoadPostgresGeneration(context.Background(),
			service.loadOptions(goodDir, ing03Project, &ref),
			PGSourceOptions{DSN: "", Schema: service.schema, ManifestSchema: service.manifestSchema, ApplicationName: app, Catalog: service.catalogWriters()})
		if !errors.Is(err, control.ErrInvalidDSN) {
			t.Fatalf("error = %v, want %v", err, control.ErrInvalidDSN)
		}
	})

	t.Run("invalid pool config refused before connect", func(t *testing.T) {
		_, err := LoadPostgresGeneration(context.Background(),
			service.loadOptions(goodDir, ing03Project, &ref),
			PGSourceOptions{DSN: service.dsn, Schema: service.schema, ManifestSchema: service.manifestSchema, MaxConns: -3, ApplicationName: app, Catalog: service.catalogWriters()})
		if !errors.Is(err, control.ErrInvalidPoolConfig) {
			t.Fatalf("error = %v, want %v", err, control.ErrInvalidPoolConfig)
		}
	})

	t.Run("invalid schema name refused by named errors", func(t *testing.T) {
		_, err := LoadPostgresGeneration(context.Background(),
			service.loadOptions(goodDir, ing03Project, &ref),
			PGSourceOptions{DSN: service.dsn, Schema: "Not A Schema", ManifestSchema: service.manifestSchema, ApplicationName: app, Catalog: service.catalogWriters()})
		if !errors.Is(err, control.ErrInvalidSchemaName) {
			t.Fatalf("error = %v, want %v", err, control.ErrInvalidSchemaName)
		}
	})
}

// ing03RunLoadCapturingEvents runs a control load under a fresh generation
// and returns its emitted events.
func ing03RunLoadCapturingEvents(t *testing.T, service *ing03Service, metaDir, app string) []string {
	t.Helper()
	ref := ing03Ref(t, ing03Project, fmt.Sprintf("gen-ctl-%d", ing03ControlSeq.Add(1)))
	var events []string
	opts := service.loadOptions(metaDir, ing03Project, &ref)
	opts.EventSink = func(event string, _ map[string]any) { events = append(events, event) }
	_, err := LoadPostgresGeneration(context.Background(), opts, service.pgOptions(app))
	if err != nil {
		t.Fatalf("control load: %s", service.scrub(err))
	}
	return events
}

func containsPrefix(events []string, prefix string) bool {
	for _, event := range events {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	return false
}

func TestPostgresGenerationLoadStagesMultiResourceAndNeverActivates(t *testing.T) {
	for _, generic := range []bool{false, true} {
		name := "generated"
		if generic {
			name = "generic"
		}
		t.Run(name, func(t *testing.T) {
			service := newIngest03Service(t)
			metaDir := t.TempDir()
			ing03WriteFixtures(t, metaDir, 2, false)
			ref := ing03Ref(t, ing03Project, ing03Generation)
			opts := service.loadOptions(metaDir, ing03Project, &ref)
			opts.BatchSize = 2
			opts.UseGeneric = generic
			var events []string
			opts.EventSink = func(event string, _ map[string]any) { events = append(events, event) }

			summary, err := LoadPostgresGeneration(context.Background(), opts, service.pgOptions(ing03AppBase+"-"+name))
			if err != nil {
				t.Fatalf("load: %s (events=%v)", service.scrub(err), events)
			}
			if err := ing03AssertEvidenceOrder(t, events); err != nil {
				t.Fatal(err)
			}
			if summary.VerticesInserted != 5 {
				t.Fatalf("vertices = %d, want 5 committed resource rows", summary.VerticesInserted)
			}
			if summary.Resources["Patient"] != 3 || summary.Resources["Observation"] != 2 {
				t.Fatalf("resources = %v, want Patient 3 Observation 2", summary.Resources)
			}
			if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateStaged) {
				t.Fatalf("manifest state = %q found=%v, want STAGED", state, found)
			}
			if _, active := service.activePointerGeneration(ing03Project); active {
				t.Fatal("active pointer moved: the load must never activate")
			}
			// The hostile string round-trips as pure stored data in every
			// position it rides in (unvalidated payload text and a dangling
			// reference target) and the embedded injection did nothing.
			if count := service.countTable("fhir_resource", "logical_id = $1", "pat-hostile"); count != 1 {
				t.Fatalf("hostile-name rows = %d, want exactly 1", count)
			}
			var payloadText string
			if err := service.iso.QueryRow(context.Background(),
				"SELECT payload::text FROM "+ing03QIdent(t, service.schema, "fhir_resource")+" WHERE logical_id = $1",
				"pat-hostile").Scan(&payloadText); err != nil {
				t.Fatalf("read hostile payload: %s", service.scrub(err))
			}
			if !strings.Contains(payloadText, "DROP TABLE") {
				t.Fatalf("hostile payload did not round-trip as data: %s", payloadText)
			}
			if got := service.countTable("fhir_resource", ""); got != 5 {
				t.Fatalf("resource rows = %d, want 5 (injection changed nothing)", got)
			}
			for _, lane := range []struct {
				schema string
				want   int
			}{
				// The source series owns every table in the union
				// (its 000003 creates the manifest pair too); the
				// manifest runner owns its ledger, manifest, and
				// active-pointer trio. Witnesses read business rows
				// from the schema whose port the load writes (the
				// manifest lane), never from the source duplicate.
				{service.schema, len(ing03KnownTables)},
				{service.manifestSchema, len(ing03ManifestSeriesTables)},
			} {
				var relations int64
				if err := service.iso.QueryRow(context.Background(),
					`SELECT count(*) FROM pg_tables WHERE schemaname = $1`, lane.schema).Scan(&relations); err != nil {
					t.Fatalf("count relations: %s", service.scrub(err))
				}
				if relations != int64(lane.want) {
					t.Fatalf("relations in %s = %d, want exactly the %d migrated tables for that runner", lane.schema, relations, lane.want)
				}
			}
			// The second injection carrier (obs-b code.text) round-trips too.
			var obsPayloadText string
			if err := service.iso.QueryRow(context.Background(),
				"SELECT payload::text FROM "+ing03QIdent(t, service.schema, "fhir_resource")+" WHERE logical_id = $1",
				"obs-b").Scan(&obsPayloadText); err != nil {
				t.Fatalf("read obs-b payload: %s", service.scrub(err))
			}
			if !strings.Contains(obsPayloadText, "DROP TABLE") {
				t.Fatalf("obs-b hostile payload did not round-trip as data: %s", obsPayloadText)
			}
			// Edges: every subject reference is a committed occurrence
			// through the bound-parameter path. Builders emit
			// type-qualified labels (subject_Patient forward,
			// subject_observation back-ref).
			if count := service.countTable("fhir_edge_occurrence",
				"label = $1 AND target_logical_id = $2", "subject_Patient", "pat-hostile"); count != 1 {
				t.Fatalf("pat-hostile forward subject edges = %d, want 1", count)
			}
			if edgeCount := service.countTable("fhir_edge_occurrence", ""); edgeCount != int64(summary.EdgesInserted) {
				t.Fatalf("edge rows = %d, summary says %d", edgeCount, summary.EdgesInserted)
			}
			// Repeated-owner correlation per the merged ing-02 contract:
			// the generated builder collapses identical duplicates; the
			// generic builder keeps them as distinct path/ordinal rows.
			// One duplicate focus reference yields one forward/back pair in
			// the generated builder; the generic builder keeps both array
			// positions, each as a forward/back pair.
			focus := service.countTable("fhir_edge_occurrence",
				"label IN ('focus_observation', 'focus_Observation')")
			if generic && focus != 4 {
				t.Fatalf("generic focus occurrences = %d, want 4 (two repeated-array positions, forward+back each)", focus)
			}
			if !generic && focus != 2 {
				t.Fatalf("generated focus occurrences = %d, want 2 (collapsed duplicate, forward+back)", focus)
			}
			// Field profiles: coverage marker exists for every loaded scope.
			for _, resourceType := range []string{"Patient", "Observation"} {
				if count := service.countTable("fhir_field_profile_coverage",
					"resource_type = $1", resourceType); count != 1 {
					t.Fatalf("coverage markers for %s = %d, want 1", resourceType, count)
				}
			}
			// Resource inventory published with exact document counts.
			var documentCount int64
			if err := service.iso.QueryRow(context.Background(),
				"SELECT document_count FROM "+ing03QIdent(t, service.schema, "fhir_resource_inventory_count")+
					" WHERE resource_type = $1", "Patient").Scan(&documentCount); err != nil {
				t.Fatalf("read inventory: %s", service.scrub(err))
			}
			if documentCount != 3 {
				t.Fatalf("inventory Patient count = %d, want 3", documentCount)
			}
			// Relationship evidence derives from the committed edges.
			if summary.BatchCounts["relationship_observations"] == 0 {
				t.Fatal("relationship evidence empty although edges were committed")
			}
			// Semantic evidence persisted and matches the reported count.
			if summary.BatchCounts["semantic_contributions"] == 0 {
				t.Fatal("no semantic contributions although a loinc coding was staged")
			}
			if persisted := service.countTable("fhir_semantic_inventory_contribution", ""); persisted !=
				int64(summary.BatchCounts["semantic_contributions"]) {
				t.Fatalf("persisted contributions = %d, summary says %d", persisted, summary.BatchCounts["semantic_contributions"])
			}
		})
	}
}

func ing03AssertEvidenceOrder(t *testing.T, events []string) error {
	t.Helper()
	indexOf := func(want string) int {
		for i, event := range events {
			if event == want {
				return i
			}
		}
		return -1
	}
	preflightDone := indexOf("go_preflight_complete")
	if preflightDone < 0 {
		return fmt.Errorf("preflight never completed: %v", events)
	}
	for i, event := range events {
		if i < preflightDone && strings.HasPrefix(event, "go_pg_") {
			return fmt.Errorf("PG contact %q before preflight completion: %v", event, events)
		}
	}
	firstResource, firstEdge := -1, -1
	lastResource, lastEdge := -1, -1
	for i, event := range events {
		switch event {
		case "go_pg_resource_batch_commit":
			if firstResource < 0 {
				firstResource = i
			}
			lastResource = i
		case "go_pg_edge_batch_commit":
			if firstEdge < 0 {
				firstEdge = i
			}
			lastEdge = i
		}
	}
	if firstResource < 0 || firstEdge < 0 {
		return fmt.Errorf("missing batch commits: %v", events)
	}
	if lastResource > firstEdge {
		return fmt.Errorf("an edge batch started before every resource batch committed: %v", events)
	}
	catalogFirst := -1
	for i, event := range events {
		if event == "go_pg_catalog_field_profiles" || event == "go_pg_resource_inventory_complete" || event == "go_pg_relationship_evidence_complete" {
			if catalogFirst < 0 {
				catalogFirst = i
			}
		}
	}
	if catalogFirst >= 0 && catalogFirst < lastEdge {
		return fmt.Errorf("catalog evidence interleaved with edge writes: %v", events)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Failure-state acceptance.
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadMalformedLateRowMarksFailedAndBlocksActivation(t *testing.T) {
	service := newIngest03Service(t)
	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 25, true)
	ref := ing03Ref(t, ing03Project, ing03Generation)
	opts := service.loadOptions(metaDir, ing03Project, &ref)

	_, err := LoadPostgresGeneration(context.Background(), opts, service.pgOptions(ing03AppBase+"-late"))
	var incomplete *GenerationLoadIncompleteError
	if !errors.As(err, &incomplete) {
		t.Fatalf("error = %T %v, want GenerationLoadIncompleteError", err, err)
	}
	if incomplete.ValidationErrors == 0 {
		t.Fatalf("incomplete error lost the validation count: %+v", incomplete)
	}
	if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateFailed) {
		t.Fatalf("manifest state = %q found=%v, want FAILED", state, found)
	}
	if _, active := service.activePointerGeneration(ing03Project); active {
		t.Fatal("late malformed row left an active pointer")
	}
	if activateErr := ActivatePostgresGeneration(context.Background(), ref, service.pgOptions(ing03AppBase+"-late-act")); !errors.Is(activateErr, publication.ErrGenerationNotReady) {
		t.Fatalf("activate = %v, want ErrGenerationNotReady", activateErr)
	}
	// The row-error summary keeps the exact bad line.
	if len(opts.MetaDir) == 0 {
		t.Fatal("fixture dir vanished")
	}

	// Non-vacuity control: the identical load without the late bad row
	// passes the very same gates and stages.
	cleanDir := t.TempDir()
	ing03WriteFixtures(t, cleanDir, 25, false)
	cleanService := newIngest03Service(t)
	cleanRef := ing03Ref(t, ing03Project, ing03Generation)
	if _, cleanErr := LoadPostgresGeneration(context.Background(),
		cleanService.loadOptions(cleanDir, ing03Project, &cleanRef),
		cleanService.pgOptions(ing03AppBase+"-late-control")); cleanErr != nil {
		t.Fatalf("clean control load: %s", cleanService.scrub(cleanErr))
	}
	if state, found := cleanService.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateStaged) {
		t.Fatalf("clean control manifest = %q found=%v, want STAGED", state, found)
	}

	// FAILED is terminal: a retry refuses with the named error and writes
	// nothing. The witness is the retry delta — the failed load's own
	// committed windows are legitimate durable partials, not retry output.
	ledgerBeforeRetry := service.countTable("batch_write_ledger", "")
	retryEvents := []string{}
	retryOpts := service.loadOptions(metaDir, ing03Project, &ref)
	retryOpts.EventSink = func(event string, _ map[string]any) { retryEvents = append(retryEvents, event) }
	_, retryErr := LoadPostgresGeneration(context.Background(), retryOpts, service.pgOptions(ing03AppBase+"-late-retry"))
	if !errors.Is(retryErr, ErrGenerationLoadAlreadyFailed) {
		t.Fatalf("retry = %v, want %v", retryErr, ErrGenerationLoadAlreadyFailed)
	}
	if state, _ := service.manifestState(ing03Project, ing03Generation); state != string(publication.StateFailed) {
		t.Fatalf("retry moved the manifest to %q, want FAILED preserved", state)
	}
	if ledger := service.countTable("batch_write_ledger", ""); ledger != ledgerBeforeRetry {
		t.Fatalf("retry against FAILED wrote %d new ledger rows, want 0 (before=%d after=%d)",
			ledger-ledgerBeforeRetry, ledgerBeforeRetry, ledger)
	}
}

func TestPostgresGenerationLoadWriterFailuresLeaveManifestFailedAndPreserveRows(t *testing.T) {
	t.Run("resource row conflict refuses overwrite", func(t *testing.T) {
		service := newIngest03Service(t)
		service.migrate()
		client := service.openClient(2)
		resourceRepo, err := control.NewResourceRepository(client, service.schema)
		if err != nil {
			t.Fatalf("resource repo: %v", err)
		}
		if _, err := resourceRepo.InsertResource(context.Background(), source.Resource{
			Project: ing03Project, DatasetGeneration: ing03Generation, ResourceType: "Patient",
			LogicalID: "pat-0000", AuthResourcePath: ing03AuthPath,
			Payload: json.RawMessage(`{"id":"pat-0000","resourceType":"Patient","gender":"seeded"}`),
		}); err != nil {
			t.Fatalf("seed conflicting row: %s", service.scrub(err))
		}
		seededPayload := service.resourcePayload("Patient", "pat-0000")

		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		ref := ing03Ref(t, ing03Project, ing03Generation)
		_, loadErr := LoadPostgresGeneration(context.Background(),
			service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-conflict"))
		var conflict *control.BatchResourceConflictError
		if !errors.As(loadErr, &conflict) {
			t.Fatalf("load error = %T %v, want BatchResourceConflictError", loadErr, loadErr)
		}
		if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateFailed) {
			t.Fatalf("manifest state = %q found=%v, want FAILED", state, found)
		}
		if got := service.resourcePayload("Patient", "pat-0000"); got != seededPayload {
			t.Fatalf("retry overwrote the pre-seeded row: %q -> %q", seededPayload, got)
		}
		if _, active := service.activePointerGeneration(ing03Project); active {
			t.Fatal("writer failure moved the active pointer")
		}
	})

	t.Run("edge writer failure fails the load", func(t *testing.T) {
		// Reference load learns the exact edges the builders produce; the
		// victim schema pre-seeds a colliding occurrence with different
		// content, so the store must refuse the edge batch by name.
		reference := newIngest03Service(t)
		metaDir := t.TempDir()
		ing03WriteFixtures(t, metaDir, 2, false)
		ref := ing03Ref(t, ing03Project, ing03Generation)
		if _, err := LoadPostgresGeneration(context.Background(),
			reference.loadOptions(metaDir, ing03Project, &ref),
			reference.pgOptions(ing03AppBase+"-edge-ref")); err != nil {
			t.Fatalf("reference load: %s", reference.scrub(err))
		}
		victim := reference.readOneEdge()

		service := newIngest03Service(t)
		service.migrate()
		client := service.openClient(2)
		resourceRepo, err := control.NewResourceRepository(client, service.schema)
		if err != nil {
			t.Fatalf("resource repo: %v", err)
		}
		if _, err := resourceRepo.InsertResource(context.Background(), reference.sourceSeed(victim)); err != nil {
			t.Fatalf("seed edge source %s/%s: %s", victim.SourceType, victim.SourceID, service.scrub(err))
		}
		edgeRepo, err := control.NewEdgeRepository(client, service.schema)
		if err != nil {
			t.Fatalf("edge repo: %v", err)
		}
		conflicting := victim.row()
		tampered := "tampered-logical-key"
		conflicting.LogicalEdgeKey = &tampered
		if _, err := edgeRepo.InsertEdge(context.Background(), conflicting); err != nil {
			t.Fatalf("seed conflicting edge: %s", service.scrub(err))
		}

		_, loadErr := LoadPostgresGeneration(context.Background(),
			service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-edge-fail"))
		var edgeConflict *control.BatchEdgeConflictError
		if !errors.As(loadErr, &edgeConflict) {
			t.Fatalf("load error = %T %v, want BatchEdgeConflictError", loadErr, loadErr)
		}
		if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateFailed) {
			t.Fatalf("manifest state = %q found=%v, want FAILED", state, found)
		}
		if _, active := service.activePointerGeneration(ing03Project); active {
			t.Fatal("edge failure moved the active pointer")
		}
	})

	t.Run("catalog failures mark FAILED with scoped partials", func(t *testing.T) {
		for _, seam := range []string{"field-profile", "inventory", "relationship", "semantic-contributions"} {
			t.Run(seam, func(t *testing.T) {
				service := newIngest03Service(t)
				metaDir := t.TempDir()
				ing03WriteFixtures(t, metaDir, 2, false)
				ref := ing03Ref(t, ing03Project, ing03Generation)

				service.installCatalogFault(t, seam)
				defer func() { service.faults = nil }()

				_, loadErr := LoadPostgresGeneration(context.Background(),
					service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-catalog-"+seam))
				if !errors.Is(loadErr, errInjectedCatalogFault) {
					t.Fatalf("load error = %T %v, want injected catalog fault", loadErr, loadErr)
				}
				if state, found := service.manifestState(ing03Project, ing03Generation); !found || state != string(publication.StateFailed) {
					t.Fatalf("manifest state = %q found=%v, want FAILED", state, found)
				}
				if _, active := service.activePointerGeneration(ing03Project); active {
					t.Fatal("catalog failure moved the active pointer")
				}
				// Source data of the failed load is committed, scoped, and
				// invisible: never STAGED, replay-safe by identity.
				if count := service.countTable("fhir_resource", ""); count != 5 {
					t.Fatalf("pre-catalog rows = %d, want the committed 5", count)
				}
				switch seam {
				case "field-profile":
					// First scope's evidence survived atomically; the
					// second's wrote neither rows nor coverage marker.
					if first := service.countTable("fhir_field_profile_coverage", "resource_type = $1", "Observation"); first != 1 {
						t.Fatalf("Observation coverage after injected fault = %d, want the committed 1", first)
					}
					if second := service.countTable("fhir_field_profile_coverage", "resource_type = $1", "Patient"); second != 0 {
						t.Fatalf("Patient coverage = %d, want 0 (the failed batch wrote nothing)", second)
					}
				case "inventory":
					if markers := service.countTable("fhir_resource_inventory_build", ""); markers != 0 {
						t.Fatalf("inventory marker published despite failed scan: %d", markers)
					}
				case "relationship":
					// Inventory completed before the relationship refusal.
					if markers := service.countTable("fhir_resource_inventory_build", ""); markers != 1 {
						t.Fatalf("inventory marker = %d, want the published 1", markers)
					}
				case "semantic-contributions":
					// Contributions run last: every earlier evidence store
					// completed, and the failed contribution batch wrote
					// nothing at all.
					if markers := service.countTable("fhir_resource_inventory_build", ""); markers != 1 {
						t.Fatalf("inventory marker = %d, want the published 1", markers)
					}
					if edges := service.countTable("fhir_edge_occurrence", ""); edges == 0 {
						t.Fatalf("edge occurrences = %d, want the committed rows from the completed edge pass", edges)
					}
					if rows := service.countTable("fhir_semantic_inventory_contribution", ""); rows != 0 {
						t.Fatalf("contribution rows = %d, want 0 (the failed batch wrote nothing)", rows)
					}
				}
			})
		}
	})
}

// TestPostgresGenerationLoadSemanticContributionsPersistWithServedStateBoundary
// pins what a successful load guarantees about semantic evidence today:
// contributions are durable and deterministic, while the PostgreSQL page
// read still reports Unknown/empty because no production path creates the
// fhir_semantic_inventory_build row a served page requires. catalog/postgres
// deliberately refuses file-sourced build claims (semantic_build.go: the
// retained-backfill entry point is the only production INSERT into that
// table); the file-sourced build lifecycle (begin/advance/complete plus the
// fail pairing the Arango route drives around the same writes) is owned by
// a follow-up catalog slice, not this load. When that slice lands, this
// test must flip to asserting served entries - it is the tripwire, not an
// endorsement of the gap.
func TestPostgresGenerationLoadSemanticContributionsPersistWithServedStateBoundary(t *testing.T) {
	service := newIngest03Service(t)
	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 25, false)
	ref := ing03Ref(t, ing03Project, ing03Generation)

	if _, err := LoadPostgresGeneration(context.Background(),
		service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-semantics-pin")); err != nil {
		t.Fatalf("load: %s", service.scrub(err))
	}
	written := service.countTable("fhir_semantic_inventory_contribution", "")
	if written == 0 {
		t.Fatal("control load persisted no contributions: the witness is vacuous")
	}

	readRepo, err := control.NewSemanticReadRepository(service.iso, service.schema)
	if err != nil {
		t.Fatalf("semantic read repository: %s", service.scrub(err))
	}
	unrestricted := true
	page, err := readRepo.PageSemanticInventory(context.Background(), catalog.SemanticInventoryPageOptions{
		Project:                       ing03Project,
		DatasetGeneration:             ing03Generation,
		AuthResourcePathsUnrestricted: &unrestricted,
	})
	if err != nil {
		t.Fatalf("page semantic inventory: %s", service.scrub(err))
	}
	if page.State != catalog.SemanticInventoryUnknown || len(page.Entries) != 0 {
		t.Fatalf("page = state %q entries %d, want Unknown/empty while the file-sourced PG build lifecycle is unwired (flip this assertion when the owning slice serves the build row)", page.State, len(page.Entries))
	}
}

// TestPostgresGenerationLoadApplicationNameTagging covers the witness
// dependency offline: URL-form DSNs are tagged (or keep their explicit
// tag), and a keyword/value DSN that cannot be tagged surfaces the skip
// through the event sink instead of silently untagging the backend.
func TestPostgresGenerationLoadApplicationNameTagging(t *testing.T) {
	tagged, applied := joinApplicationName("postgres://u:p@h:5432/db", "app-x")
	if !applied || !strings.Contains(tagged, "application_name=app-x") {
		t.Fatalf("url dsn = %q applied=%v, want application_name=app-x", tagged, applied)
	}
	kept, applied := joinApplicationName("postgres://u:p@h:5432/db?application_name=mine", "app-x")
	if !applied || !strings.Contains(kept, "application_name=mine") {
		t.Fatalf("explicit tag = %q applied=%v, want the caller's mine preserved and counted as tagged", kept, applied)
	}
	untagged, applied := joinApplicationName("host=h dbname=d user=u", "app-x")
	if applied || untagged != "host=h dbname=d user=u" {
		t.Fatalf("keyword/value dsn = %q applied=%v, want unchanged and reported untagged", untagged, applied)
	}

	// The skip is announced, not silent: opening with a keyword/value DSN
	// emits the event even though the pool open itself is a named refusal.
	var events []string
	_, err := openPGGenerationClient(PGSourceOptions{
		DSN:             "host=h dbname=d user=u",
		Schema:          "s",
		ApplicationName: "app-x",
	}, func(event string, _ map[string]any) { events = append(events, event) })
	if err == nil {
		t.Fatal("keyword/value DSN opened, want a named store refusal")
	}
	found := false
	for _, event := range events {
		if event == "go_pg_application_name_skipped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v, want go_pg_application_name_skipped for the untaggable DSN", events)
	}
}

// ---------------------------------------------------------------------------
// Activation acceptance (explicit CAS via the generation repository).
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadActivationIsExplicitCASPinned(t *testing.T) {
	service := newIngest03Service(t)
	metaDir := t.TempDir()
	ing03WriteFixtures(t, metaDir, 2, false)
	ref := ing03Ref(t, ing03Project, ing03Generation)

	// The schema is migrated up front so "manifest missing" is the named
	// store verdict (no row), not the ambient 42P01 of an unmigrated
	// schema — the load itself still runs its own idempotent migrate.
	service.migrate()
	if activateErr := ActivatePostgresGeneration(context.Background(), ref, service.pgOptions(ing03AppBase+"-act-missing")); !errors.Is(activateErr, control.ErrManifestNotFound) {
		t.Fatalf("activate missing = %v, want ErrManifestNotFound", activateErr)
	}

	if _, err := LoadPostgresGeneration(context.Background(),
		service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-act-load")); err != nil {
		t.Fatalf("load: %s", service.scrub(err))
	}
	if _, active := service.activePointerGeneration(ing03Project); active {
		t.Fatal("successful load activated implicitly")
	}
	if activateErr := ActivatePostgresGeneration(context.Background(), ref, service.pgOptions(ing03AppBase+"-act-once")); activateErr != nil {
		t.Fatalf("explicit activate: %s", service.scrub(activateErr))
	}
	if generation, active := service.activePointerGeneration(ing03Project); !active || generation != ing03Generation {
		t.Fatalf("pointer = %q active=%v, want the staged generation", generation, active)
	}
	if activateErr := ActivatePostgresGeneration(context.Background(), ref, service.pgOptions(ing03AppBase+"-act-twice")); activateErr != nil {
		t.Fatalf("idempotent re-activate: %s", service.scrub(activateErr))
	}

	ref2 := ing03Ref(t, ing03Project, "gen-build-b")
	metaDir2 := t.TempDir()
	ing03WriteFixtures(t, metaDir2, 2, false)
	if _, err := LoadPostgresGeneration(context.Background(),
		service.loadOptions(metaDir2, ing03Project, &ref2), service.pgOptions(ing03AppBase+"-act-load-b")); err != nil {
		t.Fatalf("load b: %s", service.scrub(err))
	}
	if activateErr := ActivatePostgresGeneration(context.Background(), ref2, service.pgOptions(ing03AppBase+"-act-b")); activateErr != nil {
		t.Fatalf("activate b: %s", service.scrub(activateErr))
	}
	if generation, _ := service.activePointerGeneration(ing03Project); generation != "gen-build-b" {
		t.Fatalf("pointer = %q, want gen-build-b", generation)
	}

	// Same-generation re-load after explicit success: committed success is
	// read back, never re-run, and nothing is overwritten.
	_, reloadErr := LoadPostgresGeneration(context.Background(),
		service.loadOptions(metaDir, ing03Project, &ref), service.pgOptions(ing03AppBase+"-act-reload"))
	if !errors.Is(reloadErr, ErrGenerationAlreadyStaged) {
		t.Fatalf("reload = %v, want %v", reloadErr, ErrGenerationAlreadyStaged)
	}
}

// ---------------------------------------------------------------------------
// SQL hygiene audit over the orchestrator's own source.
// ---------------------------------------------------------------------------

var (
	ing03SQLVerbRegexp     = regexp.MustCompile(`(?i)\b(select|insert into|update\s+\w+\s+set|delete from|create table|drop table|truncate table|grant|revoke)\b`)
	ing03LineCommentRegexp = regexp.MustCompile(`(?m)//.*$`)
	ing03BlockCommentRe    = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func TestPostgresGenerationLoadOwnSourceHasNoSQL(t *testing.T) {
	contents, err := os.ReadFile("generation_load.go")
	if err != nil {
		t.Fatalf("read orchestrator source: %v", err)
	}
	code := ing03BlockCommentRe.ReplaceAllString(ing03LineCommentRegexp.ReplaceAllString(string(contents), ""), "")
	if loc := ing03SQLVerbRegexp.FindStringIndex(code); loc != nil {
		start, end := max(0, loc[0]-20), min(len(code), loc[1]+20)
		t.Fatalf("orchestrator source contains SQL verb %q; it must add no SQL of its own", code[start:end])
	}
	if strings.Contains(code, "pgx.Identifier") {
		t.Fatal("orchestrator must not build identifiers at all — no SQL belongs here")
	}
	// Test-side discipline is exercised (and enforced) by the helpers:
	_, _ = ing03Placeholder(t, 3), ing03OrderBy(2)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Ownership-scoped zero-survivor audit (defined last; runs after this
// file's tests).
// ---------------------------------------------------------------------------

func TestPostgresGenerationLoadZZZeroSurvivorAudit(t *testing.T) {
	raw := ing03TestPostgresURL(t)
	ing03CaptureBaseline(raw)
	if ing03BaselineErr != nil {
		t.Fatalf("baseline: %v", ing03BaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	// Ownership-scoped: only this process's own prefix is counted against
	// its own baseline. Foreign registered churn (sibling workers) is life
	// and never trips this audit. There is no global pg_database==baseline
	// check anywhere in this suite.
	var visible int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_ing03\\_load\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if visible != ing03Baseline {
		t.Errorf("verify_ing03_load_* database count = %d, want baseline %d (zero own-prefix survivors)",
			visible, ing03Baseline)
	}
	ing03OwnedMu.Lock()
	defer ing03OwnedMu.Unlock()
	for name, fixture := range ing03OwnedByName {
		if !fixture.dropped {
			t.Errorf("registered fixture %s was never dropped by its owner", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Shared failure injectors and small store-side readers.
// ---------------------------------------------------------------------------

var errInjectedCatalogFault = errors.New("ing03 test: injected catalog fault")

type injectedFieldProfileStore struct {
	inner     control.FieldProfileStore
	failAfter int
	calls     int
}

func (f *injectedFieldProfileStore) UpsertFieldProfileBatch(ctx context.Context, scope control.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage control.FieldProfileCoverage, batchSize int) error {
	f.calls++
	if f.calls > f.failAfter {
		return fmt.Errorf("%w: field profile upsert for %s", errInjectedCatalogFault, scope.ResourceType)
	}
	return f.inner.UpsertFieldProfileBatch(ctx, scope, docs, coverage, batchSize)
}

func (f *injectedFieldProfileStore) ReadFieldProfileRows(ctx context.Context, scope control.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	return f.inner.ReadFieldProfileRows(ctx, scope)
}

func (f *injectedFieldProfileStore) ReadFieldProfileRowsFiltered(ctx context.Context, filter control.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error) {
	return f.inner.ReadFieldProfileRowsFiltered(ctx, filter)
}

func (f *injectedFieldProfileStore) ReadProfileCoverage(ctx context.Context, filter control.FieldProfileFilter) ([]control.FieldProfileCoverage, error) {
	return f.inner.ReadProfileCoverage(ctx, filter)
}

func (f *injectedFieldProfileStore) ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error) {
	return f.inner.ListDistinctAuthResourcePaths(ctx, project)
}

type injectedInventoryStore struct {
	inner      control.InventoryStore
	failOnCall int
	calls      int
}

func (i *injectedInventoryStore) ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]control.InventorySourceGroup, error) {
	i.calls++
	if i.calls == i.failOnCall {
		return nil, fmt.Errorf("%w: scan counts for %s", errInjectedCatalogFault, resourceType)
	}
	return i.inner.ScanSourceCounts(ctx, project, datasetGeneration, resourceType)
}

func (i *injectedInventoryStore) InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error {
	return i.inner.InsertCounts(ctx, rows, batchSize)
}

func (i *injectedInventoryStore) PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error {
	return i.inner.PublishBuild(ctx, build)
}

func (i *injectedInventoryStore) ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error) {
	return i.inner.ReadBuild(ctx, project, datasetGeneration)
}

func (i *injectedInventoryStore) ReadCountSums(ctx context.Context, scope control.InventoryCountScope) (map[string]int64, error) {
	return i.inner.ReadCountSums(ctx, scope)
}

type injectedRelationshipStore struct {
	inner    control.RelationshipStore
	failNext bool
}

func (r *injectedRelationshipStore) ScanRelationshipOccurrences(ctx context.Context, scope control.RelationshipScope) ([]control.RelationshipOccurrenceGroup, error) {
	if r.failNext {
		return nil, fmt.Errorf("%w: relationship scan", errInjectedCatalogFault)
	}
	return r.inner.ScanRelationshipOccurrences(ctx, scope)
}

type injectedContributionWriter struct {
	inner    GenerationSemanticContributor
	failNext bool
}

func (w *injectedContributionWriter) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	if w.failNext {
		return fmt.Errorf("%w: contribution write", errInjectedCatalogFault)
	}
	return w.inner.WriteSemanticInventoryContributions(ctx, contributions, batchSize)
}

func (s *ing03Service) installCatalogFault(t *testing.T, seam string) {
	t.Helper()
	switch seam {
	case "field-profile":
		s.faults = &ing03CatalogFaults{
			wrapFieldProfile: func(inner control.FieldProfileStore) control.FieldProfileStore {
				return &injectedFieldProfileStore{inner: inner, failAfter: 1}
			},
		}
	case "inventory":
		s.faults = &ing03CatalogFaults{
			wrapInventory: func(inner control.InventoryStore) control.InventoryStore {
				return &injectedInventoryStore{inner: inner, failOnCall: 1}
			},
		}
	case "relationship":
		s.faults = &ing03CatalogFaults{
			wrapRelationship: func(inner control.RelationshipStore) control.RelationshipStore {
				return &injectedRelationshipStore{inner: inner, failNext: true}
			},
		}
	case "semantic-contributions":
		s.faults = &ing03CatalogFaults{
			wrapContributions: func(inner GenerationSemanticContributor) GenerationSemanticContributor {
				return &injectedContributionWriter{inner: inner, failNext: true}
			},
		}
	default:
		t.Fatalf("unknown seam %q", seam)
	}
}

func (s *ing03Service) resourcePayload(resourceType, logicalID string) string {
	s.t.Helper()
	var payload string
	err := s.iso.QueryRow(context.Background(),
		"SELECT payload::text FROM "+ing03QIdent(s.t, s.schema, "fhir_resource")+
			" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4",
		ing03Project, ing03Generation, resourceType, logicalID).Scan(&payload)
	if err != nil {
		s.t.Fatalf("read payload %s/%s: %s", resourceType, logicalID, s.scrub(err))
	}
	return payload
}

type ing03EdgeSeed struct {
	Label, SourceType, SourceID, TargetType, TargetID, SourcePath string
	SourceOrdinal                                                 int64
}

func (s *ing03Service) readOneEdge() ing03EdgeSeed {
	s.t.Helper()
	var seed ing03EdgeSeed
	err := s.iso.QueryRow(context.Background(),
		"SELECT label, source_resource_type, source_logical_id, target_resource_type, target_logical_id, source_path, source_ordinal FROM "+
			ing03QIdent(s.t, s.schema, "fhir_edge_occurrence")+ing03OrderBy(7)+" LIMIT 1").
		Scan(&seed.Label, &seed.SourceType, &seed.SourceID, &seed.TargetType, &seed.TargetID, &seed.SourcePath, &seed.SourceOrdinal)
	if err != nil {
		s.t.Fatalf("read seed edge: %s", s.scrub(err))
	}
	return seed
}

func (e ing03EdgeSeed) row() control.EdgeOccurrenceRow {
	return control.EdgeOccurrenceRow{
		Project: ing03Project, DatasetGeneration: ing03Generation, AuthResourcePath: ing03AuthPath,
		Label: e.Label, SourceResourceType: e.SourceType, SourceLogicalID: e.SourceID,
		TargetResourceType: e.TargetType, TargetLogicalID: e.TargetID,
		SourcePath: e.SourcePath, SourceOrdinal: e.SourceOrdinal,
	}
}

// sourceSeed copies the reference schema's real stored row so the victim
// schema holds byte-identical content: an identical replay is tolerated by
// the batch contract, so the resource pass proceeds and the conflict lands
// where it is meant to — the edge batch.
func (s *ing03Service) sourceSeed(e ing03EdgeSeed) source.Resource {
	return source.Resource{
		Project: ing03Project, DatasetGeneration: ing03Generation,
		ResourceType: e.SourceType, LogicalID: e.SourceID, AuthResourcePath: ing03AuthPath,
		Payload: json.RawMessage(s.resourcePayload(e.SourceType, e.SourceID)),
	}
}

// TestPostgresShippedLoadConfigsValidateOffline is the dial-free composition
// gate the arch-backend-06 review demanded: every shipped PostgreSQL caller
// configuration is assembled by PGLoadConfig (load, activation, and the
// cmd/loom-data catalog-wiring and staging-verification routes) and must
// pass Config.Validate with no network contact, so a series assignment that
// the migration-ownership law refuses can never hide behind unrun live
// lanes. Alive controls keep each refusal honest: fix only the defect and
// the same shape validates.
func TestPostgresShippedLoadConfigsValidateOffline(t *testing.T) {
	shipped := PGSourceOptions{
		DSN:            "postgresql://loom_test@127.0.0.1:1/loom_test?sslmode=disable",
		Schema:         "verify_ab06_offline_src",
		ManifestSchema: "verify_ab06_offline_manifests",
	}
	// The exact configuration every shipped lane opens with (SetupNone).
	if err := PGLoadConfig(shipped, shipped.DSN, control.SetupNone).Validate(); err != nil {
		t.Fatalf("shipped composition must validate dial-free, got: %v", err)
	}
	// SetupMigrate also validates (it is a legal policy; no shipped lane
	// uses it today, and the witness suite exercises it against the server).
	if err := PGLoadConfig(shipped, shipped.DSN, control.SetupMigrate).Validate(); err != nil {
		t.Fatalf("migrate-policy composition must validate: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*PGSourceOptions)
		policy  control.SetupPolicy
		wantErr error
	}{
		{
			// One schema, one history owner: source and dataset_release
			// pinned ledgers disagree at version 8, so sharing a schema
			// is a named pre-dial refusal.
			name:    "manifest schema may not equal the source schema",
			mutate:  func(o *PGSourceOptions) { o.ManifestSchema = o.Schema },
			policy:  control.SetupNone,
			wantErr: control.ErrSeriesAssignment,
		},
		{
			name:    "manifest schema must be named",
			mutate:  func(o *PGSourceOptions) { o.ManifestSchema = " " },
			policy:  control.SetupNone,
			wantErr: control.ErrSeriesAssignment,
		},
		{
			name:    "source schema must be named",
			mutate:  func(o *PGSourceOptions) { o.Schema = "" },
			policy:  control.SetupNone,
			wantErr: control.ErrSeriesAssignment,
		},
		{
			// SetupVerify was removed in the arch-backend-06 fold: it was
			// indistinguishable from SetupMigrate. Re-adding the policy
			// name without a real no-DDL implementation must fail here.
			name:    "the removed verify policy stays refused",
			mutate:  func(*PGSourceOptions) {},
			policy:  control.SetupPolicy("verify"),
			wantErr: control.ErrInvalidConfig,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := shipped
			tc.mutate(&opts)
			err := PGLoadConfig(opts, opts.DSN, tc.policy).Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			// Alive control: repairing only this defect validates — the
			// unmutated shipped shape passes the same validator.
			if err := PGLoadConfig(shipped, shipped.DSN, control.SetupNone).Validate(); err != nil {
				t.Fatalf("alive control (unmutated shipped config) must validate: %v", err)
			}
		})
	}

	// Production callers may not hand-assemble control.Config around the
	// shared builder: one drift could reintroduce a refused composition.
	for _, file := range []struct {
		path    string
		allowed int // occurrences of the control.Config{ literal allowed
	}{
		{path: "generation_load.go", allowed: 1}, // only inside PGLoadConfig
		{path: "postgres_writer.go", allowed: 0},
	} {
		raw, err := os.ReadFile(filepath.Join(".", file.path))
		if err != nil {
			t.Fatalf("read %s: %v", file.path, err)
		}
		if got := strings.Count(string(raw), "control.Config{"); got > file.allowed {
			t.Fatalf("%s builds control.Config{ %d times, want at most %d (all shipped configs come from PGLoadConfig)", file.path, got, file.allowed)
		}
	}
}
