package execution_test

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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	postgres "github.com/calypr/loom/internal/backends/postgres"
	storepg "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec09ctPrefix  = "verify_exec09_ct_"
	exec09ctNameFmt = exec09ctPrefix + "%d_%s"

	exec09ctProject    = "project-a"
	exec09ctGeneration = "gen-a"
	exec09ctAuthPath   = "FHIR/Patient"
	exec09ctExplorer   = "patients"
)

var exec09ctNamePattern = regexp.MustCompile(`^verify_exec09_ct_[0-9]+_[0-9a-f]+$`)

var (
	exec09ctOwnedMu     sync.Mutex
	exec09ctOwnedByName = map[string]string{}
)

func exec09ctRegister(db, schema string) {
	exec09ctOwnedMu.Lock()
	defer exec09ctOwnedMu.Unlock()
	exec09ctOwnedByName[db] = schema
}

func exec09ctRegistrySnapshot() map[string]string {
	exec09ctOwnedMu.Lock()
	defer exec09ctOwnedMu.Unlock()
	out := make(map[string]string, len(exec09ctOwnedByName))
	for db, schema := range exec09ctOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec09ctBaselineOnce  sync.Once
	exec09ctBaselineNames []string
	exec09ctBaselineErr   error
)

func exec09ctCaptureBaseline(raw string) {
	exec09ctBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec09ctBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec09\_ct\_%' ESCAPE '\' UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec09\_ct\_%' ESCAPE '\'`)
		if err != nil {
			exec09ctBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec09ctBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec09ctBaselineNames = append(exec09ctBaselineNames, name)
		}
		exec09ctBaselineErr = rows.Err()
	})
}

func exec09ctRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec09ctService struct {
	ctx     context.Context
	iso     *pgxpool.Pool
	schema  string
	target  compiler.Target
	scrub   func(any) string
	fail    func(step string, err error)
	adapter dataframeexecution.SourceRows
}

func newExec09ctService(t *testing.T) *exec09ctService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 cell-trace acceptance tests")
	}
	scrub, addSecret := exec04pvScrubberFor(raw)
	exec09ctCaptureBaseline(raw)
	if exec09ctBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec09ctBaselineErr))
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

	dbName := fmt.Sprintf(exec09ctNameFmt, time.Now().UnixNano(), exec09ctRandomHex(t))
	if !exec09ctNamePattern.MatchString(dbName) {
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
	exec09ctRegister(dbName, "")
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

	schema := fmt.Sprintf(exec09ctNameFmt, time.Now().UnixNano(), exec09ctRandomHex(t))
	if !exec09ctNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec09ctRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	client, err := storepg.OpenPool(ctx, storepg.ClientConfig{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	defer client.Close()
	if _, err := storepg.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}

	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		fail("bind PostgreSQL storage placement", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		fail("select PostgreSQL compile target", err)
	}
	adapter, err := postgres.NewSourceRows(iso)
	if err != nil {
		fail("create PostgreSQL row adapter", err)
	}
	return &exec09ctService{ctx: ctx, iso: iso, schema: schema, target: target, scrub: scrub, fail: fail, adapter: adapter}
}

func exec09ctPatientsBundle(authoredIdentity bool) recipe.Bundle {
	output := recipe.Output{
		Name: exec09ctExplorer, RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "gender", Expr: recipe.Expression{Select: "root.gender"}},
		},
	}
	if authoredIdentity {
		output.Identity = &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}}
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec09_cell_trace",
		TranslationVersion:  "exec09",
		Outputs:             []recipe.Output{output},
	}
}

func exec09ctArmABundle(all bool) recipe.Bundle {
	mode := recipe.ValueModeFirst
	if all {
		mode = recipe.ValueModeAll
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec09_arm_a",
		TranslationVersion:  "exec09",
		Outputs: []recipe.Output{{
			Name: exec09ctExplorer, RootResourceType: "Patient", RowGrain: "patient",
			RootColumnNaming: recipe.RootColumnNamingExact, TraversalColumnNaming: recipe.TraversalColumnNamingExact,
			Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
			Traversals: []recipe.Traversal{{
				Name: "subject_Patient", ToResourceType: "Condition", Alias: "condition", MatchMode: recipe.MatchOptional,
				Fields: []recipe.Field{{Name: "condition_id", Expr: recipe.Expression{Select: "id"}, ValueMode: mode}},
			}},
		}},
	}
}

func exec09ctBindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{exec09ctAuthPath},
	}
}

type exec09ctSeedRow struct {
	project, generation, logicalID, authPath string
	gender                                   string
}

func (s *exec09ctService) seed(t *testing.T, rows []exec09ctSeedRow) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	for _, row := range rows {
		payload := `{"id":` + quoteExec09(row.logicalID) + `}`
		if row.gender != "" {
			payload = strings.TrimSuffix(payload, "}") + `,"gender":` + row.gender + `}`
		}
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project:           row.project,
			DatasetGeneration: row.generation,
			ResourceType:      "Patient",
			LogicalID:         row.logicalID,
			AuthResourcePath:  row.authPath,
			Payload:           json.RawMessage(payload),
		}); err != nil {
			s.fail("insert fixture patient "+row.logicalID, err)
		}
	}
}

