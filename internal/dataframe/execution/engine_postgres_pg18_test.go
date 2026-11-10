package execution

// Real-PostgreSQL 18 acceptance tests for backend-tagged dispatch (pg-exec-02;
// check-2: go test ./internal/dataframe/execution -run Postgres under
// LOOM_TEST_POSTGRES_URL against the isolated PostgreSQL 18 service).
//
// Every test drives the production Engine with a parent-tagged
// command.CompiledCommand through the real internal/dataframe/postgres
// adapter onto the real server engine. The AQL QueryRows/PreviewQueryRows
// adapters and every Arango preview control are armed landmines that fail
// the test the moment a PostgreSQL-served plan touches them, while the
// paired AQL controls keep running for AQL plans elsewhere in the package.
// Witnesses are literal values read back from PostgreSQL 18 itself: exact
// column names, driver-decoded typed values, canonical jsonb texts, verbatim
// SQL NULL versus JSON null separation, pg_backend_pid identities, and
// canonicalized row digests pinned to literals.
//
// Non-vacuity discipline: every refusal has a paired control that proceeds;
// the real adapter refuses an AQL-tagged command with the compiler's exact
// named mismatch before the driver is touched (a counting Querier proves no
// statement reached the server), and the same executor happily streams a
// correctly tagged command.
//
// Race surfaces run ten rounds on two real connections whose pg_backend_pid
// values are witnessed inside the streamed rows and asserted distinct per
// round; every abort (context cancellation or real-backend termination at
// the durability seams the dispatch exposes) converges to the
// byte-identical committed witness on rerun.
//
// Fixture databases and schemas are uniquely owned as verify_exec02_* and
// dropped by the test that created them. The final TestZZ audit is
// ownership-scoped: it checks each exact name this file created and counts
// only its own verify_exec02_* prefix, so concurrent sibling workers
// sweeping their own prefixes neither race this audit nor trip it. An
// unregistered survivor under the verify_exec02_* prefix is fatal.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec02Prefix  = "verify_exec02_"
	exec02NameFmt = exec02Prefix + "%d_%s"
)

var exec02NamePattern = regexp.MustCompile(`^verify_exec02_[0-9]+_[0-9a-f]+$`)

// Ownership registry: every database this file verified into existence is
// recorded here, and TestZZ asserts each exact name is gone. Ownership
// begins at verified CREATE.
var (
	exec02OwnedMu     sync.Mutex
	exec02OwnedByName = map[string]string{} // database -> owned schema
)

func exec02Register(db, schema string) {
	exec02OwnedMu.Lock()
	defer exec02OwnedMu.Unlock()
	exec02OwnedByName[db] = schema
}

func exec02RegistrySnapshot() map[string]string {
	exec02OwnedMu.Lock()
	defer exec02OwnedMu.Unlock()
	out := make(map[string]string, len(exec02OwnedByName))
	for k, v := range exec02OwnedByName {
		out[k] = v
	}
	return out
}

// Baseline: the verify_exec02_* databases visible before this process
// created its first fixture. Siblings churn their own prefixes; only this
// file's prefix is audited, ownership-scoped.
var (
	exec02BaselineOnce  sync.Once
	exec02BaselineNames []string
	exec02BaselineErr   error
)

func exec02CaptureBaseline(raw string) {
	exec02BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec02BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec02\\_%' ESCAPE '\\'")
		if err != nil {
			exec02BaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec02BaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec02BaselineNames = append(exec02BaselineNames, name)
		}
	})
}

func exec02URL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 dispatch integration tests")
	}
	return raw
}

type exec02Service struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	scrub    func(any) string
	fail     func(step string, err error)
	executor *dataframepostgres.Executor
}

