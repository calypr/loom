package execution

// pg-exec-04 battery (internal lane): the production-route half of
// acceptance 6 — a PostgreSQL-served preview plan can never carry a
// root-page command sequence while the production compiler refuses
// root-key output paging for PostgreSQL — plus the conditional-form loop
// leg at the streamRaw boundary with parent-shaped hand-tagged steps and
// a production-compiled row contract (the ratified pg-exec-05/#123
// precedent). Named gap pg-exec-04-GAP-1: the PostgreSQL root-key paging
// producer is missing; enabling ticket pg-sql-27-root-key-output-paging
// owns it. Until that producer exists no production path can attach
// steps, and this file never hand-tags a step in production code.
//
// Live leg (check-2: go test ./internal/dataframe/execution -run
// PostgresPreview under LOOM_TEST_POSTGRES_URL): the row contract,
// RowIdentity, and command columns come from the real
// Engine.CompileResolvedBundle + selected PostgreSQL compile target; the
// key and row statements are parent-shaped commands frozen only through
// the canonical single funnel newExec05PageCommand (command.
// NewPostgreSQLCommand); fixtures are stored through the production
// repositories in a schema installed by the production migration runner;
// witnesses are literal per-page multisets, literal cursor progression,
// and literal jsonb key pages counted at the port, never by timing.
//
// Fixture databases and schemas are uniquely owned as verify_exec04_pg_*
// and dropped by the test that created them; the TestZZ audit is
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	postgresrender "github.com/calypr/loom/internal/dataframe/compiler/render/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/source"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec04pgPrefix  = "verify_exec04_pg_"
	exec04pgNameFmt = exec04pgPrefix + "%d_%s"
)

var exec04pgNamePattern = regexp.MustCompile(`^verify_exec04_pg_[0-9]+_[0-9a-f]+$`)

var (
	exec04pgOwnedMu     sync.Mutex
	exec04pgOwnedByName = map[string]string{}
)

func exec04pgRegister(db, schema string) {
	exec04pgOwnedMu.Lock()
	defer exec04pgOwnedMu.Unlock()
	exec04pgOwnedByName[db] = schema
}

func exec04pgRegistrySnapshot() map[string]string {
	exec04pgOwnedMu.Lock()
	defer exec04pgOwnedMu.Unlock()
	out := make(map[string]string, len(exec04pgOwnedByName))
	for db, schema := range exec04pgOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec04pgBaselineOnce  sync.Once
	exec04pgBaselineNames []string
	exec04pgBaselineErr   error
)

func exec04pgCaptureBaseline(raw string) {
	exec04pgBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec04pgBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_pg\\_%' ESCAPE '\\'")
		if err != nil {
			exec04pgBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec04pgBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec04pgBaselineNames = append(exec04pgBaselineNames, name)
		}
	})
}

func exec04pgURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 preview root-page acceptance tests")
	}
	return raw
}

func exec04pgRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec04pgRegistry struct{}

func (exec04pgRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the preview battery compiles authored bundles only")
}

func (exec04pgRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the preview battery compiles authored bundles only")
}

type exec04pgService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	dsn      string
	target   compiler.Target
	scrub    func(any) string
	fail     func(step string, err error)
	executor SourceRows
}

func newExec04pgService(t *testing.T) *exec04pgService {
	t.Helper()
	raw := exec04pgURL(t)
	exec04pgCaptureBaseline(raw)
	if exec04pgBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec04pgBaselineErr)
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

	dbName := fmt.Sprintf(exec04pgNameFmt, time.Now().UnixNano(), exec04pgRandomHex(t))
	if !exec04pgNamePattern.MatchString(dbName) {
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
	exec04pgRegister(dbName, "")
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
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(exec04pgNameFmt, time.Now().UnixNano(), exec04pgRandomHex(t))
	if !exec04pgNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec04pgRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	if exec05NewRowsSource == nil {
		t.Fatal("the live battery requires the production row adapter factory (external install)")
	}
	executor, err := exec05NewRowsSource(iso)
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
	return &exec04pgService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

func (s *exec04pgService) qualifiedTable(name string) string {
	return `"` + s.schema + `"."` + name + `"`
}

func (s *exec04pgService) migrate(t *testing.T) {
	t.Helper()
	client, err := storepg.Open(s.ctx, storepg.Config{DSN: s.dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		s.fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	defer client.Close()
	if _, err := storepg.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.fail("migrate owned schema", err)
	}
}

func (s *exec04pgService) insertResource(t *testing.T, logicalID, project, generation, payload string) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	if _, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      "Patient",
		LogicalID:         logicalID,
		AuthResourcePath:  "FHIR/Patient",
		Payload:           json.RawMessage(payload),
	}); err != nil {
		s.fail("insert fixture resource "+logicalID, err)
	}
}

