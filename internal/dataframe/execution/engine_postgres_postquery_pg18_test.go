package execution_test

// pg-exec-03 live acceptance battery: Loom post-query values and named-UUID
// semantics preserved on PostgreSQL rows (check-2: go test
// ./internal/dataframe/execution -run PostgresPostQuery under
// LOOM_TEST_POSTGRES_URL against the isolated PostgreSQL 18 service; at the
// branch point this selector matched zero tests — that RED is the
// vacuous-suite guard this battery closes).
//
// Every leg is production-compiled end to end: an authored recipe.Bundle
// goes through the real Engine.CompileResolvedBundle (semantic build/resolve
// and lower) and the selected PostgreSQL compile target renders the
// parent-tagged command through the canonical command.NewPostgreSQLCommand
// path; execution streams it onto the real server via the production public
// facade postgres.NewSourceRows. Fixtures are stored through the production
// resource repository (control facade) in a schema installed by the
// production migration runner. No leg hand-builds a lower.CompiledRecipeOutput.
//
// The route these legs attest: the PostgreSQL renderer never computes UUIDs
// in SQL (render/postgres/calls.go registers uuid3/uuid5 as capability
// gaps), so Loom post-query markers ride stored payload values at
// schema-validated selector paths, arrive as the json.RawMessage the adapter
// decodes for jsonb columns, and are materialized by the shared Go
// materializer (internal/dataframe/execution/postquery.go). Witness literals
// match named_uuid_test.go and postquery_test.go byte-for-byte; the offline
// cross-target cell (TestMaterializePostQueryCrossTargetJSONNullEquivalence)
// proves the Arango and ClickHouse row shapes converge to the same public
// values, and TestPostgresPostQueryCrossTargetJSONNullConvergence below
// measures the PostgreSQL end-to-end leg with the same witnesses.
//
// Non-vacuity discipline (pg-exec-04 pattern): every refusal has a paired
// ALIVE control that proceeds on the same fixture; statement counts are
// taken at the port, never by timing; comparison viability is proven by the
// mutation-control leg. The full Arango/ClickHouse landmine set is armed on
// every leg.
//
// Fixture databases and schemas are uniquely owned as verify_exec03v1_* and
// dropped by the test that created them; the TestZZ audit is
// ownership-scoped to this file's own prefix, and the pre-snapshot baseline
// is captured before this process creates anything.

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
	"strings"
	"sync"
	"testing"
	"time"

	postgres "github.com/calypr/loom/internal/backends/postgres"
	storepg "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec03pqPrefix  = "verify_exec03v1_"
	exec03pqNameFmt = exec03pqPrefix + "%d_%s"
)

var exec03pqNamePattern = regexp.MustCompile(`^verify_exec03v1_[0-9]+_[0-9a-f]+$`)

var (
	exec03pqOwnedMu     sync.Mutex
	exec03pqOwnedByName = map[string]string{}
)

func exec03pqRegister(db, schema string) {
	exec03pqOwnedMu.Lock()
	defer exec03pqOwnedMu.Unlock()
	exec03pqOwnedByName[db] = schema
}

func exec03pqRegistrySnapshot() map[string]string {
	exec03pqOwnedMu.Lock()
	defer exec03pqOwnedMu.Unlock()
	out := make(map[string]string, len(exec03pqOwnedByName))
	for db, schema := range exec03pqOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec03pqBaselineOnce  sync.Once
	exec03pqBaselineNames []string
	exec03pqBaselineErr   error
)

func exec03pqCaptureBaseline(raw string) {
	exec03pqBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec03pqBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec03v1\\_%' ESCAPE '\\'")
		if err != nil {
			exec03pqBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec03pqBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec03pqBaselineNames = append(exec03pqBaselineNames, name)
		}
	})
}

