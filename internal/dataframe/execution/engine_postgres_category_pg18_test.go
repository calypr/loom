package execution_test

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

	postgres "github.com/calypr/loom/internal/backends/postgres"
	storepg "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	pgrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec06csPrefix  = "verify_exec06_cs_"
	exec06csNameFmt = exec06csPrefix + "%d_%s"
)

var exec06csNamePattern = regexp.MustCompile(`^verify_exec06_cs_[0-9]+_[0-9a-f]+$`)

var (
	exec06csOwnedMu     sync.Mutex
	exec06csOwnedByName = map[string]string{}
)

func exec06csRegister(db, schema string) {
	exec06csOwnedMu.Lock()
	defer exec06csOwnedMu.Unlock()
	exec06csOwnedByName[db] = schema
}

func exec06csRegistrySnapshot() map[string]string {
	exec06csOwnedMu.Lock()
	defer exec06csOwnedMu.Unlock()
	out := make(map[string]string, len(exec06csOwnedByName))
	for db, schema := range exec06csOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec06csBaselineOnce  sync.Once
	exec06csBaselineNames []string
	exec06csBaselineErr   error
)

func exec06csCaptureBaseline(raw string) {
	exec06csBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec06csBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec06\\_cs\\_%' ESCAPE '\\'")
		if err != nil {
			exec06csBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec06csBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec06csBaselineNames = append(exec06csBaselineNames, name)
		}
	})
}

func exec06csRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec06csService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	dsn      string
	target   compiler.Target
	scrub    func(any) string
	fail     func(step string, err error)
	executor dataframeexecution.SourceRows
}

func newExec06csService(t *testing.T) *exec06csService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 category scan acceptance tests")
	}
	scrub, addSecret := exec04pvScrubberFor(raw)
	exec06csCaptureBaseline(raw)
	if exec06csBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec06csBaselineErr))
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
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

	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::int8, current_setting('server_version')").Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	if err := storepg.CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required PostgreSQL 18 backend: %v", err)
	}

	dbName := fmt.Sprintf(exec06csNameFmt, time.Now().UnixNano(), exec06csRandomHex(t))
	if !exec06csNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	var created int64
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", dbName).Scan(&created); err != nil {
		fail("verify isolated database creation", err)
	}
	if created != 1 {
		t.Fatalf("isolated database %q did not verifiably come into existence", dbName)
	}
	exec06csRegister(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	addSecret(dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(exec06csNameFmt, time.Now().UnixNano(), exec06csRandomHex(t))
	if !exec06csNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec06csRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	executor, err := postgres.NewSourceRows(iso)
	if err != nil {
		fail("create PostgreSQL row executor", err)
	}
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		fail("bind PostgreSQL storage placement", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		fail("select PostgreSQL compile target", err)
	}
	return &exec06csService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

func (s *exec06csService) migrate(t *testing.T) {
	t.Helper()
	client, err := storepg.OpenPool(s.ctx, storepg.ClientConfig{DSN: s.dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		s.fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	defer client.Close()
	if _, err := storepg.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.fail("migrate owned schema", err)
	}
}

func (s *exec06csService) seed(t *testing.T, rows []exec04pvFixtureRow) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	for _, row := range rows {
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project: row.project, DatasetGeneration: row.generation, ResourceType: row.resourceType,
			LogicalID: row.logicalID, AuthResourcePath: row.authPath, Payload: json.RawMessage(row.payload),
		}); err != nil {
			s.fail("insert fixture resource "+row.logicalID, err)
		}
	}
}

