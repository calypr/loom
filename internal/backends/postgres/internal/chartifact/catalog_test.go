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

	"github.com/calypr/loom/internal/authscope"
	publicationpostgres "github.com/calypr/loom/internal/backends/postgres/internal/publication"
	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
)

var pgprivcat01ResourceNamePattern = regexp.MustCompile(`^pgprivcat01_it_[0-9]+_[0-9a-f]+$`)

func testPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 private artifact catalog integration tests")
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

func truncUS(t time.Time) time.Time { return t.Truncate(time.Microsecond) }

func jsonTimeLiteral(t *testing.T, value time.Time) string {
	t.Helper()
	encoded, err := json.Marshal(value.UTC())
	if err != nil {
		t.Fatalf("encode expected payload timestamp: %v", err)
	}
	return string(encoded)
}

type guardedPool struct {
	*pgxpool.Pool
	mu        sync.Mutex
	violation *statementViolation
}

type statementViolation struct {
	op  string
	sql string
}

func (g *guardedPool) note(op, sql string) {
	head := strings.ToUpper(strings.TrimSpace(sql))
	if strings.HasPrefix(head, "BEGIN") || strings.HasPrefix(head, "START TRANSACTION") {
		g.mu.Lock()
		if g.violation == nil {
			v := statementViolation{op: op, sql: sql}
			g.violation = &v
		}
		g.mu.Unlock()
	}
}

func (g *guardedPool) check(t *testing.T) {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.violation != nil {
		t.Fatalf("catalog opened a database transaction (%s): %s", g.violation.op, g.violation.sql)
	}
}

func (g *guardedPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	g.note("exec", sql)
	return g.Pool.Exec(ctx, sql, args...)
}

func (g *guardedPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	g.note("query", sql)
	return g.Pool.Query(ctx, sql, args...)
}

func (g *guardedPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	g.note("queryrow", sql)
	return g.Pool.QueryRow(ctx, sql, args...)
}

func (g *guardedPool) Begin(ctx context.Context) (pgx.Tx, error) {
	g.note("begin", "BEGIN")
	return nil, errors.New("guardedPool: catalog code must never begin a transaction")
}

var _ DB = (*guardedPool)(nil)

type catalogService struct {
	ctx       context.Context
	admin     *pgxpool.Pool
	iso       *pgxpool.Pool
	isoB      *pgxpool.Pool
	guarded   *guardedPool
	schema    string
	qualified string
	catalog   *Catalog
	scrub     func(any) string
	fail      func(step string, err error)
}

func newCatalogService(t *testing.T) *catalogService {
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

	dbName := fmt.Sprintf("pgprivcat01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgprivcat01ResourceNamePattern.MatchString(dbName) {
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

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fail("parse isolated database config", err)
	}
	config.MaxConns = 1
	isoB, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		fail("connect second pool to isolated database", err)
	}
	t.Cleanup(isoB.Close)

	schema := fmt.Sprintf("pgprivcat01_it_%d_%s", time.Now().UnixNano(), randomHex())
	if !pgprivcat01ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if _, err := iso.Exec(cleanupCtx,
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	s := &catalogService{
		ctx: ctx, admin: admin, iso: iso, isoB: isoB,
		schema:    schema,
		qualified: pgx.Identifier{schema, PrivateArtifactsTable}.Sanitize(),
		scrub:     scrub, fail: fail,
	}
	s.migrate(t)
	s.guarded = &guardedPool{Pool: iso}
	catalog, err := NewCatalog(s.guarded, schema)
	if err != nil {
		fail("build catalog", err)
	}
	s.catalog = catalog
	return s
}

func (s *catalogService) migrate(t *testing.T) {
	t.Helper()
	owner, err := publicationpostgres.OwnerMigrations()
	if err != nil {
		s.fail("publication owner migrations", err)
	}
	var history []storepostgres.Migration
	for _, m := range owner {
		if m.Version == 1 || m.Version == 7 {
			history = append(history, m)
		}
	}
	if len(history) != 2 || history[0].Version != 1 || history[0].Name != "ledger" ||
		history[1].Version != 7 || history[1].Name != "private_clickhouse_artifacts" {
		t.Fatalf("publication owner history subset = %+v, want exactly versions {1 ledger, 7 private_clickhouse_artifacts}", owner)
	}
	runner, err := storepostgres.NewMigrationRunner(s.iso, s.schema, history)
	if err != nil {
		s.fail("new migration runner", err)
	}
	result, err := runner.Migrate(s.ctx)
	if err != nil {
		s.fail("migrate catalog", err)
	}
	if result.LatestVersion != 7 {
		t.Fatalf("latest migration version = %d, want 7", result.LatestVersion)
	}
	wantLedger := []storepostgres.LedgerRecord{
		{Version: 1, Name: "ledger", Checksum: "db70f7758495f7557b3a694149b80ed6e5d4cb74ea5aa7d59a1a2eaa46fc9cde"},
		{Version: 7, Name: "private_clickhouse_artifacts", Checksum: "e827a5a1515df903a540a76858da6ad8e5afaede01f5c0b096f9f87a9b662cfb"},
	}
	if len(result.Applied) != len(wantLedger) {
		t.Fatalf("applied history = %+v, want %+v", result.Applied, wantLedger)
	}
	for i := range wantLedger {
		got := result.Applied[i]
		if got.Version != wantLedger[i].Version || got.Name != wantLedger[i].Name || got.Checksum != wantLedger[i].Checksum {
			t.Fatalf("ledger row %d = %+v, want %+v", i, got, wantLedger[i])
		}
	}
	var regclass *string
	if err := s.iso.QueryRow(s.ctx, `SELECT to_regclass($1)::text`, s.schema+"."+PrivateArtifactsTable).Scan(&regclass); err != nil {
		s.fail("witness to_regclass", err)
	}
	if regclass == nil || *regclass == "" {
		t.Fatalf("table %s was not created by migration 7", PrivateArtifactsTable)
	}
}