func newExec02Service(t *testing.T) *exec02Service {
	t.Helper()
	raw := exec02URL(t)
	exec02CaptureBaseline(raw)
	if exec02BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec02BaselineErr)
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

	// PostgreSQL 18 only: the fixture refuses any other server rather than
	// claiming PG18 parity against a different engine.
	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8, current_setting('server_version')").
		Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	if err := storepg.CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required PostgreSQL 18 backend: %v", err)
	}

	dbName := fmt.Sprintf(exec02NameFmt, time.Now().UnixNano(), exec02RandomHex(t))
	if !exec02NamePattern.MatchString(dbName) {
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
	exec02Register(dbName, "")
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

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(exec02NameFmt, time.Now().UnixNano(), exec02RandomHex(t))
	if !exec02NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec02Register(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	executor, err := dataframepostgres.NewExecutor(iso)
	if err != nil {
		fail("create PostgreSQL row executor", err)
	}
	return &exec02Service{ctx: ctx, admin: admin, iso: iso, schema: schema, scrub: scrub, fail: fail, executor: executor}
}

func exec02RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func (s *exec02Service) qualifiedTable(name string) string {
	return `"` + s.schema + `"."` + name + `"`
}

// exec02ForbidArango arms every AQL/Arango/ClickHouse callback as an error
// landmine: a PostgreSQL-served plan that touches any of them fails.
func exec02ForbidArango() Config {
	return Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		QueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return errors.New("AQL QueryRows must never run for a PostgreSQL-served plan")
		},
		PreviewQueryRows: func(_ context.Context, command command.CompiledCommand, _ int, _ RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return errors.New("AQL PreviewQueryRows must never run for a PostgreSQL-served plan")
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			return errors.New("preparePreviewIndex must never run for a PostgreSQL-served plan")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
			return nil, errors.New("previewCoveringIndexes must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			return "", errors.New("previewCollectionRevision must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			return PreviewCollectionScanEvidence{}, errors.New("previewCollectionScans must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			return 0, errors.New("previewCollectionCount must never run for a PostgreSQL-served plan")
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for a PostgreSQL-served plan")
		},
	}
}

func exec02EngineConfig(s *exec02Service, selection *ResolvedPostgreSQLCommand) Config {
	config := exec02ForbidArango()
	config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return selection, nil
	}
	config.PostgreSQLQueryRows = func(ctx context.Context, command command.CompiledCommand, columns []string, visit RowVisitor) error {
		return s.executor.QueryRows(ctx, command, columns, dataframepostgres.RowVisitor(visit))
	}
	return config
}

// exec02Resolved builds the request-scoped plan record for the fixture.
// WithBackend exposes the streamed pg_backend_pid witness column publicly so
// race/crash tests can read it from the engine-visible rows.
func exec02Resolved(outputName, project, generation string, withBackend bool) Resolved {
	schema := []lower.CompiledOutputColumn{
		{ID: "id", Name: "id", Label: "id", Kind: "integer", Cardinality: "one"},
		{ID: "logical_id", Name: "logical_id", Label: "logical_id", Kind: "string", Cardinality: "one"},
		{ID: "score", Name: "score", Label: "score", Kind: "number", Cardinality: "one"},
		{ID: "active", Name: "active", Label: "active", Kind: "boolean", Cardinality: "one"},
		{ID: "payload", Name: "payload", Label: "payload", Kind: "object", Cardinality: "one"},
		{ID: "note", Name: "note", Label: "note", Kind: "string", Cardinality: "optional_one", Nullable: true},
	}
	columns := []string{"id", "logical_id", "score", "active", "payload", "note"}
	if withBackend {
		schema = append([]lower.CompiledOutputColumn{{ID: "backend", Name: "backend", Label: "backend", Kind: "integer", Cardinality: "one"}}, schema...)
		columns = append([]string{"backend"}, columns...)
	}
	output := lower.CompiledRecipeOutput{
		Name:             outputName,
		RootResourceType: "Patient",
		Columns:          columns,
		OutputSchema:     schema,
		RowIdentity:      &spec.RowIdentity{Fields: []string{"id"}},
		Plan:             ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
	}
	return Resolved{
		Bundle: recipe.Bundle{Name: outputName},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: project, DatasetGeneration: generation, OutputNames: []string{outputName},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "exec02-real-" + outputName,
	}
}

