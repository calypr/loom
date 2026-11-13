package postgres_test

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

var (
	exec02OwnedMu     sync.Mutex
	exec02OwnedByName = map[string]string{}
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
			row["__loom_row_id"] = exec02StableRowIdentity(row["logical_id"])
		}
	}
	return rows
}

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

func exec02ScopedRows() []map[string]any {
	rows := exec02ScopedRowsPublic()
	for _, row := range rows {
		row["__loom_row_id"] = exec02StableRowIdentity(row["logical_id"])
	}
	return rows
}

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

func TestPostgresEngineTwoRealConnectionRaceDistinctBackends(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")

	ctxForRound := func() context.Context {
		ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
		_ = cancel
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

	abortCtx, abortCancel := context.WithCancel(s.ctx)
	if _, _, err := s.raceStreamAborted(abortCtx, abortCancel); err != nil {
		t.Fatalf("aborting stream: %v", err)
	}
	abortCancel()
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

func TestPostgresEngineBackendKillConverges(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")

	_, baseline, err := s.raceStream(s.ctx, "kill-baseline", nil)
	if err != nil {
		t.Fatalf("baseline stream: %v", err)
	}
	if want := exec02DigestRows(exec02ScopedRows()); baseline != want {
		t.Fatalf("baseline witness differs from committed resources:\n got %s\nwant %s", baseline, want)
	}

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

	if got := converge("post-crash"); got != baseline {
		t.Fatalf("post-crash witness differs from committed golden:\n got %s\nwant %s", got, baseline)
	}
}

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
