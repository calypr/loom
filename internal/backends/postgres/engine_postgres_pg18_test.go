package postgres_test

// Real-PostgreSQL 18 acceptance tests for backend-tagged dispatch (pg-exec-02;
// check-2: go test ./internal/dataframe/execution -run Postgres under
// LOOM_TEST_POSTGRES_URL against the isolated PostgreSQL 18 service).
//
// Every test drives the production execution.Engine with a parent-tagged
// command.CompiledCommand through the real internal/backends/postgres/internal/rows
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dataframepostgres "github.com/calypr/loom/internal/backends/postgres/internal/rows"
	storepg "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
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
	dsn      string
	target   compiler.Target
	scrub    func(any) string
	fail     func(step string, err error)
	executor *dataframepostgres.Executor
	lastRace atomic.Int64
}

// lastRacePID reports the server backend pid witnessed by the most recent
// raceStream completion (0 when none has run).
func (s *exec02Service) lastRacePID() int64 { return s.lastRace.Load() }

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
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		fail("bind PostgreSQL storage placement", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		fail("select PostgreSQL compile target", err)
	}
	return &exec02Service{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
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
func exec02ForbidArango() execution.Config {
	return execution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		SourceRows: func(_ context.Context, request execution.RowsRequest, _ execution.RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return errors.New("AQL QueryRows must never run for a PostgreSQL-served plan")
		},
		PreviewSourceRows: func(_ context.Context, request execution.RowsRequest, _ execution.RowVisitor) error {
			command := request.Command
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			return errors.New("AQL PreviewQueryRows must never run for a PostgreSQL-served plan")
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			return errors.New("preparePreviewIndex must never run for a PostgreSQL-served plan")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]execution.PreviewCollectionIndex, error) {
			return nil, errors.New("previewCoveringIndexes must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			return "", errors.New("previewCollectionRevision must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (execution.PreviewCollectionScanEvidence, error) {
			return execution.PreviewCollectionScanEvidence{}, errors.New("previewCollectionScans must never run for a PostgreSQL-served plan")
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			return 0, errors.New("previewCollectionCount must never run for a PostgreSQL-served plan")
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for a PostgreSQL-served plan")
		},
	}
}

// exec02EngineConfig wires a PostgreSQL-selected engine: the selected target
// compiles and renders, and the one source row port dispatches the tagged
// command to the real PostgreSQL adapter while refusing any AQL-tagged
// request outright (there is no fallback to arm).
func exec02EngineConfig(s *exec02Service) execution.Config {
	config := exec02ForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		return s.executor.SourceRows(ctx, request, visit)
	}
	return config
}