// exec02SeedFixture creates the literal typed fixture table inside the owned
// schema. Values are the goldens asserted by the streaming tests.
func (s *exec02Service) seedFixture(t *testing.T, name string) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, `CREATE TABLE `+s.qualifiedTable(name)+` (
		id bigint PRIMARY KEY,
		project text NOT NULL,
		dataset_generation text NOT NULL,
		logical_id text NOT NULL,
		score double precision NOT NULL,
		active boolean NOT NULL,
		payload jsonb NOT NULL,
		note text
	)`); err != nil {
		s.fail("create fixture table", err)
	}
	rows := []struct {
		id      int64
		project string
		gen     string
		logical string
		score   float64
		active  bool
		payload string
		note    any
	}{
		{1, "P1", "gen-a", "patient-ada", 3.5, true, `{"name": "Ada", "middle": null}`, "note-ada"},
		{2, "P1", "gen-a", "patient-grace", 2.25, false, `{"name": "Grace"}`, nil},
		{3, "P1", "gen-a", "patient-alan", 1.125, true, `null`, "note-alan"},
		{4, "P1", "gen-b", "patient-other-gen", 9.5, true, `{"name": "Other"}`, "note-other-gen"},
		{5, "other", "gen-a", "patient-other-project", 8.5, true, `{"name": "Intruder"}`, "note-intruder"},
	}
	for _, row := range rows {
		tag, err := s.iso.Exec(s.ctx,
			`INSERT INTO `+s.qualifiedTable(name)+` (id, project, dataset_generation, logical_id, score, active, payload, note) VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`,
			row.id, row.project, row.gen, row.logical, row.score, row.active, row.payload, row.note)
		if err != nil {
			s.fail("insert fixture row", err)
		}
		if tag.RowsAffected() != 1 {
			t.Fatalf("fixture insert affected %d rows", tag.RowsAffected())
		}
	}
}

func (s *exec02Service) countRows(t *testing.T, name string) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, `SELECT count(*) FROM `+s.qualifiedTable(name)).Scan(&count); err != nil {
		s.fail("count fixture rows", err)
	}
	return count
}

// convergePreview reruns a bounded preview until it succeeds, the contract
// after an abort or a terminated backend: a poisoned pool connection fails
// at most once and is discarded, and convergence must arrive within a
// bounded number of attempts. The visitor is rebuilt per attempt so
// accumulated state resets. A run that never converges fails with the last
// error; a converged run is still compared against literal goldens by the
// caller.
func (s *exec02Service) convergePreview(engine *Engine, resolved Resolved, limit int, newVisitor func() func(map[string]any) error) (PreviewSummary, error) {
	var summary PreviewSummary
	var err error
	for attempt := 1; attempt <= 6; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.ctx.Done():
				return summary, s.ctx.Err()
			}
		}
		summary, err = engine.PreviewOutput(s.ctx, resolved, PreviewRequest{Output: "resources", Limit: limit}, newVisitor())
		if err == nil {
			return summary, nil
		}
	}
	return summary, err
}

const exec02ScopedSQLFmt = `SELECT id, logical_id, score, active, payload, note FROM %s WHERE project = $1 AND dataset_generation = $2 ORDER BY id`

func exec02ScopedSelection(s *exec02Service) *ResolvedPostgreSQLCommand {
	compiledCommand, err := command.NewPostgreSQLCommand(
		fmt.Sprintf(exec02ScopedSQLFmt, s.qualifiedTable("exec02_resource")),
		[]command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "P1"},
			{Type: command.PostgreSQLParameterText, Value: "gen-a"},
		})
	if err != nil {
		panic(err)
	}
	return &ResolvedPostgreSQLCommand{Command: compiledCommand, Columns: []string{"id", "logical_id", "score", "active", "payload", "note"}}
}

func exec02DigestRows(rows []map[string]any) string {
	encoded, err := json.Marshal(rows)
	if err != nil {
		return "unencodable:" + err.Error()
	}
	return string(encoded)
}