func (s *exec06csService) countResources(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM "`+s.schema+`"."fhir_resource"`).Scan(&count); err != nil {
		s.fail("count fixture rows", err)
	}
	return count
}

func (s *exec06csService) witness(t *testing.T, query string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, query, args...)
	if err != nil {
		s.fail("direct-SQL witness", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		var left, right any
		if err := rows.Scan(&left, &right); err != nil {
			s.fail("witness scan", err)
		}
		line := make([]string, 2)
		for index, cell := range []any{left, right} {
			if cell == nil {
				line[index] = "<null>"
				continue
			}
			line[index] = fmt.Sprintf("%v", cell)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness rows", err)
	}
	return out
}

type exec06csRecorder struct {
	s        *exec06csService
	calls    []exec06csCall
	afterRow func(rowsSeen int)
	failPort error
}

type exec06csCall struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

func (r *exec06csRecorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	if r.failPort != nil {
		return r.failPort
	}
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.calls = append(r.calls, exec06csCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	seen := 0
	wrapped := func(row map[string]any) error {
		if err := visit(row); err != nil {
			return err
		}
		seen++
		if r.afterRow != nil {
			r.afterRow(seen)
		}
		return nil
	}
	return r.s.executor(ctx, request, wrapped)
}

func (r *exec06csRecorder) counts() int { return len(r.calls) }

func (s *exec06csService) engine(t *testing.T, recorder *exec06csRecorder) *dataframeexecution.Engine {
	t.Helper()
	config := exec04pvForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec06-cs-scope" }
	config.SourceRows = recorder.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

func exec06csCategoryFixture() []exec04pvFixtureRow {
	return []exec04pvFixtureRow{
		{"P1", "gen-a", "Patient", "p-female-1", "FHIR/Patient", `{"gender":"female"}`},
		{"P1", "gen-a", "Patient", "p-female-2", "FHIR/Patient", `{"gender":"female"}`},
		{"P1", "gen-a", "Patient", "p-male", "FHIR/Patient", `{"gender":"male"}`},
		{"P1", "gen-a", "Patient", "p-absent", "FHIR/Patient", `{"active":true}`},
		{"P1", "gen-a", "Patient", "p-null", "FHIR/Patient", `{"gender":null}`},
		{"P1", "gen-a", "Patient", "p-empty", "FHIR/Patient", `{"gender":""}`},
		{"P1", "gen-a", "Patient", "p-false", "FHIR/Patient", `{"gender":false}`},
		{"P1", "gen-a", "Patient", "p-zero", "FHIR/Patient", `{"gender":0}`},
		{"P2", "gen-a", "Patient", "p-decoy-project", "FHIR/Patient", `{"gender":"decoy-project"}`},
		{"P1", "gen-b", "Patient", "p-decoy-generation", "FHIR/Patient", `{"gender":"decoy-generation"}`},
		{"P1", "gen-a", "Patient", "p-decoy-auth", "FHIR/Encounter", `{"gender":"decoy-auth"}`},
		{"P1", "gen-a", "Observation", "o-decoy-type", "FHIR/Observation", `{"gender":"decoy-type"}`},
	}
}

func exec06csScan(t *testing.T, s *exec06csService, engine *dataframeexecution.Engine, resolved dataframeexecution.Resolved, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
	t.Helper()
	return engine.ScanCategories(s.ctx, resolved, request)
}

func exec06csPatientBundle() recipe.Bundle { return exec04pvBundle() }

func (s *exec06csService) preparePatients(t *testing.T, project, generation string, recorder *exec06csRecorder) (*dataframeexecution.Engine, dataframeexecution.Resolved) {
	t.Helper()
	s.migrate(t)
	s.seed(t, exec06csCategoryFixture())
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec06csPatientBundle(), exec04pvBindings(project, generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	return engine, resolved
}

func exec06csWantValues() []dataframeexecution.CategoryValue {
	return []dataframeexecution.CategoryValue{
		{Present: false, Value: nil},
		{Present: true, Value: false},
		{Present: true, Value: nil},
		{Present: true, Value: float64(0)},
		{Present: true, Value: ""},
		{Present: true, Value: "female"},
		{Present: true, Value: "male"},
	}
}

func TestPostgresCategoryExactValuesLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	engine, resolved := s.preparePatients(t, "P1", "gen-a", recorder)
	result, err := exec06csScan(t, s, engine, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("scan: %s", s.scrub(err))
	}
	want := exec06csWantValues()
	if !reflect.DeepEqual(result.Values, want) {
		t.Fatalf("values = %#v, want %#v", result.Values, want)
	}
	if !result.Complete || result.Overflow || result.ConclusiveMissing {
		t.Fatalf("status = Complete:%v Overflow:%v ConclusiveMissing:%v", result.Complete, result.Overflow, result.ConclusiveMissing)
	}
	if result.Proof.Version != 1 || result.Proof.Output != "patients" || result.Proof.Column != "gender" ||
		result.Proof.MaxValues != 8 || !result.Proof.PresenceTracked || strings.TrimSpace(result.Proof.Fingerprint) == "" {
		t.Fatalf("proof not bound to the request: %#v", result.Proof)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want exactly one bounded page statement", recorder.counts())
	}
	call := recorder.calls[0]
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("tag = %q, want POSTGRESQL", call.tag)
	}
	if len(call.declared) != 2 || call.declared[0] != "present" || call.declared[1] != "value" {
		t.Fatalf("declared contract = %v, want [present value]", call.declared)
	}
	if !strings.Contains(call.sql, "GROUP BY") || !strings.Contains(call.sql, "LIMIT") || strings.Contains(call.sql, "@") {
		t.Fatalf("statement is not the bounded distinct page: %s", call.sql)
	}
	last := call.args[len(call.args)-1]
	if last.Type != command.PostgreSQLParameterBigint || last.Value != int64(9) {
		t.Fatalf("bound argument = %+v, want bigint 9 (maxValues+1)", last)
	}
	for _, forbidden := range []string{"decoy-project", "decoy-generation", "decoy-auth", "decoy-type"} {
		for _, value := range result.Values {
			if text, ok := value.Value.(string); ok && strings.Contains(text, forbidden) {
				t.Fatalf("foreign-scope data leaked into the page: %v", value)
			}
		}
	}
}

func TestPostgresCategoryDeterministicRerunLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	engine, resolved := s.preparePatients(t, "P1", "gen-a", recorder)
	first, err := exec06csScan(t, s, engine, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("first scan: %s", s.scrub(err))
	}
	second, err := exec06csScan(t, s, engine, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("second scan: %s", s.scrub(err))
	}
	if !reflect.DeepEqual(first.Values, second.Values) {
		t.Fatalf("page order was not deterministic:\nfirst:  %#v\nsecond: %#v", first.Values, second.Values)
	}
	direct := s.witness(t, `
		SELECT present::text,
		       CASE WHEN value IS NULL THEN '<sql-null>'
		            WHEN jsonb_typeof(value) = 'null' THEN '<json-null>'
		            ELSE value #>> '{}' END
		FROM (
			SELECT COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false) AS present,
			       CASE WHEN COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false)
			            THEN payload #> ARRAY['gender'] END AS value
			FROM "`+s.schema+`"."fhir_resource"
			WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'
			  AND auth_resource_path = ANY($3::text[])
		) grouped GROUP BY present, value`,
		"P1", "gen-a", []string{"FHIR/Patient"})
	witnessSet := map[string]int{}
	for _, row := range direct {
		witnessSet[row[0]+"|"+row[1]]++
	}
	scanSet := map[string]int{}
	for _, got := range first.Values {
		encoded := fmt.Sprintf("%v", got.Value)
		if got.Value == nil {
			encoded = "<sql-null>"
			if got.Present {
				encoded = "<json-null>"
			}
		} else if text, ok := got.Value.(string); ok {
			encoded = text
		}
		scanSet[fmt.Sprintf("%v", got.Present)+"|"+encoded]++
	}
	if !reflect.DeepEqual(witnessSet, scanSet) {
		t.Fatalf("decoded page disagrees with the direct-SQL witness:\nscan:    %v\nwitness: %v", scanSet, witnessSet)
	}
	if recorder.counts() != 2 {
		t.Fatalf("port calls = %d, want two (one per scan)", recorder.counts())
	}
}

func TestPostgresCategoryOverflowLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	s.migrate(t)
	s.seed(t, exec06csCategoryFixture())
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec06csPatientBundle(), exec04pvBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	before := recorder.counts()
	result, err := exec06csScan(t, s, engine, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 2})
	if err != nil {
		t.Fatalf("overflow scan: %s", s.scrub(err))
	}
	if recorder.counts()-before != 1 {
		t.Fatalf("overflow scan issued %d statements, want exactly one — truncation is page-owned", recorder.counts()-before)
	}
	if len(result.Values) != 2 {
		t.Fatalf("values = %#v, want exactly two capped", result.Values)
	}
	if result.Complete || !result.Overflow || result.ConclusiveMissing {
		t.Fatalf("status = Complete:%v Overflow:%v ConclusiveMissing:%v, want Complete=false Overflow=true", result.Complete, result.Overflow, result.ConclusiveMissing)
	}
}

func TestPostgresCategoryEmptyScopeNoLeakLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	engine, resolved := s.preparePatients(t, "P-none", "gen-a", recorder)
	result, err := exec06csScan(t, s, engine, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("empty-scope scan: %s", s.scrub(err))
	}
	if len(result.Values) != 0 || !result.Complete || result.Overflow {
		t.Fatalf("empty scope result = %#v, want zero values, complete", result)
	}
	if total := s.countResources(t); total != 12 {
		t.Fatalf("fixture witness: fhir_resource holds %d rows, want 12 (scope-empty, not database-empty)", total)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
	call := recorder.calls[0]
	if len(call.args) < 5 {
		t.Fatalf("statement carries %d arguments, want at least the five scope/window arguments: %s", len(call.args), call.sql)
	}
	if call.args[0].Type != command.PostgreSQLParameterText || call.args[0].Value != "P-none" {
		t.Fatalf("project argument = %+v, want text \"P-none\"", call.args[0])
	}
	if call.args[1].Type != command.PostgreSQLParameterText || call.args[1].Value != "gen-a" {
		t.Fatalf("generation argument = %+v, want text \"gen-a\"", call.args[1])
	}
}

func TestPostgresCategoryCancellationLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	engine, resolved := s.preparePatients(t, "P1", "gen-a", recorder)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	recorder.afterRow = func(rowsSeen int) {
		if rowsSeen == 1 {
			cancel()
		}
	}
	result, err := engine.ScanCategories(ctx, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled scan = (%#v, %v), want an errors.Is(context.Canceled) error", result, err)
	}
	if result.Complete || result.Overflow || len(result.Values) != 0 {
		t.Fatalf("cancelled scan must be zero-result and never complete, got %#v", result)
	}
	recorder.afterRow = nil
	var alive dataframeexecution.CategoryScanResult
	var aliveErr error
	for attempt := 1; attempt <= 6; attempt++ {
		alive, aliveErr = engine.ScanCategories(s.ctx, resolved, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
		if aliveErr == nil {
			break
		}
	}
	if aliveErr != nil {
		t.Fatalf("ALIVE control never converged: %s", s.scrub(aliveErr))
	}
	if !reflect.DeepEqual(alive.Values, exec06csWantValues()) || !alive.Complete {
		t.Fatalf("ALIVE control result = %#v", alive)
	}
}

func TestPostgresCategoryAuthScopeLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	s.migrate(t)
	fixture := append(exec06csCategoryFixture(),
		exec04pvFixtureRow{"P1", "gen-a", "Patient", "p-secret", "FHIR/Secret", `{"gender":"secret-gender"}`})
	s.seed(t, fixture)
	engine := s.engine(t, recorder)
	narrowedBindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "gen-a", AuthResourcePaths: []string{"FHIR/Patient"}}
	narrowed, err := engine.CompileResolvedBundle(s.ctx, exec06csPatientBundle(), narrowedBindings)
	if err != nil {
		t.Fatalf("CompileResolvedBundle narrowed: %s", s.scrub(err))
	}
	result, err := exec06csScan(t, s, engine, narrowed, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("narrowed scan: %s", s.scrub(err))
	}
	if !reflect.DeepEqual(result.Values, exec06csWantValues()) {
		t.Fatalf("narrowed values = %#v, want the seven in-scope groups", result.Values)
	}
	call := recorder.calls[recorder.counts()-1]
	argTexts := make([]string, 0, len(call.args))
	for _, arg := range call.args {
		argTexts = append(argTexts, fmt.Sprintf("%s", arg.Value))
	}
	joined := strings.Join(argTexts, "\n")
	if !strings.Contains(joined, "FHIR/Patient") {
		t.Fatalf("authorized path FHIR/Patient missing from the scoped statement arguments:\n%s\nstatement: %s", joined, call.sql)
	}
	if strings.Contains(joined, "FHIR/Secret") {
		t.Fatalf("unauthorized path FHIR/Secret appears in the scoped statement arguments:\n%s", joined)
	}
	widenedBindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "gen-a", AuthResourcePaths: []string{"FHIR/Patient", "FHIR/Secret"}}
	widened, err := engine.CompileResolvedBundle(s.ctx, exec06csPatientBundle(), widenedBindings)
	if err != nil {
		t.Fatalf("CompileResolvedBundle widened: %s", s.scrub(err))
	}
	wide, err := exec06csScan(t, s, engine, widened, dataframeexecution.CategoryScanRequest{Output: "patients", Column: "gender", MaxValues: 8})
	if err != nil {
		t.Fatalf("widened scan: %s", s.scrub(err))
	}
	found := false
	for _, value := range wide.Values {
		if text, ok := value.Value.(string); ok && text == "secret-gender" {
			found = true
		}
	}
	if !found {
		t.Fatalf("widened scan = %#v, want secret-gender once the path is authorized", wide.Values)
	}
}

func TestPostgresCategoryStageScanLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	s.migrate(t)
	s.seed(t, []exec04pvFixtureRow{
		{"P1", "gen-a", "Observation", "o-final-1", "FHIR/Observation", `{"status":"final","valueInteger":1}`},
		{"P1", "gen-a", "Observation", "o-final-2", "FHIR/Observation", `{"status":"final","valueInteger":2}`},
		{"P1", "gen-a", "Observation", "o-amended", "FHIR/Observation", `{"status":"amended","valueInteger":3}`},
		{"P2", "gen-a", "Observation", "o-decoy", "FHIR/Observation", `{"status":"decoy","valueInteger":4}`},
	})
	engine := s.engine(t, recorder)
	bindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "gen-a", AuthResourcePaths: []string{"FHIR/Observation"}}
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec06ConstructionBundle(), bindings)
	if err != nil {
		t.Fatalf("CompileResolvedBundle (construction): %s", s.scrub(err))
	}
	result, err := engine.ScanCategories(s.ctx, resolved, dataframeexecution.CategoryScanRequest{
		Output: "trace", StageID: recipe.ConstructionSourceProjectionID,
		ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 8,
	})
	if err != nil {
		t.Fatalf("stage scan: %s", s.scrub(err))
	}
	want := []dataframeexecution.CategoryValue{{Present: true, Value: "amended"}, {Present: true, Value: "final"}}
	if !reflect.DeepEqual(result.Values, want) {
		t.Fatalf("stage values = %#v, want %#v", result.Values, want)
	}
	if !result.Complete || result.Overflow {
		t.Fatalf("stage status = Complete:%v Overflow:%v", result.Complete, result.Overflow)
	}
	if result.Proof.Version != 2 || result.Proof.StageID != recipe.ConstructionSourceProjectionID ||
		result.Proof.ColumnID != "status_id" || result.Proof.ValueColumnID != "amount_id" {
		t.Fatalf("stage proof not bound: %#v", result.Proof)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
	if recorder.calls[0].tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("tag = %q, want POSTGRESQL", recorder.calls[0].tag)
	}
}

func TestPostgresCategoryRefusalsZeroTouchLivePG18(t *testing.T) {
	s := newExec06csService(t)
	recorder := &exec06csRecorder{s: s}
	s.migrate(t)
	s.seed(t, []exec04pvFixtureRow{
		{"P1", "gen-a", "Observation", "o-final", "FHIR/Observation", `{"status":"final","valueInteger":1}`},
	})
	engine := s.engine(t, recorder)
	bindings := recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "gen-a", AuthResourcePaths: []string{"FHIR/Observation"}}
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec06ConstructionBundle(), bindings)
	if err != nil {
		t.Fatalf("CompileResolvedBundle (construction): %s", s.scrub(err))
	}
	cases := []struct {
		name    string
		request dataframeexecution.CategoryScanRequest
		check   func(*testing.T, error)
	}{
		{"composed stage keeps its typed capability gap",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: "derive_total", ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 8},
			func(t *testing.T, err error) {
				if !errors.Is(err, pgrender.ErrUnsupportedPostgresPlan) {
					t.Fatalf("composed-stage refusal = %v, want errors.Is(ErrUnsupportedPostgresPlan)", err)
				}
			}},
		{"unknown stage refuses at the compiler entry",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: "nope_stage", ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 8},
			func(t *testing.T, err error) {
				code, ok := compiler.CategoryScanRefusalCodeOf(err)
				if !ok || code != compiler.CategoryScanStageUnknown {
					t.Fatalf("unknown-stage refusal = %v, want typed CATEGORY_STAGE_UNKNOWN", err)
				}
			}},
		{"unknown stage column refuses at the compiler entry",
			dataframeexecution.CategoryScanRequest{Output: "trace", StageID: recipe.ConstructionSourceProjectionID, ColumnID: "nope_id", ValueColumnID: "amount_id", MaxValues: 8},
			func(t *testing.T, err error) {
				code, ok := compiler.CategoryScanRefusalCodeOf(err)
				if !ok || code != compiler.CategoryScanColumnUnknown {
					t.Fatalf("unknown-column refusal = %v, want typed CATEGORY_COLUMN_UNKNOWN", err)
				}
			}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			before := recorder.counts()
			result, err := engine.ScanCategories(s.ctx, resolved, test.request)
			if err == nil {
				t.Fatalf("refusal expected, got result %#v", result)
			}
			test.check(t, err)
			if result.Complete || result.Overflow || result.ConclusiveMissing || len(result.Values) != 0 {
				t.Fatalf("refusal softened into a result page: %#v", result)
			}
			if recorder.counts() != before {
				t.Fatalf("refusal executed %d statements, want zero before the port", recorder.counts()-before)
			}
		})
	}
	result, err := engine.ScanCategories(s.ctx, resolved, dataframeexecution.CategoryScanRequest{
		Output: "trace", StageID: recipe.ConstructionSourceProjectionID,
		ColumnID: "status_id", ValueColumnID: "amount_id", MaxValues: 8,
	})
	if err != nil {
		t.Fatalf("ALIVE control stage scan: %s", s.scrub(err))
	}
	if !result.Complete || len(result.Values) != 1 || result.Values[0] != (dataframeexecution.CategoryValue{Present: true, Value: "final"}) {
		t.Fatalf("ALIVE control result = %#v", result)
	}
}

func TestZZPostgresCategoryZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 category scan acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scrub, _ := exec04pvScrubberFor(raw)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec06csRegistrySnapshot()
	for db := range owned {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Fatalf("audit %s: %s", db, scrub(err))
		}
		if count != 0 {
			t.Errorf("owned fixture database %s survived", db)
		}
	}
	baseline := map[string]bool{}
	for _, name := range exec06csBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec06\\_cs\\_%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("audit sweep: %s", scrub(err))
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("audit scan: %s", scrub(err))
		}
		if !baseline[name] {
			t.Errorf("unregistered survivor under owned prefix: %s", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %s", scrub(err))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("audit timed out")
	}
}