// seedRoots stores five scoped roots p0001..p0005 with deterministic gender
// and birthDate payloads, plus two scope intruders that the bound
// project/generation parameters must never surface.
func (s *exec04pgService) seedRoots(t *testing.T) {
	t.Helper()
	s.migrate(t)
	roots := []struct{ id, gender, birth string }{
		{"p0001", "female", "2001-01-01"},
		{"p0002", "male", "2002-02-02"},
		{"p0003", "female", "2003-03-03"},
		{"p0004", "male", "2004-04-04"},
		{"p0005", "female", "2005-05-05"},
	}
	for _, root := range roots {
		s.insertResource(t, root.id, "P1", "gen-a", fmt.Sprintf(`{"gender":%q,"birthDate":%q}`, root.gender, root.birth))
	}
	s.insertResource(t, "p0900", "P2", "gen-a", `{"gender":"other","birthDate":"1990-01-01"}`)
	s.insertResource(t, "p0901", "P1", "gen-b", `{"gender":"unknown","birthDate":"1991-02-02"}`)
}

// exec04pgBundle is the authored recipe compiled for real by both lanes.
func exec04pgBundle() recipe.Bundle {
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

func exec04pgBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{"FHIR/Patient"},
	}
}

func exec04pgCanonicalRow(row map[string]any) string {
	encoded, err := json.Marshal(row)
	if err != nil {
		return "unencodable:" + err.Error()
	}
	return string(encoded)
}

// TestPostgresPreviewStreamNeverAttachesRootPages is the offline half of
// acceptance 6: with Config.RootPageRows armed, the production-compiled
// PostgreSQL preview stream carries no page shape of either backend, while
// the paired ALIVE control proves the AQL engine does page (so the PG
// inertness assertion can fail and is not a constant).
func TestPostgresPreviewStreamNeverAttachesRootPages(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("verify_exec04_pg_it")
	if err != nil {
		t.Fatalf("bind offline PostgreSQL placement: %v", err)
	}
	pgTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("offline PostgreSQL target: %v", err)
	}

	t.Run("PostgreSQL preview stream never attaches either page route", func(t *testing.T) {
		engine, err := New(Config{
			CompileTarget: pgTarget,
			Registry:      exec04pgRegistry{},
			RootPageRows:  25,
			SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				return errors.New("no statement may run in the attach-inertness control")
			},
		})
		if err != nil {
			t.Fatalf("engine: %v", err)
		}
		resolved, err := engine.CompileResolvedBundle(context.Background(), exec04pgBundle(), exec04pgBindings("P1", "gen-a"))
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		stream, _, err := engine.streamForOutput(context.Background(), resolved, "patients", 25)
		if err != nil {
			t.Fatalf("streamForOutput: %v", err)
		}
		if stream.page != nil || stream.pgRootPage != nil || stream.rootPageRows != 0 {
			t.Fatalf("PostgreSQL preview stream carries page fields: page=%v pgRootPage=%v rootPageRows=%d",
				stream.page, stream.pgRootPage, stream.rootPageRows)
		}
		declared := false
		for _, column := range stream.commandColumns {
			if column == "_key" {
				declared = true
			}
		}
		if !declared {
			t.Fatalf("compiled command columns %v do not carry the root identity column", stream.commandColumns)
		}
	})

	t.Run("alive control: the AQL engine does page", func(t *testing.T) {
		var requests []map[string]any
		engine, err := New(Config{
			CompileTarget: compiler.ArangoTarget(),
			Registry:      exec04pgRegistry{},
			RootPageRows:  1,
			SourceRows: func(_ context.Context, request RowsRequest, visit RowVisitor) error {
				_, binds, dispatchErr := request.Command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}
				requests = append(requests, binds)
				if after, isKeyQuery := binds[compiler.RootPageAfterKeyBind]; isKeyQuery {
					// One root exists: the first key query yields it; every
					// later key query is empty so the loop terminates.
					if cursor, _ := after.(string); cursor == "" {
						return visit(map[string]any{"_key": "root-1"})
					}
					return nil
				}
				return visit(map[string]any{"gender": "female", "birthDate": "2001-01-01"})
			},
		})
		if err != nil {
			t.Fatalf("engine: %v", err)
		}
		resolved, err := engine.CompileResolvedBundle(context.Background(), exec04pgBundle(), exec04pgBindings("P1", "gen-a"))
		if err != nil {
			t.Fatalf("CompileResolvedBundle: %v", err)
		}
		stream, _, err := engine.streamForOutput(context.Background(), resolved, "patients", 25)
		if err != nil {
			t.Fatalf("streamForOutput: %v", err)
		}
		if stream.page == nil {
			t.Fatalf("alive control failed: the AQL engine did not page with RootPageRows armed")
		}
		if err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("AQL paged streamRaw: %v", err)
		}
		sawKeyQuery := false
		for _, binds := range requests {
			if _, ok := binds[compiler.RootPageAfterKeyBind]; ok {
				sawKeyQuery = true
			}
		}
		if !sawKeyQuery {
			t.Fatalf("alive control failed: no root-key statement was dispatched through the AQL page loop")
		}
	})
}