func quoteExec09(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func (s *exec09ctService) seedEdges(t *testing.T, patient string, conditions []string) {
	t.Helper()
	resources, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	edges, err := storepg.NewEdgeRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("edge repository: %v", err)
	}
	for index, condition := range conditions {
		if _, err := resources.InsertResource(s.ctx, source.Resource{
			Project: exec09ctProject, DatasetGeneration: exec09ctGeneration,
			ResourceType: "Condition", LogicalID: condition, AuthResourcePath: exec09ctAuthPath,
			Payload: json.RawMessage(`{"id":` + quoteExec09(condition) + `}`),
		}); err != nil {
			s.fail("insert fixture condition "+condition, err)
		}
		if _, err := edges.InsertEdge(s.ctx, storepg.EdgeOccurrenceRow{
			Project: exec09ctProject, DatasetGeneration: exec09ctGeneration, AuthResourcePath: exec09ctAuthPath,
			Label: "subject_Patient", SourceResourceType: "Condition", SourceLogicalID: condition,
			TargetResourceType: "Patient", TargetLogicalID: patient, SourcePath: "subject", SourceOrdinal: int64(index),
		}); err != nil {
			s.fail("insert fixture edge "+condition+"->"+patient, err)
		}
	}
}

type exec09ctCall struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec09ctRecorder struct {
	adapter dataframeexecution.SourceRows
	mu      sync.Mutex
	calls   []exec09ctCall
	rows    []map[string]any
	capture bool
}

func (r *exec09ctRecorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.calls = append(r.calls, exec09ctCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	r.mu.Unlock()
	if !r.capture {
		return r.adapter(ctx, request, visit)
	}
	return r.adapter(ctx, request, func(row map[string]any) error {
		captured := make(map[string]any, len(row))
		for key, value := range row {
			captured[key] = value
		}
		r.mu.Lock()
		r.rows = append(r.rows, captured)
		r.mu.Unlock()
		return visit(row)
	})
}

func (r *exec09ctRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *exec09ctRecorder) last() exec09ctCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

func (r *exec09ctRecorder) capturedRows() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, len(r.rows))
	copy(out, r.rows)
	return out
}

func wantCellTraceContract(t *testing.T, declared []string, identityColumn string) {
	t.Helper()
	want := []string{
		ir.PhysicalCellTraceValueField, ir.PhysicalCellTraceContributionsField, identityColumn,
		ir.PhysicalCellTraceStatusField, ir.PhysicalCellTraceHasMoreField, ir.PhysicalCellTraceOmissionField,
	}
	if strings.Join(declared, ",") != strings.Join(want, ",") {
		t.Fatalf("declared contract = %v, want %v", declared, want)
	}
}

func (s *exec09ctService) engine(t *testing.T, recorder *exec09ctRecorder) *dataframeexecution.Engine {
	t.Helper()
	config := exec04pvForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec09-scope" }
	config.SourceRows = recorder.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

func (s *exec09ctService) resolve(t *testing.T, engine *dataframeexecution.Engine, project, generation string, bundle recipe.Bundle) dataframeexecution.Resolved {
	t.Helper()
	resolved, err := engine.CompileResolvedBundle(s.ctx, bundle, exec09ctBindings(project, generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle(%s/%s): %s", project, generation, s.scrub(err))
	}
	return resolved
}

func (s *exec09ctService) previewIdentity(t *testing.T, engine *dataframeexecution.Engine, resolved dataframeexecution.Resolved, wantID string) string {
	t.Helper()
	var identity string
	matched := 0
	_, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: exec09ctExplorer, Limit: 50, IncludeRowIdentity: true}, func(row map[string]any) error {
		if published, _ := row["patient_id"].(string); published == wantID {
			matched++
			cell, ok := row["__loom_row_id"].(string)
			if !ok || cell == "" {
				t.Fatalf("preview row did not publish its stable identity: %#v", row)
			}
			identity = cell
		}
		return nil
	})
	if err != nil {
		t.Fatalf("preview: %s", s.scrub(err))
	}
	if matched != 1 {
		t.Fatalf("preview published %d rows for patient_id %q, want exactly one", matched, wantID)
	}
	return identity
}

