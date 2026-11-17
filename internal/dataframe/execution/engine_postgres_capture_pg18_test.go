package execution_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
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
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec11lvPrefix  = "verify_exec11_"
	exec11lvNameFmt = exec11lvPrefix + "%d_%s"
)

var exec11lvNamePattern = regexp.MustCompile(`^verify_exec11_[0-9]+_[0-9a-f]+$`)

var exec11lvIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func clickhouseIdentifier(name string) bool { return exec11lvIdentifierRE.MatchString(name) }

var (
	exec11lvOwnedMu     sync.Mutex
	exec11lvOwnedByName = map[string]string{}
)

func exec11lvRegister(db, schema string) {
	exec11lvOwnedMu.Lock()
	defer exec11lvOwnedMu.Unlock()
	exec11lvOwnedByName[db] = schema
}

func exec11lvRegistrySnapshot() map[string]string {
	exec11lvOwnedMu.Lock()
	defer exec11lvOwnedMu.Unlock()
	out := make(map[string]string, len(exec11lvOwnedByName))
	for db, schema := range exec11lvOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec11lvBaselineOnce  sync.Once
	exec11lvBaselineNames []string
	exec11lvBaselineErr   error
)

func exec11lvCaptureBaseline(raw string) {
	exec11lvBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec11lvBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec11\\_%' ESCAPE '\\'")
		if err != nil {
			exec11lvBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec11lvBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec11lvBaselineNames = append(exec11lvBaselineNames, name)
		}
	})
}

type exec11lvService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	dsn      string
	scrub    func(any) string
	fail     func(step string, err error)
	executor dataframeexecution.SourceRows
}