type rowWitness struct {
	ArtifactID        string
	PhysicalTable     string
	State             string
	RowCount          int64
	ByteCount         int64
	LeaseOwner        string
	LeaseUntil        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ExecutionID       string
	OutputID          string
	StageID           string
	Project           string
	DatasetGeneration string
	AuthScopeMode     string
	ScopeMode         string
	SchemaDigest      string
	ScopeDigest       string
	Payload           string
}

func (s *catalogService) witnessRow(t *testing.T, artifactID string) rowWitness {
	t.Helper()
	var row rowWitness
	err := s.iso.QueryRow(s.ctx,
		"SELECT artifact_id, physical_table, state, row_count, byte_count, lease_owner,"+
			" lease_until, created_at, updated_at, execution_id, output_id, stage_id, project,"+
			" dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload::text"+
			" FROM "+s.qualified+" WHERE artifact_id = $1",
		artifactID).Scan(
		&row.ArtifactID, &row.PhysicalTable, &row.State, &row.RowCount, &row.ByteCount, &row.LeaseOwner,
		&row.LeaseUntil, &row.CreatedAt, &row.UpdatedAt, &row.ExecutionID, &row.OutputID, &row.StageID,
		&row.Project, &row.DatasetGeneration, &row.AuthScopeMode, &row.ScopeMode, &row.SchemaDigest,
		&row.ScopeDigest, &row.Payload)
	if err != nil {
		s.fail("witness row "+artifactID, err)
	}
	return row
}

func (s *catalogService) requireNoRow(t *testing.T, artifactID string) {
	t.Helper()
	var count int
	if err := s.iso.QueryRow(s.ctx,
		"SELECT count(*) FROM "+s.qualified+" WHERE artifact_id = $1", artifactID).Scan(&count); err != nil {
		s.fail("witness absence "+artifactID, err)
	}
	if count != 0 {
		t.Fatalf("artifact %q row still present, want deleted", artifactID)
	}
}

func (s *catalogService) witnessSingleRow(t *testing.T) rowWitness {
	t.Helper()
	var total int
	if err := s.iso.QueryRow(s.ctx, "SELECT count(*) FROM "+s.qualified).Scan(&total); err != nil {
		s.fail("witness row count", err)
	}
	if total != 1 {
		t.Fatalf("catalog holds %d rows, want exactly 1", total)
	}
	var row rowWitness
	err := s.iso.QueryRow(s.ctx,
		"SELECT artifact_id, physical_table, state, row_count, byte_count, lease_owner,"+
			" lease_until, created_at, updated_at, execution_id, output_id, stage_id, project,"+
			" dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload::text"+
			" FROM "+s.qualified).Scan(
		&row.ArtifactID, &row.PhysicalTable, &row.State, &row.RowCount, &row.ByteCount, &row.LeaseOwner,
		&row.LeaseUntil, &row.CreatedAt, &row.UpdatedAt, &row.ExecutionID, &row.OutputID, &row.StageID,
		&row.Project, &row.DatasetGeneration, &row.AuthScopeMode, &row.ScopeMode, &row.SchemaDigest,
		&row.ScopeDigest, &row.Payload)
	if err != nil {
		s.fail("witness single row", err)
	}
	return row
}

func (s *catalogService) payloadMember(t *testing.T, artifactID, member string) string {
	t.Helper()
	var value string
	err := s.iso.QueryRow(s.ctx,
		"SELECT (payload->'"+member+"')::text FROM "+s.qualified+" WHERE artifact_id = $1",
		artifactID).Scan(&value)
	if err != nil {
		s.fail("witness payload member "+artifactID+" "+member, err)
	}
	return value
}

func (s *catalogService) payloadTextField(t *testing.T, artifactID, member string) string {
	t.Helper()
	var value *string
	err := s.iso.QueryRow(s.ctx,
		"SELECT (payload -> '"+member+"')::text FROM "+s.qualified+" WHERE artifact_id = $1",
		artifactID).Scan(&value)
	if err != nil {
		s.fail("witness payload field "+artifactID+" "+member, err)
	}
	if value == nil {
		return "<absent>"
	}
	return *value
}

func (s *catalogService) rawExec(t *testing.T, sql string, args ...any) pgconn.CommandTag {
	t.Helper()
	tag, err := s.iso.Exec(s.ctx, sql, args...)
	if err != nil {
		s.fail("raw exec", err)
	}
	return tag
}

func fixedTime() time.Time {
	return time.Date(2026, 11, 9, 12, 0, 0, 123456789, time.UTC)
}

func sampleManifest(artifactID string) chartifact.Manifest {
	return sampleManifestWithLease(artifactID, fixedTime().Add(10*time.Minute))
}

func sampleManifestWithLease(artifactID string, leaseUntil time.Time) chartifact.Manifest {
	created := fixedTime()
	return chartifact.Manifest{
		ArtifactID: artifactID,
		Identity: chartifact.Identity{
			ExecutionID: "execution-1", OutputID: "output-1", StageID: "stage-1",
			Project: "project-a", DatasetGeneration: "generation-1",
			RecipeDigest: "recipe-digest", PlanDigest: "plan-digest",
			SchemaDigest: "schema-digest", ScopeDigest: "scope-digest",
			AuthScopeMode:     authscope.ReadScopeRestricted,
			AuthResourcePaths: []string{"/Programs/a"},
			ScopeMode:         chartifact.ScopeModeRows,
		},
		Columns: []chartifact.Column{
			{ID: "loom:row_id", Name: "__loom_row_id", SemanticPath: "loom:row_id", LogicalType: "string", ClickHouseType: "String", Internal: true},
			{ID: "col-name", Name: "name", SemanticPath: "Patient.name", LogicalType: "string", ClickHouseType: "String"},
		},
		PhysicalTable: "loom_private_stage_" + strings.ReplaceAll(artifactID, "-", ""),
		State:         chartifact.StateCreating,
		LeaseOwner:    "writer-1",
		LeaseUntil:    leaseUntil,
		CreatedAt:     created,
		UpdatedAt:     created,
	}
}

