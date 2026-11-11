package execution_test

// pg-exec-04 live acceptance battery: bounded PostgreSQL previews through
// PreviewOutput (check-2: go test ./internal/dataframe/execution -run
// PostgresPreview under LOOM_TEST_POSTGRES_URL against the isolated
// PostgreSQL 18 service; at the branch point this selector matched zero
// tests — that RED is the vacuous-suite guard this battery closes).
//
// Every leg is production-compiled end to end: an authored recipe.Bundle
// (schema version, RootResourceType, RowGrain, Fields with schema-validated
// selectors) goes through the real Engine.CompileResolvedBundle —
// semantic.BuildRecipePlan/ResolveRecipePlan and
// lower.CompileResolvedRecipePlan — and the selected PostgreSQL compile
// target renders the parent-tagged command through the canonical
// command.NewPostgreSQLCommand path; execution then streams it onto the real
// server via the production internal/dataframe/postgres adapter. No leg
// hand-builds a lower.CompiledRecipeOutput. Fixtures are stored through the
// production resource repository in a schema installed by the production
// migration runner.
//
// Witnesses are literal values: exact per-leg row JSON, driver-decoded
// values, ordered typed statement arguments pinned at the port, and the
// Complete/Truncated/RowCount summary contract from the ticket acceptance.
// Non-vacuity discipline: every refusal has a paired control that proceeds
// on the same fixture (see the mutation-control leg); statement counts are
// taken at the port, never by timing; the deadline leg blocks inside the
// visitor so it is deterministic rather than racing the server. The full
// exec02ForbidArango landmine set is armed on every leg: a PostgreSQL-served
// preview that touches any Arango/ClickHouse control fails the test.
//
// Fixture databases and schemas are uniquely owned as verify_exec04_pv_* and
// dropped by the test that created them; the TestZZ audit is
// ownership-scoped to this file's own prefix.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	postgres "github.com/calypr/loom/internal/backends/postgres"
	storepg "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec04pvPrefix  = "verify_exec04_pv_"
	exec04pvNameFmt = exec04pvPrefix + "%d_%s"
)

var exec04pvNamePattern = regexp.MustCompile(`^verify_exec04_pv_[0-9]+_[0-9a-f]+$`)

var (
	exec04pvOwnedMu     sync.Mutex
	exec04pvOwnedByName = map[string]string{}
)

func exec04pvRegister(db, schema string) {
	exec04pvOwnedMu.Lock()
	defer exec04pvOwnedMu.Unlock()
	exec04pvOwnedByName[db] = schema
}

func exec04pvRegistrySnapshot() map[string]string {
	exec04pvOwnedMu.Lock()
	defer exec04pvOwnedMu.Unlock()
	out := make(map[string]string, len(exec04pvOwnedByName))
	for db, schema := range exec04pvOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec04pvBaselineOnce  sync.Once
	exec04pvBaselineNames []string
	exec04pvBaselineErr   error
)

func exec04pvCaptureBaseline(raw string) {
	exec04pvBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec04pvBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_pv\\_%' ESCAPE '\\'")
		if err != nil {
			exec04pvBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec04pvBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec04pvBaselineNames = append(exec04pvBaselineNames, name)
		}
	})
}

