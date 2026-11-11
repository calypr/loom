package server

// pg-exec-04 D3 cell: today's server composition pins that the preview
// route's AQL source-row wiring fail-closes a PostgreSQL-tagged preview at
// the boundary — through the production configuredAQLSourceRows function
// that wires both row ports in server.go — instead of silently rendering
// or executing AQL for it. The PostgreSQL preview composition itself
// belongs to pg-runtime-server-composition; this cell pins that
// "belongs to another ticket" is an observed refusal, not an assertion.
//
// Live PostgreSQL 18 (check-1/2 under LOOM_TEST_POSTGRES_URL): the plan is
// production-compiled (Engine.CompileResolvedBundle + the selected
// PostgreSQL target) against a schema installed by the production
// migration runner, fixtures stored through the production repository.
// The refusal is non-vacuous through two paired alive controls on the
// same plan and fixture: the same compiled preview streams exact rows
// through the real PostgreSQL adapter, and the same server wiring
// function proceeds for an AQL-tagged preview.
//
// Fixture databases and schemas are uniquely owned as verify_exec04_srv_*
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/source"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec04srvPrefix  = "verify_exec04_srv_"
	exec04srvNameFmt = exec04srvPrefix + "%d_%s"
)

var exec04srvNamePattern = regexp.MustCompile(`^verify_exec04_srv_[0-9]+_[0-9a-f]+$`)

var (
	exec04srvOwnedMu     sync.Mutex
	exec04srvOwnedByName = map[string]string{}
)

func exec04srvRegister(db, schema string) {
	exec04srvOwnedMu.Lock()
	defer exec04srvOwnedMu.Unlock()
	exec04srvOwnedByName[db] = schema
}

func exec04srvRegistrySnapshot() map[string]string {
	exec04srvOwnedMu.Lock()
	defer exec04srvOwnedMu.Unlock()
	out := make(map[string]string, len(exec04srvOwnedByName))
	for db, schema := range exec04srvOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec04srvBaselineOnce  sync.Once
	exec04srvBaselineNames []string
	exec04srvBaselineErr   error
)

func exec04srvCaptureBaseline(raw string) {
	exec04srvBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec04srvBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_srv\\_%' ESCAPE '\\'")
		if err != nil {
			exec04srvBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec04srvBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec04srvBaselineNames = append(exec04srvBaselineNames, name)
		}
	})
}

type exec04srvService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	dsn      string
	target   compiler.Target
	scrub    func(any) string
	fail     func(step string, err error)
	executor *dataframepostgres.Executor
}

func newExec04srvService(t *testing.T) *exec04srvService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 server-preview wiring tests")
	}
	exec04srvCaptureBaseline(raw)
	if exec04srvBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec04srvBaselineErr)
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

	dbName := fmt.Sprintf(exec04srvNameFmt, time.Now().UnixNano(), exec04srvRandomHex(t))
	if !exec04srvNamePattern.MatchString(dbName) {
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
	exec04srvRegister(dbName, "")
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

	schema := fmt.Sprintf(exec04srvNameFmt, time.Now().UnixNano(), exec04srvRandomHex(t))
	if !exec04srvNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec04srvRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	executor, err := dataframepostgres.NewExecutor(iso)
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
	return &exec04srvService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

func exec04srvRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func (s *exec04srvService) migrateSeed(t *testing.T) {
	t.Helper()
	client, err := storepg.Open(s.ctx, storepg.Config{DSN: s.dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		s.fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	defer client.Close()
	if _, err := storepg.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.fail("migrate owned schema", err)
	}
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	for _, row := range []struct{ logical, payload string }{
		{"patient-ada", `{"gender":"female","birthDate":"1815-12-10"}`},
		{"patient-alan", `{"gender":"male","birthDate":"1912-06-23"}`},
	} {
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project: "P1", DatasetGeneration: "gen-a", ResourceType: "Patient",
			LogicalID: row.logical, AuthResourcePath: "FHIR/Patient", Payload: json.RawMessage(row.payload),
		}); err != nil {
			s.fail("insert fixture resource", err)
		}
	}
}

func exec04srvBundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec04_srv_preview",
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

func exec04srvBindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "P1", DatasetGeneration: "gen-a",
		AuthResourcePaths: []string{"FHIR/Patient"},
	}
}

func (s *exec04srvService) compile(t *testing.T, target compiler.Target) dataframeexecution.Resolved {
	t.Helper()
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: target,
		Registry:      compilerTestRegistry{},
		SourceRows: func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
			return errors.New("compile leg streams nothing")
		},
	})
	if err != nil {
		t.Fatalf("engine: %s", s.scrub(err))
	}
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec04srvBundle(), exec04srvBindings())
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	return resolved
}