// TestPostgresPreviewRootPageProducerRefusalProbe is the probe-attested
// unreachability witness for named gap pg-exec-04-GAP-1 (enabling ticket
// pg-sql-27-root-key-output-paging): the whole-plan compiler entry for PG
// page steps stays a typed refusal, so no preview plan can ever carry a
// root-page command sequence and the streamRaw loop is production-dark.
func TestPostgresPreviewRootPageProducerRefusalProbe(t *testing.T) {
	binding, err := ir.NewPostgresStorageBinding("verify_exec04_pg_it")
	if err != nil {
		t.Fatalf("bind offline PostgreSQL placement: %v", err)
	}
	pgTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("offline PostgreSQL target: %v", err)
	}
	engine, err := New(Config{
		CompileTarget: pgTarget,
		Registry:      exec04pgRegistry{},
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			return errors.New("no statement may run in the producer-refusal probe")
		},
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), exec04pgBundle(), exec04pgBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %v", err)
	}
	if len(resolved.Compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(resolved.Compiled.Outputs))
	}
	output := resolved.Compiled.Outputs[0]
	_, err = compiler.CompileRecipeOutputPage(pgTarget, output, exec04pgBindings("P1", "gen-a"), 2, ir.DefaultPhysicalOptimizationPolicy())
	if !errors.Is(err, postgresrender.ErrUnsupportedPostgresPlan) {
		t.Fatalf("PostgreSQL page producer error = %v, want typed ErrUnsupportedPostgresPlan (pg-exec-04-GAP-1 stays refused)", err)
	}
	if !strings.Contains(err.Error(), "root-key output paging is unsupported on the PostgreSQL compile target") {
		t.Fatalf("PostgreSQL page producer refusal lost its anchor message: %v", err)
	}
}

// exec04pgCall records one statement through the single source row port.
type exec04pgCall struct {
	kind  string
	sql   string
	args  []command.PostgreSQLParameter
	bound []string
}

type exec04pgPort struct {
	s         *exec04pgService
	calls     []exec04pgCall
	afterCall func(p *exec04pgPort, kind string, callIndex int)
}