func exec04pvRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec04pvService struct {
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

// exec04pvScrubberFor returns an error formatter that replaces every
// credential-bearing form of the raw test service URL with <redacted>, so no
// fatal path echoes credentials into the test log: connection failures from
// pgxpool.New / pgx.ParseConfig on the raw URL can quote the full URL.
// addSecret registers further forms (the derived isolated-database DSN) for
// scrubbing.
func exec04pvScrubberFor(raw string) (func(any) string, func(string)) {
	secrets := []string{raw}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
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
	return scrub, func(s string) { secrets = append(secrets, s) }
}

// TestExec04pvScrubberHidesCredentialForms pins the scrubber contract for
// the credential-bearing URL forms: every verbatim occurrence of the raw
// URL, the userinfo, the bare password, and registered derived DSNs must be
// replaced, so no error path that echoes a pool or config failure can print
// a credential. Offline string check: never contacts a service.
func TestExec04pvScrubberHidesCredentialForms(t *testing.T) {
	raw := "postgres://verify_user:S3cr3t-Pw@db.internal:5432/loomsvc?sslmode=require"
	scrub, addSecret := exec04pvScrubberFor(raw)
	dsn := "postgres://verify_user:S3cr3t-Pw@db.internal:5432/verify_exec04_pg_it"
	addSecret(dsn)
	probe := fmt.Errorf("cannot parse `%s`: pq: auth failed for %s via %s", raw, "verify_user:S3cr3t-Pw", dsn)
	got := scrub(probe)
	for _, secret := range []string{raw, "verify_user:S3cr3t-Pw", "S3cr3t-Pw", dsn} {
		if strings.Contains(got, secret) {
			t.Fatalf("scrubbed text still contains credential form %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("scrubbed text lost the redaction marker: %s", got)
	}
}

func newExec04pvService(t *testing.T) *exec04pvService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 preview acceptance tests")
	}
	scrub, addSecret := exec04pvScrubberFor(raw)
	exec04pvCaptureBaseline(raw)
	if exec04pvBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec04pvBaselineErr))
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

	dbName := fmt.Sprintf(exec04pvNameFmt, time.Now().UnixNano(), exec04pvRandomHex(t))
	if !exec04pvNamePattern.MatchString(dbName) {
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
	exec04pvRegister(dbName, "")
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

	schema := fmt.Sprintf(exec04pvNameFmt, time.Now().UnixNano(), exec04pvRandomHex(t))
	if !exec04pvNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec04pvRegister(dbName, schema)
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
	return &exec04pvService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

// migrate installs the real pg-store-02 resource placement through the
// production migration runner.
func (s *exec04pvService) migrate(t *testing.T) {
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

type exec04pvFixtureRow struct {
	project, generation, resourceType, logicalID, authPath, payload string
}

// acceptanceFixture is the committed fixture set. The P1/gen-a scope holds
// exactly three patients ordered patient-ada, patient-alan, patient-grace
// under the compiled pg-sql-12 window (logical_id, resource_seq); the
// P2/gen-c scope holds exactly two (patient-two-a, patient-two-b); the two
// intruders exist only to prove a bounded preview never leaks another
// project or generation.
func exec04pvAcceptanceFixture() []exec04pvFixtureRow {
	return []exec04pvFixtureRow{
		{"P1", "gen-a", "Patient", "patient-ada", "FHIR/Patient", `{"gender":"female","birthDate":"1815-12-10"}`},
		{"P1", "gen-a", "Patient", "patient-alan", "FHIR/Patient", `{"gender":"male","birthDate":"1912-06-23"}`},
		{"P1", "gen-a", "Patient", "patient-grace", "FHIR/Patient", `{"gender":"female","birthDate":"1906-12-09"}`},
		{"P2", "gen-c", "Patient", "patient-two-a", "FHIR/Patient", `{"gender":"male","birthDate":"2000-01-01"}`},
		{"P2", "gen-c", "Patient", "patient-two-b", "FHIR/Patient", `{"gender":"female","birthDate":"2001-02-02"}`},
		{"P1", "gen-b", "Patient", "patient-intruder-generation", "FHIR/Patient", `{"gender":"other","birthDate":"1990-01-01"}`},
		{"intruder-project", "gen-a", "Patient", "patient-intruder-project", "FHIR/Patient", `{"gender":"unknown","birthDate":"1991-02-02"}`},
	}
}

func (s *exec04pvService) seed(t *testing.T, rows []exec04pvFixtureRow) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	for _, row := range rows {
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project:           row.project,
			DatasetGeneration: row.generation,
			ResourceType:      row.resourceType,
			LogicalID:         row.logicalID,
			AuthResourcePath:  row.authPath,
			Payload:           json.RawMessage(row.payload),
		}); err != nil {
			s.fail("insert fixture resource "+row.logicalID, err)
		}
	}
}