// TestPostgresPreviewServerWiringFailClosesPGPlan is the ratified D3 cell:
// a production-compiled PostgreSQL preview handed to the server's preview
// wiring (the production configuredAQLSourceRows adapter used by server.go
// for both row ports) returns the compiler's typed mismatch before the
// Arango callback or driver is reached — never a silent AQL render.
func TestPostgresPreviewServerWiringFailClosesPGPlan(t *testing.T) {
	s := newExec04srvService(t)
	s.migrateSeed(t)
	resolved := s.compile(t, s.target)

	var arangoCalls atomic.Int64
	arangoLandmine := func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		arangoCalls.Add(1)
		return errors.New("the Arango adapter must never run for a PostgreSQL-served preview")
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: s.target,
		Registry:      compilerTestRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "exec04-srv" },
		SourceRows:    configuredAQLSourceRows(nil, "query_rows", arangoLandmine),
		PreviewSourceRows: configuredAQLSourceRows(nil, "preview_query_rows", func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			arangoCalls.Add(1)
			return arangoLandmine(ctx, request, visit)
		}),
	})
	if err != nil {
		t.Fatalf("engine: %s", s.scrub(err))
	}
	summary, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 2}, func(map[string]any) error { return nil })
	if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("server AQL wiring over a PostgreSQL preview error = %v (%s), want the typed backend mismatch refusal", err, s.scrub(err))
	}
	if summary.Complete || summary.Truncated || summary.RowCount != 0 {
		t.Fatalf("refused preview carried a result marker: %+v", summary)
	}
	if arangoCalls.Load() != 0 {
		t.Fatalf("Arango callback ran %d times for a PostgreSQL-tagged preview", arangoCalls.Load())
	}

	t.Run("alive: the same compiled preview streams through the real PostgreSQL adapter", func(t *testing.T) {
		adapterEngine, err := dataframeexecution.New(dataframeexecution.Config{
			CompileTarget: s.target,
			Registry:      compilerTestRegistry{},
			ScopeDigest:   func(recipe.RuntimeBindings) string { return "exec04-srv" },
			SourceRows: func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
				if _, _, dispatchErr := request.Command.PostgreSQLParameters(); dispatchErr != nil {
					return dispatchErr
				}
				return s.executor.SourceRows(ctx, request, visit)
			},
		})
		if err != nil {
			t.Fatalf("adapter engine: %s", s.scrub(err))
		}
		var rows []map[string]any
		adapterSummary, adapterErr := adapterEngine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 3}, func(row map[string]any) error {
			rows = append(rows, row)
			return nil
		})
		if adapterErr != nil {
			t.Fatalf("alive control through the real adapter: %s", s.scrub(adapterErr))
		}
		// Limit exceeds the two-row scope so the bounded stop (count==limit)
		// cannot fire: this leg witnesses natural exhaustion.
		if len(rows) != 2 || !adapterSummary.Complete || adapterSummary.Truncated {
			t.Fatalf("alive control rows=%d summary Complete=%v Truncated=%v, want 2 rows naturally exhausted", len(rows), adapterSummary.Complete, adapterSummary.Truncated)
		}
		if arangoCalls.Load() != 0 {
			t.Fatalf("the Arango landmine ran on the alive adapter leg")
		}
	})

	t.Run("alive: the same server wiring proceeds for an AQL preview", func(t *testing.T) {
		aqlCalls := atomic.Int64{}
		aqlEngine, err := dataframeexecution.New(dataframeexecution.Config{
			CompileTarget: compiler.ArangoTarget(),
			Registry:      compilerTestRegistry{},
			ScopeDigest:   func(recipe.RuntimeBindings) string { return "exec04-srv" },
			SourceRows: configuredAQLSourceRows(nil, "query_rows", func(_ context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
				aqlCalls.Add(1)
				if _, _, dispatchErr := request.Command.AQLParameters(); dispatchErr != nil {
					return dispatchErr
				}
				return visit(map[string]any{"gender": "female", "birthDate": "1815-12-10"})
			}),
		})
		if err != nil {
			t.Fatalf("AQL engine: %s", s.scrub(err))
		}
		aqlResolved := s.compile(t, compiler.ArangoTarget())
		aqlRows := 0
		if _, aqlErr := aqlEngine.PreviewOutput(s.ctx, aqlResolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 1}, func(map[string]any) error { aqlRows++; return nil }); aqlErr != nil {
			t.Fatalf("alive AQL control through the same server wiring: %s", s.scrub(aqlErr))
		}
		if aqlCalls.Load() == 0 || aqlRows == 0 {
			t.Fatalf("alive AQL control did not proceed: calls=%d rows=%d", aqlCalls.Load(), aqlRows)
		}
	})
}

// TestZZPostgresPreviewServerZeroLeftoverFixtures is the ownership-scoped
// audit for this file's own prefix.
func TestZZPostgresPreviewServerZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 server-preview wiring tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %v", err)
	}
	defer admin.Close()
	for db := range exec04srvRegistrySnapshot() {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Fatalf("audit %s: %v", db, err)
		}
		if count != 0 {
			t.Errorf("owned fixture database %s survived", db)
		}
	}
	baseline := map[string]bool{}
	for _, name := range exec04srvBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec04\\_srv\\_%' ESCAPE '\\'")
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