func idsOf(manifests []chartifact.Manifest) []string {
	ids := make([]string, len(manifests))
	for i, manifest := range manifests {
		ids[i] = manifest.ArtifactID
	}
	return ids
}

func TestNewCatalogRejectsNilPoolAndUnsafeSchemas(t *testing.T) {
	if _, err := NewCatalog(nil, "public"); err == nil {
		t.Fatal("NewCatalog accepted a nil pool")
	}
	var pool DB = &guardedPool{}
	for _, schema := range []string{"", "Public", "bad-name", "bad name", "a;b", strings.Repeat("s", 64)} {
		if _, err := NewCatalog(pool, schema); !errors.Is(err, ErrInvalidSchema) {
			t.Fatalf("NewCatalog(%q) = %v, want ErrInvalidSchema", schema, err)
		}
	}
	if _, err := NewCatalog(pool, "pgprivcat01_ok"); err != nil {
		t.Fatalf("NewCatalog on a safe schema = %v", err)
	}
}

func TestCatalogMethodsPropagateCancellationBeforeQuerying(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.catalog.Create(ctx, sampleManifest("cancelled-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create cancellation = %v, want context.Canceled", err)
	}
	if _, err := s.catalog.ListExpired(ctx, time.Now().UTC(), 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListExpired cancellation = %v, want context.Canceled", err)
	}
	s.guarded.check(t)
}

func TestCreateStoresLiteralRowAndDuplicatePreservesOriginal(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	manifest := sampleManifest("art-create")
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	want := rowWitness{
		ArtifactID: "art-create", PhysicalTable: manifest.PhysicalTable, State: "CREATING",
		RowCount: 0, ByteCount: 0, LeaseOwner: "writer-1",
		LeaseUntil: truncUS(fixedTime().Add(10 * time.Minute)),
		CreatedAt:  truncUS(fixedTime()), UpdatedAt: truncUS(fixedTime()),
		ExecutionID: "execution-1", OutputID: "output-1", StageID: "stage-1",
		Project: "project-a", DatasetGeneration: "generation-1",
		AuthScopeMode: "restricted", ScopeMode: "ROW_SCOPED",
		SchemaDigest: "schema-digest", ScopeDigest: "scope-digest",
	}
	got := s.witnessRow(t, "art-create")
	got.LeaseUntil, got.CreatedAt, got.UpdatedAt = got.LeaseUntil.UTC(), got.CreatedAt.UTC(), got.UpdatedAt.UTC()
	if got.ArtifactID != want.ArtifactID || got.PhysicalTable != want.PhysicalTable || got.State != want.State ||
		got.RowCount != want.RowCount || got.ByteCount != want.ByteCount || got.LeaseOwner != want.LeaseOwner ||
		!got.LeaseUntil.Equal(want.LeaseUntil) || !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		got.ExecutionID != want.ExecutionID || got.OutputID != want.OutputID || got.StageID != want.StageID ||
		got.Project != want.Project || got.DatasetGeneration != want.DatasetGeneration ||
		got.AuthScopeMode != want.AuthScopeMode || got.ScopeMode != want.ScopeMode ||
		got.SchemaDigest != want.SchemaDigest || got.ScopeDigest != want.ScopeDigest {
		t.Fatalf("literal row mismatch:\n got %+v\nwant %+v", got, want)
	}
	if payloadTime := s.payloadTextField(t, "art-create", "createdAt"); payloadTime != `"2026-11-09T12:00:00.123456789Z"` {
		t.Fatalf("payload createdAt = %s, want the exact nanosecond JSON string", payloadTime)
	}

	before := s.witnessRow(t, "art-create")
	rogue := sampleManifest("art-create")
	rogue.PhysicalTable = "loom_private_stage_rogue"
	rogue.Identity.Project = "project-b"
	rogue.LeaseOwner = "writer-rogue"
	if err := s.catalog.Create(s.ctx, rogue); !errors.Is(err, ErrArtifactExists) {
		t.Fatalf("duplicate Create = %v, want ErrArtifactExists", err)
	}
	after := s.witnessRow(t, "art-create")
	if before.Payload != after.Payload || before.PhysicalTable != after.PhysicalTable ||
		before.Project != after.Project || before.LeaseOwner != after.LeaseOwner {
		t.Fatalf("duplicate Create mutated the stored manifest:\n before %+v\n after %+v", before, after)
	}

	if err := s.catalog.Create(s.ctx, sampleManifest(" spaced")); err == nil {
		t.Fatal("Create accepted an untrimmed artifact ID")
	}
	bad := sampleManifest("art-bad-state")
	bad.State = "SOMETHING_ELSE"
	if err := s.catalog.Create(s.ctx, bad); err == nil || !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("Create with invalid state = %v, want explicit refusal", err)
	}
	negative := sampleManifest("art-negative")
	negative.RowCount = -1
	if err := s.catalog.Create(s.ctx, negative); err == nil || !strings.Contains(err.Error(), "nonnegative") {
		t.Fatalf("Create with negative count = %v, want explicit refusal", err)
	}
}