func (p *exec04pgPort) port(ctx context.Context, request RowsRequest, visit RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	kind := "rows"
	if len(request.DeclaredColumns) == 1 && request.DeclaredColumns[0] == "root_key" {
		kind = "keys"
	}
	p.calls = append(p.calls, exec04pgCall{kind: kind, sql: sqlText, args: args, bound: append([]string(nil), request.DeclaredColumns...)})
	callIndex := len(p.calls)
	if err := p.s.executor(ctx, request, visit); err != nil {
		return err
	}
	if p.afterCall != nil {
		p.afterCall(p, kind, callIndex)
	}
	return nil
}

func (p *exec04pgPort) counts() (keys, rows int) {
	for _, call := range p.calls {
		if call.kind == "keys" {
			keys++
		} else {
			rows++
		}
	}
	return keys, rows
}

func (p *exec04pgPort) recordedKeyPages() []string {
	var pages []string
	for _, call := range p.calls {
		if call.kind == "rows" {
			pages = append(pages, string(call.args[4].Value.(json.RawMessage)))
		}
	}
	return pages
}

// keySQLTemplate is the parent-shaped key-discovery statement over the
// production fhir_resource placement: scoped, keyset-ordered by the root
// continuation identity, bounded by a parameterized LIMIT. Parameter
// positions: $1 project, $2 dataset generation, $3 resource type,
// $4 auth path, $5 cursor (text, engine-bound), $6 page size (bigint,
// engine-bound).
func (s *exec04pgService) keySQLTemplate() string {
	return "SELECT r.logical_id AS root_key FROM " + s.qualifiedTable("fhir_resource") +
		" r WHERE r.project = $1 AND r.dataset_generation = $2 AND r.resource_type = $3" +
		" AND r.auth_resource_path = $4 AND r.logical_id > $5" +
		" ORDER BY r.logical_id ASC LIMIT $6"
}

// rowsSQLTemplate is the parent-shaped row statement: the complete output
// for exactly the requested key page. The key page arrives as one jsonb
// array parameter; the returned columns carry the renderer's canonical
// positional aliases (renderReturn emits every projection as "AS c" plus
// its zero-based index, and the adapter accepts exactly those aliases
// against the declared contract, which keeps recipe-provided names out of
// the SQL text — quoted identity-case aliases like "birthDate" would be
// folded to lowercase and refused), and the ORDER BY is the pg-sql-12
// final-row window (logical_id, resource_seq). Parameter positions:
// $1 project, $2 dataset generation, $3 resource type, $4 auth path,
// $5 key page (jsonb, engine-bound).
func (s *exec04pgService) rowsSQLTemplate() string {
	return `SELECT r.logical_id AS c0, r.payload -> 'gender' AS c1, r.payload -> 'birthDate' AS c2` +
		" FROM " + s.qualifiedTable("fhir_resource") + " r" +
		" WHERE r.project = $1 AND r.dataset_generation = $2 AND r.resource_type = $3" +
		" AND r.auth_resource_path = $4" +
		" AND r.logical_id IN (SELECT k FROM jsonb_array_elements_text($5) AS k)" +
		" ORDER BY r.logical_id ASC, r.resource_seq ASC"
}

func (s *exec04pgService) keyArgs(cursor string, size int64) []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "FHIR/Patient"},
		{Type: command.PostgreSQLParameterText, Value: cursor},
		{Type: command.PostgreSQLParameterBigint, Value: size},
	}
}

func (s *exec04pgService) rowsArgs() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "FHIR/Patient"},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`[]`)},
	}
}

// newExec04pgSteps freezes the parent-shaped page pair through the single
// canonical funnel newExec05PageCommand; RowRootKeyColumn names the
// production-compiled identity column of the row contract.
func (s *exec04pgService) newExec04pgSteps(t *testing.T, pageSize int) *PostgreSQLRootPageSteps {
	t.Helper()
	return &PostgreSQLRootPageSteps{
		RootKeys:         newExec05PageCommand(t, s.keySQLTemplate(), s.keyArgs("", int64(pageSize))),
		Rows:             newExec05PageCommand(t, s.rowsSQLTemplate(), s.rowsArgs()),
		RootKeyColumn:    "root_key",
		RowRootKeyColumn: "_key",
		CursorArgIndex:   4,
		PageSizeArgIndex: 5,
		PageKeysArgIndex: 4,
		PageSize:         pageSize,
	}
}