func newExec11lvService(t *testing.T) *exec11lvService {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 private-capture acceptance tests")
	}
	scrub, addSecret := exec03pqScrubberFor(raw)
	exec11lvCaptureBaseline(raw)
	if exec11lvBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec11lvBaselineErr))
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

	dbName := fmt.Sprintf(exec11lvNameFmt, time.Now().UnixNano(), exec03pqRandomHex(t))
	if !exec11lvNamePattern.MatchString(dbName) {
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
	exec11lvRegister(dbName, "")
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx), `DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	u, err := parseExec11lvURL(raw)
	if err != nil {
		t.Fatal(scrub(err))
	}
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

	schema := fmt.Sprintf(exec11lvNameFmt, time.Now().UnixNano(), exec03pqRandomHex(t))
	if !exec11lvNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec11lvRegister(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	executor, err := postgres.NewSourceRows(iso)
	if err != nil {
		fail("create PostgreSQL row executor", err)
	}
	return &exec11lvService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, scrub: scrub, fail: fail, executor: executor}
}

func (s *exec11lvService) compileTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(s.schema)
	if err != nil {
		t.Fatalf("bind PostgreSQL storage placement: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("select PostgreSQL compile target: %v", err)
	}
	return target
}

func (s *exec11lvService) migrateAndSeed(t *testing.T) {
	t.Helper()
	client, err := storepg.OpenPool(s.ctx, storepg.ClientConfig{DSN: s.dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
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
	type seedRow struct{ project, generation, kind, logicalID, authPath, payload string }
	fixture := []seedRow{
		{"project-a", "generation-2", "Patient", "exec11-ada", "/programs/a", `{"id":"patient-ada"}`},
		{"project-a", "generation-2", "Patient", "exec11-grace", "/programs/a", `{"id":"patient-grace"}`},
		{"project-a", "generation-2", "Patient", "exec11-alan", "/programs/a", `{"id":"patient-alan"}`},
		{"project-a", "generation-2", "Patient", "exec11-hidden", "/programs/b", `{"id":"patient-hidden"}`},
		{"project-b", "generation-2", "Patient", "exec11-intruder", "/programs/a", `{"id":"patient-intruder"}`},
		{"project-a", "generation-other", "Patient", "exec11-stale", "/programs/a", `{"id":"patient-stale"}`},
	}
	for _, row := range fixture {
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project: row.project, DatasetGeneration: row.generation, ResourceType: row.kind,
			LogicalID: row.logicalID, AuthResourcePath: row.authPath, Payload: []byte(row.payload),
		}); err != nil {
			s.fail("insert fixture resource "+row.logicalID, err)
		}
	}
}

func (s *exec11lvService) witnessIDs(t *testing.T) []string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, fmt.Sprintf(
		`SELECT payload->>'id' FROM "%s".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND auth_resource_path=$3 ORDER BY 1`,
		s.schema), "project-a", "generation-2", "/programs/a")
	if err != nil {
		s.fail("direct-SQL witness", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			s.fail("scan witness row", err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness rows", err)
	}
	return got
}

func parseExec11lvURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, errors.New("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	return u, nil
}

type exec11lvInvalidRegistry struct{}

func (exec11lvInvalidRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exec-11 capture battery compiles authored bundles only")
}

func (exec11lvInvalidRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exec-11 capture battery compiles authored bundles only")
}

type exec11lvCatalog struct {
	mu    sync.Mutex
	items map[string]chartifact.Manifest
}

func (c *exec11lvCatalog) Create(_ context.Context, value chartifact.Manifest) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[value.ArtifactID] = value
	return nil
}

func (c *exec11lvCatalog) Update(_ context.Context, id, owner string, progress chartifact.Progress) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	value.State, value.RowCount, value.ByteCount, value.UpdatedAt = progress.State, progress.RowCount, progress.ByteCount, progress.UpdatedAt
	if progress.LeaseUntil.After(value.LeaseUntil) || progress.State == chartifact.StateCleanupPending {
		value.LeaseUntil = progress.LeaseUntil
	}
	c.items[id] = value
	return nil
}

func (c *exec11lvCatalog) Renew(_ context.Context, id, owner string, until time.Time) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return false, nil
	}
	if until.After(value.LeaseUntil) {
		value.LeaseUntil = until
		c.items[id] = value
	}
	return true, nil
}

func (c *exec11lvCatalog) ListExpired(_ context.Context, before time.Time, limit int) ([]chartifact.Manifest, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]chartifact.Manifest, 0)
	for _, item := range c.items {
		if !item.LeaseUntil.After(before) {
			result = append(result, item)
		}
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (c *exec11lvCatalog) ClaimCleanup(_ context.Context, id, owner string, expiredBefore, until time.Time) (chartifact.Manifest, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseUntil.After(expiredBefore) {
		return chartifact.Manifest{}, false, nil
	}
	value.LeaseOwner, value.LeaseUntil, value.State = owner, until, chartifact.StateCleanupPending
	c.items[id] = value
	return value, true, nil
}

func (c *exec11lvCatalog) Delete(_ context.Context, id, owner string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	delete(c.items, id)
	return nil
}

func (c *exec11lvCatalog) ReleaseCleanup(_ context.Context, id, owner string, until time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.items[id]
	if !ok || value.LeaseOwner != owner {
		return chartifact.ErrLeaseLost
	}
	value.LeaseOwner, value.LeaseUntil = "", until
	c.items[id] = value
	return nil
}

func (c *exec11lvCatalog) snapshot() []chartifact.Manifest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]chartifact.Manifest, 0, len(c.items))
	for _, manifest := range c.items {
		out = append(out, manifest)
	}
	return out
}

func (c *exec11lvCatalog) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

type exec11lvClickHouse struct {
	mu    sync.Mutex
	table map[string][]clickhouse.Column
	rows  map[string][]map[string]any
}

func (c *exec11lvClickHouse) CreateTable(_ context.Context, table string, columns []clickhouse.Column) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.table[table] = append([]clickhouse.Column(nil), columns...)
	c.rows[table] = nil
	return nil
}

func (c *exec11lvClickHouse) InsertRows(_ context.Context, table string, columns []clickhouse.Column, rows []map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.table[table], columns) {
		return fmt.Errorf("unexpected insert schema for %s", table)
	}
	for _, row := range rows {
		copied := make(map[string]any, len(row))
		for key, value := range row {
			copied[key] = value
		}
		c.rows[table] = append(c.rows[table], copied)
	}
	return nil
}

func (c *exec11lvClickHouse) VerifyOutput(_ context.Context, table string, columns []clickhouse.Column, expected int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !reflect.DeepEqual(c.table[table], columns) || int64(len(c.rows[table])) != expected {
		return fmt.Errorf("artifact verification mismatch for %s", table)
	}
	return nil
}

func (c *exec11lvClickHouse) DropTable(_ context.Context, table string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.table, table)
	delete(c.rows, table)
	return nil
}

func (c *exec11lvClickHouse) rowsSnapshot(table string) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows := c.rows[table]
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		copied := make(map[string]any, len(row))
		for key, value := range row {
			copied[key] = value
		}
		out = append(out, copied)
	}
	return out
}

func (c *exec11lvClickHouse) allRows() map[string][]map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]map[string]any, len(c.rows))
	for table, rows := range c.rows {
		if len(rows) == 0 {
			continue
		}
		copied := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			item := make(map[string]any, len(row))
			for key, value := range row {
				item[key] = value
			}
			copied = append(copied, item)
		}
		out[table] = copied
	}
	return out
}

type exec11lvPorts struct {
	combineQueries []string
	combineArgs    [][]any
	seenDuringRun  []chartifact.Manifest
	capturedRows   map[string][]map[string]any
	visibleDuringQ int
	pinRevisions   [][]string
	resolveCalls   int
}

func newExec11lvPorts() *exec11lvPorts {
	return &exec11lvPorts{capturedRows: map[string][]map[string]any{}}
}

func (p *exec11lvPorts) queryRows(catalog *exec11lvCatalog, ch *exec11lvClickHouse, visitRows []map[string]any) dataframeexecution.ClickHouseQueryRows {
	return func(_ context.Context, query string, _ []string, visit func(map[string]any) error, args ...any) error {
		p.combineQueries = append(p.combineQueries, query)
		p.combineArgs = append(p.combineArgs, append([]any(nil), args...))
		manifests := catalog.snapshot()
		p.visibleDuringQ = len(manifests)
		for _, manifest := range manifests {
			p.seenDuringRun = append(p.seenDuringRun, manifest)
			if rows := ch.rowsSnapshot(manifest.PhysicalTable); rows != nil {
				p.capturedRows[manifest.PhysicalTable] = rows
			}
		}
		for _, row := range visitRows {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func exec11lvBindings() recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: "generation-2", AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"/programs/a"},
	}
}

func exec11lvBundle() recipe.Bundle {
	output := func(name string) recipe.Output {
		return recipe.Output{
			Name: name, RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "value", ColumnID: "value-id", Expr: recipe.Expression{Select: "root.id"}}},
		}
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "exec11-live-combine", TranslationVersion: "test",
		Outputs: []recipe.Output{output("left"), output("right")},
	}
}

func exec11lvCombineOutput(name string, inputs []ir.PhysicalCombineInputRef) lower.CompiledRecipeOutput {
	plan := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend, Inputs: append([]ir.PhysicalCombineInputRef(nil), inputs...),
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value-id", Name: "value", LogicalType: "string", ClickHouseType: "Nullable(String)", Nullable: true}},
	}
	for index := range inputs {
		plan.Projections = append(plan.Projections, ir.PhysicalCombineProjection{OutputColumnID: "value-id", InputIndex: index, InputColumnID: "value-id"})
	}
	return lower.CompiledRecipeOutput{
		Name: name, Columns: []string{"value"},
		Stages: []lower.CompiledStageDescriptor{{ID: "combine-final"}},
		Plan:   ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &plan},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "loom:row_id", Name: "__loom_row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
			{ID: "value-id", Name: "value", Kind: "string", Cardinality: "optional_one", Nullable: true},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"__loom_row_id"}},
	}
}

func exec11lvResolved(t *testing.T, engine *dataframeexecution.Engine, withPublished bool) dataframeexecution.Resolved {
	t.Helper()
	resolved, err := engine.CompileResolvedBundle(context.Background(), exec11lvBundle(), exec11lvBindings())
	if err != nil {
		t.Fatalf("compile live bundle: %v", err)
	}
	if len(resolved.Compiled.Outputs) != 2 || resolved.Compiled.Outputs[0].Name != "left" || resolved.Compiled.Outputs[1].Name != "right" {
		t.Fatalf("live bundle outputs drifted: %s/%s", resolved.Compiled.Outputs[0].Name, resolved.Compiled.Outputs[1].Name)
	}
	dependencies := append([]lower.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
	refs := []ir.PhysicalCombineInputRef{{WorkspaceOutputID: "left"}, {WorkspaceOutputID: "right"}}
	if withPublished {
		refs = append(refs, ir.PhysicalCombineInputRef{TableID: "exec11_pub_table", RevisionID: "exec11_pub_rev", OutputID: "pub"})
	}
	resolved.Compiled.Outputs = []lower.CompiledRecipeOutput{exec11lvCombineOutput("joined", refs)}
	resolved.Compiled.WorkspaceDependencies = dependencies
	resolved.Semantic.SemanticPlan.Bindings.OutputNames = []string{"joined"}
	return resolved
}

func exec11lvJoin(t *testing.T, engine *dataframeexecution.Engine, resolved dataframeexecution.Resolved) dataframeexecution.OutputStream {
	t.Helper()
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatalf("build live workspace Combine streams: %v", err)
	}
	for _, stream := range streams {
		if stream.Name == "joined" {
			return stream
		}
	}
	names := make([]string, 0, len(streams))
	for _, stream := range streams {
		names = append(names, stream.Name)
	}
	t.Fatalf("joined stream missing from %v", names)
	return dataframeexecution.OutputStream{}
}

func TestExec11lvLiveRestrictedCaptureFeedsCombine(t *testing.T) {
	service := newExec11lvService(t)
	service.migrateAndSeed(t)
	witness := service.witnessIDs(t)
	if strings.Join(witness, ",") != "patient-ada,patient-alan,patient-grace" {
		t.Fatalf("witness drifted: %v", witness)
	}

	catalog := &exec11lvCatalog{items: map[string]chartifact.Manifest{}}
	ch := &exec11lvClickHouse{table: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	ports := newExec11lvPorts()
	bindings := exec11lvBindings()
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              service.compileTarget(t),
		Registry:                   exec11lvInvalidRegistry{},
		ScopeDigest:                func(recipe.RuntimeBindings) string { return "exec11-live-scope" },
		SourceRows:                 service.executor,
		PrivateClickHouseArtifacts: manager,
		WithExecutionReadPins: func(_ context.Context, revisions []string, visit func(context.Context) error) error {
			ports.pinRevisions = append(ports.pinRevisions, append([]string(nil), revisions...))
			return visit(context.Background())
		},
		ResolveClickHouseInputs: func(_ context.Context, plan ir.PhysicalClickHouseCombine, got recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			ports.resolveCalls++
			if got.Project != bindings.Project || got.DatasetGeneration != bindings.DatasetGeneration {
				return nil, fmt.Errorf("published resolution lost the exact project/generation")
			}
			for _, input := range plan.Inputs {
				if input.RevisionID == "" {
					return nil, fmt.Errorf("published resolver received a non-published reference")
				}
			}
			return []ir.ResolvedClickHouseTable{{
				TableID: "exec11_pub_table", RevisionID: "exec11_pub_rev", OutputID: "pub",
				Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
				ReceiptID: "receipt-exec11-pub", SchemaDigest: "schema-exec11-pub", ScopeDigest: "scope-exec11-pub",
				PhysicalTable: "exec11_published_physical", AuthResourcePaths: []string{"/programs/a"}, ScopeMode: chartifact.ScopeModeRows,
				Columns: []ir.ResolvedClickHouseColumn{
					{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
					{ID: "value-id", Name: "value", LogicalType: "string", ClickHouseType: "Nullable(String)"},
					{ID: "auth-path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "String"},
				},
			}}, nil
		},
		ClickHouseQueryRows: ports.queryRows(catalog, ch, []map[string]any{
			{"__loom_row_id": "joined-1", "value": "patient-ada", "auth_resource_path": "/programs/a"},
			{"__loom_row_id": "joined-2", "value": "patient-alan", "auth_resource_path": "/programs/a"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := exec11lvResolved(t, engine, true)
	stream := exec11lvJoin(t, engine, resolved)
	var rows []map[string]any
	result, err := stream.Stream(context.Background(), func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("live workspace Combine over PostgreSQL captures: %v", err)
	}
	if result.RowCount != 2 || len(rows) != 2 || rows[0]["auth_resource_path"] != "/programs/a" {
		t.Fatalf("terminal rows drifted: %#v (%#v)", rows, result)
	}

	if len(ports.pinRevisions) != 1 || strings.Join(ports.pinRevisions[0], ",") != "exec11_pub_rev" {
		t.Fatalf("published pin revisions = %v, want exec11_pub_rev", ports.pinRevisions)
	}
	if ports.resolveCalls != 1 {
		t.Fatalf("published resolution calls = %d, want 1", ports.resolveCalls)
	}
	if len(ports.combineQueries) != 1 {
		t.Fatalf("Combine query count = %d, want 1: %v", len(ports.combineQueries), ports.combineQueries)
	}
	terminal := ports.combineQueries[0]
	if !strings.Contains(terminal, "loom_private_") || !strings.Contains(terminal, "exec11_published_physical") {
		t.Fatalf("Combine query did not join both private captures and the published table: %s", terminal)
	}
	var flatArgs []string
	var flattenExec11 func(any)
	flattenExec11 = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, element := range typed {
				flattenExec11(element)
			}
		case []string:
			flatArgs = append(flatArgs, typed...)
		default:
			flatArgs = append(flatArgs, fmt.Sprintf("%v", typed))
		}
	}
	for _, group := range ports.combineArgs {
		for _, arg := range group {
			flattenExec11(arg)
		}
	}
	reapplied := false
	for _, arg := range flatArgs {
		if arg == "/programs/a" {
			reapplied = true
		}
		if arg == "/programs/b" || arg == "patient-hidden" || arg == "patient-intruder" || arg == "patient-stale" {
			t.Fatalf("out-of-scope value %q reached the Combine query", arg)
		}
	}
	if !reapplied {
		t.Fatalf("restricted scope evidence was not reapplied to the Combine read: args=%v", ports.combineArgs)
	}

	if ports.visibleDuringQ != 2 {
		t.Fatalf("live captures visible during Combine = %d, want 2", ports.visibleDuringQ)
	}
	if len(ports.seenDuringRun) != 2 {
		t.Fatalf("manifest observations = %d, want 2", len(ports.seenDuringRun))
	}
	for _, manifest := range ports.seenDuringRun {
		if manifest.Identity.AuthScopeMode != authscope.ReadScopeRestricted ||
			len(manifest.Identity.AuthResourcePaths) != 1 || manifest.Identity.AuthResourcePaths[0] != "/programs/a" ||
			manifest.Identity.ScopeMode != chartifact.ScopeModeRows || manifest.Identity.PlanDigest == "" ||
			manifest.Identity.Project != "project-a" || manifest.Identity.DatasetGeneration != "generation-2" {
			t.Fatalf("live capture lost its restricted compiler scope identity: %#v", manifest.Identity)
		}
		captured := ports.capturedRows[manifest.PhysicalTable]
		if int64(len(captured)) != manifest.RowCount || manifest.RowCount != int64(len(witness)) {
			t.Fatalf("capture %s row count = %d/%d, want %d", manifest.PhysicalTable, len(captured), manifest.RowCount, len(witness))
		}
		var values []string
		for _, row := range captured {
			if row["auth_resource_path"] != "/programs/a" || row["project_id"] != "project-a" {
				t.Fatalf("captured row lost the real authorization path: %v", row)
			}
			values = append(values, fmt.Sprintf("%v", row["value"]))
		}
		sort.Strings(values)
		if strings.Join(values, ",") != strings.Join(witness, ",") {
			t.Fatalf("captured values %v drifted from the direct-SQL witness %v", values, witness)
		}
	}
	for table, inserted := range ch.allRows() {
		for _, row := range inserted {
			value := fmt.Sprintf("%v", row["value"])
			if value == "patient-hidden" || value == "patient-intruder" || value == "patient-stale" {
				t.Fatalf("out-of-scope source row %q was captured into %s", value, table)
			}
		}
	}
	if catalog.count() != 0 || len(ch.allRows()) != 0 {
		t.Fatalf("live Combine leaked %d manifests and %d tables", catalog.count(), len(ch.allRows()))
	}
}

func TestExec11lvLiveCaptureRejectsForgedAuthPath(t *testing.T) {
	service := newExec11lvService(t)
	service.migrateAndSeed(t)
	catalog := &exec11lvCatalog{items: map[string]chartifact.Manifest{}}
	ch := &exec11lvClickHouse{table: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	ports := newExec11lvPorts()
	var portCalls int
	tampered := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		portCalls++
		forge := portCalls == 2
		return service.executor(ctx, request, func(row map[string]any) error {
			if forge {
				forged := make(map[string]any, len(row))
				for key, value := range row {
					forged[key] = value
				}
				forged["auth_resource_path"] = "/programs/b"
				return visit(forged)
			}
			return visit(row)
		})
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              service.compileTarget(t),
		Registry:                   exec11lvInvalidRegistry{},
		ScopeDigest:                func(recipe.RuntimeBindings) string { return "exec11-live-scope" },
		SourceRows:                 tampered,
		PrivateClickHouseArtifacts: manager,
		WithExecutionReadPins: func(_ context.Context, _ []string, visit func(context.Context) error) error {
			return visit(context.Background())
		},
		ClickHouseQueryRows: ports.queryRows(catalog, ch, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := exec11lvResolved(t, engine, false)
	stream := exec11lvJoin(t, engine, resolved)
	if _, err := stream.Stream(context.Background(), func(map[string]any) error { return nil }); err == nil ||
		!strings.Contains(err.Error(), "outside the exact restricted scope") {
		t.Fatalf("forged authorization path error = %v, want exact-scope rejection", err)
	}
	if len(ports.combineQueries) != 0 {
		t.Fatalf("Combine ran %d times after a forged scope proof: %v", len(ports.combineQueries), ports.combineQueries)
	}
	if catalog.count() != 0 || len(ch.allRows()) != 0 {
		t.Fatalf("forged capture leaked %d manifests and %d tables", catalog.count(), len(ch.allRows()))
	}
}

func TestExec11lvLiveCaptureCancellationLeavesNothing(t *testing.T) {
	service := newExec11lvService(t)
	service.migrateAndSeed(t)
	catalog := &exec11lvCatalog{items: map[string]chartifact.Manifest{}}
	ch := &exec11lvClickHouse{table: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	ports := newExec11lvPorts()
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	var portCalls int
	cancelling := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		portCalls++
		return service.executor(ctx, request, func(row map[string]any) error {
			if err := visit(row); err != nil {
				return err
			}
			if portCalls == 1 {
				cancelRun()
			}
			return nil
		})
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              service.compileTarget(t),
		Registry:                   exec11lvInvalidRegistry{},
		ScopeDigest:                func(recipe.RuntimeBindings) string { return "exec11-live-scope" },
		SourceRows:                 cancelling,
		PrivateClickHouseArtifacts: manager,
		WithExecutionReadPins: func(_ context.Context, _ []string, visit func(context.Context) error) error {
			return visit(context.Background())
		},
		ClickHouseQueryRows: ports.queryRows(catalog, ch, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := exec11lvResolved(t, engine, false)
	stream := exec11lvJoin(t, engine, resolved)
	if _, err := stream.Stream(runCtx, func(map[string]any) error { return nil }); err == nil {
		t.Fatal("cancelled capture reported success")
	} else if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("cancellation surfaced as an unrelated error: %v", err)
	}
	if len(ports.combineQueries) != 0 {
		t.Fatalf("Combine ran %d times after cancellation: %v", len(ports.combineQueries), ports.combineQueries)
	}
	if catalog.count() != 0 || len(ch.allRows()) != 0 {
		t.Fatalf("cancelled capture leaked %d manifests and %d tables", catalog.count(), len(ch.allRows()))
	}
}

func TestExec11lvLiveEvidenceFreeCaptureRefused(t *testing.T) {
	service := newExec11lvService(t)
	service.migrateAndSeed(t)
	catalog := &exec11lvCatalog{items: map[string]chartifact.Manifest{}}
	ch := &exec11lvClickHouse{table: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: ch, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	ports := newExec11lvPorts()
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              service.compileTarget(t),
		Registry:                   exec11lvInvalidRegistry{},
		ScopeDigest:                func(recipe.RuntimeBindings) string { return "exec11-live-scope" },
		SourceRows:                 service.executor,
		PrivateClickHouseArtifacts: manager,
		WithExecutionReadPins: func(_ context.Context, _ []string, visit func(context.Context) error) error {
			return visit(context.Background())
		},
		ResolveClickHouseInputs: func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			return nil, nil
		},
		ClickHouseQueryRows: ports.queryRows(catalog, ch, nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := exec11lvResolved(t, engine, false)
	dependencies := append([]lower.CompiledRecipeOutput(nil), resolved.Compiled.WorkspaceDependencies...)
	for index := range dependencies {
		dependencies[index].ScopeEvidence = nil
		dependencies[index].ScopeEvidenceIdentity = nil
	}
	resolved.Compiled.WorkspaceDependencies = dependencies
	stream := exec11lvJoin(t, engine, resolved)
	_, err = stream.Stream(context.Background(), func(map[string]any) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "requires opaque evidence for its exact complete source plan") {
		t.Fatalf("evidence-free live capture error = %v, want the opaque-evidence refusal", err)
	}
	if catalog.count() != 0 || len(ch.allRows()) != 0 || len(ports.combineQueries) != 0 {
		t.Fatalf("evidence-free capture leaked %d manifests, %d tables, %d Combine queries",
			catalog.count(), len(ch.allRows()), len(ports.combineQueries))
	}
}

func TestExec11lvLiveCombineExecutesAgainstClickHouse(t *testing.T) {
	service := newExec11lvService(t)
	service.migrateAndSeed(t)
	witness := service.witnessIDs(t)
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if chURL == "" {
		t.Skip("set LOOM_CLICKHOUSE_URL to execute the live workspace Combine against a real ClickHouse")
	}
	database := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_DATABASE"))
	if database == "" {
		database = "loom_test"
	}
	if !clickhouseIdentifier(database) {
		t.Fatalf("LOOM_CLICKHOUSE_DATABASE %q is not a plain identifier", database)
	}
	client, err := clickhouse.New(clickhouse.Options{
		URL: chURL, Database: database,
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.EnsureDatabase(ctx); err != nil {
		t.Fatal(err)
	}
	pubColumns := []clickhouse.Column{
		{Name: "__loom_row_id", Type: "String"},
		{Name: "value", Type: "Nullable(String)"},
		{Name: "auth_resource_path", Type: "String"},
	}
	if err := client.CreateTable(ctx, "exec11_published_physical", pubColumns); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.DropTable(context.Background(), "exec11_published_physical") })
	if err := client.InsertRows(ctx, "exec11_published_physical", pubColumns, []map[string]any{
		{"__loom_row_id": "exec11-pub-1", "value": "patient-published", "auth_resource_path": "/programs/a"},
	}); err != nil {
		t.Fatal(err)
	}

	catalog := &exec11lvCatalog{items: map[string]chartifact.Manifest{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: client, BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	bindings := exec11lvBindings()
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget:              service.compileTarget(t),
		Registry:                   exec11lvInvalidRegistry{},
		ScopeDigest:                func(recipe.RuntimeBindings) string { return "exec11-live-scope" },
		SourceRows:                 service.executor,
		PrivateClickHouseArtifacts: manager,
		WithExecutionReadPins: func(_ context.Context, revisions []string, visit func(context.Context) error) error {
			if strings.Join(revisions, ",") != "exec11_pub_rev" {
				return fmt.Errorf("published pins = %v, want exec11_pub_rev", revisions)
			}
			return visit(context.Background())
		},
		ResolveClickHouseInputs: func(_ context.Context, _ ir.PhysicalClickHouseCombine, got recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
			if got.Project != bindings.Project || got.DatasetGeneration != bindings.DatasetGeneration {
				return nil, fmt.Errorf("published resolution lost the exact project/generation")
			}
			return []ir.ResolvedClickHouseTable{{
				TableID: "exec11_pub_table", RevisionID: "exec11_pub_rev", OutputID: "pub",
				Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration,
				ReceiptID: "receipt-exec11-pub", SchemaDigest: "schema-exec11-pub", ScopeDigest: "scope-exec11-pub",
				PhysicalTable: "exec11_published_physical", AuthResourcePaths: []string{"/programs/a"}, ScopeMode: chartifact.ScopeModeRows,
				Columns: []ir.ResolvedClickHouseColumn{
					{ID: "loom:row_id", Name: "__loom_row_id", LogicalType: "string", ClickHouseType: "String"},
					{ID: "value-id", Name: "value", LogicalType: "string", ClickHouseType: "Nullable(String)"},
					{ID: "auth-path", Name: "auth_resource_path", LogicalType: "string", ClickHouseType: "String"},
				},
			}}, nil
		},
		ClickHouseQueryRows: client.QueryRowsArgsVisit,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved := exec11lvResolved(t, engine, true)
	stream := exec11lvJoin(t, engine, resolved)
	var values []string
	result, err := stream.Stream(ctx, func(row map[string]any) error {
		value := fmt.Sprintf("%v", row["value"])
		if value == "patient-hidden" || value == "patient-intruder" || value == "patient-stale" {
			return fmt.Errorf("out-of-scope value %q crossed the real ClickHouse Combine", value)
		}
		values = append(values, value)
		return nil
	})
	if err != nil {
		t.Fatalf("real ClickHouse Combine: %v", err)
	}
	sort.Strings(values)
	want := append(append(append([]string(nil), witness...), witness...), "patient-published")
	sort.Strings(want)
	if result.RowCount != len(want) || strings.Join(values, ",") != strings.Join(want, ",") {
		t.Fatalf("real ClickHouse Combine rows = %v (%d rows), want %v", values, result.RowCount, want)
	}
	if catalog.count() != 0 {
		t.Fatalf("%d manifests survived the completed real-ClickHouse Combine", catalog.count())
	}
	var residue []string
	if err := client.QueryRowsArgsVisit(ctx,
		"SELECT name FROM system.tables WHERE database = '"+database+"'", []string{"name"},
		func(row map[string]any) error {
			residue = append(residue, fmt.Sprintf("%v", row["name"]))
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	for _, name := range residue {
		if strings.HasPrefix(name, "loom_private_") {
			t.Fatalf("private artifact table %q survived the completed Combine", name)
		}
	}
}

func TestZZPostgresCaptureZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 private-capture acceptance tests")
	}
	scrub, _ := exec03pqScrubberFor(raw)
	exec11lvCaptureBaseline(raw)
	if exec11lvBaselineErr != nil {
		t.Fatalf("capture pg_database baseline for audit: %s", scrub(exec11lvBaselineErr))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec11lvRegistrySnapshot()
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
	for _, name := range exec11lvBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec11\\_%' ESCAPE '\\'")
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