func exec03pqRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// exec03pqScrubberFor mirrors the pg-exec-04 credential scrubber: no fatal
// path may echo any credential-bearing form of the raw service URL.
func exec03pqScrubberFor(raw string) (func(any) string, func(string)) {
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

// TestExec03pqScrubberHidesCredentialForms pins the scrubber contract
// offline (never contacts a service).
func TestExec03pqScrubberHidesCredentialForms(t *testing.T) {
	raw := "postgres://verify_user:S3cr3t-Pw@db.internal:5432/loomsvc?sslmode=require"
	scrub, addSecret := exec03pqScrubberFor(raw)
	dsn := "postgres://verify_user:S3cr3t-Pw@db.internal:5432/verify_exec03v1_it"
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

type exec03pqService struct {
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

func newExec03pqService(t *testing.T) *exec03pqService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 post-query acceptance tests")
	}
	scrub, addSecret := exec03pqScrubberFor(raw)
	exec03pqCaptureBaseline(raw)
	if exec03pqBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec03pqBaselineErr))
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

	dbName := fmt.Sprintf(exec03pqNameFmt, time.Now().UnixNano(), exec03pqRandomHex(t))
	if !exec03pqNamePattern.MatchString(dbName) {
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
	exec03pqRegister(dbName, "")
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

	schema := fmt.Sprintf(exec03pqNameFmt, time.Now().UnixNano(), exec03pqRandomHex(t))
	if !exec03pqNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec03pqRegister(dbName, schema)
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
	return &exec03pqService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

func (s *exec03pqService) migrate(t *testing.T) {
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

type exec03pqFixtureRow struct {
	project, generation, resourceType, logicalID, authPath, payload string
}

// Post-query marker wires. These are byte-identical to the offline pins in
// postquery_test.go / named_uuid_test.go; the verifier re-derives the two
// accepted UUID literals independently (github.com/google/uuid).
const (
	exec03pqDNSNamespace = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"

	exec03pqUUID5Wire    = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03pqUUID3Wire    = `{"__loom_exact_uuid_operation":"uuid3","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.widgets.com"]}`
	exec03pqUUID5Literal = "21f7f8de-8051-5b89-8680-0195ef798b6a"
	exec03pqUUID3Literal = "3d813cbb-47fb-32ba-91df-831e1593ac29"

	// Textual namespace derived through MD5 over DNS, name "widgets".
	exec03pqTextNSWire    = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["www.widgets.com","widgets"]}`
	exec03pqTextNSLiteral = "c0f58def-a15e-5bfd-928f-ec80a4aa1516"

	// Multi-name concatenation under the parsed DNS namespace.
	exec03pqConcatMarkerWire = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8","www.","widgets",".com"]}`

	// Nil argument propagates null.
	exec03pqNilArgWire = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":["6ba7b810-9dad-11d1-80b4-00c04fd430c8",null]}`

	// Nested markers: canonical_json over an exact-UUID marker, and a
	// post-query concat call over a nested marker plus scalars.
	exec03pqNestedCanonicalWire = `{"__loom_postquery_call":"canonical_json","__loom_postquery_args":[` + exec03pqUUID5Wire + `]}`
	exec03pqNestedCanonicalWant = `"` + exec03pqUUID5Literal + `"`
	exec03pqNestedConcatWire    = `{"__loom_postquery_call":"concat","__loom_postquery_args":["x",` + exec03pqUUID3Wire + `,null,42]}`
	exec03pqNestedConcatWant    = "x" + exec03pqUUID3Literal + "42"

	// Malformed marker SHAPE inside valid JSON (the server accepts it; the
	// materializer must refuse before publishing the row).
	exec03pqMalformedMarkerWire = `{"__loom_exact_uuid_operation":"uuid5","__loom_exact_uuid_args":"nope"}`
)

// exec03pqAcceptanceFixture is the committed fixture set. Scopes:
//   - P1/gen-a (marker preview): marker-ada < marker-alan < marker-nested
//     in the compiled (logical_id, resource_seq) window order.
//   - P2/gen-b (malformed middle): bad-a, bad-b (malformed marker), bad-c.
//   - P3/gen-c (nulls and parity): c-01-null, c-02-textns, c-03-concatns,
//     c-04-nilarg.
//   - P4/gen-s (full stream): s-1-uuid3, s-2-sibling.
//   - Intruders under other projects/generations prove scoping.
func exec03pqAcceptanceFixture() []exec03pqFixtureRow {
	return []exec03pqFixtureRow{
		{"P1", "gen-a", "Patient", "marker-ada", "FHIR/Patient", `{"gender":` + exec03pqUUID5Wire + `,"birthDate":"1815-12-10","meta":{"tag":[` + exec03pqUUID3Wire + `,null,1.5]}}`},
		{"P1", "gen-a", "Patient", "marker-alan", "FHIR/Patient", `{"gender":"female","birthDate":"1912-06-23","meta":{"source":"plain"}}`},
		{"P1", "gen-a", "Patient", "marker-nested", "FHIR/Patient", `{"gender":` + exec03pqNestedCanonicalWire + `,"birthDate":"1906-12-09","meta":{"k":` + exec03pqNestedConcatWire + `}}`},

		{"P2", "gen-b", "Patient", "bad-a", "FHIR/Patient", `{"gender":"alpha","birthDate":"2000-01-01","meta":{"ok":true}}`},
		{"P2", "gen-b", "Patient", "bad-b", "FHIR/Patient", `{"gender":` + exec03pqMalformedMarkerWire + `,"birthDate":"2000-01-02","meta":{"ok":true}}`},
		{"P2", "gen-b", "Patient", "bad-c", "FHIR/Patient", `{"gender":"gamma","birthDate":"2000-01-03","meta":{"ok":true}}`},

		{"P3", "gen-c", "Patient", "c-01-null", "FHIR/Patient", `{"gender":null,"birthDate":"1900-01-01","meta":{"a":null}}`},
		{"P3", "gen-c", "Patient", "c-02-textns", "FHIR/Patient", `{"gender":` + exec03pqTextNSWire + `,"birthDate":"1900-01-02","meta":{"ok":true}}`},
		{"P3", "gen-c", "Patient", "c-03-concatns", "FHIR/Patient", `{"gender":` + exec03pqConcatMarkerWire + `,"birthDate":"1900-01-03","meta":{"ok":true}}`},
		{"P3", "gen-c", "Patient", "c-04-nilarg", "FHIR/Patient", `{"gender":` + exec03pqNilArgWire + `,"birthDate":"1900-01-04","meta":{"ok":true}}`},

		{"P4", "gen-s", "Patient", "s-1-uuid3", "FHIR/Patient", `{"id":"s-1","gender":` + exec03pqUUID3Wire + `,"birthDate":"1950-01-01"}`},
		{"P4", "gen-s", "Patient", "s-2-sibling", "FHIR/Patient", `{"id":"s-2","gender":"male","birthDate":"1950-01-02"}`},

		{"P1", "gen-x", "Patient", "intruder-generation", "FHIR/Patient", `{"gender":"intruder","birthDate":"1990-01-01","meta":{"ok":true}}`},
		{"P-other", "gen-a", "Patient", "intruder-project", "FHIR/Patient", `{"gender":"intruder","birthDate":"1991-02-02","meta":{"ok":true}}`},
	}
}

func (s *exec03pqService) seed(t *testing.T, rows []exec03pqFixtureRow) {
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

func (s *exec03pqService) countResources(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM "`+s.schema+`"."fhir_resource"`).Scan(&count); err != nil {
		s.fail("count fixture rows", err)
	}
	return count
}

// exec03pqCall records one statement through the single source row port.
type exec03pqCall struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec03pqRecorder struct {
	s     *exec03pqService
	calls []exec03pqCall
}

func (r *exec03pqRecorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.calls = append(r.calls, exec03pqCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	return r.s.executor(ctx, request, visit)
}

func (r *exec03pqRecorder) counts() int { return len(r.calls) }

type exec03pqInvalidRegistry struct{}

func (exec03pqInvalidRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the post-query battery compiles authored bundles only")
}

func (exec03pqInvalidRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the post-query battery compiles authored bundles only")
}

// exec03pqForbidArango arms every AQL/Arango/ClickHouse callback as an
// error landmine: a PostgreSQL-served plan that touches any of them fails.
func exec03pqForbidArango() dataframeexecution.Config {
	return dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      exec03pqInvalidRegistry{},
		SourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL QueryRows must never run for a PostgreSQL-served plan")
		},
		PreviewSourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
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

func (s *exec03pqService) engine(t *testing.T, recorder *exec03pqRecorder, target compiler.Target) *dataframeexecution.Engine {
	t.Helper()
	config := exec03pqForbidArango()
	config.CompileTarget = target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec03-pq-scope" }
	config.SourceRows = recorder.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

// exec03pqMarkerBundle is the authored recipe the preview legs compile for
// real: three fields with schema-validated selectors over Patient (meta is a non-repeated object slot; the identifier array slot stays a registered render gap). No
// dynamic columns (the PostgreSQL renderer keeps dynamic-column lowering as
// a typed capability gap — see the refusal leg).
func exec03pqMarkerBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec03_postquery",
		TranslationVersion:  "exec03",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{
				{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
				{Name: "birthDate", ColumnID: "birthDate", Expr: recipe.Expression{Select: "root.birthDate"}},
				{Name: "meta", ColumnID: "meta", Expr: recipe.Expression{Select: "root.meta"}},
			},
		}},
	}
}

// exec03pqStreamBundle carries a declared Identity so the compiled RETURN
// emits the hidden __loom_row_id projection; OutputStream.Stream (the
// full-stream and workspace-capture route) then satisfies its stable row
// identity contract exactly as production callers expect.
func exec03pqStreamBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec03_postquery_stream",
		TranslationVersion:  "exec03",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Identity: &recipe.Identity{Name: "rowId", Expr: recipe.Expression{Select: "root.id"}},
			Fields: []recipe.Field{
				{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
				{Name: "birthDate", ColumnID: "birthDate", Expr: recipe.Expression{Select: "root.birthDate"}},
			},
		}},
	}
}

func exec03pqBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{"FHIR/Patient"},
	}
}

func exec03pqCanonicalRows(rows []map[string]any) []string {
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

// chainText renders the full unwrap chain so wrapped refusals can still be
// asserted on the specifics they name.
func chainText(err error) string {
	parts := []string{}
	for err != nil {
		parts = append(parts, err.Error())
		un, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = un.Unwrap()
	}
	return strings.Join(parts, "; ")
}

func exec03pqEqualStrings(got, want []string) bool {
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

// runPreview collects one production-compiled preview and returns the
// collected rows, the summary, the recorder, and the error.
func (s *exec03pqService) runPreview(t *testing.T, project, generation string, limit int) ([]map[string]any, dataframeexecution.PreviewSummary, *exec03pqRecorder, error) {
	t.Helper()
	recorder := &exec03pqRecorder{s: s}
	engine := s.engine(t, recorder, s.target)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec03pqMarkerBundle(), exec03pqBindings(project, generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	var rows []map[string]any
	summary, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: limit}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	return rows, summary, recorder, err
}

// TestPostgresPostQueryPreviewComputesNamedUUID5 is acceptance 1: Preview
// computes UUID5(DNS namespace, www.widgets.com) as the pinned literal on
// a real PostgreSQL row, with the ordered-argument marker array
// materialized inside the meta object (UUID3 literal, null hole, number)
// and the non-marker sibling row (ALIVE control) published unchanged.
func TestPostgresPostQueryPreviewComputesNamedUUID5(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	rows, summary, recorder, err := s.runPreview(t, "P1", "gen-a", 5)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if summary.RowCount != 3 || !summary.Complete || summary.Truncated {
		t.Fatalf("summary %+v, want three complete rows", summary)
	}
	if got, ok := rows[0]["gender"].(string); !ok || got != exec03pqUUID5Literal {
		t.Fatalf("preview gender = %#v (Go %T), want the UUID5 literal %s", rows[0]["gender"], rows[0]["gender"], exec03pqUUID5Literal)
	}
	encodedMeta, err := json.Marshal(rows[0]["meta"])
	if err != nil {
		t.Fatalf("encode meta: %v", err)
	}
	if want := `{"tag":["` + exec03pqUUID3Literal + `",null,1.5]}`; string(encodedMeta) != want {
		t.Fatalf("preview meta = %s, want %s", encodedMeta, want)
	}
	// ALIVE control: the non-marker sibling published byte-identically.
	if rows[1]["gender"] != "female" || rows[1]["birthDate"] != "1912-06-23" {
		t.Fatalf("ALIVE sibling row = %v, want the untouched stored values", rows[1])
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
	if recorder.calls[0].tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("statement tag = %q, want PostgreSQL", recorder.calls[0].tag)
	}
	if strings.Contains(recorder.calls[0].sql, "@") {
		t.Fatalf("statement carries AQL bind syntax: %s", recorder.calls[0].sql)
	}
}

// TestPostgresPostQueryStreamComputesNamedUUID3 is acceptance 2: the full
// stream route (Engine.Run -> OutputStream.Stream, the same materializer
// call the workspace-capture path shares) computes UUID3 on a real
// PostgreSQL row. The declared Identity makes the stream carry the
// production stable-row-identity contract.
func TestPostgresPostQueryStreamComputesNamedUUID3(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	recorder := &exec03pqRecorder{s: s}
	engine := s.engine(t, recorder, s.target)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec03pqStreamBundle(), exec03pqBindings("P4", "gen-s"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	outputs, err := engine.Run(s.ctx, resolved)
	if err != nil {
		t.Fatalf("Run: %s", s.scrub(err))
	}
	rows := outputs["patients"]
	if len(rows) != 2 {
		t.Fatalf("stream rows = %d, want both P4/gen-s rows: %v", len(rows), rows)
	}
	if got, ok := rows[0]["gender"].(string); !ok || got != exec03pqUUID3Literal {
		t.Fatalf("stream gender = %#v (Go %T), want the UUID3 literal %s", rows[0]["gender"], rows[0]["gender"], exec03pqUUID3Literal)
	}
	if rows[1]["gender"] != "male" {
		t.Fatalf("ALIVE sibling stream row = %v, want \"male\"", rows[1])
	}
	if id, ok := rows[0]["__loom_row_id"].(string); !ok || id == "" {
		t.Fatalf("stream row lost the production stable row identity: %#v", rows[0]["__loom_row_id"])
	}
	if recorder.counts() != 1 {
		t.Fatalf("port calls = %d, want one", recorder.counts())
	}
}

// TestPostgresPostQueryNamedUUIDSemanticsParity is acceptance 3: null
// argument propagates null; textual namespace normalization and
// multiple-name concatenation match existing Go behavior — measured
// end-to-end on real PostgreSQL rows.
func TestPostgresPostQueryNamedUUIDSemanticsParity(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	rows, _, _, err := s.runPreview(t, "P3", "gen-c", 5)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want the four P3/gen-c rows", len(rows))
	}
	// c-01-null: stored JSON null gender materializes to the untyped nil
	// public value (presence asserted, value nil).
	if value, present := rows[0]["gender"]; !present || value != nil {
		t.Fatalf("JSON null column public value = %#v (present %v), want untyped nil", value, present)
	}
	// c-02-textns: textual namespace derived through MD5 over DNS.
	if rows[1]["gender"] != exec03pqTextNSLiteral {
		t.Fatalf("textual namespace = %#v, want %s", rows[1]["gender"], exec03pqTextNSLiteral)
	}
	// c-03-concatns: multiple name arguments concatenate in order to the
	// single-name accepted UUID5 witness.
	if rows[2]["gender"] != exec03pqUUID5Literal {
		t.Fatalf("multi-name concat = %#v, want %s", rows[2]["gender"], exec03pqUUID5Literal)
	}
	// c-04-nilarg: a null argument propagates null, never "<nil>" text.
	if value, present := rows[3]["gender"]; !present || value != nil {
		t.Fatalf("nil-argument propagation = %#v (present %v), want untyped nil", value, present)
	}
	// ALIVE control inside the same scope: the sibling rows' birthDate
	// columns are the untouched stored strings.
	for i, want := range []string{"1900-01-01", "1900-01-02", "1900-01-03", "1900-01-04"} {
		if rows[i]["birthDate"] != want {
			t.Fatalf("row %d birthDate = %#v, want %s", i, rows[i]["birthDate"], want)
		}
	}
}

// TestPostgresPostQueryNestedMarkersMaterialize is acceptance 5: nested
// marker values materialize recursively — canonical_json over an exact
// UUID marker and concat over a nested UUID3 marker, including a marker
// nested inside the meta object — and non-marker sibling values are
// untouched.
func TestPostgresPostQueryNestedMarkersMaterialize(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	rows, _, _, err := s.runPreview(t, "P1", "gen-a", 5)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	nested := rows[2]
	if nested["gender"] != exec03pqNestedCanonicalWant {
		t.Fatalf("nested canonical_json = %#v, want %s", nested["gender"], exec03pqNestedCanonicalWant)
	}
	encodedMeta, err := json.Marshal(nested["meta"])
	if err != nil {
		t.Fatalf("encode meta: %v", err)
	}
	if want := `{"k":"` + exec03pqNestedConcatWant + `"}`; string(encodedMeta) != want {
		t.Fatalf("nested meta = %s, want %s", encodedMeta, want)
	}
	// ALIVE control: the plain sibling in the same preview.
	plainMeta, ok := rows[1]["meta"].(map[string]any)
	if !ok || plainMeta["source"] != "plain" {
		t.Fatalf("plain meta row = %v", rows[1]["meta"])
	}
}

// TestPostgresPostQueryMalformedMarkerFailsBeforeRow is acceptance 4: a
// malformed marker fails before publishing the affected row — the preview
// errors, only the preceding row was ever visited, the summary carries no
// complete marker — with the ALIVE rerun converging on the exact witness.
func TestPostgresPostQueryMalformedMarkerFailsBeforeRow(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	rows, summary, _, err := s.runPreview(t, "P2", "gen-b", 5)
	if err == nil {
		t.Fatalf("malformed marker preview returned nil error with rows %v", rows)
	}
	if len(rows) != 1 || rows[0]["gender"] != "alpha" {
		t.Fatalf("visited rows before the malformed row = %v, want exactly [bad-a]", rows)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("failed preview summary Complete=%v Truncated=%v, want both false", summary.Complete, summary.Truncated)
	}
	if summary.RowCount != 1 {
		t.Fatalf("failed preview RowCount = %d, want the one row published before the failure", summary.RowCount)
	}
	// ALIVE control: the same engine kind on the well-formed scope
	// converges on the exact witness.
	good, goodSummary, _, err := s.runPreview(t, "P1", "gen-a", 5)
	if err != nil {
		t.Fatalf("ALIVE rerun: %s", s.scrub(err))
	}
	if !goodSummary.Complete || len(good) != 3 {
		t.Fatalf("ALIVE rerun summary = %+v", goodSummary)
	}
	if got := good[0]["gender"]; got != exec03pqUUID5Literal {
		t.Fatalf("ALIVE rerun first row gender = %#v, want %s", got, exec03pqUUID5Literal)
	}
}

// TestPostgresPostQueryThreeStateValues is acceptance 3's null contract on
// live rows: SQL NULL (missing stored key extracted by #>) and the JSON
// null scalar both publish as the untyped nil public value — the
// adapter-side three-state distinction (nil vs json.RawMessage("null")) is
// pinned in the rows-package battery (rows_postquery_postgres_test.go).
func TestPostgresPostQueryThreeStateValues(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())
	// c-01-null stores gender as JSON null; birthDate is stored, so the
	// JSON-null path is distinct from a missing key. A missing key reads
	// SQL NULL from #> and publishes nil identically.
	recorder := &exec03pqRecorder{s: s}
	engine := s.engine(t, recorder, s.target)
	bundle := exec03pqMarkerBundle()
	resolved, err := engine.CompileResolvedBundle(s.ctx, bundle, exec03pqBindings("P3", "gen-c"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	var rows []map[string]any
	if _, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	nullRow := rows[0]
	if value, present := nullRow["gender"]; !present || value != nil {
		t.Fatalf("JSON null column public value = %#v (present %v), want untyped nil", value, present)
	}
	// meta {"a": null}: the null member stays nil (type identity, not
	// JSON-text equality).
	meta, ok := nullRow["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta public shape = %#v, want map[string]any", nullRow["meta"])
	}
	value, present := meta["a"]
	if !present || value != nil {
		t.Fatalf("meta null member = %#v (present %v), want untyped nil", value, present)
	}
	// SQL NULL leg: a payload without birthDate at all.
	sqlNullScope := []exec03pqFixtureRow{{"P5", "gen-n", "Patient", "n-1", "FHIR/Patient", `{"gender":"ok"}`}}
	s.seed(t, sqlNullScope)
	nullRows, _, _, err := s.runPreview(t, "P5", "gen-n", 5)
	if err != nil {
		t.Fatalf("sql-null preview: %s", s.scrub(err))
	}
	if len(nullRows) != 1 {
		t.Fatalf("sql-null preview rows = %v", nullRows)
	}
	if value, present := nullRows[0]["birthDate"]; !present || value != nil {
		t.Fatalf("SQL NULL column public value = %#v (present %v), want untyped nil", value, present)
	}
	if nullRows[0]["gender"] != "ok" {
		t.Fatalf("sql-null scope gender = %#v, want \"ok\"", nullRows[0]["gender"])
	}
}

// TestPostgresPostQueryCrossTargetJSONNullConvergence is the live
// PostgreSQL leg of the ratified cross-target cell: the same JSON-null
// payload publishes the identical public value measured end to end through
// the production adapter and PreviewOutput, matching the three-target
// offline cell TestMaterializePostQueryCrossTargetJSONNullEquivalence
// (PostgreSQL/Arango/ClickHouse row shapes → identical public values). The
// wires below are byte-identical to that cell's constants.
func TestPostgresPostQueryCrossTargetJSONNullConvergence(t *testing.T) {
	const (
		genderWire = `null`
		tagsWire   = `["a",null]`
	)
	s := newExec03pqService(t)
	s.migrate(t)
	fixture := []exec03pqFixtureRow{
		{"P6", "gen-x", "Patient", "x-1", "FHIR/Patient", `{"gender":` + genderWire + `,"birthDate":"1777-01-01","meta":{"v":` + tagsWire + `}}`},
		{"P6", "gen-x", "Patient", "x-2-alive", "FHIR/Patient", `{"gender":"female","birthDate":"1777-01-02","meta":{"a":null}}`},
	}
	s.seed(t, fixture)
	rows, summary, _, err := s.runPreview(t, "P6", "gen-x", 5)
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if !summary.Complete || len(rows) != 2 {
		t.Fatalf("summary %+v rows %v, want two complete rows", summary, rows)
	}
	// Same public value the offline Arango/ClickHouse legs produce for the
	// identical wires: untyped nil, Go string, []any{"a", nil}.
	if value, present := rows[0]["gender"]; !present || value != nil {
		t.Fatalf("PostgreSQL leg public value for JSON null = %#v (present %v), want untyped nil", value, present)
	}
	meta, ok := rows[0]["meta"].(map[string]any)
	if !ok {
		t.Fatalf("PostgreSQL leg meta = %#v, want map[string]any", rows[0]["meta"])
	}
	if items, ok := meta["v"].([]any); !ok || len(items) != 2 || items[0] != "a" || items[1] != nil {
		t.Fatalf("PostgreSQL leg meta.v public shape = %#v, want []any{\"a\", nil}", meta["v"])
	}
	if _, isString := rows[0]["birthDate"].(string); !isString {
		t.Fatalf("PostgreSQL leg birthDate has Go type %T, want string", rows[0]["birthDate"])
	}
	// ALIVE control: the non-null sibling converges to the string.
	if rows[1]["gender"] != "female" {
		t.Fatalf("ALIVE control gender = %#v, want \"female\"", rows[1]["gender"])
	}
}

// TestPostgresPostQueryDynamicColumnsRefuseAndMarkersNeedNoDynamicSurface
// pins the named gap (plan §5.3): dynamic-column lowering emits
// EXPRESSION_LET, which the v1 PostgreSQL render surface refuses with its
// typed capability-gap sentinel — so PostgreSQL-served streams never carry
// a non-empty DynamicChecks set today, and the drift behavior is attested
// offline through the production materializer on PG-shaped rows. The ALIVE
// control compiles the identical bundle for the Arango target.
func TestPostgresPostQueryDynamicColumnsRefuseAndMarkersNeedNoDynamicSurface(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec03_dynamic",
		TranslationVersion:  "exec03",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{
				{Name: "gender", ColumnID: "gender", Expr: recipe.Expression{Select: "root.gender"}},
			},
			DynamicColumns: []recipe.DynamicColumn{{
				Name: "identifier", Source: recipe.Expression{Select: "root.identifier[].value"},
				Columns: []string{"urn:hl7"},
			}},
		}},
	}
	s := newExec03pqService(t)
	s.migrate(t)
	recorder := &exec03pqRecorder{s: s}
	engine := s.engine(t, recorder, s.target)
	// The bundle compiles (semantic + lower build the DynamicChecks
	// metadata); the typed refusal fires where the physical plan renders
	// for the selected PostgreSQL target — inside stream construction,
	// before any statement reaches the port.
	resolved, err := engine.CompileResolvedBundle(s.ctx, bundle, exec03pqBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("the metadata half of dynamic lowering must still compile: %s", s.scrub(err))
	}
	_, err = engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("dynamic-columns plan rendered for the PostgreSQL target; the named gap disappeared without a plan update")
	}
	if !errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
		t.Fatalf("dynamic-columns refusal %v is not the typed ErrUnsupportedPostgresPlan capability gap", err)
	}
	if !strings.Contains(chainText(err), "EXPRESSION_LET") {
		t.Fatalf("dynamic-columns refusal %v does not name the EXPRESSION_LET capability gap", err)
	}
	if recorder.counts() != 0 {
		t.Fatalf("the refused plan touched the server %d time(s)", recorder.counts())
	}
	// ALIVE control: the identical bundle compiles for the Arango target.
	aqlRecorder := &exec03pqRecorder{s: s}
	aqlEngine := s.engine(t, aqlRecorder, compiler.ArangoTarget())
	if _, err := aqlEngine.CompileResolvedBundle(s.ctx, bundle, exec03pqBindings("P1", "gen-a")); err != nil {
		t.Fatalf("ALIVE control: the same bundle refused on the Arango target: %s", s.scrub(err))
	}
}

// TestPostgresPostQueryMutationControls proves the marker legs die under
// mutation: comparing the correct collection against flipped goldens must
// be detected by the same comparison the legs use, and an input flip must
// break the exact witness (batch law for production-compiled legs).
func TestPostgresPostQueryMutationControls(t *testing.T) {
	s := newExec03pqService(t)
	s.migrate(t)
	s.seed(t, exec03pqAcceptanceFixture())

	t.Run("witness flip is detected", func(t *testing.T) {
		rows, _, _, err := s.runPreview(t, "P1", "gen-a", 5)
		if err != nil {
			t.Fatalf("preview: %s", s.scrub(err))
		}
		got := exec03pqCanonicalRows(rows)
		flipped := exec03pqCanonicalRows([]map[string]any{{
			"gender": strings.ToUpper(exec03pqUUID5Literal), "birthDate": "1815-12-10",
			"meta": map[string]any{"tag": []any{exec03pqUUID3Literal, nil, 1.5}},
		}})
		if len(got) == 0 || got[0] == flipped[0] {
			t.Fatal("mutation control failed: leg accepted a case-flipped UUID witness")
		}
	})

	t.Run("fixture flip breaks the goldens", func(t *testing.T) {
		s2 := newExec03pqService(t)
		s2.migrate(t)
		mutated := exec03pqAcceptanceFixture()
		for index := range mutated {
			if mutated[index].logicalID == "marker-ada" {
				mutated[index].payload = strings.Replace(mutated[index].payload, exec03pqUUID5Wire, `"changed"`, 1)
			}
		}
		s2.seed(t, mutated)
		recorder := &exec03pqRecorder{s: s2}
		engine := s2.engine(t, recorder, s2.target)
		resolved, err := engine.CompileResolvedBundle(s2.ctx, exec03pqMarkerBundle(), exec03pqBindings("P1", "gen-a"))
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %s", s2.scrub(err))
		}
		var rows []map[string]any
		if _, err := engine.PreviewOutput(s2.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 5}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		}); err != nil {
			t.Fatalf("mutated preview: %s", s2.scrub(err))
		}
		if rows[0]["gender"] == exec03pqUUID5Literal {
			t.Fatalf("mutation control failed: leg accepted the UUID witness after the payload flip")
		}
		if rows[0]["gender"] != "changed" {
			t.Fatalf("mutated preview gender = %#v, want the mutated stored text", rows[0]["gender"])
		}
	})
}

// TestZZPostgresPostQueryZeroLeftoverFixtures is the ownership-scoped
// audit: every database this file verified into existence must be gone,
// and no unregistered survivor may remain under this file's own prefix.
// The LIKE pattern is read-only and own-prefix only; cleanup above drops
// only the exact minted names.
func TestZZPostgresPostQueryZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 post-query acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scrub, _ := exec03pqScrubberFor(raw)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec03pqRegistrySnapshot()
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
	for _, name := range exec03pqBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec03v1\\_%' ESCAPE '\\'")
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