// TestPostgresPreviewRootPageLoopProductionRowContract is the
// conditional-form half of acceptance 6: when a preview plan shape carries
// a root-page command sequence (here parent-shaped steps, since the
// production producer is refused-and-registered under pg-exec-04-GAP-1
// pending pg-sql-27-root-key-output-paging), the bounded loop returns
// compiler-defined stable rows at page boundaries with no duplicate
// identities: the row contract comes from the production-compiled stream,
// pages are discovered through the key statement, and every row is
// re-verified against its requested key page.
func TestPostgresPreviewRootPageLoopProductionRowContract(t *testing.T) {
	s := newExec04pgService(t)
	s.seedRoots(t)

	engine, err := New(Config{
		CompileTarget: s.target,
		Registry:      exec04pgRegistry{},
		SourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			if _, _, dispatchErr := request.Command.PostgreSQLParameters(); dispatchErr != nil {
				return dispatchErr
			}
			return s.executor(ctx, request, visit)
		},
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec04pgBundle(), exec04pgBindings("P1", "gen-a"))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	stream, query, err := engine.streamForOutput(s.ctx, resolved, "patients", 0)
	if err != nil {
		t.Fatalf("streamForOutput: %s", s.scrub(err))
	}
	// The row contract is the production-compiled one.
	if fmt.Sprint(stream.commandColumns) != fmt.Sprint(query.Columns) || len(query.Columns) == 0 {
		t.Fatalf("stream columns %v do not equal the compiled command columns %v", stream.commandColumns, query.Columns)
	}
	if stream.command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("compiled command tag = %q, want PostgreSQL", stream.command.Backend())
	}

	want := []string{
		`{"_key":"p0001","birthDate":"2001-01-01","gender":"female"}`,
		`{"_key":"p0002","birthDate":"2002-02-02","gender":"male"}`,
		`{"_key":"p0003","birthDate":"2003-03-03","gender":"female"}`,
		`{"_key":"p0004","birthDate":"2004-04-04","gender":"male"}`,
		`{"_key":"p0005","birthDate":"2005-05-05","gender":"female"}`,
	}

	t.Run("stable rows at page boundaries, no duplicate identities", func(t *testing.T) {
		port := &exec04pgPort{s: s}
		var visited []string
		var pageBoundaries []int
		port.afterCall = func(_ *exec04pgPort, kind string, _ int) {
			if kind == "rows" {
				pageBoundaries = append(pageBoundaries, len(visited))
			}
		}
		stream.pgRootPage = s.newExec04pgSteps(t, 2)
		stream.sourceRows = port.port
		if err := stream.streamRaw(s.ctx, func(row map[string]any) error {
			visited = append(visited, exec04pgCanonicalRow(row))
			return nil
		}); err != nil {
			s.fail("paged streamRaw", err)
		}
		if fmt.Sprint(visited) != fmt.Sprint(want) {
			t.Fatalf("paged visitation = %v, want the compiler-defined stable order across pages", visited)
		}
		keys, rows := port.counts()
		if keys != 3 || rows != 3 {
			t.Fatalf("port calls keys=%d rows=%d, want 3/3 (short final page ends without a fourth key query)", keys, rows)
		}
		var cursors []string
		for _, call := range port.calls {
			if call.kind == "keys" {
				cursors = append(cursors, call.args[4].Value.(string))
				if call.args[5].Value.(int64) != 2 {
					t.Fatalf("key-step page size = %v, want the fixed 2", call.args[5].Value)
				}
			}
		}
		if fmt.Sprint(cursors) != fmt.Sprint([]string{"", "p0002", "p0004"}) {
			t.Fatalf("literal cursor progression = %v, want [ p0002 p0004]", cursors)
		}
		keyPages := port.recordedKeyPages()
		if fmt.Sprint(keyPages) != fmt.Sprint([]string{`["p0001","p0002"]`, `["p0003","p0004"]`, `["p0005"]`}) {
			t.Fatalf("literal jsonb key pages = %v", keyPages)
		}
		seen := map[string]int{}
		start := 0
		for pageIndex, end := range pageBoundaries {
			var pageKeys []string
			if err := json.Unmarshal([]byte(keyPages[pageIndex]), &pageKeys); err != nil {
				t.Fatalf("decode page %d: %v", pageIndex, err)
			}
			requested := map[string]bool{}
			for _, key := range pageKeys {
				requested[key] = true
			}
			for _, encoded := range visited[start:end] {
				var row struct {
					Key string `json:"_key"`
				}
				if err := json.Unmarshal([]byte(encoded), &row); err != nil {
					t.Fatalf("decode visited row: %v", err)
				}
				if !requested[row.Key] {
					t.Fatalf("page %d row %v belongs to root %q outside the requested key page %s", pageIndex, encoded, row.Key, keyPages[pageIndex])
				}
				if earlier, duplicate := seen[row.Key]; duplicate {
					t.Fatalf("root identity %q appears on pages %d and %d: duplicate identities across pages", row.Key, earlier, pageIndex)
				}
				seen[row.Key] = pageIndex
			}
			start = end
		}
		for _, encoded := range visited {
			if strings.Contains(encoded, "p09") {
				t.Fatalf("scope intruder surfaced in paged output: %s", encoded)
			}
		}
	})

	t.Run("cancellation between key and row step never dispatches the row step", func(t *testing.T) {
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()
		port := &exec04pgPort{s: s}
		port.afterCall = func(p *exec04pgPort, kind string, callIndex int) {
			if kind == "keys" && callIndex == 1 {
				cancel()
			}
		}
		stream.pgRootPage = s.newExec04pgSteps(t, 2)
		stream.sourceRows = port.port
		err := stream.streamRaw(ctx, func(map[string]any) error { return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted loop error = %v, want context cancellation", err)
		}
		keys, rows := port.counts()
		if keys != 1 || rows != 0 {
			t.Fatalf("port calls keys=%d rows=%d, want 1/0 — the row step must never be dispatched after cancellation", keys, rows)
		}
	})

	t.Run("composition sabotage refuses with the sentinel, alive control proceeds", func(t *testing.T) {
		sabotaged := s.newExec04pgSteps(t, 2)
		sabotaged.RowRootKeyColumn = "not_declared"
		port := &exec04pgPort{s: s}
		stream.pgRootPage = sabotaged
		stream.sourceRows = port.port
		err := stream.streamRaw(s.ctx, func(map[string]any) error { return nil })
		if !errors.Is(err, ErrPostgreSQLRootPageSteps) {
			t.Fatalf("sabotaged steps error = %v, want ErrPostgreSQLRootPageSteps", err)
		}
		if len(port.calls) != 0 {
			t.Fatalf("sabotaged steps dispatched %d statements before refusing", len(port.calls))
		}
		// ALIVE control on the same fixture and stream: the untampered steps proceed.
		livePort := &exec04pgPort{s: s}
		stream.pgRootPage = s.newExec04pgSteps(t, 2)
		stream.sourceRows = livePort.port
		visited := 0
		if err := stream.streamRaw(s.ctx, func(map[string]any) error { visited++; return nil }); err != nil {
			s.fail("alive-control streamRaw", err)
		}
		if visited != len(want) {
			t.Fatalf("alive control visited %d rows, want %d", visited, len(want))
		}
	})
}

// TestZZPostgresPreviewRootPageZeroLeftoverFixtures is the ownership-scoped
// audit for this file's own prefix.
func TestZZPostgresPreviewRootPageZeroLeftoverFixtures(t *testing.T) {
	if strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL")) == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 preview root-page acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, exec04pgURL(t))
	if err != nil {
		t.Fatalf("connect for audit: %v", err)
	}
	defer admin.Close()
	for db := range exec04pgRegistrySnapshot() {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Fatalf("audit %s: %v", db, err)
		}
		if count != 0 {
			t.Errorf("owned fixture database %s survived", db)
		}
	}
	baseline := map[string]bool{}
	for _, name := range exec04pgBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_pg\\_%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("audit sweep: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		if !baseline[name] {
			t.Errorf("unregistered survivor under owned prefix: %s", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows: %v", err)
	}
}