func (s *exec04pvService) countResources(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM "`+s.schema+`"."fhir_resource"`).Scan(&count); err != nil {
		s.fail("count fixture rows", err)
	}
	return count
}

// exec04pvCall records one statement through the single source row port.
type exec04pvCall struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

// exec04pvRecorder wraps the production adapter: it refuses any non-PostgreSQL
// tag before the driver (there is no fallback to arm) and records every
// statement, its ordered typed arguments, and its declared column contract.
type exec04pvRecorder struct {
	s     *exec04pvService
	calls []exec04pvCall
}

func (r *exec04pvRecorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.calls = append(r.calls, exec04pvCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	return r.s.executor(ctx, request, visit)
}

func (r *exec04pvRecorder) counts() int { return len(r.calls) }

// exec04pvInvalidRegistry refuses recipe loads: this battery compiles
// authored bundles only, mirroring the relocated exec-02 witness.
type exec04pvInvalidRegistry struct{}

func (exec04pvInvalidRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the preview battery compiles authored bundles only")
}

func (exec04pvInvalidRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the preview battery compiles authored bundles only")
}

// exec04pvForbidArango arms every AQL/Arango/ClickHouse callback as an
// error landmine (the arch-backend-06 relocation moved the shared
// exec02ForbidArango into the backends tree with its battery): a
// PostgreSQL-served plan that touches any of them fails.
func exec04pvForbidArango() dataframeexecution.Config {
	return dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      exec04pvInvalidRegistry{},
		SourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			cmd := request.Command
			_, _, dispatchErr := cmd.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL QueryRows must never run for a PostgreSQL-served plan")
		},
		PreviewSourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			cmd := request.Command
			_, _, dispatchErr := cmd.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL PreviewQueryRows must never run for a PostgreSQL-served plan")
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			return errors.New("preparePreviewIndex must never run for a PostgreSQL-served plan")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]dataframeexecution.PreviewCollectionIndex, error) {
			return nil, errors.New("previewCoveringIndexes must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			return "", errors.New("previewCollectionRevision must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (dataframeexecution.PreviewCollectionScanEvidence, error) {
			return dataframeexecution.PreviewCollectionScanEvidence{}, errors.New("previewCollectionScans must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			return 0, errors.New("previewCollectionCount must never run for a PostgreSQL-served plan")
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for a PostgreSQL-served plan")
		},
	}
}

// engine wires a PostgreSQL-selected engine with every Arango/ClickHouse
// control armed as a landmine and the recording wrapper around the real
// adapter as the single source row port.
func (s *exec04pvService) engine(t *testing.T, recorder *exec04pvRecorder, rootPageRows int) *dataframeexecution.Engine {
	t.Helper()
	config := exec04pvForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec04-pv-scope" }
	config.RootPageRows = rootPageRows
	config.SourceRows = recorder.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

// previewBundle is the authored recipe every leg compiles for real: two
// scalar fields with schema-validated selectors over Patient.
func exec04pvBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec04_preview",
		TranslationVersion:  "exec04",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{
				{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
				{Name: "birthDate", ColumnID: "birthDate", Expr: recipe.Expression{Select: "root.birthDate"}},
			},
		}},
	}
}

func exec04pvBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{"FHIR/Patient"},
	}
}

// canonicalRows renders each public preview row as its canonical JSON text
// (Go json.Marshal sorts map keys), and driver-decoded json.RawMessage and
// string forms of the same JSON value collapse to one witness.
func exec04pvCanonicalRows(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			out = append(out, "unencodable:"+err.Error())
			continue
		}
		out = append(out, string(encoded))
	}
	return out
}

func exec04pvRowKeys(rows []map[string]any) []string {
	seen := map[string]bool{}
	for _, row := range rows {
		keys := make([]string, 0, len(row))
		for key := range row {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		seen[strings.Join(keys, ",")] = true
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

const (
	exec04pvGoldAda   = `{"birthDate":"1815-12-10","gender":"female"}`
	exec04pvGoldAlan  = `{"birthDate":"1912-06-23","gender":"male"}`
	exec04pvGoldGrace = `{"birthDate":"1906-12-09","gender":"female"}`
	exec04pvGoldTwoA  = `{"birthDate":"2000-01-01","gender":"male"}`
	exec04pvGoldTwoB  = `{"birthDate":"2001-02-02","gender":"female"}`
)

// runPreview collects one production-compiled preview and returns the
// collected rows, the summary, and the recorder.
func (s *exec04pvService) runPreview(t *testing.T, rootPageRows int, project, generation string, limit int, mutateVisitor func(*[]map[string]any) func(map[string]any) error) ([]map[string]any, dataframeexecution.PreviewSummary, *exec04pvRecorder, error) {
	t.Helper()
	s.migrate(t)
	s.seed(t, exec04pvAcceptanceFixture())
	recorder := &exec04pvRecorder{s: s}
	engine := s.engine(t, recorder, rootPageRows)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec04pvBundle(), exec04pvBindings(project, generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	var rows []map[string]any
	visit := func(row map[string]any) error { rows = append(rows, row); return nil }
	if mutateVisitor != nil {
		visit = mutateVisitor(&rows)
	}
	summary, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: limit}, visit)
	return rows, summary, recorder, err
}

// TestPostgresPreviewTruncatedAtLimit is acceptance 1: three fixture rows
// with limit two return the exact first two rows, Complete=false,
// Truncated=true, with exactly one statement through the single port.
func TestPostgresPreviewTruncatedAtLimit(t *testing.T) {
	s := newExec04pvService(t)
	rows, summary, recorder, err := s.runPreview(t, 0, "P1", "gen-a", 2, nil)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if got := exec04pvCanonicalRows(rows); !equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan}) {
		t.Fatalf("bounded preview rows = %v, want exactly the first two by the compiled window", got)
	}
	if summary.Complete || !summary.Truncated || summary.RowCount != 2 {
		t.Fatalf("summary Complete=%v Truncated=%v RowCount=%d, want Complete=false Truncated=true RowCount=2", summary.Complete, summary.Truncated, summary.RowCount)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want exactly one bounded statement", recorder.counts())
	}
	if recorder.calls[0].tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("statement tag = %q, want PostgreSQL", recorder.calls[0].tag)
	}
}

// TestPostgresPreviewNaturalExhaustion is acceptance 2: the two-row P2/gen-c
// scope with limit three returns both rows, Complete=true, Truncated=false.
func TestPostgresPreviewNaturalExhaustion(t *testing.T) {
	s := newExec04pvService(t)
	rows, summary, recorder, err := s.runPreview(t, 0, "P2", "gen-c", 3, nil)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if got := exec04pvCanonicalRows(rows); !equalStrings(got, []string{exec04pvGoldTwoA, exec04pvGoldTwoB}) {
		t.Fatalf("preview rows = %v, want both P2/gen-c rows", got)
	}
	if !summary.Complete || summary.Truncated || summary.RowCount != 2 {
		t.Fatalf("summary Complete=%v Truncated=%v RowCount=%d, want Complete=true Truncated=false RowCount=2", summary.Complete, summary.Truncated, summary.RowCount)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
}

// TestPostgresPreviewEmptyScopeNoLeak is acceptance 3: a preview over a
// scope with no rows returns zero rows while the same database holds
// witnesses under other projects and generations, and the ordered typed
// statement arguments carry the exact empty scope.
func TestPostgresPreviewEmptyScopeNoLeak(t *testing.T) {
	s := newExec04pvService(t)
	rows, summary, recorder, err := s.runPreview(t, 0, "P-none", "gen-a", 5, nil)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if len(rows) != 0 || summary.RowCount != 0 {
		t.Fatalf("empty scope returned %d rows (summary %d), want zero", len(rows), summary.RowCount)
	}
	if !summary.Complete || summary.Truncated {
		t.Fatalf("summary Complete=%v Truncated=%v, want Complete=true Truncated=false", summary.Complete, summary.Truncated)
	}
	if total := s.countResources(t); total != 7 {
		t.Fatalf("fixture witness: fhir_resource holds %d rows, want 7 (the empty scope must be scope-empty, not database-empty)", total)
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
	if call.args[2].Type != command.PostgreSQLParameterJSONB {
		t.Fatalf("auth argument type = %q, want jsonb", call.args[2].Type)
	}
	var auth [][]string
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", call.args[2].Value)), &auth); err != nil {
		// The auth payload is a jsonb array of scope paths; accept either
		// nesting shape but require the exact path inside.
		var flat []string
		if err2 := json.Unmarshal([]byte(fmt.Sprintf("%s", call.args[2].Value)), &flat); err2 != nil {
			t.Fatalf("auth argument is not decodable jsonb (%v / %v): %s", err, err2, call.args[2].Value)
		}
		auth = [][]string{flat}
	}
	foundPath := false
	for _, group := range auth {
		for _, path := range group {
			if path == "FHIR/Patient" {
				foundPath = true
			}
		}
	}
	if !foundPath {
		t.Fatalf("auth argument %s does not carry the exact scope path FHIR/Patient", call.args[2].Value)
	}
	if call.args[3].Type != command.PostgreSQLParameterText || call.args[3].Value != "Patient" {
		t.Fatalf("root type argument = %+v, want text \"Patient\"", call.args[3])
	}
	if call.args[4].Type != command.PostgreSQLParameterBigint || call.args[4].Value != int64(5) {
		t.Fatalf("window argument = %+v, want bigint 5", call.args[4])
	}
}

// TestPostgresPreviewCancellationNeverComplete is acceptance 4a: cancelling
// the request mid-stream returns an error and never a false complete
// result, with the summary explicitly not carrying a completed marker.
func TestPostgresPreviewCancellationNeverComplete(t *testing.T) {
	s := newExec04pvService(t)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	s.migrate(t)
	s.seed(t, exec04pvAcceptanceFixture())
	recorder := &exec04pvRecorder{s: s}
	engine := s.engine(t, recorder, 0)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec04pvBundle(), exec04pvBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	var rows []map[string]any
	summary, err := engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(row map[string]any) error {
		rows = append(rows, row)
		cancel()
		return nil
	})
	if err == nil {
		t.Fatalf("cancelled preview returned nil error with %d rows, summary %+v", len(rows), summary)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("cancelled summary Complete=%v Truncated=%v, want both false (no false complete result)", summary.Complete, summary.Truncated)
	}
	if len(rows) == 0 {
		t.Fatalf("cancellation must fire after at least one delivered row for this to be a mid-stream control, got 0")
	}
	// ALIVE control on the same engine and fixture: an uncancelled rerun
	// converges to the exact bounded witness.
	var rerun []map[string]any
	var rerunSummary dataframeexecution.PreviewSummary
	var rerunErr error
	for attempt := 1; attempt <= 6; attempt++ {
		rerun = nil
		rerunSummary, rerunErr = engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 2}, func(row map[string]any) error {
			rerun = append(rerun, row)
			return nil
		})
		if rerunErr == nil {
			break
		}
	}
	if rerunErr != nil {
		t.Fatalf("alive-control rerun never converged: %s", s.scrub(rerunErr))
	}
	if got := exec04pvCanonicalRows(rerun); !equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan}) {
		t.Fatalf("alive-control rerun rows = %v, want the exact first two", got)
	}
	if rerunSummary.Complete || !rerunSummary.Truncated {
		t.Fatalf("alive-control rerun summary Complete=%v Truncated=%v, want the bounded result", rerunSummary.Complete, rerunSummary.Truncated)
	}
}

// TestPostgresPreviewDeadlineNeverComplete is acceptance 4b in its
// deterministic form: the visitor blocks past the request deadline so the
// deadline is guaranteed to be live mid-stream (the naive post-compile
// 1ms-deadline shape spuriously passed on fast local streams — methodology
// RED R2, replaced per the ratified plan). The run must return an error
// with no completed marker, then converge on rerun.
func TestPostgresPreviewDeadlineNeverComplete(t *testing.T) {
	s := newExec04pvService(t)
	s.migrate(t)
	s.seed(t, exec04pvAcceptanceFixture())
	recorder := &exec04pvRecorder{s: s}
	engine := s.engine(t, recorder, 0)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec04pvBundle(), exec04pvBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	ctx, cancel := context.WithTimeout(s.ctx, 200*time.Millisecond)
	defer cancel()
	summary, err := engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(map[string]any) error {
		time.Sleep(300 * time.Millisecond) // block past the deadline on the first row
		return nil
	})
	if err == nil {
		t.Fatalf("deadline preview returned nil error, summary %+v", summary)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("deadline summary Complete=%v Truncated=%v, want both false (never a false complete result)", summary.Complete, summary.Truncated)
	}
	var rerun []map[string]any
	var rerunErr error
	for attempt := 1; attempt <= 6; attempt++ {
		rerun = nil
		_, rerunErr = engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(row map[string]any) error {
			rerun = append(rerun, row)
			return nil
		})
		if rerunErr == nil {
			break
		}
	}
	if rerunErr != nil {
		t.Fatalf("alive-control rerun never converged: %s", s.scrub(rerunErr))
	}
	if got := exec04pvCanonicalRows(rerun); !equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan, exec04pvGoldGrace}) {
		t.Fatalf("alive-control rerun rows = %v, want all three P1/gen-a rows", got)
	}
}

// TestPostgresPreviewPublicColumnsCompiledOrder is acceptance 5: the public
// columns retain the compiled order and names, the summary column contract
// matches them, and internal projection columns never cross.
func TestPostgresPreviewPublicColumnsCompiledOrder(t *testing.T) {
	s := newExec04pvService(t)
	rows, summary, recorder, err := s.runPreview(t, 0, "P1", "gen-a", 5, nil)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if !equalStrings(summary.Columns, []string{"gender", "birthDate"}) {
		t.Fatalf("summary columns = %v, want the compiled public order [gender birthDate]", summary.Columns)
	}
	if got := exec04pvRowKeys(rows); !equalStrings(got, []string{"birthDate,gender"}) {
		t.Fatalf("public row key sets = %v, want exactly {gender, birthDate} (internal projections must not cross)", got)
	}
	if got := exec04pvCanonicalRows(rows); !equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan, exec04pvGoldGrace}) {
		t.Fatalf("rows = %v, want the three literal witnesses in compiled window order", got)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
	if strings.Contains(recorder.calls[0].sql, "@") {
		t.Fatalf("statement carries AQL bind syntax: %s", recorder.calls[0].sql)
	}
}

// TestPostgresPreviewRootPagePreviewPlanNeverCarries is the production half
// of acceptance 6: with Config.RootPageRows armed, a PostgreSQL-served
// preview still runs exactly one statement — the production compiler refuses
// root-key output paging for PostgreSQL, so no preview plan can carry a
// root-page command sequence, and the AQL page loop never attaches. The
// paired ALIVE control (the AQL engine does page) and the typed producer
// refusal live in the internal offline battery.
func TestPostgresPreviewRootPagePreviewPlanNeverCarries(t *testing.T) {
	s := newExec04pvService(t)
	rows, summary, recorder, err := s.runPreview(t, 25, "P1", "gen-a", 5, nil)
	if err != nil {
		t.Fatalf("preview with RootPageRows armed: %s", s.scrub(err))
	}
	if got := exec04pvCanonicalRows(rows); !equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan, exec04pvGoldGrace}) {
		t.Fatalf("rows = %v, want all three P1/gen-a rows", got)
	}
	if !summary.Complete || summary.Truncated {
		t.Fatalf("summary Complete=%v Truncated=%v, want natural exhaustion", summary.Complete, summary.Truncated)
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want exactly one — no root-key page sequence may attach to a PostgreSQL preview", recorder.counts())
	}
	for _, call := range recorder.calls {
		if !strings.Contains(call.sql, "LIMIT") {
			t.Fatalf("the single preview statement must be the bounded window statement: %s", call.sql)
		}
	}
}

// TestPostgresPreviewMutationControls proves the bounded-preview legs die
// under mutation (batch law: production-compiled legs must not survive
// input mutation). (a) witness mutation: comparing the correct collection
// against a flipped golden must be detected by the same comparison the legs
// use; (b) input mutation: one flipped fixture gender must break the exact
// goldens. Both arms have their alive side exercised by the legs above on
// the unmutated fixture.
func TestPostgresPreviewMutationControls(t *testing.T) {
	s := newExec04pvService(t)

	t.Run("witness flip is detected", func(t *testing.T) {
		rows, _, _, err := s.runPreview(t, 0, "P1", "gen-a", 2, nil)
		if err != nil {
			t.Fatalf("preview: %s", s.scrub(err))
		}
		got := exec04pvCanonicalRows(rows)
		flipped := []string{exec04pvGoldAda, `{"birthDate":"1912-06-23","gender":"female"}`}
		if equalStrings(got, flipped) {
			t.Fatalf("mutation control failed: leg accepted a flipped gender witness")
		}
		shifted := []string{exec04pvGoldAlan, exec04pvGoldGrace}
		if equalStrings(got, shifted) {
			t.Fatalf("mutation control failed: leg accepted a window-shifted row pair")
		}
	})

	t.Run("fixture flip breaks the goldens", func(t *testing.T) {
		s2 := newExec04pvService(t)
		s2.migrate(t)
		mutated := exec04pvAcceptanceFixture()
		for index := range mutated {
			if mutated[index].logicalID == "patient-ada" {
				mutated[index].payload = `{"gender":"other","birthDate":"1815-12-10"}`
			}
		}
		s2.seed(t, mutated)
		recorder := &exec04pvRecorder{s: s2}
		engine := s2.engine(t, recorder, 0)
		resolved, err := engine.CompileResolvedBundle(s2.ctx, exec04pvBundle(), exec04pvBindings("P1", "gen-a"))
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %s", s2.scrub(err))
		}
		var rows []map[string]any
		if _, err := engine.PreviewOutput(s2.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 2}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		}); err != nil {
			t.Fatalf("mutated preview: %s", s2.scrub(err))
		}
		got := exec04pvCanonicalRows(rows)
		if equalStrings(got, []string{exec04pvGoldAda, exec04pvGoldAlan}) {
			t.Fatalf("mutation control failed: leg accepted the unmutated goldens after an input flip (rows %v)", got)
		}
		if !equalStrings(got, []string{`{"birthDate":"1815-12-10","gender":"other"}`, exec04pvGoldAlan}) {
			t.Fatalf("mutated preview rows = %v, want the mutated first row and the untouched second", got)
		}
	})
}

// TestZZPostgresPreviewZeroLeftoverFixtures is the ownership-scoped audit:
// every database this file verified into existence must be gone, and no
// unregistered survivor may remain under this file's own prefix.
func TestZZPostgresPreviewZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 preview acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scrub, _ := exec04pvScrubberFor(raw)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec04pvRegistrySnapshot()
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
	for _, name := range exec04pvBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_pv\\_%' ESCAPE '\\'")
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