func TestCreateRefusesRowsItCouldNeverReadBack(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	cases := []struct {
		name   string
		mutate func(*chartifact.Manifest)
	}{
		{"zero lease until", func(m *chartifact.Manifest) { m.LeaseUntil = time.Time{} }},
		{"zero created at", func(m *chartifact.Manifest) { m.CreatedAt = time.Time{} }},
		{"zero updated at", func(m *chartifact.Manifest) { m.UpdatedAt = time.Time{} }},
		{"blank physical table", func(m *chartifact.Manifest) { m.PhysicalTable = "" }},
		{"whitespace physical table", func(m *chartifact.Manifest) { m.PhysicalTable = "   " }},
		{"untrimmed physical table", func(m *chartifact.Manifest) { m.PhysicalTable = " loom_private_stage_x " }},
		{"blank lease owner", func(m *chartifact.Manifest) { m.LeaseOwner = "" }},
		{"whitespace lease owner", func(m *chartifact.Manifest) { m.LeaseOwner = " " }},
		{"untrimmed lease owner", func(m *chartifact.Manifest) { m.LeaseOwner = " writer-1 " }},
		{"blank execution ID", func(m *chartifact.Manifest) { m.Identity.ExecutionID = "" }},
		{"blank output ID", func(m *chartifact.Manifest) { m.Identity.OutputID = "" }},
		{"blank stage ID", func(m *chartifact.Manifest) { m.Identity.StageID = "" }},
		{"blank project", func(m *chartifact.Manifest) { m.Identity.Project = "" }},
		{"blank dataset generation", func(m *chartifact.Manifest) { m.Identity.DatasetGeneration = "" }},
	}
	for index, c := range cases {
		artifactID := fmt.Sprintf("art-gate-%d", index)
		manifest := sampleManifest(artifactID)
		c.mutate(&manifest)
		err := s.catalog.Create(s.ctx, manifest)
		if err == nil {
			t.Fatalf("%s: Create = nil, want a pre-write refusal", c.name)
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			t.Fatalf("%s: Create reached the server with SQLSTATE %s, want a catalog validation refusal", c.name, pgErr.Code)
		}
		if !strings.Contains(err.Error(), "chartifact postgres: create manifest") {
			t.Fatalf("%s: Create = %v, want a catalog validation error", c.name, s.scrub(err))
		}
		s.requireNoRow(t, artifactID)
	}
}

func TestCreatePreservesMissingVersusNullVersusEmptyAndFailsOnNullishPayload(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)

	nilPaths := sampleManifest("scope-nil")
	nilPaths.Identity.AuthScopeMode = authscope.ReadScopeUnrestricted
	nilPaths.Identity.AuthResourcePaths = nil
	emptyPaths := sampleManifest("scope-empty")
	emptyPaths.Identity.AuthScopeMode = authscope.ReadScopeUnrestricted
	emptyPaths.Identity.AuthResourcePaths = []string{}
	for _, manifest := range []chartifact.Manifest{nilPaths, emptyPaths} {
		if err := s.catalog.Create(s.ctx, manifest); err != nil {
			t.Fatalf("Create %s = %v", manifest.ArtifactID, err)
		}
	}
	if typeof := s.payloadTextField(t, "scope-nil", "identity"); typeof == "<absent>" {
		t.Fatal("identity member absent")
	}
	var storedType string
	if err := s.iso.QueryRow(s.ctx,
		"SELECT jsonb_typeof(payload #> '{identity,AuthResourcePaths}') FROM "+s.qualified+" WHERE artifact_id = $1",
		"scope-nil").Scan(&storedType); err != nil {
		s.fail("witness nil paths type", err)
	}
	if storedType != "null" {
		t.Fatalf("nil AuthResourcePaths stored as %s, want JSON null", storedType)
	}
	if err := s.iso.QueryRow(s.ctx,
		"SELECT jsonb_typeof(payload #> '{identity,AuthResourcePaths}') FROM "+s.qualified+" WHERE artifact_id = $1",
		"scope-empty").Scan(&storedType); err != nil {
		s.fail("witness empty paths type", err)
	}
	if storedType != "array" {
		t.Fatalf("empty AuthResourcePaths stored as %s, want JSON array", storedType)
	}
	got, err := s.catalog.ListExpired(s.ctx, fixedTime().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListExpired = %v", err)
	}
	byID := map[string]chartifact.Manifest{}
	for _, manifest := range got {
		byID[manifest.ArtifactID] = manifest
	}
	nilRead, ok := byID["scope-nil"]
	if !ok || nilRead.Identity.AuthResourcePaths != nil {
		t.Fatalf("nil paths did not round-trip as nil: %#v", nilRead.Identity.AuthResourcePaths)
	}
	emptyRead, ok := byID["scope-empty"]
	if !ok || emptyRead.Identity.AuthResourcePaths == nil || len(emptyRead.Identity.AuthResourcePaths) != 0 {
		t.Fatalf("empty paths did not round-trip as empty non-nil: %#v", emptyRead.Identity.AuthResourcePaths)
	}
	if !reflect.DeepEqual(nilRead.Columns, nilPaths.Columns) {
		t.Fatalf("columns did not round-trip: got %#v want %#v", nilRead.Columns, nilPaths.Columns)
	}
	if !reflect.DeepEqual(nilRead.Identity, nilPaths.Identity) {
		t.Fatalf("identity did not round-trip: got %#v want %#v", nilRead.Identity, nilPaths.Identity)
	}
	if !nilRead.CreatedAt.Equal(nilPaths.CreatedAt) || !nilRead.LeaseUntil.Equal(nilPaths.LeaseUntil) {
		t.Fatalf("nanosecond stamps did not round-trip: created %v lease %v", nilRead.CreatedAt, nilRead.LeaseUntil)
	}

	rawTime := fixedTime()
	if _, err := s.iso.Exec(s.ctx,
		"INSERT INTO "+s.qualified+" (artifact_id, physical_table, state, row_count, byte_count,"+
			" lease_owner, lease_until, created_at, updated_at, execution_id, output_id, stage_id,"+
			" project, dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload)"+
			" VALUES ($1,$2,'READY',0,0,'writer-null', $3, $3, $3,'e','o','s','p','g','unrestricted','','','',"+
			" jsonb_build_object('artifactId','payload-null-lease','leaseUntil', null))",
		"payload-null-lease", "loom_private_stage_nulllease", rawTime); err != nil {
		s.fail("insert nullish payload fixture", err)
	}
	if _, err := s.catalog.ListExpired(s.ctx, rawTime.Add(time.Hour), 50); err == nil ||
		!strings.Contains(err.Error(), "nullish timestamp member") {
		t.Fatalf("nullish payload ListExpired = %v, want an explicit refusal", err)
	}

	cases := []struct {
		name      string
		sql       string
		payload   string
		wantState string
	}{
		{"state outside the closed set", `VALUES ($1,'t','BOGUS',0,0,'w',$2,$2,$2,'e','o','s','p','g','unrestricted','','','',$3)`, `{"artifactId":"x"}`, "23514"},
		{"negative row count", `VALUES ($1,'t','READY',-1,0,'w',$2,$2,$2,'e','o','s','p','g','unrestricted','','','',$3)`, `{"artifactId":"x"}`, "23514"},
		{"empty lease owner", `VALUES ($1,'t','READY',0,0,'',$2,$2,$2,'e','o','s','p','g','unrestricted','','','',$3)`, `{"artifactId":"x"}`, "23514"},
		{"non-object payload", `VALUES ($1,'t','READY',0,0,'w',$2,$2,$2,'e','o','s','p','g','unrestricted','','','',$3)`, `[]`, "23514"},
		{"SQL NULL scan column", `VALUES ($1,'t','READY',0,0,'w',$2,$2,$2,'e','o',NULL,'p','g','unrestricted','','','',$3)`, `{"artifactId":"x"}`, "23502"},
	}
	for index, c := range cases {
		_, err := s.iso.Exec(s.ctx,
			"INSERT INTO "+s.qualified+" (artifact_id, physical_table, state, row_count, byte_count,"+
				" lease_owner, lease_until, created_at, updated_at, execution_id, output_id, stage_id,"+
				" project, dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload)"+
				" "+c.sql,
			fmt.Sprintf("reject-%d", index), rawTime, c.payload)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != c.wantState {
			t.Fatalf("%s: err = %v, want SQLSTATE %s", c.name, s.scrub(err), c.wantState)
		}
	}
}