func exec09ctIdentityDigestOf(t *testing.T, cell any) string {
	t.Helper()
	var parts []any
	switch typed := cell.(type) {
	case []any:
		parts = typed
	case json.RawMessage:
		if err := json.Unmarshal(typed, &parts); err != nil {
			t.Fatalf("published identity parts %q do not decode: %v", string(typed), err)
		}
	case []byte:
		if err := json.Unmarshal(typed, &parts); err != nil {
			t.Fatalf("published identity parts %q do not decode: %v", string(typed), err)
		}
	default:
		t.Fatalf("published identity parts have an unexpected shape %T", cell)
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		t.Fatalf("encode identity parts: %v", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func exec09ctRowIdentity(t *testing.T, rows []map[string]any, wantID string) string {
	t.Helper()
	for _, row := range rows {
		cell, ok := row[ir.PhysicalCellTraceIdentityPartsField]
		if !ok {
			t.Fatalf("captured trace row carries no identity parts column: %#v", row)
		}
		var parts []string
		switch typed := cell.(type) {
		case []any:
			for _, part := range typed {
				if text, ok := part.(string); ok {
					parts = append(parts, text)
				}
			}
		case json.RawMessage:
			_ = json.Unmarshal(typed, &parts)
		case []byte:
			_ = json.Unmarshal(typed, &parts)
		}
		if len(parts) > 0 && parts[len(parts)-1] == wantID {
			return exec09ctIdentityDigestOf(t, cell)
		}
	}
	t.Fatalf("no captured trace row carries identity parts ending in %q", wantID)
	return ""
}

func exec09ctCellTraceTriples(result dataframeexecution.CellTraceResult) []contributorTriple {
	out := make([]contributorTriple, 0, len(result.Contributions))
	for _, contributor := range result.Contributions {
		out = append(out, contributorTriple{contributor.ResourceType, contributor.ResourceID, fmt.Sprint(contributor.Value)})
	}
	return out
}

func TestPostgresCellTracePreviewRoundTripServesEvidenceThroughThePort(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`},
		{exec09ctProject, exec09ctGeneration, "p-2", exec09ctAuthPath, `"female"`},
	})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))

	previewGender := map[string]string{}
	identityByLogical := map[string]string{}
	_, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: exec09ctExplorer, Limit: 50, IncludeRowIdentity: true}, func(row map[string]any) error {
		id, _ := row["patient_id"].(string)
		gender, _ := row["gender"].(string)
		cell, _ := row["__loom_row_id"].(string)
		if id == "" || cell == "" {
			t.Fatalf("preview row lacks a published id or identity: %#v", row)
		}
		previewGender[id] = gender
		identityByLogical[id] = cell
		return nil
	})
	if err != nil || len(previewGender) != 2 {
		t.Fatalf("preview must publish both scoped patients: %v (%s)", previewGender, s.scrub(err))
	}

	for _, wantID := range []string{"p-1", "p-2"} {
		before := recorder.count()
		result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
			Output: exec09ctExplorer, RowID: identityByLogical[wantID], Column: "gender", Offset: 0, Limit: 25,
		})
		if err != nil {
			t.Fatalf("CellTrace %s: %s", wantID, s.scrub(err))
		}
		if got := recorder.count() - before; got != 1 {
			t.Fatalf("trace for %s ran %d statements, want exactly one", wantID, got)
		}
		call := recorder.last()
		if call.tag != command.CompiledBackendPostgreSQL {
			t.Fatalf("trace command tag = %q", call.tag)
		}
		identityColumn := ir.PhysicalCellTraceIdentityPartsField
		if _, hasExplicit := map[string]bool{}[call.declared[2]]; !hasExplicit {
			switch call.declared[2] {
			case ir.PhysicalCellTraceIdentityPartsField, ir.PhysicalCellTraceExplicitIdentityField:
				identityColumn = call.declared[2]
			default:
				t.Fatalf("third contract column = %q, want an identity spelling", call.declared[2])
			}
		}
		wantCellTraceContract(t, call.declared, identityColumn)
		if len(call.args) == 0 {
			t.Fatal("every scope and page fact must travel as an ordered parameter")
		}
		for _, leaked := range []string{exec09ctProject, exec09ctGeneration, exec09ctAuthPath, "male", "female", identityByLogical[wantID]} {
			if strings.Contains(call.sql, leaked) {
				t.Fatalf("value %q leaked into statement text: %s", leaked, call.sql)
			}
		}
		boundScope := 0
		for _, arg := range call.args {
			if text, ok := arg.Value.(string); ok {
				switch text {
				case exec09ctProject, exec09ctGeneration:
					boundScope++
				}
			}
		}
		if boundScope != 2 {
			t.Fatalf("project and generation must arrive as ordered typed parameters: %d of 2 among %d args", boundScope, len(call.args))
		}
		for _, arg := range call.args {
			if text, ok := arg.Value.(string); ok && text == identityByLogical[wantID] {
				t.Fatalf("the requested identity must match scan-side, not arrive as a bound parameter: %s", text)
			}
		}
		if result.Status != dataframeexecution.CellTraceValue || result.Value != previewGender[wantID] {
			t.Fatalf("traced value %#v/%s drifted from the published preview value %#v", result.Value, result.Status, previewGender[wantID])
		}
		if !result.Complete || result.HasMore || result.NextOffset != 0 || result.OmissionCode != "" {
			t.Fatalf("page summary = %#v, want a complete offset-free page", result)
		}
		want := []contributorTriple{{"Patient", wantID, previewGender[wantID]}}
		if got := exec09ctCellTraceTriples(result); len(got) != 1 || got[0] != want[0] {
			t.Fatalf("%s contributors = %v, want the traced row itself %v", wantID, got, want)
		}
		again, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
			Output: exec09ctExplorer, RowID: identityByLogical[wantID], Column: "gender", Offset: 0, Limit: 25,
		})
		if err != nil || again.Status != dataframeexecution.CellTraceValue || again.Value != previewGender[wantID] {
			t.Fatalf("page drifted between executions: %#v (%s)", again, s.scrub(err))
		}
	}
}

func TestPostgresCellTraceNullValueStatesStayDistinctFromAValue(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`},
		{exec09ctProject, exec09ctGeneration, "p-2", exec09ctAuthPath, "null"},
		{exec09ctProject, exec09ctGeneration, "p-3", exec09ctAuthPath, ""},
	})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))

	for _, wantID := range []string{"p-1", "p-2", "p-3"} {
		identity := s.previewIdentity(t, engine, resolved, wantID)
		before := recorder.count()
		result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
			Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: 0, Limit: 25,
		})
		if err != nil {
			t.Fatalf("CellTrace %s: %s", wantID, s.scrub(err))
		}
		if got := recorder.count() - before; got != 1 {
			t.Fatalf("%s ran %d statements, want exactly one", wantID, got)
		}
		switch wantID {
		case "p-1":
			if result.Status != dataframeexecution.CellTraceValue || result.Value != "male" || len(result.Contributions) != 1 {
				t.Fatalf("value state = %#v, want VALUE \"male\" with the self contributor", result)
			}
		case "p-2", "p-3":
			if result.Status != dataframeexecution.CellTraceNoMatch {
				t.Fatalf("%s status = %s (%#v), want NO_MATCH — the registered arm drops the null candidate before the status reads", wantID, result.Status, result.Value)
			}
			if result.Value != nil || len(result.Contributions) != 0 || result.HasMore {
				t.Fatalf("%s must publish a nil value and an empty page: %#v", wantID, result)
			}
		}
	}
}