// exec02StableRowIdentity mirrors the engine's stable row identity contract
// (sha256 over the JSON array of ordered identity field values) so the
// golden witness pins the exact digest each fixture row must carry.
func exec02StableRowIdentity(parts ...any) string {
	encoded, err := json.Marshal(parts)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// exec02AsInt64 decodes a server integer witness column: pgx delivers
// int4 (pg_backend_pid) as int32 and int8 (id) as int64.
func exec02AsInt64(v any) (int64, bool) {
	switch value := v.(type) {
	case int64:
		return value, true
	case int32:
		return int64(value), true
	default:
		return 0, false
	}
}

// exec02GoldenRows pins the exact engine-visible witness for the three
// P1/gen-a fixture rows as delivered by the real adapter through the engine
// pipeline: driver-decoded typed values, canonical jsonb text, verbatim SQL
// NULL versus JSON null separation, and the stable row identity digests.
// The Stream path carries the stable row identity digest; the PreviewOutput
// public-row filter drops it, so previews compare the identity-less form.
// These literals are load-bearing; break-one probes (flipping any single
// literal) fail the suite.
func exec02GoldenRows(withIdentity bool) []map[string]any {
	rows := []map[string]any{
		{
			"id": int64(1), "logical_id": "patient-ada", "score": 3.5, "active": true,
			"payload": json.RawMessage(`{"name": "Ada", "middle": null}`), "note": "note-ada",
		},
		{
			"id": int64(2), "logical_id": "patient-grace", "score": 2.25, "active": false,
			"payload": json.RawMessage(`{"name": "Grace"}`), "note": nil,
		},
		{
			"id": int64(3), "logical_id": "patient-alan", "score": 1.125, "active": true,
			"payload": json.RawMessage(`null`), "note": "note-alan",
		},
	}
	if withIdentity {
		for _, row := range rows {
			row["__loom_row_id"] = exec02StableRowIdentity(row["id"])
		}
	}
	return rows
}

// TestPostgresEngineExecutesCompiledCommandThroughRealAdapter is the
// acceptance-1 witness: the PostgreSQL plan streams exact golden rows
// through the real adapter and engine pipeline with every AQL/Arango control
// armed as an error, and the ordered typed parameters arrive unchanged.
func TestPostgresEngineExecutesCompiledCommandThroughRealAdapter(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	engine, err := New(exec02EngineConfig(s, exec02ScopedSelection(s)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	streams, err := engine.Streams(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(streams))
	}
	var got []map[string]any
	result, err := streams[0].Stream(s.ctx, func(row map[string]any) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if result.RowCount != 3 || len(got) != 3 {
		t.Fatalf("rows=%d visited=%d, want 3/3 (project/generation scoping leaked or was not applied)", result.RowCount, len(got))
	}
	if got[1]["logical_id"] == "patient-other-project" || got[1]["logical_id"] == "patient-other-gen" {
		t.Fatalf("scoped execution leaked another project/generation: %v", got)
	}
	if want := exec02DigestRows(exec02GoldenRows(true)); exec02DigestRows(got) != want {
		t.Fatalf("streamed witness drifted from golden literals:\n got %s\nwant %s", exec02DigestRows(got), want)
	}
	// Verbatim three-state separation: SQL NULL decodes to nil while the
	// JSON null column stays a non-nil raw message.
	if got[1]["note"] != nil {
		t.Fatalf("SQL NULL note must decode to nil: %#v", got[1]["note"])
	}
	if raw, ok := got[2]["payload"].(json.RawMessage); !ok || string(raw) != "null" {
		t.Fatalf("jsonb JSON null must stay a non-nil raw message: %#v", got[2]["payload"])
	}
}

// TestPostgresEnginePreviewBoundsCompletionOnRealServer pins the completed
// versus truncated markers for the same bounded preview class on real rows.
func TestPostgresEnginePreviewBoundsCompletionOnRealServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	engine, err := New(exec02EngineConfig(s, exec02ScopedSelection(s)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", false)

	var bounded []map[string]any
	summary, err := engine.PreviewOutput(s.ctx, resolved,
		PreviewRequest{Output: "resources", Limit: 2}, func(row map[string]any) error {
			bounded = append(bounded, row)
			return nil
		})
	if err != nil {
		t.Fatalf("bounded PreviewOutput: %v", err)
	}
	if summary.RowCount != 2 || summary.Complete || !summary.Truncated {
		t.Fatalf("limit-2 preview on 3 rows: rowcount=%d complete=%v truncated=%v", summary.RowCount, summary.Complete, summary.Truncated)
	}
	if bounded[0]["logical_id"] != "patient-ada" || bounded[1]["logical_id"] != "patient-grace" {
		t.Fatalf("preview order drifted: %v", bounded)
	}

	var all []map[string]any
	summary, err = engine.PreviewOutput(s.ctx, resolved,
		PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
			all = append(all, row)
			return nil
		})
	if err != nil {
		t.Fatalf("full PreviewOutput: %v", err)
	}
	if summary.RowCount != 3 || !summary.Complete || summary.Truncated {
		t.Fatalf("limit-10 preview on 3 rows: rowcount=%d complete=%v truncated=%v", summary.RowCount, summary.Complete, summary.Truncated)
	}
}

// TestPostgresEnginePreviewCancellationRealServer proves acceptance 5 on the
// real server: cancellation stops the stream, the error satisfies
// errors.Is(context.Canceled), no completed marker is emitted, and the
// post-abort rerun converges to the byte-identical committed witness.
func TestPostgresEnginePreviewCancellationRealServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	engine, err := New(exec02EngineConfig(s, exec02ScopedSelection(s)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", false)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	visits := 0
	summary, err := engine.PreviewOutput(ctx, resolved,
		PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
			visits++
			cancel()
			return nil
		})
	if err == nil {
		t.Fatalf("canceled preview must not complete: summary=%+v", summary)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preview error must satisfy errors.Is(context.Canceled): %v", err)
	}
	if visits != 1 {
		t.Fatalf("visitor continued after cancellation: %d visits", visits)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("canceled preview emitted a completion marker: %+v", summary)
	}

	var witness []map[string]any
	rerun, err := s.convergePreview(engine, resolved, 10, func() func(map[string]any) error {
		witness = nil
		return func(row map[string]any) error {
			witness = append(witness, row)
			return nil
		}
	})
	if err != nil {
		t.Fatalf("post-abort rerun: %v", err)
	}
	if !rerun.Complete || rerun.Truncated {
		t.Fatalf("post-abort rerun must converge complete: %+v", rerun)
	}
	if want := exec02DigestRows(exec02GoldenRows(false)); exec02DigestRows(witness) != want {
		t.Fatalf("post-abort witness differs from committed golden:\n got %s\nwant %s", exec02DigestRows(witness), want)
	}
}

// TestPostgresEngineTwoRealConnectionRaceDistinctBackends runs ten rounds of
// two concurrent engine streams; each round must run on two distinct real
// server backends (witnessed inside the streamed rows via
// pg_backend_pid()), committed witnesses must be byte-identical across
// every round, and an aborted round must converge to the same witness.
func TestPostgresEngineTwoRealConnectionRaceDistinctBackends(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	streamSQL := `SELECT pg_backend_pid() AS backend, id, logical_id, score, active, payload, note FROM ` + s.qualifiedTable("exec02_resource") + ` WHERE project = $1 AND dataset_generation = $2 ORDER BY id`
	compiledCommand, err := command.NewPostgreSQLCommand(streamSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
	})
	if err != nil {
		t.Fatalf("build race command: %v", err)
	}
	engine, err := New(exec02EngineConfig(s, &ResolvedPostgreSQLCommand{
		Command: compiledCommand,
		Columns: []string{"backend", "id", "logical_id", "score", "active", "payload", "note"},
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", true)

	// runSide streams once, witnessing its serving backend. The overlap
	// hook runs once on the first row while the side still holds its pool
	// connection, so a round cannot pass unless both queries are genuinely
	// in flight at the same instant (small fast responses would otherwise
	// let the sides serialize and reuse one connection and one backend).
	runSide := func(label string, overlap func()) (int64, string, error) {
		ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		defer cancel()
		streams, err := engine.Streams(ctx, resolved)
		if err != nil {
			return 0, "", fmt.Errorf("%s Streams: %w", label, err)
		}
		var backend int64
		var rows []map[string]any
		if _, err := streams[0].Stream(ctx, func(row map[string]any) error {
			if pid, ok := exec02AsInt64(row["backend"]); ok && backend == 0 {
				backend = pid
				if overlap != nil {
					overlap()
				}
			}
			delete(row, "backend")
			rows = append(rows, row)
			return nil
		}); err != nil {
			return 0, "", fmt.Errorf("%s Stream: %w", label, err)
		}
		if backend <= 0 {
			return 0, "", fmt.Errorf("%s never witnessed a server backend pid", label)
		}
		return backend, exec02DigestRows(rows), nil
	}

	var stableWitness string
	for round := 0; round < 10; round++ {
		type sideResult struct {
			backend int64
			digest  string
			err     error
		}
		var bothInFlight sync.WaitGroup
		bothInFlight.Add(2)
		barrier := func() {
			bothInFlight.Done()
			bothInFlight.Wait()
		}
		leftDone := make(chan sideResult, 1)
		rightDone := make(chan sideResult, 1)
		go func() {
			backend, digest, err := runSide(fmt.Sprintf("round-%d-left", round), barrier)
			leftDone <- sideResult{backend, digest, err}
		}()
		go func() {
			backend, digest, err := runSide(fmt.Sprintf("round-%d-right", round), barrier)
			rightDone <- sideResult{backend, digest, err}
		}()
		left, right := <-leftDone, <-rightDone
		if left.err != nil {
			t.Fatal(left.err)
		}
		if right.err != nil {
			t.Fatal(right.err)
		}
		if left.backend == right.backend {
			t.Fatalf("round %d: both concurrent streams ran on the same backend pid %d", round, left.backend)
		}
		if left.digest != right.digest {
			t.Fatalf("round %d: concurrent witnesses differ:\nleft  %s\nright %s", round, left.digest, right.digest)
		}
		if stableWitness == "" {
			stableWitness = left.digest
		} else if left.digest != stableWitness {
			t.Fatalf("round %d: witness drifted across rounds:\n got %s\nwant %s", round, left.digest, stableWitness)
		}
	}

	// Abort one side mid-stream, then require the same committed witness.
	abortCtx, abortCancel := context.WithCancel(s.ctx)
	defer abortCancel()
	streams, err := engine.Streams(abortCtx, resolved)
	if err != nil {
		t.Fatalf("abort Streams: %v", err)
	}
	firstRow := true
	_, streamErr := streams[0].Stream(abortCtx, func(row map[string]any) error {
		if firstRow {
			firstRow = false
			abortCancel()
		}
		return nil
	})
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		t.Fatalf("abort must report the context cause: %v", streamErr)
	}
	// Post-abort convergence: a poisoned connection may fail at most once
	// before the pool discards it; the witness must converge.
	var backend int64
	var digest string
	var runErr error
	for attempt := 1; attempt <= 6; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.ctx.Done():
				t.Fatalf("post-abort convergence window expired: %v", s.ctx.Err())
			}
		}
		backend, digest, runErr = runSide(fmt.Sprintf("post-abort-%d", attempt), nil)
		if runErr == nil {
			break
		}
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	if backend <= 0 || digest != stableWitness {
		t.Fatalf("post-abort witness differs: pid=%d\n got %s\nwant %s", backend, digest, stableWitness)
	}
}

// TestPostgresEngineParameterizedValuesCannotChangeShape proves the bound
// punctuation payload stays a value: the injection-shaped filter binds as
// data, returns zero rows, leaves the table intact, and the golden witness
// is unchanged afterwards.
func TestPostgresEngineParameterizedValuesCannotChangeShape(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	before := s.countRows(t, "exec02_resource")
	injection := `x'; DROP TABLE "` + s.schema + `"."exec02_resource"; --`
	compiledCommand, err := command.NewPostgreSQLCommand(
		`SELECT id, logical_id, score, active, payload, note FROM `+s.qualifiedTable("exec02_resource")+` WHERE project = $1 AND dataset_generation = $2 AND logical_id = $3 ORDER BY id`,
		[]command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: "P1"},
			{Type: command.PostgreSQLParameterText, Value: "gen-a"},
			{Type: command.PostgreSQLParameterText, Value: injection},
		})
	if err != nil {
		t.Fatalf("build injection command: %v", err)
	}
	engine, err := New(exec02EngineConfig(s, &ResolvedPostgreSQLCommand{
		Command: compiledCommand,
		Columns: []string{"id", "logical_id", "score", "active", "payload", "note"},
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rows, err := engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("Run with injection payload: %v", err)
	}
	if len(rows["resources"]) != 0 {
		t.Fatalf("injection payload matched %d rows; it must remain a value", len(rows["resources"]))
	}
	if after := s.countRows(t, "exec02_resource"); after != before {
		t.Fatalf("table contents changed under a bound payload: %d rows, want %d", after, before)
	}
	goldenEngine, err := New(exec02EngineConfig(s, exec02ScopedSelection(s)))
	if err != nil {
		t.Fatalf("New golden engine: %v", err)
	}
	golden, err := goldenEngine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("golden Run: %v", err)
	}
	if want := exec02DigestRows(exec02GoldenRows(true)); exec02DigestRows(golden["resources"]) != want {
		t.Fatalf("golden witness changed after injection probe:\n got %s\nwant %s", exec02DigestRows(golden["resources"]), want)
	}
}

type exec02CountingQuerier struct {
	inner *pgxpool.Pool
	mu    *sync.Mutex
	count *int
}

func (c *exec02CountingQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.mu.Lock()
	*c.count++
	c.mu.Unlock()
	return c.inner.Query(ctx, sql, args...)
}

// TestPostgresEngineCrossTagRefusedBeforeServer: the tagged envelope plus
// the real adapter refuse an AQL-tagged command with the compiler's exact
// named mismatch before the driver is touched; the paired correctly tagged
// control streams on the same counting querier.
func TestPostgresEngineCrossTagRefusedBeforeServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	queries := 0
	var mu sync.Mutex
	counting := &exec02CountingQuerier{inner: s.iso, mu: &mu, count: &queries}
	executor, err := dataframepostgres.NewExecutor(counting)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	config := exec02ForbidArango()
	crossTag, err := command.NewAQLCommand("FOR d IN exec02_resource RETURN d", map[string]any{"project": "P1"})
	if err != nil {
		t.Fatalf("build AQL command: %v", err)
	}
	config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return &ResolvedPostgreSQLCommand{Command: crossTag, Columns: []string{"id"}}, nil
	}
	config.PostgreSQLQueryRows = func(ctx context.Context, command command.CompiledCommand, columns []string, visit RowVisitor) error {
		return executor.QueryRows(ctx, command, columns, dataframepostgres.RowVisitor(visit))
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = engine.streamForOutput(s.ctx, exec02Resolved("resources", "P1", "gen-a", false), "resources", 5)
	if !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("cross-tag dispatch must report the exact mismatch sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "refusing to pass AQL query to the PostgreSQL statement executor") {
		t.Fatalf("refusal must name the routing defect exactly: %v", err)
	}
	mu.Lock()
	touched := queries
	mu.Unlock()
	if touched != 0 {
		t.Fatalf("refused cross-tag command reached the real server %d time(s)", touched)
	}

	// Paired control: the same counting querier streams a correct tag.
	controlEngine, err := New(exec02EngineConfig(s, exec02ScopedSelection(s)))
	if err != nil {
		t.Fatalf("New control: %v", err)
	}
	rows, err := controlEngine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("control Run: %v", err)
	}
	if len(rows["resources"]) != 3 {
		t.Fatalf("control streamed %d rows, want 3", len(rows["resources"]))
	}
}