func TestUpdateRequiresLeaseOwnerAndPreservesIdentity(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	manifest := sampleManifestWithLease("art-update", now.Add(10*time.Minute))
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	beforeMembers := map[string]string{}
	for _, member := range []string{"identity", "columns", "physicalTable", "artifactId", "createdAt"} {
		beforeMembers[member] = s.payloadMember(t, "art-update", member)
	}

	if err := s.catalog.Update(s.ctx, "art-update", "writer-2", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(20 * time.Minute), UpdatedAt: now.Add(time.Second),
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("wrong-owner Update = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Update(s.ctx, "missing-artifact", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(20 * time.Minute), UpdatedAt: now.Add(time.Second),
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("missing-row Update = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Update(s.ctx, "art-update", "writer-1", chartifact.Progress{
		State: "NOT_A_STATE", RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(20 * time.Minute), UpdatedAt: now.Add(time.Second),
	}); err == nil || !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("invalid-state Update = %v, want explicit refusal", err)
	}
	if err := s.catalog.Update(s.ctx, "art-update", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096, LeaseUntil: now.Add(20 * time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "lease timestamps") {
		t.Fatalf("zero-updatedAt Update = %v, want explicit refusal", err)
	}

	progress := chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(20 * time.Minute), UpdatedAt: now.Add(time.Second),
	}
	if err := s.catalog.Update(s.ctx, "art-update", "writer-1", progress); err != nil {
		t.Fatalf("owner Update = %v", err)
	}
	after := s.witnessRow(t, "art-update")
	if after.State != "WRITING" || after.RowCount != 5 || after.ByteCount != 4096 ||
		!after.LeaseUntil.UTC().Equal(truncUS(progress.LeaseUntil)) {
		t.Fatalf("progress not stored: %+v", after)
	}
	beforeRow := s.witnessRow(t, "art-update")
	for name, pair := range map[string][2]string{
		"physical_table": {manifest.PhysicalTable, after.PhysicalTable},
		"execution_id":   {manifest.Identity.ExecutionID, after.ExecutionID},
		"output_id":      {manifest.Identity.OutputID, after.OutputID},
		"stage_id":       {manifest.Identity.StageID, after.StageID},
		"project":        {manifest.Identity.Project, after.Project},
		"generation":     {manifest.Identity.DatasetGeneration, after.DatasetGeneration},
		"auth_scope":     {string(manifest.Identity.AuthScopeMode), after.AuthScopeMode},
		"schema_digest":  {manifest.Identity.SchemaDigest, after.SchemaDigest},
		"scope_digest":   {manifest.Identity.ScopeDigest, after.ScopeDigest},
		"created_at":     {truncUS(manifest.CreatedAt).String(), after.CreatedAt.UTC().String()},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s changed across Update: %q -> %q", name, pair[0], pair[1])
		}
	}
	_ = beforeRow
	for member, before := range beforeMembers {
		if after := s.payloadMember(t, "art-update", member); after != before {
			t.Errorf("Update mutated immutable payload member %q:\n before %s\n after %s", member, before, after)
		}
	}
	if state := s.payloadTextField(t, "art-update", "state"); state != `"WRITING"` {
		t.Fatalf("payload state = %s, want WRITING", state)
	}
	if rows := s.payloadTextField(t, "art-update", "rowCount"); rows != "5" {
		t.Fatalf("payload rowCount = %s, want the JSON number 5", rows)
	}
	if lease := s.payloadTextField(t, "art-update", "leaseUntil"); lease != jsonTimeLiteral(t, progress.LeaseUntil) {
		t.Fatalf("payload leaseUntil = %s, want the supplied lease", lease)
	}
}