func TestPostgresCellTraceOffsetGateSkipsTheSoleCandidateWithoutChangingStatus(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`}})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))
	identity := s.previewIdentity(t, engine, resolved, "p-1")

	for _, offset := range []int{1, 2} {
		before := recorder.count()
		result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
			Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: offset, Limit: 25,
		})
		if err != nil {
			t.Fatalf("CellTrace offset %d: %s", offset, s.scrub(err))
		}
		if got := recorder.count() - before; got != 1 {
			t.Fatalf("offset %d ran %d statements, want exactly one", offset, got)
		}
		if result.Status != dataframeexecution.CellTraceValue || result.Value != "male" {
			t.Fatalf("offset %d: status/value must stay offset-free: %#v", offset, result)
		}
		if len(result.Contributions) != 0 || result.HasMore || !result.Complete {
			t.Fatalf("offset %d must publish the empty page: %#v", offset, result)
		}
	}
	before := recorder.count()
	control, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil {
		t.Fatalf("offset-0 control: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("control ran %d statements, want exactly one", got)
	}
	if want := []contributorTriple{{"Patient", "p-1", "male"}}; !equalExec09Triples(exec09ctCellTraceTriples(control), want) {
		t.Fatalf("offset-0 control must publish the self contributor (the gate is not always-empty): %v", exec09ctCellTraceTriples(control))
	}
}

func equalExec09Triples(got, want []contributorTriple) bool {
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

func TestPostgresCellTraceAuthoredIdentityResolvesThroughTheExplicitIdentityColumn(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`},
		{exec09ctProject, exec09ctGeneration, "p-2", exec09ctAuthPath, `"female"`},
	})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))
	identity := s.previewIdentity(t, engine, resolved, "p-1")

	before := recorder.count()
	result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil {
		t.Fatalf("CellTrace on the authored-Identity output: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("authored-identity trace ran %d statements, want exactly one", got)
	}
	call := recorder.last()
	wantCellTraceContract(t, call.declared, ir.PhysicalCellTraceExplicitIdentityField)
	if result.Status != dataframeexecution.CellTraceValue || result.Value != "male" {
		t.Fatalf("authored-identity evidence = %#v, want VALUE \"male\"", result)
	}
	if want := []contributorTriple{{"Patient", "p-1", "male"}}; !equalExec09Triples(exec09ctCellTraceTriples(result), want) {
		t.Fatalf("authored-identity contributors = %v, want %v", exec09ctCellTraceTriples(result), want)
	}
	other := s.previewIdentity(t, engine, resolved, "p-2")
	female, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: other, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil || female.Status != dataframeexecution.CellTraceValue || female.Value != "female" {
		t.Fatalf("authored-identity control = %#v (%s)", female, s.scrub(err))
	}
}

func TestPostgresCellTraceForgedIdentityIsTypedNotFoundAndRealIdentityResolves(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`}})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))

	forgedDigest := sha256.Sum256([]byte(`["nobody"]`))
	forged := hex.EncodeToString(forgedDigest[:])
	before := recorder.count()
	_, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: forged, Column: "gender", Offset: 0, Limit: 25,
	})
	if !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		t.Fatalf("forged identity = %v, want the typed not-found", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("forged-identity trace ran %d statements, want exactly one", got)
	}
	identity := s.previewIdentity(t, engine, resolved, "p-1")
	result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil || result.Status != dataframeexecution.CellTraceValue || result.Value != "male" {
		t.Fatalf("real identity failed on the same fixture: %#v (%s)", result, s.scrub(err))
	}
}

func TestPostgresCellTraceWitnessCapIsIncompleteNotAbsence(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`},
		{exec09ctProject, exec09ctGeneration, "p-2", exec09ctAuthPath, `"female"`},
	})
	probe := &exec09ctRecorder{adapter: s.adapter, capture: true}
	engine := s.engine(t, probe)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(false))

	forgedDigest := sha256.Sum256([]byte(`["nobody"]`))
	if _, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: hex.EncodeToString(forgedDigest[:]), Column: "gender", Offset: 0, Limit: 25,
	}); !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		t.Fatalf("uncapped probe = %v, want the typed not-found", s.scrub(err))
	}
	rows := probe.capturedRows()
	if len(rows) != 2 {
		t.Fatalf("probe captured %d rows, want the two scoped patients", len(rows))
	}
	first := exec09ctRowIdentity(t, rows, "p-1")
	second := exec09ctRowIdentity(t, rows, "p-2")
	target, firstID := second, first
	if digest := exec09ctIdentityDigestOf(t, rows[0][ir.PhysicalCellTraceIdentityPartsField]); digest == second {
		target = first
		firstID = second
	}
	if digest := exec09ctIdentityDigestOf(t, rows[0][ir.PhysicalCellTraceIdentityPartsField]); digest == target {
		t.Fatalf("aiming witness failed: the first visited row is the target")
	}
	beforeCapped := probe.count()
	capped, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: target, Column: "gender", Offset: 0, Limit: 25, MaxWitnessRows: 1,
	})
	if err != nil {
		t.Fatalf("capped trace: %s", s.scrub(err))
	}
	if capped.Status != dataframeexecution.CellTraceIncomplete || capped.OmissionCode != "TRACE_WITNESS_INCOMPLETE" || capped.Complete {
		t.Fatalf("capped trace = %#v, want INCOMPLETE/TRACE_WITNESS_INCOMPLETE, never absence", capped)
	}
	if got := probe.count() - beforeCapped; got != 1 {
		t.Fatalf("capped trace ran %d statements, want exactly one", got)
	}
	uncapped, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: target, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil || uncapped.Status != dataframeexecution.CellTraceValue {
		t.Fatalf("uncapped control = %#v (%s), want the row resolved", uncapped, s.scrub(err))
	}
	firstUncapped, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: firstID, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil || firstUncapped.Status != dataframeexecution.CellTraceValue {
		t.Fatalf("first-visited control = %#v (%s), want the row resolved", firstUncapped, s.scrub(err))
	}
}