// TestPostgresEngineBackendKillConverges injects a crash at the durability
// seams the dispatch exposes: the real backend serving an engine stream is
// terminated with pg_terminate_backend, both between runs and mid-stream
// (per-row server sleep keeps the stream live at kill time). A failed
// stream must never carry a completed marker, and reruns must converge to
// the byte-identical committed witness.
func TestPostgresEngineBackendKillConverges(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	// The response carries a 500 KB blob per row so the result exceeds the
	// socket buffers: the server must still be writing (and sleeping per
	// row) when the visitor runs, which is what makes a mid-stream
	// pg_terminate_backend observable. A small buffered response would be
	// delivered in full before any kill could land (verified against the
	// real server: kills after full delivery leave no error).
	streamSQL := `SELECT pg_backend_pid() AS backend, id, logical_id, score, active, payload, note, repeat('x', 500000) AS blob, pg_sleep(0.25) IS NOT NULL AS tick FROM ` + s.qualifiedTable("exec02_resource") + ` WHERE project = $1 AND dataset_generation = $2 ORDER BY id`
	compiledCommand, err := command.NewPostgreSQLCommand(streamSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
	})
	if err != nil {
		t.Fatalf("build kill command: %v", err)
	}
	selection := &ResolvedPostgreSQLCommand{Command: compiledCommand, Columns: []string{"backend", "id", "logical_id", "score", "active", "payload", "note", "blob", "tick"}}
	engine, err := New(exec02EngineConfig(s, selection))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", true)

	// Seam 1: terminate the served backend between completed runs; the pool
	// replaces the connection and the witness must converge.
	var servingPid int64
	summary, err := engine.PreviewOutput(s.ctx, resolved, PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
		if pid, ok := exec02AsInt64(row["backend"]); ok && servingPid == 0 {
			servingPid = pid
		}
		return nil
	})
	if err != nil {
		t.Fatalf("warm-up stream: %v", err)
	}
	if !summary.Complete || servingPid <= 0 {
		t.Fatalf("warm-up must complete with a witnessed backend: %+v pid=%d", summary, servingPid)
	}
	var killed bool
	if err := s.admin.QueryRow(s.ctx, `SELECT pg_terminate_backend($1)`, servingPid).Scan(&killed); err != nil {
		s.fail("pg_terminate_backend between runs", err)
	}
	if !killed {
		t.Fatalf("pg_terminate_backend(%d) reported no kill", servingPid)
	}
	var witness []map[string]any
	summary, err = s.convergePreview(engine, resolved, 10, func() func(map[string]any) error {
		witness = nil
		return func(row map[string]any) error {
			delete(row, "backend")
			delete(row, "tick")
			witness = append(witness, row)
			return nil
		}
	})
	if err != nil {
		t.Fatalf("post-kill rerun: %v", err)
	}
	if !summary.Complete || summary.Truncated {
		t.Fatalf("post-kill rerun must converge complete: %+v", summary)
	}
	if want := exec02DigestRows(exec02GoldenRows(false)); exec02DigestRows(witness) != want {
		t.Fatalf("post-kill witness differs from committed golden:\n got %s\nwant %s", exec02DigestRows(witness), want)
	}

	// Seam 2: kill the live backend mid-stream. The per-row server-side
	// pg_sleep keeps rows two and three in production while the visitor
	// runs, so terminating the serving backend from inside the visitor
	// strikes a genuinely in-flight stream: the engine must surface the
	// failure and must never emit a completed marker.
	var midPid int64
	var midKilled bool
	midSummary, midErr := engine.PreviewOutput(s.ctx, resolved, PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
		if midPid != 0 {
			return nil
		}
		pid, ok := exec02AsInt64(row["backend"])
		if !ok || pid <= 0 {
			return errors.New("stream row carried no server backend pid witness")
		}
		midPid = pid
		if err := s.admin.QueryRow(s.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&midKilled); err != nil {
			return fmt.Errorf("pg_terminate_backend live backend %d: %w", pid, err)
		}
		return nil
	})
	if midPid == 0 {
		t.Fatal("mid-stream run never witnessed its serving backend")
	}
	if !midKilled {
		t.Fatalf("pg_terminate_backend(%d) reported no kill for the live backend", midPid)
	}
	if midErr == nil {
		t.Fatalf("stream survived termination of its live backend; crash was not injected (pid %d)", midPid)
	}
	if midSummary.Complete || midSummary.Truncated {
		t.Fatalf("failed mid-stream run carried a completion marker: %+v", midSummary)
	}
	// Final convergence: the healthy rerun reproduces the committed witness.
	var finalRows []map[string]any
	final, err := s.convergePreview(engine, resolved, 10, func() func(map[string]any) error {
		finalRows = nil
		return func(row map[string]any) error {
			delete(row, "backend")
			delete(row, "tick")
			finalRows = append(finalRows, row)
			return nil
		}
	})
	if err != nil || !final.Complete || final.Truncated {
		t.Fatalf("convergence run after crash injection: complete=%v truncated=%v err=%v", final.Complete, final.Truncated, err)
	}
	if want := exec02DigestRows(exec02GoldenRows(false)); exec02DigestRows(finalRows) != want {
		t.Fatalf("post-crash witness differs from committed golden:\n got %s\nwant %s", exec02DigestRows(finalRows), want)
	}
}