func TestUpdateLeaseIsMonotonicExceptCleanupAndCleanupIsTerminal(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	longLease := now.Add(30 * time.Minute)
	manifest := sampleManifestWithLease("art-lease", longLease)
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	storedLease := s.payloadTextField(t, "art-lease", "leaseUntil")

	if err := s.catalog.Update(s.ctx, "art-lease", "writer-1", chartifact.Progress{
		State: chartifact.StateReady, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(time.Minute), UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("shorten attempt Update = %v", err)
	}
	after := s.witnessRow(t, "art-lease")
	if !after.LeaseUntil.UTC().Equal(truncUS(longLease)) {
		t.Fatalf("active Update shortened the lease to %v, want %v", after.LeaseUntil, longLease)
	}
	if lease := s.payloadTextField(t, "art-lease", "leaseUntil"); lease != storedLease {
		t.Fatalf("payload leaseUntil moved on a shortening update: %s != %s", lease, storedLease)
	}
	if state := s.payloadTextField(t, "art-lease", "state"); state != `"READY"` {
		t.Fatalf("payload state = %s, want READY", state)
	}

	reclaimAt := now.Add(30 * time.Minute)
	if err := s.catalog.Update(s.ctx, "art-lease", "writer-1", chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 5, ByteCount: 4096,
		LeaseUntil: reclaimAt, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cleanup Update = %v", err)
	}
	if got := s.witnessRow(t, "art-lease"); !got.LeaseUntil.UTC().Equal(truncUS(reclaimAt)) || got.State != "CLEANUP_PENDING" {
		t.Fatalf("cleanup lease not applied: %+v", got)
	}
	if err := s.catalog.Update(s.ctx, "art-lease", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(40 * time.Minute), UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("active transition from cleanup = %v, want ErrLeaseLost", err)
	}
	shorter := now.Add(-time.Minute)
	if err := s.catalog.Update(s.ctx, "art-lease", "writer-1", chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 5, ByteCount: 4096,
		LeaseUntil: shorter, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cleanup retry = %v", err)
	}
	if got := s.witnessRow(t, "art-lease"); !got.LeaseUntil.UTC().Equal(truncUS(shorter)) {
		t.Fatalf("cleanup retry lease = %v, want %v", got.LeaseUntil, shorter)
	}
}

func TestUpdateAndRenewRejectExpiredLeases(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	expired := now.Add(-time.Hour)
	manifest := sampleManifestWithLease("art-expired", expired)
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	if err := s.catalog.Update(s.ctx, "art-expired", "writer-1", chartifact.Progress{
		State: chartifact.StateWriting, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now.Add(time.Hour), UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("expired Update = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Update(s.ctx, "art-expired", "writer-1", chartifact.Progress{
		State: chartifact.StateCleanupPending, RowCount: 5, ByteCount: 4096,
		LeaseUntil: now, UpdatedAt: now,
	}); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("expired cleanup Update = %v, want ErrLeaseLost", err)
	}
	renewed, err := s.catalog.Renew(s.ctx, "art-expired", "writer-1", now.Add(time.Hour))
	if err != nil || renewed {
		t.Fatalf("expired Renew = (%t, %v), want no resurrection", renewed, err)
	}
	got := s.witnessRow(t, "art-expired")
	if !got.LeaseUntil.UTC().Equal(truncUS(expired)) || got.State != "CREATING" || got.LeaseOwner != "writer-1" {
		t.Fatalf("expired-row mutation leaked: %+v", got)
	}
}

func TestRenewExtendsOwnedActiveUnexpiredLeaseOnly(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	held := now.Add(10 * time.Minute)
	manifest := sampleManifestWithLease("art-renew", held)
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	storedLease := s.payloadTextField(t, "art-renew", "leaseUntil")

	if _, err := s.catalog.Renew(s.ctx, "art-renew", "writer-1", time.Time{}); err == nil {
		t.Fatal("Renew accepted a zero lease time")
	}
	if renewed, err := s.catalog.Renew(s.ctx, "art-renew", "not-the-owner", now.Add(time.Hour)); err != nil || renewed {
		t.Fatalf("wrong-owner Renew = (%t, %v), want (false, nil)", renewed, err)
	}
	if renewed, err := s.catalog.Renew(s.ctx, "art-renew", "writer-1", now.Add(time.Minute)); err != nil || !renewed {
		t.Fatalf("shortening Renew = (%t, %v), want a no-op success", renewed, err)
	}
	if got := s.witnessRow(t, "art-renew"); !got.LeaseUntil.UTC().Equal(truncUS(held)) {
		t.Fatalf("Renew shortened the lease to %v", got.LeaseUntil)
	}
	if lease := s.payloadTextField(t, "art-renew", "leaseUntil"); lease != storedLease {
		t.Fatalf("payload leaseUntil moved on a shortening renew: %s != %s", lease, storedLease)
	}

	later := now.Add(time.Hour)
	if renewed, err := s.catalog.Renew(s.ctx, "art-renew", "writer-1", later); err != nil || !renewed {
		t.Fatalf("extending Renew = (%t, %v)", renewed, err)
	}
	if got := s.witnessRow(t, "art-renew"); !got.LeaseUntil.UTC().Equal(truncUS(later)) {
		t.Fatalf("extended lease = %v, want %v", got.LeaseUntil, later)
	}
	if lease := s.payloadTextField(t, "art-renew", "leaseUntil"); lease != jsonTimeLiteral(t, later) {
		t.Fatalf("payload leaseUntil = %s, want the extended lease", lease)
	}

	if err := s.catalog.Update(s.ctx, "art-renew", "writer-1", chartifact.Progress{
		State: chartifact.StateCleanupPending, LeaseUntil: now.Add(-time.Minute), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cleanup Update = %v", err)
	}
	if renewed, err := s.catalog.Renew(s.ctx, "art-renew", "writer-1", now.Add(2*time.Hour)); err != nil || renewed {
		t.Fatalf("cleanup-state Renew = (%t, %v), want (false, nil)", renewed, err)
	}
}

func TestListExpiredIsBoundedAndStableWithByteOrderTieBreak(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	base := fixedTime()
	expire := func(id string, offset time.Duration) {
		manifest := sampleManifestWithLease(id, base.Add(offset))
		if err := s.catalog.Create(s.ctx, manifest); err != nil {
			t.Fatalf("Create %s = %v", id, err)
		}
	}
	expire("tie-B", 3*time.Minute)
	expire("tie-a", 3*time.Minute)
	expire("early-1", 1*time.Minute)
	expire("late-1", 30*time.Minute)
	active := sampleManifestWithLease("active-1", base.Add(100*time.Hour))
	if err := s.catalog.Create(s.ctx, active); err != nil {
		t.Fatalf("Create active = %v", err)
	}

	if got, err := s.catalog.ListExpired(s.ctx, time.Time{}, 10); err == nil {
		t.Fatalf("zero-cutoff ListExpired = %#v, want an error", got)
	}
	if got, err := s.catalog.ListExpired(s.ctx, base.Add(10*time.Minute), 0); err != nil || len(got) != 0 {
		t.Fatalf("limit<=0 ListExpired = (%#v, %v), want empty", got, err)
	}
	if got, err := s.catalog.ListExpired(s.ctx, base.Add(10*time.Minute), -3); err != nil || len(got) != 0 {
		t.Fatalf("negative-limit ListExpired = (%#v, %v), want empty", got, err)
	}

	pageOne, err := s.catalog.ListExpired(s.ctx, base.Add(10*time.Minute), 2)
	if err != nil {
		t.Fatalf("ListExpired = %v", err)
	}
	if len(pageOne) != 2 || pageOne[0].ArtifactID != "early-1" || pageOne[1].ArtifactID != "tie-B" {
		t.Fatalf("bounded page = %v, want [early-1 tie-B]", idsOf(pageOne))
	}
	again, err := s.catalog.ListExpired(s.ctx, base.Add(10*time.Minute), 2)
	if err != nil || len(again) != 2 || again[0].ArtifactID != pageOne[0].ArtifactID || again[1].ArtifactID != pageOne[1].ArtifactID {
		t.Fatalf("unstable page: %v vs %v (%v)", idsOf(pageOne), idsOf(again), err)
	}
	full, err := s.catalog.ListExpired(s.ctx, base.Add(30*time.Minute), 10)
	if err != nil {
		t.Fatalf("full ListExpired = %v", err)
	}
	if want := []string{"early-1", "tie-B", "tie-a", "late-1"}; !reflect.DeepEqual(idsOf(full), want) {
		t.Fatalf("expired page = %v, want %v (inclusive cutoff, byte-order tie)", idsOf(full), want)
	}
	if full[0].PhysicalTable != sampleManifestWithLease("early-1", base).PhysicalTable ||
		!reflect.DeepEqual(full[0].Identity, sampleManifest("early-1").Identity) {
		t.Fatalf("decoded manifest lost identity or physical table: %#v", full[0])
	}
}

func TestListExpiredCapsOversizedPages(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	base := fixedTime()
	tag := s.rawExec(t,
		"INSERT INTO "+s.qualified+" (artifact_id, physical_table, state, row_count, byte_count,"+
			" lease_owner, lease_until, created_at, updated_at, execution_id, output_id, stage_id,"+
			" project, dataset_generation, auth_scope_mode, scope_mode, schema_digest, scope_digest, payload)"+
			" SELECT 'cap-' || to_char(g, 'FM0000'), 'loom_private_stage_cap' || to_char(g, 'FM0000'),"+
			" 'READY', 0, 0, 'writer-cap', $1::timestamptz + make_interval(secs => g), $1, $1,"+
			" 'e','o','s','p','g','unrestricted','','','',"+
			" jsonb_build_object('artifactId', 'cap-' || to_char(g, 'FM0000'), 'state', 'READY',"+
			"   'leaseUntil', to_char($1::timestamptz + make_interval(secs => g), 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'),"+
			"   'createdAt', to_char($1, 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'),"+
			"   'updatedAt', to_char($1, 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'))"+
			" FROM generate_series(1, $2) g",
		base, 1003)
	if tag.RowsAffected() != 1003 {
		t.Fatalf("cap fixture inserted %d rows, want 1003", tag.RowsAffected())
	}
	got, err := s.catalog.ListExpired(s.ctx, base.Add(time.Hour), maxExpiredPageSize+7)
	if err != nil {
		t.Fatalf("oversized ListExpired = %v", err)
	}
	if len(got) != maxExpiredPageSize {
		t.Fatalf("page size = %d, want the %d cap", len(got), maxExpiredPageSize)
	}
	if got[0].ArtifactID != "cap-0001" {
		t.Fatalf("capped page does not start at the stable head: %s", got[0].ArtifactID)
	}
}

func TestClaimCleanupRechecksExpiryAndHasOneWinner(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	base := fixedTime()
	cutoff := base.Add(time.Hour)
	claimLease := base.Add(3 * time.Hour)
	manifest := sampleManifestWithLease("art-claim", base)
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	unexpired := sampleManifestWithLease("art-unexpired", base.Add(30*time.Minute))
	if err := s.catalog.Create(s.ctx, unexpired); err != nil {
		t.Fatalf("Create unexpired = %v", err)
	}

	if _, ok, err := s.catalog.ClaimCleanup(s.ctx, "art-missing", "cleanup-x", cutoff, claimLease); err != nil || ok {
		t.Fatalf("missing-row claim = (%t, %v), want (false, nil)", ok, err)
	}
	if _, ok, err := s.catalog.ClaimCleanup(s.ctx, "art-unexpired", "cleanup-x", base, claimLease); err != nil || ok {
		t.Fatalf("not-yet-expired claim (lease 12:30 > cutoff 12:00) = (%t, %v), want (false, nil)", ok, err)
	}
	if _, _, err := s.catalog.ClaimCleanup(s.ctx, "art-claim", "cleanup-x", cutoff, cutoff); err == nil {
		t.Fatal("claim with a non-future cleanup lease succeeded")
	}
	if _, _, err := s.catalog.ClaimCleanup(s.ctx, "art-claim", "", cutoff, claimLease); err == nil {
		t.Fatal("claim with a blank owner succeeded")
	}

	catalogB, err := NewCatalog(s.isoB, s.schema)
	if err != nil {
		s.fail("build second catalog", err)
	}
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		owners  []string
		results []bool
		winner  chartifact.Manifest
		start   = make(chan struct{})
	)
	run := func(c *Catalog, owner string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, ok, err := c.ClaimCleanup(s.ctx, "art-claim", owner, cutoff, claimLease)
			if err != nil {
				s.fail("concurrent claim "+owner, err)
			}
			mu.Lock()
			defer mu.Unlock()
			results = append(results, ok)
			if ok {
				winner = claimed
				owners = append(owners, owner)
			}
		}()
	}
	run(s.catalog, "cleanup-1")
	run(catalogB, "cleanup-2")
	close(start)
	wg.Wait()

	if len(results) != 2 || (results[0] == results[1]) {
		t.Fatalf("concurrent claims did not have exactly one winner: %v", results)
	}
	if winner.State != chartifact.StateCleanupPending ||
		!winner.LeaseUntil.Equal(claimLease) ||
		!reflect.DeepEqual(winner.Identity, manifest.Identity) ||
		winner.PhysicalTable != manifest.PhysicalTable {
		t.Fatalf("claimed manifest wrong: %#v", winner)
	}
	row := s.witnessRow(t, "art-claim")
	if row.State != "CLEANUP_PENDING" || row.LeaseOwner != owners[0] {
		t.Fatalf("stored claim wrong: %+v (winner %s)", row, owners[0])
	}
	if lease := s.payloadTextField(t, "art-claim", "leaseOwner"); lease != `"`+owners[0]+`"` {
		t.Fatalf("payload leaseOwner = %s, want winner %s", lease, owners[0])
	}
	if lease := s.payloadTextField(t, "art-claim", "state"); lease != `"CLEANUP_PENDING"` {
		t.Fatalf("payload state = %s, want CLEANUP_PENDING", lease)
	}
	if _, ok, err := s.catalog.ClaimCleanup(s.ctx, "art-claim", "cleanup-3", cutoff, claimLease); err != nil || ok {
		t.Fatalf("claim after the winner moved the lease = (%t, %v), want (false, nil)", ok, err)
	}
}