func TestPostgresCellTraceForeignWitnessesAreExcluded(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"female"`},
		{exec09ctProject, exec09ctGeneration, "p-2", exec09ctAuthPath, `"female"`},
		{"project-b", exec09ctGeneration, "p-foreign-project", exec09ctAuthPath, `"female"`},
		{exec09ctProject, "gen-b", "p-foreign-generation", exec09ctAuthPath, `"female"`},
		{exec09ctProject, exec09ctGeneration, "p-foreign-auth", "FHIR/Restricted", `"female"`},
	})
	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved := s.resolve(t, engine, exec09ctProject, exec09ctGeneration, exec09ctPatientsBundle(true))
	identity := s.previewIdentity(t, engine, resolved, "p-1")

	before := recorder.count()
	result, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "gender", Offset: 0, Limit: 25,
	})
	if err != nil {
		t.Fatalf("CellTrace: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("trace ran %d statements, want exactly one", got)
	}
	if result.Status != dataframeexecution.CellTraceValue || result.Value != "female" {
		t.Fatalf("in-scope trace = %#v", result)
	}
	if want := []contributorTriple{{"Patient", "p-1", "female"}}; !equalExec09Triples(exec09ctCellTraceTriples(result), want) {
		t.Fatalf("foreign witnesses leaked: contributors = %v, want %v", exec09ctCellTraceTriples(result), want)
	}
	if result.HasMore {
		t.Fatalf("foreign witnesses inflated hasMore: %#v", result)
	}

	foreignDigest := sha256.Sum256([]byte(`["p-foreign-project"]`))
	if _, err := engine.CellTrace(s.ctx, resolved, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: hex.EncodeToString(foreignDigest[:]), Column: "gender", Offset: 0, Limit: 25,
	}); !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		t.Fatalf("foreign identity = %v, want the typed not-found", s.scrub(err))
	}

	seen := 0
	_, err = engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: exec09ctExplorer, Limit: 50}, func(row map[string]any) error {
		seen++
		id, _ := row["patient_id"].(string)
		if id != "p-1" && id != "p-2" {
			t.Fatalf("out-of-scope witness row reached the preview: %#v", row)
		}
		return nil
	})
	if err != nil || seen != 2 {
		t.Fatalf("in-scope preview affected by foreign witnesses: rows=%d err=%s", seen, s.scrub(err))
	}
}

func exec09ctLowerArmA(t *testing.T, s *exec09ctService, all bool) lower.CompiledRecipeOutput {
	t.Helper()
	bundle := exec09ctArmABundle(all)
	if err := bundle.Validate(); err != nil {
		t.Fatalf("bundle validate: %v", err)
	}
	plan, err := semantic.BuildRecipePlan(bundle, exec09ctBindings(exec09ctProject, exec09ctGeneration))
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, exec09ctProject, exec09ctGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy().
		WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false))
	if err != nil {
		t.Fatalf("lower arm A: %s", s.scrub(err))
	}
	for _, candidate := range compiled.Outputs {
		if candidate.Name == exec09ctExplorer {
			return candidate
		}
	}
	t.Fatalf("lowered outputs do not carry %q", exec09ctExplorer)
	return lower.CompiledRecipeOutput{}
}

func TestPostgresCellTraceArmAReducedSetPagesThroughThePublicSeam(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, ""},
		{exec09ctProject, exec09ctGeneration, "p-none", exec09ctAuthPath, ""},
	})
	s.seedEdges(t, "p-1", []string{"c-1", "c-2", "c-3"})

	recorder := &exec09ctRecorder{adapter: s.adapter, capture: true}
	engine := s.engine(t, recorder)
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false)
	output := exec09ctLowerArmA(t, s, true)

	compiled, err := compiler.CompileCellTraceOutput(s.target, output, "condition_id", 0, 2, policy)
	if err != nil {
		t.Fatalf("CompileCellTraceOutput (ALL mode): %s", s.scrub(err))
	}
	if compiled.ValueColumn != ir.PhysicalCellTraceValueField || compiled.StatusColumn != ir.PhysicalCellTraceStatusField ||
		compiled.ContributionsColumn != ir.PhysicalCellTraceContributionsField || compiled.HasMoreColumn != ir.PhysicalCellTraceHasMoreField ||
		compiled.OmissionColumn != ir.PhysicalCellTraceOmissionField {
		t.Fatalf("arm A compiled contract = %#v", compiled)
	}

	forgedDigest := sha256.Sum256([]byte(`["nobody"]`))
	if _, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: hex.EncodeToString(forgedDigest[:]), Column: "condition_id", Offset: 0, Limit: 2,
	}); !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		t.Fatalf("arm A probe = %v, want the typed not-found", s.scrub(err))
	}
	rows := recorder.capturedRows()
	if len(rows) != 2 {
		t.Fatalf("arm A probe captured %d rows, want the two scoped patients", len(rows))
	}
	identity := exec09ctRowIdentity(t, rows, "p-1")
	noneIdentity := exec09ctRowIdentity(t, rows, "p-none")

	before := recorder.count()
	page1, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "condition_id", Offset: 0, Limit: 2,
	})
	if err != nil {
		t.Fatalf("arm A page 1: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("arm A page 1 ran %d statements, want exactly one", got)
	}
	call := recorder.last()
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("arm A command tag = %q", call.tag)
	}
	wantCellTraceContract(t, call.declared, ir.PhysicalCellTraceIdentityPartsField)
	if page1.Status != dataframeexecution.CellTraceValue {
		t.Fatalf("arm A status = %s (%#v), want VALUE", page1.Status, page1)
	}
	reduced, ok := page1.Value.([]any)
	if !ok || len(reduced) != 3 {
		t.Fatalf("ALL-mode value column = %#v, want the published three-item reduced array", page1.Value)
	}
	if want := []contributorTriple{{"Condition", "c-1", "c-1"}, {"Condition", "c-2", "c-2"}}; !equalExec09Triples(exec09ctCellTraceTriples(page1), want) {
		t.Fatalf("arm A page 1 = %v, want %v", exec09ctCellTraceTriples(page1), want)
	}
	if !page1.HasMore || page1.NextOffset != 2 {
		t.Fatalf("arm A page 1 summary = %#v, want HasMore with NextOffset 2", page1)
	}

	compiled2, err := compiler.CompileCellTraceOutput(s.target, output, "condition_id", 2, 2, policy)
	if err != nil {
		t.Fatalf("CompileCellTraceOutput offset 2: %s", s.scrub(err))
	}
	before = recorder.count()
	page2, err := engine.CellTraceCompiled(s.ctx, compiled2, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: identity, Column: "condition_id", Offset: 2, Limit: 2,
	})
	if err != nil {
		t.Fatalf("arm A page 2: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("arm A page 2 ran %d statements, want exactly one", got)
	}
	if want := []contributorTriple{{"Condition", "c-3", "c-3"}}; !equalExec09Triples(exec09ctCellTraceTriples(page2), want) {
		t.Fatalf("arm A page 2 = %v, want %v", exec09ctCellTraceTriples(page2), want)
	}
	if page2.HasMore || page2.NextOffset != 0 {
		t.Fatalf("arm A page 2 summary = %#v, want the terminal page", page2)
	}

	before = recorder.count()
	nonePage, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: noneIdentity, Column: "condition_id", Offset: 0, Limit: 2,
	})
	if err != nil {
		t.Fatalf("arm A no-contributor row: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("arm A zero-contributor row ran %d statements, want exactly one", got)
	}
	if nonePage.Status != dataframeexecution.CellTraceNoMatch || len(nonePage.Contributions) != 0 || nonePage.HasMore {
		t.Fatalf("arm A zero-contributor row = %#v, want NO_MATCH with an empty page", nonePage)
	}
}

func TestPostgresCellTraceArmALossyFirstReadsAmbiguous(t *testing.T) {
	s := newExec09ctService(t)
	s.seed(t, []exec09ctSeedRow{
		{exec09ctProject, exec09ctGeneration, "p-1", exec09ctAuthPath, ""},
		{exec09ctProject, exec09ctGeneration, "p-solo", exec09ctAuthPath, ""},
	})
	s.seedEdges(t, "p-1", []string{"c-1", "c-2", "c-3"})
	s.seedEdges(t, "p-solo", []string{"c-9"})

	recorder := &exec09ctRecorder{adapter: s.adapter, capture: true}
	engine := s.engine(t, recorder)
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false)
	output := exec09ctLowerArmA(t, s, false)
	compiled, err := compiler.CompileCellTraceOutput(s.target, output, "condition_id", 0, 25, policy)
	if err != nil {
		t.Fatalf("CompileCellTraceOutput (FIRST mode): %s", s.scrub(err))
	}

	forgedDigest := sha256.Sum256([]byte(`["nobody"]`))
	if _, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: hex.EncodeToString(forgedDigest[:]), Column: "condition_id", Offset: 0, Limit: 25,
	}); !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		t.Fatalf("FIRST probe = %v, want the typed not-found", s.scrub(err))
	}
	rows := recorder.capturedRows()
	ambiguousID := exec09ctRowIdentity(t, rows, "p-1")
	soloID := exec09ctRowIdentity(t, rows, "p-solo")

	before := recorder.count()
	ambiguous, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: ambiguousID, Column: "condition_id", Offset: 0, Limit: 25,
	})
	if err != nil {
		t.Fatalf("FIRST ambiguous page: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("FIRST ambiguous page ran %d statements, want exactly one", got)
	}
	if ambiguous.Status != dataframeexecution.CellTraceAmbiguous {
		t.Fatalf("FIRST page over three candidates = %s (%#v), want AMBIGUOUS — the compiler cannot order the ties", ambiguous.Status, ambiguous)
	}
	if len(ambiguous.Contributions) == 0 || len(ambiguous.Contributions) > 25 {
		t.Fatalf("AMBIGUOUS page must still serve its bounded contributors: %#v", ambiguous)
	}

	solo, err := engine.CellTraceCompiled(s.ctx, compiled, dataframeexecution.CellTraceRequest{
		Output: exec09ctExplorer, RowID: soloID, Column: "condition_id", Offset: 0, Limit: 25,
	})
	if err != nil || solo.Status != dataframeexecution.CellTraceValue {
		t.Fatalf("solo control = %#v (%s), want VALUE", solo, s.scrub(err))
	}
}

type exec09ctExplorerStore struct {
	explorer.Store
}

type exec09ctAuthorized struct {
	mu        sync.Mutex
	snapshot  capability.Snapshot
	scope     authscope.ReadScope
	hookCalls int
}

func (a *exec09ctAuthorized) set(snapshot capability.Snapshot, scope authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot = snapshot
	a.scope = scope
}

func (a *exec09ctAuthorized) authorized() (capability.Snapshot, authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot, a.scope
}

func (a *exec09ctAuthorized) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hookCalls
}

func exec09ctScopeDigest(scope authscope.ReadScope) string {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:])
}

func exec09ctSnapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	return capability.Snapshot{
		Identity: capability.SnapshotIdentity{
			Project: project, Generation: generation,
			AuthorizationScopeDigest: exec09ctScopeDigest(scope),
			SchemaDigest:             "exec09-schema", ShapeDigest: "exec09-shape",
		},
		Status: capability.StatusReady, Complete: true, Token: token,
	}
}

func exec09ctReceipt(t *testing.T, snapshot capability.Snapshot) explorer.CompilationReceipt {
	t.Helper()
	bundle := exec09ctPatientsBundle(true)
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient","logicalType":"string","filterable":true,"chartable":false},{"column":"gender","label":"Gender","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	emitted := []explorer.EmittedColumn{
		{OutputID: exec09ctExplorer, EmissionID: "em_patient_id", PublicColumn: "patient_id", Label: "Patient", LogicalType: "string", Filterable: true},
		{OutputID: exec09ctExplorer, EmissionID: "em_gender", PublicColumn: "gender", Label: "Gender", LogicalType: "string", Filterable: true, Chartable: true},
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatalf("bundle digest: %v", err)
	}
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:     explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion:  explorer.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               exec09ctExplorer,
		IntentDigest:             "exec09-intent",
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		RecipeDigest:             bundleDigest,
		ResolvedRecipeDigest:     bundleDigest,
		ResolvedSchemaDigest:     "exec09-resolved-schema",
		CompiledConfig:           json.RawMessage(`{}`),
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		EmittedColumns:           emitted,
		OutputColumnProvenance:   map[string]map[string]string{exec09ctExplorer: {"patient_id": "EXPLICIT", "gender": "EXPLICIT"}},
	}
	receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(contract)
	if err != nil {
		t.Fatalf("contract digest: %v", err)
	}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		t.Fatalf("compilation key: %v", err)
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatalf("receipt id: %v", err)
	}
	return receipt
}

func exec09ctLifecycleService(t *testing.T, authorized *exec09ctAuthorized, engine *dataframeexecution.Engine, receipts ...explorer.CompilationReceipt) *lifecycle.Service {
	t.Helper()
	byID := map[string]explorer.CompilationReceipt{}
	for _, receipt := range receipts {
		byID[receipt.ID] = receipt
	}
	domain, err := explorer.NewService(&exec09ctExplorerStore{})
	if err != nil {
		t.Fatalf("explorer service: %v", err)
	}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				snapshot, scope := authorized.authorized()
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
		},
		ReceiptLookup: func(_ context.Context, _, _, receiptID string) (*explorer.CompilationReceipt, error) {
			got, ok := byID[receiptID]
			if !ok {
				return nil, fmt.Errorf("receipt %q is not in this test's store", receiptID)
			}
			return &got, nil
		},
	}
	config.CellTrace = func(ctx context.Context, got *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		authorized.mu.Lock()
		authorized.hookCalls++
		authorized.mu.Unlock()
		resolved, err := engine.CompileResolvedBundle(ctx, got.Bundle, bindings)
		if err != nil {
			return dataframeexecution.CellTraceResult{}, err
		}
		return engine.CellTrace(ctx, resolved, request)
	}
	service, err := lifecycle.New(domain, config)
	if err != nil {
		t.Fatalf("lifecycle service: %v", err)
	}
	return service
}

func exec09ctLifecycleRequest(receipt explorer.CompilationReceipt, rowID string, offset, limit int) lifecycle.CellTraceRequest {
	return lifecycle.CellTraceRequest{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID,
		OutputID: exec09ctExplorer, RowID: rowID, Column: "gender", Offset: offset, Limit: limit,
	}
}

func TestPostgresCellTraceStaleReceiptClassification(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{exec09ctAuthPath}}
	fresh := exec09ctSnapshot(exec09ctProject, exec09ctGeneration, "token-1", scope)
	receipt := exec09ctReceipt(t, fresh)
	authorized := &exec09ctAuthorized{snapshot: fresh, scope: scope}
	var calls []exec09ctCall
	var mu sync.Mutex

	fakeAdapter := func(_ context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		sqlText, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		mu.Lock()
		calls = append(calls, exec09ctCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
		mu.Unlock()
		return nil
	}
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	config := exec04pvForbidArango()
	config.CompileTarget = target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec09-scope" }
	config.SourceRows = fakeAdapter
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	service := exec09ctLifecycleService(t, authorized, engine, receipt)

	statementCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(calls)
	}

	hooksBefore := authorized.calls()
	statementsBefore := statementCount()
	_, err = service.CellTrace(context.Background(), exec09ctLifecycleRequest(receipt, "offline-identity", 0, 25))
	notFound := errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound)
	var lifecycleErr *lifecycle.Error
	previewMissing := errors.As(err, &lifecycleErr) && lifecycleErr.Class == lifecycle.ClassNotFound
	if err != nil && !notFound && !previewMissing {
		t.Fatalf("fresh control failed at the hook: %v", err)
	}
	if authorized.calls() != hooksBefore+1 {
		t.Fatalf("fresh control hook witness: %d invocations, want 1", authorized.calls()-hooksBefore)
	}
	if statementCount()-statementsBefore < 1 {
		t.Fatalf("fresh control executed %d statements, want at least one", statementCount()-statementsBefore)
	}

	for _, test := range []struct {
		name   string
		mutate func(snapshot capability.Snapshot, scope authscope.ReadScope) (capability.Snapshot, authscope.ReadScope)
	}{
		{"generation moved on", func(snapshot capability.Snapshot, scope authscope.ReadScope) (capability.Snapshot, authscope.ReadScope) {
			snapshot.Identity.Generation = "gen-b"
			return snapshot, scope
		}},
		{"authorization scope digest changed", func(snapshot capability.Snapshot, scope authscope.ReadScope) (capability.Snapshot, authscope.ReadScope) {
			scope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"FHIR/Other"}}
			snapshot.Identity.AuthorizationScopeDigest = exec09ctScopeDigest(scope)
			return snapshot, scope
		}},
		{"snapshot token rotated", func(snapshot capability.Snapshot, scope authscope.ReadScope) (capability.Snapshot, authscope.ReadScope) {
			snapshot.Token = "token-2"
			return snapshot, scope
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			authorized.set(test.mutate(fresh, scope))
			hooksBefore := authorized.calls()
			statementsBefore := statementCount()
			_, err := service.CellTrace(context.Background(), exec09ctLifecycleRequest(receipt, "offline-identity", 0, 25))
			var lifecycleErr *lifecycle.Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != lifecycle.ClassConflict || lifecycleErr.Code != "RECEIPT_STALE" {
				t.Fatalf("stale receipt error = %v, want ClassConflict/RECEIPT_STALE", err)
			}
			if authorized.calls() != hooksBefore {
				t.Fatalf("stale receipt reached the cell-trace hook: %d extra invocations", authorized.calls()-hooksBefore)
			}
			if executed := statementCount() - statementsBefore; executed != 0 {
				t.Fatalf("stale receipt executed %d statements, want the statement-count-0 witness", executed)
			}
		})
	}

	authorized.set(fresh, scope)
	hooksBefore = authorized.calls()
	if _, err := service.CellTrace(context.Background(), exec09ctLifecycleRequest(receipt, "offline-identity", 0, 25)); err != nil && !errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
		var lifecycleErr *lifecycle.Error
		if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != lifecycle.ClassNotFound {
			t.Fatalf("restored fresh receipt failed: %v", err)
		}
	}
	if authorized.calls() != hooksBefore+1 {
		t.Fatalf("restored fresh receipt did not reach the hook: %d invocations", authorized.calls()-hooksBefore)
	}
}

func TestPostgresCellTraceStaleReceiptRejectsBeforeExecution(t *testing.T) {
	s := newExec09ctService(t)
	seedProject := projectid.Legacy(exec09ctProject)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{exec09ctAuthPath}}
	fresh := exec09ctSnapshot(exec09ctProject, exec09ctGeneration, "token-1", scope)
	receipt := exec09ctReceipt(t, fresh)

	s.seed(t, []exec09ctSeedRow{
		{seedProject, exec09ctGeneration, "p-1", exec09ctAuthPath, `"male"`},
		{seedProject, "gen-b", "p-new", exec09ctAuthPath, `"male"`},
	})

	recorder := &exec09ctRecorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	authorized := &exec09ctAuthorized{snapshot: fresh, scope: scope}
	current := exec09ctSnapshot(exec09ctProject, "gen-b", "token-2", scope)
	currentReceipt := exec09ctReceipt(t, current)
	service := exec09ctLifecycleService(t, authorized, engine, receipt, currentReceipt)

	identity := s.previewIdentity(t, engine, s.resolve(t, engine, seedProject, exec09ctGeneration, exec09ctPatientsBundle(true)), "p-1")
	result, err := service.CellTrace(s.ctx, exec09ctLifecycleRequest(receipt, identity, 0, 25))
	if err != nil || result.Trace.Status != dataframeexecution.CellTraceValue || result.Trace.Value != "male" {
		t.Fatalf("fresh receipt failed on live state: %#v %s", result, s.scrub(err))
	}

	stale := fresh
	stale.Identity.Generation = "gen-b"
	authorized.set(stale, scope)
	hooksBefore := authorized.calls()
	statementsBefore := recorder.count()
	_, err = service.CellTrace(s.ctx, exec09ctLifecycleRequest(receipt, identity, 0, 25))
	var lifecycleErr *lifecycle.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != lifecycle.ClassConflict || lifecycleErr.Code != "RECEIPT_STALE" {
		t.Fatalf("stale receipt error = %v, want ClassConflict/RECEIPT_STALE", s.scrub(err))
	}
	if authorized.calls() != hooksBefore {
		t.Fatalf("stale receipt reached the engine hook: %d extra invocations", authorized.calls()-hooksBefore)
	}
	if got := recorder.count() - statementsBefore; got != 0 {
		t.Fatalf("stale receipt executed %d statements, want the statement-count-0 witness at the production adapter", got)
	}

	authorized.set(current, scope)
	newIdentity := s.previewIdentity(t, engine, s.resolve(t, engine, seedProject, "gen-b", exec09ctPatientsBundle(true)), "p-new")
	recovered, err := service.CellTrace(s.ctx, exec09ctLifecycleRequest(currentReceipt, newIdentity, 0, 25))
	if err != nil {
		t.Fatalf("current receipt failed: %s", s.scrub(err))
	}
	if recovered.Trace.Status != dataframeexecution.CellTraceValue || recovered.Trace.Value != "male" {
		t.Fatalf("current receipt trace = %#v, want the gen-b cell", recovered.Trace)
	}
}

func TestZZPostgresCellTraceZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return
	}
	exec09ctCaptureBaseline(raw)
	if exec09ctBaselineErr != nil {
		t.Fatalf("baseline: %v", exec09ctBaselineErr)
	}
	baseline := map[string]bool{}
	for _, name := range exec09ctBaselineNames {
		baseline[name] = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %v", err)
	}
	defer admin.Close()
	var survivors []string
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec09\_ct\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatalf("audit databases: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan audit: %v", err)
		}
		if !baseline[name] {
			survivors = append(survivors, name)
		}
	}
	rows.Close()
	rows, err = admin.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec09\_ct\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatalf("audit schemas: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan audit: %v", err)
		}
		if !baseline[name] {
			survivors = append(survivors, name)
		}
	}
	rows.Close()
	if len(survivors) != 0 {
		sort.Strings(survivors)
		t.Fatalf("verify_exec09_ct_* fixtures survived the run: %v (owned registry: %v)", survivors, exec09ctRegistrySnapshot())
	}
}