// TestZZZeroLeftoverExec02PostgresFixtures audits ownership, not global
// state: every exact name this file created must be gone, and no
// verify_exec02_* database may survive beyond the baseline captured before
// this process created anything. Sibling prefixes belong to concurrent
// workers and are never counted.
func TestZZZeroLeftoverExec02PostgresFixtures(t *testing.T) {
	raw := exec02URL(t)
	exec02CaptureBaseline(raw)
	if exec02BaselineErr != nil {
		t.Fatalf("capture baseline: %v", exec02BaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()

	for db := range exec02RegistrySnapshot() {
		var exists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, db).Scan(&exists); err != nil {
			t.Fatalf("check registry member %s: %v", db, err)
		}
		if exists {
			t.Fatalf("created database %s was not dropped", db)
		}
	}

	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec02\\_%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("prefix scan: %v", err)
	}
	var survivors []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("prefix scan scan: %v", err)
		}
		survivors = append(survivors, name)
	}
	rows.Close()
	if len(survivors) != len(exec02BaselineNames) {
		t.Fatalf("verify_exec02_* survivors under own prefix: got %v, baseline %v", survivors, exec02BaselineNames)
	}
	baselineSet := map[string]bool{}
	for _, name := range exec02BaselineNames {
		baselineSet[name] = true
	}
	for _, name := range survivors {
		if !baselineSet[name] {
			t.Fatalf("unregistered verify_exec02_* survivor (fatal): %s", name)
		}
	}
}