func TestDeleteAndReleaseCleanupValidateCleanupOwner(t *testing.T) {
	s := newCatalogService(t)
	defer s.guarded.check(t)
	now := time.Now().UTC()
	manifest := sampleManifestWithLease("art-delete", now.Add(-time.Hour))
	if err := s.catalog.Create(s.ctx, manifest); err != nil {
		t.Fatalf("Create = %v", err)
	}
	if err := s.catalog.Delete(s.ctx, "art-delete", "writer-1"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("pre-claim Delete = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.ReleaseCleanup(s.ctx, "art-delete", "writer-1", now); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("pre-claim ReleaseCleanup = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Delete(s.ctx, "", ""); err == nil {
		t.Fatal("Delete accepted empty arguments")
	}

	claimed, ok, err := s.catalog.ClaimCleanup(s.ctx, "art-delete", "cleanup-1", now, now.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("ClaimCleanup = (%#v, %t, %v)", claimed, ok, err)
	}
	if err := s.catalog.Delete(s.ctx, "art-delete", "cleanup-2"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("wrong-owner Delete = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.ReleaseCleanup(s.ctx, "art-delete", "cleanup-2", now); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("wrong-owner ReleaseCleanup = %v, want ErrLeaseLost", err)
	}
	releaseAt := now.Add(-time.Minute)
	if err := s.catalog.ReleaseCleanup(s.ctx, "art-delete", "cleanup-1", releaseAt); err != nil {
		t.Fatalf("owner ReleaseCleanup = %v", err)
	}
	row := s.witnessRow(t, "art-delete")
	if row.State != "CLEANUP_PENDING" || !row.LeaseUntil.UTC().Equal(truncUS(releaseAt)) {
		t.Fatalf("release did not make the manifest reclaimable: %+v", row)
	}
	if lease := s.payloadTextField(t, "art-delete", "leaseUntil"); lease != jsonTimeLiteral(t, releaseAt) {
		t.Fatalf("payload leaseUntil = %s, want the released lease", lease)
	}
	second, ok, err := s.catalog.ClaimCleanup(s.ctx, "art-delete", "cleanup-2", now.Add(time.Minute), now.Add(2*time.Hour))
	if err != nil || !ok {
		t.Fatalf("re-claim = (%#v, %t, %v)", second, ok, err)
	}
	if err := s.catalog.Delete(s.ctx, "art-delete", "cleanup-1"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("stale-owner Delete = %v, want ErrLeaseLost", err)
	}
	if err := s.catalog.Delete(s.ctx, "art-delete", "cleanup-2"); err != nil {
		t.Fatalf("owner Delete = %v", err)
	}
	s.requireNoRow(t, "art-delete")

	active := sampleManifestWithLease("art-active-delete", now.Add(10*time.Minute))
	if err := s.catalog.Create(s.ctx, active); err != nil {
		t.Fatalf("Create active = %v", err)
	}
	if err := s.catalog.Delete(s.ctx, "art-active-delete", "writer-1"); !errors.Is(err, chartifact.ErrLeaseLost) {
		t.Fatalf("active-state Delete = %v, want ErrLeaseLost", err)
	}
}

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
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'pgprivcat01\_it\_%' ESCAPE '\'`).Scan(&leftoverDBs); err != nil {
		t.Fatalf("count leftover databases: %v", err)
	}
	if leftoverDBs != 0 {
		var names []string
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'pgprivcat01\_it\_%' ESCAPE '\'`)
		if err == nil {
			for rows.Next() {
				var name string
				if err := rows.Scan(&name); err == nil {
					names = append(names, name)
				}
			}
			rows.Close()
		}
		t.Fatalf("%d isolated databases left behind: %v", leftoverDBs, names)
	}

	var leftoverSchemas int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgprivcat01\_it\_%' ESCAPE '\'`).Scan(&leftoverSchemas); err != nil {
		t.Fatalf("count leftover schemas: %v", err)
	}
	if leftoverSchemas != 0 {
		t.Fatalf("%d owned schemas left behind in the service database", leftoverSchemas)
	}
}