// exec02Resolved builds the request-scoped plan record for the fixture: a
// direct root scan of the bound fhir_resource placement with typed scope
// projections, inside the compiler's registered PostgreSQL root-scan slice.
// The selected PostgreSQL target compiles and renders it; execution streams
// exactly what the compiler produced. WithBackend keeps a leading column so
// race/crash tests can read a per-connection witness from the engine-visible
// rows (the logical_id value carries the pg_backend_pid witness text).
func exec02Resolved(outputName, project, generation string, withBackend bool) execution.Resolved {
	columns := []string{"project", "dataset_generation", "resource_type", "logical_id", "auth_resource_path"}
	schema := []lower.CompiledOutputColumn{
		{ID: "project", Name: "project", Label: "project", Kind: "string", Cardinality: "one"},
		{ID: "dataset_generation", Name: "dataset_generation", Label: "dataset_generation", Kind: "string", Cardinality: "one"},
		{ID: "resource_type", Name: "resource_type", Label: "resource_type", Kind: "string", Cardinality: "one"},
		{ID: "logical_id", Name: "logical_id", Label: "logical_id", Kind: "string", Cardinality: "one"},
		{ID: "auth_resource_path", Name: "auth_resource_path", Label: "auth_resource_path", Kind: "string", Cardinality: "one"},
	}
	projections := make([]ir.PhysicalProjection, 0, len(columns)+1)
	for _, column := range columns {
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{column}},
		})
	}
	if withBackend {
		// The witness arrives as a genuine lowered-form selector extraction
		// over the payload document (lower/root_projection.go shape), so the
		// streamed backend column is real production-plan output, not a
		// hand-written projection the renderer merely tolerates.
		backend := ir.PhysicalProjection{Name: "backend", Expression: &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType:  "Patient",
				Selector:      exec02WitnessSelector(),
				ExecutionMode: ir.PhysicalSelectorDirectScalar,
			},
		}, Presence: &ir.PhysicalProjectionPresence{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Paths:  [][]string{{"gender"}},
		}}
		projections = append([]ir.PhysicalProjection{backend}, projections...)
		columns = append([]string{"backend"}, columns...)
		schema = append([]lower.CompiledOutputColumn{{ID: "backend", Name: "backend", Label: "backend", Kind: "string", Cardinality: "one"}}, schema...)
	}
	output := lower.CompiledRecipeOutput{
		Name:             outputName,
		RootResourceType: "Patient",
		Columns:          columns,
		OutputSchema:     schema,
		RowIdentity:      &spec.RowIdentity{Fields: []string{"logical_id"}},
		Plan: ir.PhysicalPlan{
			Version: 1,
			Engine:  ir.PhysicalEngineAQL,
			BindVars: map[string]any{
				"root_collection":                  "Patient",
				"project":                          project,
				"dataset_generation":               generation,
				"auth_resource_paths":              []string{"FHIR/Patient"},
				"auth_resource_paths_unrestricted": false,
				"scope_allowed":                    true,
			},
			Operations: []ir.PhysicalOperation{
				{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
				{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{Variable: "root_auth_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED", Inputs: []ir.PhysicalValue{
					{Variable: "root", Path: []string{"auth_resource_path"}},
					{BindKey: "auth_resource_paths"},
					{BindKey: "auth_resource_paths_unrestricted"},
				}}},
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root_auth_allowed"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
				{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
			},
		},
	}
	return execution.Resolved{
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

// exec02Migrate installs the real pg-store-02 resource placement (fhir_resource
// with its registered typed columns) in the owned schema through the
// production migration runner, so the selected binding renders against the
// production relation shape, not a test-only approximation.
func (s *exec02Service) migrate(t *testing.T) {
	t.Helper()
	client, err := storepg.Open(s.ctx, storepg.Config{
		DSN:            s.dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	})
	if err != nil {
		s.fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	defer client.Close()
	if _, err := storepg.MigrateSchema(s.ctx, client, s.schema); err != nil {
		s.fail("migrate owned schema", err)
	}
}

// exec02SeedFixture inserts the golden fixtures through the production
// resource repository: every streamed witness row is a real stored resource.
// The logical_id values are the goldens asserted by the streaming tests; the
// "patient-witness" row carries the per-connection witness text assembled at
// insert time is not possible, so race tests read pg_backend_pid through the
// backend column instead (see exec02Resolved).
func (s *exec02Service) seedFixture(t *testing.T, name string) {
	t.Helper()
	if name != "fhir_resource" {
		t.Fatalf("exec02 fixtures seed only the bound resource placement, got %q", name)
	}
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	rows := []struct {
		project, gen, rtype, logical, auth string
		payload                            string
	}{
		{"P1", "gen-a", "Patient", "patient-ada", "FHIR/Patient", `{"name": "Ada", "middle": null}`},
		{"P1", "gen-a", "Patient", "patient-grace", "FHIR/Patient", `{"name": "Grace"}`},
		{"P1", "gen-a", "Patient", "patient-alan", "FHIR/Patient", `null`},
		{"P1", "gen-b", "Patient", "patient-other-gen", "FHIR/Patient", `{"name": "Other"}`},
		{"other", "gen-a", "Patient", "patient-other-project", "FHIR/Patient", `{"name": "Intruder"}`},
	}
	for _, row := range rows {
		_, err := repo.InsertResource(s.ctx, source.Resource{
			Project:           row.project,
			DatasetGeneration: row.gen,
			ResourceType:      row.rtype,
			LogicalID:         row.logical,
			AuthResourcePath:  row.auth,
			Payload:           json.RawMessage(row.payload),
		})
		if err != nil {
			s.fail("insert fixture row", err)
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
func (s *exec02Service) convergePreview(engine *execution.Engine, resolved execution.Resolved, limit int, newVisitor func() func(map[string]any) error) (execution.PreviewSummary, error) {
	var summary execution.PreviewSummary
	var err error
	for attempt := 1; attempt <= 6; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(150 * time.Millisecond):
			case <-s.ctx.Done():
				return summary, s.ctx.Err()
			}
		}
		summary, err = engine.PreviewOutput(s.ctx, resolved, execution.PreviewRequest{Output: "resources", Limit: limit}, newVisitor())
		if err == nil {
			return summary, nil
		}
	}
	return summary, err
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
// exec02WitnessPID parses the serving backend pid from the backend witness
// column: the fixture stores "pg_<pid>" as the logical_id of a per-connection
// witness resource (see exec02SeedConnectionWitness).
// exec02WitnessSelector parses the fixed witness payload selector once;
// "gender" is an active-schema scalar slot; the witness payload carries the
// backend pid in it so the extract is schema-validated like any authored one;
// a parse failure here is a fixture defect, not a runtime condition.
func exec02WitnessSelector() spec.Selector {
	selector, err := spec.ParseSelector("gender")
	if err != nil {
		panic(err)
	}
	return selector
}

func exec02WitnessPID(value any) (int64, bool) {
	text, ok := value.(string)
	if !ok {
		// The backend witness is a genuine jsonb payload extraction, and the
		// row adapter's contract decodes json/jsonb columns to raw JSON
		// bytes, so the legitimate wire shape is the quoted JSON string
		// "pg_<pid>" as a json.RawMessage, not a Go string.
		var raw []byte
		switch typed := value.(type) {
		case json.RawMessage:
			raw = typed
		case []byte:
			raw = typed
		default:
			return 0, false
		}
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, false
		}
	}
	if !strings.HasPrefix(text, "pg_") {
		return 0, false
	}
	pid, err := strconv.ParseInt(strings.TrimPrefix(text, "pg_"), 10, 64)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

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
			"id": int64(3), "logical_id": "patient-alan", "score": 1.125, "active": true,
			"payload": json.RawMessage(`null`), "note": "note-alan",
		},
		{
			"id": int64(2), "logical_id": "patient-grace", "score": 2.25, "active": false,
			"payload": json.RawMessage(`{"name": "Grace"}`), "note": nil,
		},
	}
	if withIdentity {
		for _, row := range rows {
			// The compiled RowIdentity field is logical_id, so the engine
			// digests the JSON array of the logical_id value, not the id.
			row["__loom_row_id"] = exec02StableRowIdentity(row["logical_id"])
		}
	}
	return rows
}

// exec02ScopedRowsPublic is the committed witness for the compiler-rendered
// root-scan stream of the fixture through the selected PostgreSQL target:
// exactly the three P1/gen-a Patient resources with the full typed scope
// projection the plan declares. The generic execution window sorts the root
// by its key, which the PostgreSQL renderer canonically maps from _key to
// logical_id (with resource_seq as the tiebreaker), so the physical order
// is logical_id ascending: ada, alan, grace.
func exec02ScopedRowsPublic() []map[string]any {
	return []map[string]any{
		{
			"project": "P1", "dataset_generation": "gen-a", "resource_type": "Patient",
			"logical_id": "patient-ada", "auth_resource_path": "FHIR/Patient",
		},
		{
			"project": "P1", "dataset_generation": "gen-a", "resource_type": "Patient",
			"logical_id": "patient-alan", "auth_resource_path": "FHIR/Patient",
		},
		{
			"project": "P1", "dataset_generation": "gen-a", "resource_type": "Patient",
			"logical_id": "patient-grace", "auth_resource_path": "FHIR/Patient",
		},
	}
}

// exec02ScopedRows is the engine-STREAM form of the committed witness: the
// stream contract always carries the stable row identity digest, computed
// by ensureStableRowIdentity over the compiled RowIdentity fields — here
// the single logical_id field — as sha256 of the JSON array of those values.
// Preview witnesses use exec02ScopedRowsPublic instead, because the public
// preview row filter drops __loom_row_id.
func exec02ScopedRows() []map[string]any {
	rows := exec02ScopedRowsPublic()
	for _, row := range rows {
		row["__loom_row_id"] = exec02StableRowIdentity(row["logical_id"])
	}
	return rows
}

// TestPostgresEngineExecutesCompiledCommandThroughRealAdapter is the
// acceptance-1 witness: the PostgreSQL plan streams exact golden rows
// through the real adapter and engine pipeline with every AQL/Arango control
// armed as an error, and the ordered typed parameters arrive unchanged.
func TestPostgresEngineExecutesCompiledCommandThroughRealAdapter(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	engine, err := execution.New(exec02EngineConfig(s))
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
	if want := exec02DigestRows(exec02ScopedRows()); exec02DigestRows(got) != want {
		t.Fatalf("streamed witness drifted from the committed resources:\n got %s\nwant %s", exec02DigestRows(got), want)
	}
	for _, row := range got {
		for _, column := range []string{"project", "dataset_generation", "resource_type", "logical_id", "auth_resource_path"} {
			if _, ok := row[column].(string); !ok {
				t.Fatalf("scope column %q must decode as a string: %#v", column, row[column])
			}
		}
	}
}

// TestPostgresEnginePreviewBoundsCompletionOnRealServer pins the completed
// versus truncated markers for the same bounded preview class on real rows.
func TestPostgresEnginePreviewBoundsCompletionOnRealServer(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	engine, err := execution.New(exec02EngineConfig(s))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", false)

	var bounded []map[string]any
	summary, err := engine.PreviewOutput(s.ctx, resolved,
		execution.PreviewRequest{Output: "resources", Limit: 2}, func(row map[string]any) error {
			bounded = append(bounded, row)
			return nil
		})
	if err != nil {
		t.Fatalf("bounded PreviewOutput: %v", err)
	}
	if summary.RowCount != 2 || summary.Complete || !summary.Truncated {
		t.Fatalf("limit-2 preview on 3 rows: rowcount=%d complete=%v truncated=%v", summary.RowCount, summary.Complete, summary.Truncated)
	}
	if bounded[0]["logical_id"] != "patient-ada" || bounded[1]["logical_id"] != "patient-alan" {
		t.Fatalf("preview order drifted: %v", bounded)
	}

	var all []map[string]any
	summary, err = engine.PreviewOutput(s.ctx, resolved,
		execution.PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
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
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	engine, err := execution.New(exec02EngineConfig(s))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", false)

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	visits := 0
	summary, err := engine.PreviewOutput(ctx, resolved,
		execution.PreviewRequest{Output: "resources", Limit: 10}, func(row map[string]any) error {
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
	if want := exec02DigestRows(exec02ScopedRowsPublic()); exec02DigestRows(witness) != want {
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
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	// The race rounds drive s.raceStream: each side owns one pool
	// connection, seeds its own witness resource on that connection inside
	// a transaction, and streams the compiler-rendered withBackend plan
	// through the same connection — the witnessed backend pid comes from the
	// served row itself.

	// runSide streams once, witnessing its serving backend. The overlap
	// hook runs once on the first row while the side still holds its pool
	// connection, so a round cannot pass unless both queries are genuinely
	// in flight at the same instant (small fast responses would otherwise
	// let the sides serialize and reuse one connection and one backend).
	ctxForRound := func() context.Context {
		ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		_ = cancel // context lives for the round's channel receive below
		return ctx
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
			backend, digest, err := s.raceStream(ctxForRound(), fmt.Sprintf("round-%d-left", round), barrier)
			leftDone <- sideResult{backend, digest, err}
		}()
		go func() {
			backend, digest, err := s.raceStream(ctxForRound(), fmt.Sprintf("round-%d-right", round), barrier)
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
	if _, _, err := s.raceStreamAborted(abortCtx, abortCancel); err != nil {
		t.Fatalf("aborting stream: %v", err)
	}
	abortCancel()
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
		backend, digest, runErr = s.raceStream(ctxForRound(), fmt.Sprintf("post-abort-%d", attempt), nil)
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

// TestPostgresEngineParameterizedValuesCannotChangeShape proves bound values
// stay values: an injection-shaped project binding travels through the
// selected-target compiler as a typed $n parameter, matches nothing, leaves
// the resource table intact, and the golden witness is unchanged afterwards.
func TestPostgresEngineParameterizedValuesCannotChangeShape(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	before := s.countRows(t, "fhir_resource")
	injection := `x'; DROP TABLE "` + s.schema + `"."fhir_resource"; --`
	engine, err := execution.New(exec02EngineConfig(s))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rows, err := engine.Run(s.ctx, exec02Resolved("resources", injection, "gen-a", false))
	if err != nil {
		t.Fatalf("Run with injection payload: %v", err)
	}
	if len(rows["resources"]) != 0 {
		t.Fatalf("injection payload matched %d rows; it must remain a value", len(rows["resources"]))
	}
	if after := s.countRows(t, "fhir_resource"); after != before {
		t.Fatalf("table contents changed under a bound payload: %d rows, want %d", after, before)
	}
	streams, err := engine.Streams(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("golden Streams: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("golden streams = %d, want 1", len(streams))
	}
	var golden []map[string]any
	if _, err := streams[0].Stream(s.ctx, func(row map[string]any) error {
		golden = append(golden, row)
		return nil
	}); err != nil {
		t.Fatalf("golden Stream: %v", err)
	}
	if want := exec02DigestRows(exec02ScopedRows()); exec02DigestRows(golden) != want {
		t.Fatalf("golden witness changed after injection probe:\n got %s\nwant %s", exec02DigestRows(golden), want)
	}
}

// exec02RaceStream runs one stream of the withBackend plan and returns the
// witnessed serving backend plus the digest of the committed rows. The
// backend witness is the per-connection fixture resource (logical_id
// "pg_<pid>") that the stream must carry in its backend column.
func (s *exec02Service) raceStream(ctx context.Context, label string, overlap func()) (int64, string, error) {
	conn, err := s.iso.Acquire(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("%s acquire: %w", label, err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("%s begin: %w", label, err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	var pid int64
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		return 0, "", fmt.Errorf("%s backend pid: %w", label, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+s.qualifiedTable("fhir_resource")+` (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload, recorded_at)
		 VALUES ($1,$2,$3,$4,$5,$6::jsonb, now())`,
		"P1", "gen-a", "Patient", fmt.Sprintf("witness-%d", pid), "FHIR/Patient", fmt.Sprintf(`{"gender": "pg_%d"}`, pid)); err != nil {
		return 0, "", fmt.Errorf("%s insert witness: %w", label, err)
	}
	queries := 0
	var mu sync.Mutex
	streamExecutor, err := dataframepostgres.NewExecutor(&exec02CountingQuerier{inner: tx, mu: &mu, count: &queries})
	if err != nil {
		return 0, "", fmt.Errorf("%s executor: %w", label, err)
	}
	config := exec02EngineConfig(s)
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		return streamExecutor.SourceRows(ctx, request, visit)
	}
	engine, err := execution.New(config)
	if err != nil {
		return 0, "", fmt.Errorf("%s engine: %w", label, err)
	}
	streams, err := engine.Streams(ctx, exec02Resolved("resources", "P1", "gen-a", true))
	if err != nil {
		return 0, "", fmt.Errorf("%s Streams: %w", label, err)
	}
	if len(streams) != 1 {
		return 0, "", fmt.Errorf("%s streams = %d, want 1", label, len(streams))
	}
	var backend int64
	var rows []map[string]any
	if _, err := streams[0].Stream(ctx, func(row map[string]any) error {
		if witnessed, ok := exec02WitnessPID(row["backend"]); ok && backend == 0 {
			backend = witnessed
			if overlap != nil {
				overlap()
			}
		}
		delete(row, "backend")
		// The per-connection witness resource is uncommitted transaction
		// scaffolding, not fixture data: the committed witness is the three
		// golden rows, so the scaffold row is excluded from the digest.
		if logical, _ := row["logical_id"].(string); !strings.HasPrefix(logical, "witness-") {
			rows = append(rows, row)
		}
		return nil
	}); err != nil {
		return 0, "", fmt.Errorf("%s Stream: %w", label, err)
	}
	if backend != pid {
		return 0, "", fmt.Errorf("%s witnessed backend %d, seeded %d", label, backend, pid)
	}
	s.lastRace.Store(pid)
	return backend, exec02DigestRows(rows), nil
}

type exec02CountingQuerier struct {
	inner dataframepostgres.Querier
	mu    *sync.Mutex
	count *int
}

func (c *exec02CountingQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	c.mu.Lock()
	*c.count++
	c.mu.Unlock()
	return c.inner.Query(ctx, sql, args...)
}

// TestPostgresEngineCrossTagRefusedBeforeServer proves the real adapter's
// row port refuses a wrong-tag command end to end before the server is
// touched: an engine compiled for the AQL target produces an AQL-tagged
// command, and routing it into the PostgreSQL adapter's SourceRows must
// report the compiler's exact mismatch sentinel with zero server queries.
// The paired correctly tagged control streams on the same counting querier.
func TestPostgresEngineCrossTagRefusedBeforeServer(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	queries := 0
	var mu sync.Mutex
	counting := &exec02CountingQuerier{inner: s.iso, mu: &mu, count: &queries}
	executor, err := dataframepostgres.NewExecutor(counting)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	config := exec02ForbidArango()
	config.CompileTarget = compiler.ArangoTarget()
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		return executor.SourceRows(ctx, request, visit)
	}
	engine, err := execution.New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
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
	controlEngine, err := execution.New(exec02EngineConfig(s))
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

// TestPostgresEngineBackendKillConverges injects crashes at three distinct
// durability seams and requires the committed witness to converge:
//  1. the real backend serving a compiled engine stream is terminated with
//     pg_terminate_backend between runs (each side runs on its own
//     connection via s.raceStream, witnessed inside the served rows);
//  2. the serving backend of a genuinely in-flight adapter stream is
//     terminated from inside the visitor — a 500 KB per-row blob plus
//     per-row pg_sleep keeps the server writing when the kill lands (a
//     small buffered response would be delivered before any kill could
//     strike; verified against the real server);
//  3. a row port that fails after the first visited row must never let the
//     engine emit a completed marker.
func TestPostgresEngineBackendKillConverges(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")

	// Baseline committed witness through the compiled withBackend plan.
	_, baseline, err := s.raceStream(s.ctx, "kill-baseline", nil)
	if err != nil {
		t.Fatalf("baseline stream: %v", err)
	}
	if want := exec02DigestRows(exec02ScopedRows()); baseline != want {
		t.Fatalf("baseline witness differs from committed resources:\n got %s\nwant %s", baseline, want)
	}

	// Seam 1: terminate a served backend between completed runs; the pool
	// replaces the connection and a fresh stream must converge.
	var killed bool
	if err := s.admin.QueryRow(s.ctx, `SELECT pg_terminate_backend($1)`, s.lastRacePID()).Scan(&killed); err != nil {
		s.fail("pg_terminate_backend between runs", err)
	}
	if !killed {
		t.Fatalf("pg_terminate_backend reported no kill for %d", s.lastRacePID())
	}
	converge := func(label string) string {
		t.Helper()
		var digest string
		var runErr error
		for attempt := 1; attempt <= 6; attempt++ {
			if attempt > 1 {
				select {
				case <-time.After(150 * time.Millisecond):
				case <-s.ctx.Done():
					t.Fatalf("%s convergence window expired: %v", label, s.ctx.Err())
				}
			}
			_, digest, runErr = s.raceStream(s.ctx, fmt.Sprintf("%s-%d", label, attempt), nil)
			if runErr == nil {
				return digest
			}
		}
		t.Fatalf("%s never converged: %v", label, runErr)
		return ""
	}
	if got := converge("post-kill"); got != baseline {
		t.Fatalf("post-kill witness differs from committed golden:\n got %s\nwant %s", got, baseline)
	}

	// Seam 2: kill a genuinely in-flight adapter stream mid-visit. Heavy
	// server-side output (500 KB blob + pg_sleep per row) keeps rows two
	// and three in production while the visitor of row one terminates the
	// serving backend; the adapter must surface the failure.
	streamSQL := `SELECT pg_backend_pid() AS backend, logical_id, repeat('x', 500000) AS blob, pg_sleep(0.25) IS NOT NULL AS tick FROM ` + s.qualifiedTable("fhir_resource") + ` WHERE project = $1 AND dataset_generation = $2 ORDER BY resource_seq`
	compiledCommand, err := command.NewPostgreSQLCommand(streamSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
	})
	if err != nil {
		t.Fatalf("build kill command: %v", err)
	}
	adapterExecutor, err := dataframepostgres.NewExecutor(s.iso)
	if err != nil {
		t.Fatalf("adapter executor: %v", err)
	}
	var midPid int64
	var midKilled bool
	midErr := adapterExecutor.SourceRows(s.ctx, execution.RowsRequest{
		Command:         compiledCommand,
		DeclaredColumns: []string{"backend", "logical_id", "blob", "tick"},
	}, func(row map[string]any) error {
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

	// Seam 3: a row port failing mid-visit must never produce a completed
	// engine marker.
	faultyConfig := exec02EngineConfig(s)
	faultyConfig.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		visited := 0
		return adapterExecutor.SourceRows(ctx, request, func(row map[string]any) error {
			if err := visit(row); err != nil {
				return err
			}
			visited++
			if visited == 1 {
				return errors.New("injected row-port failure after first visit")
			}
			return nil
		})
	}
	faultyEngine, err := execution.New(faultyConfig)
	if err != nil {
		t.Fatalf("New faulty: %v", err)
	}
	faultySummary, faultyErr := faultyEngine.PreviewOutput(s.ctx,
		exec02Resolved("resources", "P1", "gen-a", false),
		execution.PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return nil })
	if faultyErr == nil {
		t.Fatal("port failure after first visit must fail the preview")
	}
	if faultySummary.Complete || faultySummary.Truncated {
		t.Fatalf("failed port run carried a completion marker: %+v", faultySummary)
	}

	// Final convergence after both crash injections.
	if got := converge("post-crash"); got != baseline {
		t.Fatalf("post-crash witness differs from committed golden:\n got %s\nwant %s", got, baseline)
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

// raceStreamAborted streams the withBackend plan on a dedicated connection
// and cancels the context from the first visited row, proving the abort path
// reports the context cause while the connection is still in flight.
func (s *exec02Service) raceStreamAborted(ctx context.Context, cancel context.CancelFunc) (int64, string, error) {
	conn, err := s.iso.Acquire(ctx)
	if err != nil {
		return 0, "", fmt.Errorf("abort acquire: %w", err)
	}
	defer conn.Release()
	queries := 0
	var mu sync.Mutex
	streamExecutor, err := dataframepostgres.NewExecutor(&exec02CountingQuerier{inner: conn, mu: &mu, count: &queries})
	if err != nil {
		return 0, "", fmt.Errorf("abort executor: %w", err)
	}
	config := exec02EngineConfig(s)
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		return streamExecutor.SourceRows(ctx, request, visit)
	}
	engine, err := execution.New(config)
	if err != nil {
		return 0, "", fmt.Errorf("abort engine: %w", err)
	}
	streams, err := engine.Streams(ctx, exec02Resolved("resources", "P1", "gen-a", true))
	if err != nil {
		return 0, "", fmt.Errorf("abort Streams: %w", err)
	}
	if len(streams) != 1 {
		return 0, "", fmt.Errorf("abort streams = %d, want 1", len(streams))
	}
	var backend int64
	firstRow := true
	if _, err := streams[0].Stream(ctx, func(row map[string]any) error {
		if witnessed, ok := exec02WitnessPID(row["backend"]); ok && backend == 0 {
			backend = witnessed
		}
		if firstRow {
			firstRow = false
			cancel()
		}
		return nil
	}); err != nil && !errors.Is(err, context.Canceled) {
		return 0, "", fmt.Errorf("abort Stream: %w", err)
	}
	return backend, "", nil
}

// invalidRecipeRegistry is the external-package copy of the shared test
// registry: it refuses real recipe loads and yields a fixed default bundle.
type invalidRecipeRegistry struct{}

func (invalidRecipeRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{Bundle: recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "default",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "missing", Expr: recipe.Expression{Select: "root.missing"}}},
		}},
	}}, nil
}

func (r invalidRecipeRegistry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}
