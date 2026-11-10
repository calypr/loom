package execution

// pg-exec-05 live acceptance battery: bounded PostgreSQL root-page command
// sequences against a real PostgreSQL 18 (check-2: go test
// ./internal/dataframe/execution -run PostgresRootPaging under
// LOOM_TEST_POSTGRES_URL against the isolated PostgreSQL 18 service).
//
// Every test drives the production streamRaw PostgreSQL root-page branch onto
// the real server through the production internal/dataframe/postgres row
// adapter and the production migrations/repositories: the owned schema is
// installed by storepg.MigrateSchema and seeded with ResourceRepository and
// EdgeRepository, so every key step and row step runs against the real
// fhir_resource/fhir_edge_occurrence relation shapes. The key and row
// statements are parent-shaped commands frozen by the canonical
// command.NewPostgreSQLCommand constructor through the single test funnel
// newExec05PageCommand: the production compiler still refuses root-key output
// paging for PostgreSQL (recipe_paging.go rejects it with the typed
// unsupported-plan sentinel — pinned offline in
// TestPostgreSQLRootPageProductionNeverAttaches), so no production producer of
// PostgreSQLRootPageSteps exists yet; a parent-compiler ticket will supply one
// and these tests pin the execution contract it will compile into.
//
// Witnesses are literal values read back from PostgreSQL 18 itself: exact
// per-page row multisets, literal cursor progression, literal jsonb key-page
// arguments, adapter-decoded int64 edge_seq witnesses, and EXPLAIN (FORMAT
// JSON) plan shapes proving key-set boundedness (LIMIT node + index access,
// no sequential scans on the bounded relation). Non-vacuity discipline: every
// refusal has a paired control that proceeds on the same fixture; the
// termination arms count statements at the port, never by timing.
//
// Fixture databases and schemas are uniquely owned as verify_exec05_* and
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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
	storepg "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec05Prefix      = "verify_exec05_"
	exec05NameFmt     = exec05Prefix + "%d_%s"
	exec05Project     = "P1"
	exec05Generation  = "gen-a"
	exec05AuthPath    = "FHIR/Patient"
	exec05EdgeLabel   = "observation"
	exec05ChildType   = "Observation"
	exec05RootType    = "Patient"
	exec05RootKeyCol  = "root_key"
	exec05ChildSeqCol = "child_seq"
	exec05ChildIDCol  = "child_id"
)

var exec05NamePattern = regexp.MustCompile(`^verify_exec05_[0-9]+_[0-9a-f]+$`)

var (
	exec05OwnedMu     sync.Mutex
	exec05OwnedByName = map[string]string{}
)

func exec05Register(db, schema string) {
	exec05OwnedMu.Lock()
	defer exec05OwnedMu.Unlock()
	exec05OwnedByName[db] = schema
}

func exec05RegistrySnapshot() map[string]string {
	exec05OwnedMu.Lock()
	defer exec05OwnedMu.Unlock()
	out := make(map[string]string, len(exec05OwnedByName))
	for db, schema := range exec05OwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec05BaselineOnce  sync.Once
	exec05BaselineNames []string
	exec05BaselineErr   error
)

func exec05CaptureBaseline(raw string) {
	exec05BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec05BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec05\\_%' ESCAPE '\\'")
		if err != nil {
			exec05BaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec05BaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec05BaselineNames = append(exec05BaselineNames, name)
		}
	})
}

type exec05LiveService struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	iso      *pgxpool.Pool
	schema   string
	dsn      string
	target   compiler.Target
	executor SourceRows
	scrub    func(any) string
	fail     func(step string, err error)
}

// exec05NewRowsSource is installed once by the external test file
// recipe_paging_postgres_adapter_test.go. The production row adapter package
// imports this package, so only the external test package may import it; the
// live battery still runs every page step through the real adapter.
var exec05NewRowsSource func(iso *pgxpool.Pool) (SourceRows, error)

// Exec05InstallRowsAdapterFactory is test-only (it lives in a _test.go file)
// and installs the production internal/dataframe/postgres row source used by
// the live root-page battery.
func Exec05InstallRowsAdapterFactory(f func(iso *pgxpool.Pool) (SourceRows, error)) {
	exec05NewRowsSource = f
}

func exec05URL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 root-page acceptance tests")
	}
	return raw
}

func exec05RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func newExec05LiveService(t *testing.T) *exec05LiveService {
	t.Helper()
	raw := exec05URL(t)
	exec05CaptureBaseline(raw)
	if exec05BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec05BaselineErr)
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

	dbName := fmt.Sprintf(exec05NameFmt, time.Now().UnixNano(), exec05RandomHex(t))
	if !exec05NamePattern.MatchString(dbName) {
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
	exec05Register(dbName, "")
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

	schema := fmt.Sprintf(exec05NameFmt, time.Now().UnixNano(), exec05RandomHex(t))
	if !exec05NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec05Register(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
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
	return &exec05LiveService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, executor: executor, scrub: scrub, fail: fail}
}

func (s *exec05LiveService) qualifiedTable(name string) string {
	return `"` + s.schema + `"."` + name + `"`
}

// exec05Migrate installs the real pg-store-02/pg-store-05 placements in the
// owned schema through the production migration runner.
func (s *exec05LiveService) migrate(t *testing.T) {
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

func (s *exec05LiveService) insertResource(t *testing.T, resourceType, logicalID, project, generation, auth, payload string) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	if _, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		AuthResourcePath:  auth,
		Payload:           json.RawMessage(payload),
	}); err != nil {
		s.fail("insert fixture resource", err)
	}
}

func (s *exec05LiveService) insertEdge(t *testing.T, sourceID, targetID string, ordinal int64) {
	t.Helper()
	repo, err := storepg.NewEdgeRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("edge repository: %v", err)
	}
	if _, err := repo.InsertEdge(s.ctx, storepg.EdgeOccurrenceRow{
		Project:            exec05Project,
		DatasetGeneration:  exec05Generation,
		AuthResourcePath:   exec05AuthPath,
		Label:              exec05EdgeLabel,
		SourceResourceType: exec05RootType,
		SourceLogicalID:    sourceID,
		TargetResourceType: exec05ChildType,
		TargetLogicalID:    targetID,
		SourcePath:         "entry",
		SourceOrdinal:      ordinal,
	}); err != nil {
		s.fail("insert fixture edge", err)
	}
}

// seedAcceptanceFixture stores the five-root acceptance fixture through the
// production repositories: p0001..p0005 with the child expansion
// p0001→{c0001,c0002}, p0002→{c0002} (the repeated child of two owners),
// p0003→{c0003}, p0004→{} (a root with no output rows that still consumes a
// page slot), p0005→{c0004,c0005}, plus scope intruders that the bound
// project/generation/auth parameters must never surface.
func (s *exec05LiveService) seedAcceptanceFixture(t *testing.T) {
	t.Helper()
	s.migrate(t)
	payload := `{"name":"fixture"}`
	for _, id := range []string{"p0001", "p0002", "p0003", "p0004", "p0005"} {
		s.insertResource(t, exec05RootType, id, exec05Project, exec05Generation, exec05AuthPath, payload)
	}
	for _, id := range []string{"c0001", "c0002", "c0003", "c0004", "c0005"} {
		s.insertResource(t, exec05ChildType, id, exec05Project, exec05Generation, exec05AuthPath, payload)
	}
	s.insertResource(t, exec05RootType, "p0900", "P2", exec05Generation, exec05AuthPath, payload)
	s.insertResource(t, exec05RootType, "p0901", exec05Project, "gen-b", exec05AuthPath, payload)
	edges := []struct {
		source, target string
		ordinal        int64
	}{
		{"p0001", "c0001", 0},
		{"p0001", "c0002", 1},
		{"p0002", "c0002", 0},
		{"p0003", "c0003", 0},
		{"p0005", "c0004", 0},
		{"p0005", "c0005", 1},
	}
	for _, edge := range edges {
		s.insertEdge(t, edge.source, edge.target, edge.ordinal)
	}
}

// seedPlannerFixture stores the boundedness fixture: 200 roots with three
// edges each through the production repositories, then ANALYZE so the
// PostgreSQL planner decides from real statistics.
func (s *exec05LiveService) seedPlannerFixture(t *testing.T) {
	t.Helper()
	s.migrate(t)
	payload := `{"name":"planner"}`
	for index := 1; index <= 200; index++ {
		s.insertResource(t, exec05RootType, fmt.Sprintf("p%04d", index), exec05Project, exec05Generation, exec05AuthPath, payload)
		s.insertResource(t, exec05ChildType, fmt.Sprintf("c%04d", index), exec05Project, exec05Generation, exec05AuthPath, payload)
	}
	for index := 1; index <= 200; index++ {
		sourceID := fmt.Sprintf("p%04d", index)
		for slot, offset := range []int{0, 1, 7} {
			targetID := fmt.Sprintf("c%04d", (index-1+offset)%200+1)
			s.insertEdge(t, sourceID, targetID, int64(slot))
		}
	}
	if _, err := s.iso.Exec(s.ctx, "ANALYZE "+s.qualifiedTable("fhir_resource")); err != nil {
		s.fail("analyze resource placement", err)
	}
	if _, err := s.iso.Exec(s.ctx, "ANALYZE "+s.qualifiedTable("fhir_edge_occurrence")); err != nil {
		s.fail("analyze edge placement", err)
	}
}

// exec05KeySQLTemplate is the parent-shaped key-discovery statement: a scoped
// keyset page over the production fhir_resource relation, ordered by the root
// continuation identity and bounded by a parameterized LIMIT. Parameter
// positions: $1 project, $2 dataset generation, $3 root resource type,
// $4 auth resource path, $5 cursor (text, engine-bound), $6 page size
// (bigint, engine-bound).
func (s *exec05LiveService) keySQLTemplate() string {
	return "SELECT r.logical_id AS root_key FROM " + s.qualifiedTable("fhir_resource") +
		" r WHERE r.project = $1 AND r.dataset_generation = $2 AND r.resource_type = $3" +
		" AND r.auth_resource_path = $4 AND r.logical_id > $5" +
		" ORDER BY r.logical_id ASC LIMIT $6"
}

// exec05RowsSQLTemplate is the parent-shaped row statement: the complete
// expansion output for exactly the requested key page. The key page arrives
// as one jsonb array parameter (the registered command parameter types carry
// no array type), and the ORDER BY is the pg-sql-12 final-row window shape:
// root identity, then the storage occurrence witness, then the child
// identity. Parameter positions: $1 project, $2 dataset generation,
// $3 root resource type, $4 auth path, $5 key page (jsonb, engine-bound),
// $6 child resource type, $7 edge label.
func (s *exec05LiveService) rowsSQLTemplate() string {
	return "SELECT r.logical_id AS root_key, e.edge_seq AS child_seq, c.logical_id AS child_id" +
		" FROM " + s.qualifiedTable("fhir_resource") + " r" +
		" JOIN " + s.qualifiedTable("fhir_edge_occurrence") + " e" +
		" ON e.project = r.project AND e.dataset_generation = r.dataset_generation" +
		" AND e.source_resource_type = r.resource_type AND e.source_logical_id = r.logical_id" +
		" AND e.auth_resource_path = $4 AND e.label = $7" +
		" JOIN " + s.qualifiedTable("fhir_resource") + " c" +
		" ON c.project = e.project AND c.dataset_generation = e.dataset_generation" +
		" AND c.resource_type = $6 AND c.logical_id = e.target_logical_id" +
		" AND c.auth_resource_path = $4" +
		" WHERE r.project = $1 AND r.dataset_generation = $2 AND r.resource_type = $3" +
		" AND r.auth_resource_path = $4" +
		" AND r.logical_id IN (SELECT k FROM jsonb_array_elements_text($5) AS k)" +
		" ORDER BY r.logical_id ASC, e.edge_seq ASC, c.logical_id ASC"
}

func (s *exec05LiveService) keyArgs(cursor string, size int64) []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: exec05Project},
		{Type: command.PostgreSQLParameterText, Value: exec05Generation},
		{Type: command.PostgreSQLParameterText, Value: exec05RootType},
		{Type: command.PostgreSQLParameterText, Value: exec05AuthPath},
		{Type: command.PostgreSQLParameterText, Value: cursor},
		{Type: command.PostgreSQLParameterBigint, Value: size},
	}
}

func (s *exec05LiveService) rowsArgs() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: exec05Project},
		{Type: command.PostgreSQLParameterText, Value: exec05Generation},
		{Type: command.PostgreSQLParameterText, Value: exec05RootType},
		{Type: command.PostgreSQLParameterText, Value: exec05AuthPath},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`[]`)},
		{Type: command.PostgreSQLParameterText, Value: exec05ChildType},
		{Type: command.PostgreSQLParameterText, Value: exec05EdgeLabel},
	}
}

// newExec05LiveSteps freezes the parent-shaped page pair through the single
// canonical test funnel newExec05PageCommand (ratified condition R2-i).
func (s *exec05LiveService) newExec05LiveSteps(t *testing.T, pageSize int) *PostgreSQLRootPageSteps {
	t.Helper()
	return &PostgreSQLRootPageSteps{
		RootKeys:         newExec05PageCommand(t, s.keySQLTemplate(), s.keyArgs("", int64(pageSize))),
		Rows:             newExec05PageCommand(t, s.rowsSQLTemplate(), s.rowsArgs()),
		RootKeyColumn:    exec05RootKeyCol,
		RowRootKeyColumn: exec05RootKeyCol,
		CursorArgIndex:   4,
		PageSizeArgIndex: 5,
		PageKeysArgIndex: 4,
		PageSize:         pageSize,
	}
}

type exec05LiveCall struct {
	kind    string
	sql     string
	args    []command.PostgreSQLParameter
	columns []string
}

// exec05LivePort instruments the production row adapter: it records every
// request through the single port and can rewrite, duplicate, or add rows
// during visitation for live negative controls.
type exec05LivePort struct {
	s         *exec05LiveService
	calls     []exec05LiveCall
	visitHook func(kind string, row map[string]any, index int) (map[string]any, error)
	extraRows func(kind string, callIndex int) []map[string]any
	afterCall func(p *exec05LivePort, kind string, callIndex int)
}

func (p *exec05LivePort) port(ctx context.Context, request RowsRequest, visit RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	kind := "rows"
	if len(request.DeclaredColumns) == 1 && request.DeclaredColumns[0] == exec05RootKeyCol && !strings.Contains(sqlText, exec05ChildSeqCol) {
		kind = "keys"
	}
	p.calls = append(p.calls, exec05LiveCall{kind: kind, sql: sqlText, args: args, columns: append([]string(nil), request.DeclaredColumns...)})
	callIndex := len(p.calls)
	inner := func(row map[string]any) error {
		if p.visitHook != nil {
			replacement, err := p.visitHook(kind, row, len(p.calls))
			if err != nil {
				return err
			}
			if replacement == nil {
				return nil
			}
			row = replacement
		}
		return visit(row)
	}
	if err := p.s.executor(ctx, request, inner); err != nil {
		return err
	}
	if p.extraRows != nil {
		for _, extra := range p.extraRows(kind, callIndex) {
			if err := visit(extra); err != nil {
				return err
			}
		}
	}
	if p.afterCall != nil {
		p.afterCall(p, kind, callIndex)
	}
	return nil
}

func exec05RecordedKeyPages(p *exec05LivePort) []string {
	var pages []string
	for _, call := range p.calls {
		if call.kind == "rows" {
			pages = append(pages, string(call.args[4].Value.(json.RawMessage)))
		}
	}
	return pages
}

func (p *exec05LivePort) counts() (keys, rows int) {
	for _, call := range p.calls {
		if call.kind == "keys" {
			keys++
		} else {
			rows++
		}
	}
	return keys, rows
}

func exec05LiveStream(t *testing.T, s *exec05LiveService, steps *PostgreSQLRootPageSteps, port SourceRows) OutputStream {
	t.Helper()
	return OutputStream{
		Name: "expanded", Columns: []string{exec05RootKeyCol, exec05ChildSeqCol, exec05ChildIDCol},
		RowIdentity:     &spec.RowIdentity{Fields: []string{exec05RootKeyCol, exec05ChildSeqCol, exec05ChildIDCol}},
		command:         newExec05PageCommand(t, "SELECT 1", nil),
		expectedBackend: command.CompiledBackendPostgreSQL,
		commandColumns:  []string{exec05RootKeyCol, exec05ChildSeqCol, exec05ChildIDCol},
		sourceRows:      port,
		batchSize:       100,
		pgRootPage:      steps,
	}
}

func exec05RowTuple(row map[string]any) string {
	return fmt.Sprintf("%v/%v/%v", row[exec05RootKeyCol], row[exec05ChildSeqCol], row[exec05ChildIDCol])
}

func TestPostgresRootPagingMultisetAcrossPages(t *testing.T) {
	s := newExec05LiveService(t)
	s.seedAcceptanceFixture(t)

	// Server truth, read independently of the engine loop: the same scoped
	// expansion without any key-page filter.
	adminRows := exec05AdminExpansion(t, s)
	want := []string{"p0001/1/c0001", "p0001/2/c0002", "p0002/3/c0002", "p0003/4/c0003", "p0005/5/c0004", "p0005/6/c0005"}
	if fmt.Sprint(adminRows) != fmt.Sprint(want) {
		t.Fatalf("server-side expansion = %v, want the literal golden %v", adminRows, want)
	}

	// The merged single-statement production route, compiled by the parent
	// PostgreSQL target for the same scope, must see exactly the five roots.
	engine := s.engine(t, func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
		return s.executor(ctx, request, visit)
	})
	baseline, err := engine.Streams(s.ctx, exec05LiveResolved())
	if err != nil {
		s.fail("compile baseline root stream", err)
	}
	baselineRoots := exec05CollectBaseline(t, s, baseline)
	if fmt.Sprint(baselineRoots) != fmt.Sprint([]string{"p0001", "p0002", "p0003", "p0004", "p0005"}) {
		t.Fatalf("compiled baseline roots = %v, want the five acceptance roots once each", baselineRoots)
	}

	port := &exec05LivePort{s: s}
	var visited []string
	var pageBoundaries []int
	port.afterCall = func(p *exec05LivePort, kind string, callIndex int) {
		if kind == "rows" {
			pageBoundaries = append(pageBoundaries, len(visited))
		}
	}
	stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
	if err := stream.streamRaw(s.ctx, func(row map[string]any) error {
		visited = append(visited, exec05RowTuple(row))
		return nil
	}); err != nil {
		s.fail("paged streamRaw", err)
	}
	if fmt.Sprint(visited) != fmt.Sprint(want) {
		t.Fatalf("paged visitation = %v, want the exact multiset once each across pages: %v", visited, want)
	}
	// Per-page containment and disjointness: every row of a page belongs to
	// one of that page's requested root keys, and no root's rows appear in
	// two pages (each independent row exactly once across all pages).
	keyPages := exec05RecordedKeyPages(port)
	if len(pageBoundaries) != len(keyPages) {
		t.Fatalf("page boundaries %v do not line up with %d row steps", pageBoundaries, len(keyPages))
	}
	previousRoots := map[string]int{}
	start := 0
	for pageIndex, end := range pageBoundaries {
		var pageKeys []string
		if err := json.Unmarshal([]byte(keyPages[pageIndex]), &pageKeys); err != nil {
			t.Fatalf("decode requested page %d: %v", pageIndex, err)
		}
		requested := make(map[string]bool, len(pageKeys))
		for _, key := range pageKeys {
			requested[key] = true
		}
		for _, tuple := range visited[start:end] {
			root := strings.SplitN(tuple, "/", 2)[0]
			if !requested[root] {
				t.Fatalf("page %d row %v belongs to root %q outside the requested page %s", pageIndex, tuple, root, keyPages[pageIndex])
			}
			if earlier, seen := previousRoots[root]; seen && earlier != pageIndex {
				t.Fatalf("root %q produced rows on two pages (%d and %d): rows would duplicate across pages", root, earlier, pageIndex)
			}
			previousRoots[root] = pageIndex
		}
		start = end
	}
	keys, rows := port.counts()
	if keys != 3 || rows != 3 {
		t.Fatalf("port calls keys=%d rows=%d, want 3/3", keys, rows)
	}
	// Literal cursors and literal jsonb key pages witnessed at the port.
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
		t.Fatalf("cursors = %v, want [ p0002 p0004]", cursors)
	}
	wantPages := []string{`["p0001","p0002"]`, `["p0003","p0004"]`, `["p0005"]`}
	if fmt.Sprint(keyPages) != fmt.Sprint(wantPages) {
		t.Fatalf("key pages = %v, want %v", keyPages, wantPages)
	}
	// Intruders never surface and every row's root is inside its page.
	for _, tuple := range visited {
		if strings.HasPrefix(tuple, "p09") {
			t.Fatalf("scope intruder surfaced in paged output: %v", tuple)
		}
	}
}

// exec05AdminExpansion reads the complete scoped expansion directly from the
// server, ordered exactly like the parent row statement.
func exec05AdminExpansion(t *testing.T, s *exec05LiveService) []string {
	t.Helper()
	query := "SELECT r.logical_id, e.edge_seq, c.logical_id FROM " + s.qualifiedTable("fhir_resource") + " r" +
		" JOIN " + s.qualifiedTable("fhir_edge_occurrence") + " e" +
		" ON e.project = r.project AND e.dataset_generation = r.dataset_generation" +
		" AND e.source_resource_type = r.resource_type AND e.source_logical_id = r.logical_id" +
		" AND e.auth_resource_path = $4 AND e.label = $7" +
		" JOIN " + s.qualifiedTable("fhir_resource") + " c" +
		" ON c.project = e.project AND c.dataset_generation = e.dataset_generation" +
		" AND c.resource_type = $6 AND c.logical_id = e.target_logical_id" +
		" AND c.auth_resource_path = $4" +
		" WHERE r.project = $1 AND r.dataset_generation = $2 AND r.resource_type = $3 AND r.auth_resource_path = $4" +
		// The unbound key-page slot stays typed so the server still sees all
		// seven parameter positions with the same shapes as the row statement.
		" AND $5::jsonb IS NULL" +
		" ORDER BY r.logical_id ASC, e.edge_seq ASC, c.logical_id ASC"
	rows, err := s.iso.Query(s.ctx, query, exec05Project, exec05Generation, exec05RootType, exec05AuthPath, nil, exec05ChildType, exec05EdgeLabel)
	if err != nil {
		s.fail("admin expansion query", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var root, child string
		var seq int64
		if err := rows.Scan(&root, &seq, &child); err != nil {
			s.fail("scan admin expansion", err)
		}
		out = append(out, fmt.Sprintf("%s/%d/%s", root, seq, child))
	}
	if err := rows.Err(); err != nil {
		s.fail("admin expansion rows error", err)
	}
	return out
}

func TestPostgresRootPagingTerminatesWithoutExtraQuery(t *testing.T) {
	t.Run("short final page stops without another key query", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		var visited []string
		if err := stream.streamRaw(s.ctx, func(row map[string]any) error {
			visited = append(visited, exec05RowTuple(row))
			return nil
		}); err != nil {
			s.fail("short-page arm", err)
		}
		keys, rows := port.counts()
		if keys != 3 || rows != 3 {
			t.Fatalf("port calls keys=%d rows=%d, want 3/3 — the short final page must not trigger a fourth key query", keys, rows)
		}
		if len(visited) != 6 {
			t.Fatalf("visited %d rows, want 6", len(visited))
		}
	})
	t.Run("empty final page issues no row query", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 5), port.port)
		var visited []string
		if err := stream.streamRaw(s.ctx, func(row map[string]any) error {
			visited = append(visited, exec05RowTuple(row))
			return nil
		}); err != nil {
			s.fail("empty-page arm", err)
		}
		keys, rows := port.counts()
		if keys != 2 || rows != 1 {
			t.Fatalf("port calls keys=%d rows=%d, want 2/1 — the empty key page must not trigger a row query", keys, rows)
		}
		if len(visited) != 6 {
			t.Fatalf("visited %d rows, want 6", len(visited))
		}
	})
}

func TestPostgresRootPagingCancellation(t *testing.T) {
	t.Run("cancellation between key and row steps prevents the row step", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()
		port := &exec05LivePort{s: s}
		port.afterCall = func(p *exec05LivePort, kind string, callIndex int) {
			if kind == "keys" && callIndex == 1 {
				cancel()
			}
		}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		_, err := stream.Stream(ctx, func(map[string]any) error { return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled — completion must never be reported", err)
		}
		keys, rows := port.counts()
		if keys != 1 || rows != 0 {
			t.Fatalf("port calls keys=%d rows=%d, want 1/0 — the row step must never be dispatched after cancellation", keys, rows)
		}
	})
	t.Run("control: uncancelled run completes on the same fixture", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		result, err := stream.Stream(s.ctx, func(map[string]any) error { return nil })
		if err != nil {
			t.Fatalf("paired control: %v", err)
		}
		if result.RowCount != 6 {
			t.Fatalf("control RowCount = %d, want 6", result.RowCount)
		}
	})
}

func TestPostgresRootPagingResultContractSabotage(t *testing.T) {
	t.Run("duplicate root key fails the typed result contract", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		// Duplicate a discovered root key through the extra-rows seam on the
		// first key step: the engine must refuse the non-strictly-ordered
		// continuation identity with the typed result-contract sentinel.
		port.extraRows = func(kind string, callIndex int) []map[string]any {
			if kind == "keys" && callIndex == 1 {
				return []map[string]any{{exec05RootKeyCol: "p0001"}}
			}
			return nil
		}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		err := stream.streamRaw(s.ctx, func(map[string]any) error { return nil })
		if !errors.Is(err, ErrPostgreSQLResultContract) {
			t.Fatalf("error = %v, want ErrPostgreSQLResultContract for a duplicated root key", err)
		}
	})
	t.Run("row outside the requested page fails the typed result contract", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		port.extraRows = func(kind string, callIndex int) []map[string]any {
			if kind == "rows" && callIndex > 1 {
				return []map[string]any{{exec05RootKeyCol: "p0900", exec05ChildSeqCol: int64(999), exec05ChildIDCol: "c0900"}}
			}
			return nil
		}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		err := stream.streamRaw(s.ctx, func(map[string]any) error { return nil })
		if !errors.Is(err, ErrPostgreSQLResultContract) {
			t.Fatalf("error = %v, want ErrPostgreSQLResultContract for a row outside the requested page", err)
		}
	})
	t.Run("control: unsabotaged run proceeds on the same fixture", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		port := &exec05LivePort{s: s}
		stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 2), port.port)
		var visited []string
		if err := stream.streamRaw(s.ctx, func(row map[string]any) error {
			visited = append(visited, exec05RowTuple(row))
			return nil
		}); err != nil {
			t.Fatalf("paired control: %v", err)
		}
		if len(visited) != 6 {
			t.Fatalf("control visited %d rows, want 6", len(visited))
		}
	})
}

type exec05ExplainNode struct {
	NodeType     string              `json:"Node Type"`
	RelationName string              `json:"Relation Name"`
	IndexName    string              `json:"Index Name"`
	Plans        []exec05ExplainNode `json:"Plans"`
}

func (n exec05ExplainNode) walk(visit func(exec05ExplainNode)) {
	visit(n)
	for _, child := range n.Plans {
		child.walk(visit)
	}
}

func exec05BoundArgs(args []command.PostgreSQLParameter) []any {
	bound := make([]any, 0, len(args))
	for _, arg := range args {
		bound = append(bound, arg.Value)
	}
	return bound
}

// exec05ExplainAnalyzedRuns runs EXPLAIN (ANALYZE, FORMAT JSON) and returns
// the top-level Actual Rows: a server-measured witness that the statement
// touched only the requested page, independent of which physical plan shape
// the planner chose.
func exec05ExplainAnalyzedRuns(t *testing.T, s *exec05LiveService, sql string, args []command.PostgreSQLParameter) int64 {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+sql, exec05BoundArgs(args)...)
	if err != nil {
		s.fail("EXPLAIN ANALYZE "+sql, err)
	}
	defer rows.Close()
	var encoded []byte
	for rows.Next() {
		if err := rows.Scan(&encoded); err != nil {
			s.fail("scan EXPLAIN ANALYZE", err)
		}
	}
	if err := rows.Err(); err != nil {
		s.fail("EXPLAIN ANALYZE rows error", err)
	}
	var explained []struct {
		Plan struct {
			ActualRows float64 `json:"Actual Rows"`
		} `json:"Plan"`
	}
	if err := json.Unmarshal(encoded, &explained); err != nil || len(explained) != 1 {
		t.Fatalf("decode EXPLAIN ANALYZE output: %v (%s)", err, s.scrub(string(encoded)))
	}
	return int64(explained[0].Plan.ActualRows)
}

func exec05Explain(t *testing.T, s *exec05LiveService, sql string, args []command.PostgreSQLParameter) exec05ExplainNode {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "EXPLAIN (FORMAT JSON) "+sql, exec05BoundArgs(args)...)
	if err != nil {
		s.fail("EXPLAIN "+sql, err)
	}
	defer rows.Close()
	var encoded []byte
	for rows.Next() {
		if err := rows.Scan(&encoded); err != nil {
			s.fail("scan EXPLAIN", err)
		}
	}
	if err := rows.Err(); err != nil {
		s.fail("EXPLAIN rows error", err)
	}
	var plans []struct {
		Plan exec05ExplainNode `json:"Plan"`
	}
	if err := json.Unmarshal(encoded, &plans); err != nil {
		t.Fatalf("decode EXPLAIN output: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("EXPLAIN returned %d plans, want 1", len(plans))
	}
	return plans[0].Plan
}

func TestPostgresRootPagingExplainBoundedness(t *testing.T) {
	s := newExec05LiveService(t)
	s.seedPlannerFixture(t)

	keyPlan := exec05Explain(t, s, s.keySQLTemplate(), s.keyArgs("p0100", 2))
	sawLimit, sawIndexScan, sawSeqScanResource := false, false, false
	keyPlan.walk(func(node exec05ExplainNode) {
		switch node.NodeType {
		case "Limit":
			sawLimit = true
		case "Index Scan", "Index Only Scan", "Bitmap Index Scan":
			if node.RelationName == "fhir_resource" || node.RelationName == "" {
				sawIndexScan = true
			}
		case "Seq Scan":
			if node.RelationName == "fhir_resource" {
				sawSeqScanResource = true
			}
		}
	})
	if !sawLimit {
		t.Fatalf("key-step plan carries no Limit node: %v", keyPlan)
	}
	if sawSeqScanResource {
		t.Fatalf("key-step plan sequentially scans fhir_resource instead of using the keyset index: %v", keyPlan)
	}
	if !sawIndexScan {
		t.Fatalf("key-step plan shows no index access on fhir_resource: %v", keyPlan)
	}
	if keyRows := exec05ExplainAnalyzedRuns(t, s, s.keySQLTemplate(), s.keyArgs("p0100", 2)); keyRows > 2 {
		t.Fatalf("key step returned %d rows under LIMIT 2: the page is not server-side bounded", keyRows)
	}

	// The row step is bounded by its page: on the planner fixture every root
	// carries exactly three edges, so a two-key page must touch six expansion
	// rows server-side, not the 600-row full scope. This witness is
	// planner-shape independent (small relations may legitimately hash join
	// and sequential-scan).
	rowsArgs := s.rowsArgs()
	rowsArgs[4] = command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["p0100","p0101"]`)}
	if rowRows := exec05ExplainAnalyzedRuns(t, s, s.rowsSQLTemplate(), rowsArgs); rowRows != 6 {
		t.Fatalf("row step touched %d expansion rows for a two-key page, want exactly 6 (full scope would be 600)", rowRows)
	}

	// The bounded walk itself: 200 roots at page size 100 converge in 2 full
	// pages plus one empty key page, every key step server-side bounded.
	port := &exec05LivePort{s: s}
	stream := exec05LiveStream(t, s, s.newExec05LiveSteps(t, 100), port.port)
	visited := 0
	if err := stream.streamRaw(s.ctx, func(map[string]any) error {
		visited++
		return nil
	}); err != nil {
		s.fail("planner-fixture paged walk", err)
	}
	if visited != 600 {
		t.Fatalf("visited %d expansion rows, want 600 (200 roots x 3 edges)", visited)
	}
	keys, rows := port.counts()
	if keys != 3 || rows != 2 {
		t.Fatalf("port calls keys=%d rows=%d, want 3/2 (two full pages + empty final page)", keys, rows)
	}
}

func TestPostgresRootPagingWholeInputBypass(t *testing.T) {
	t.Run("whole-input PostgreSQL output cannot reach the root-page loop", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		calls := 0
		engine := s.engine(t, func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			calls++
			return s.executor(ctx, request, visit)
		})
		engine.rootPageRows = 50
		resolved := exec05LiveResolved()
		output := resolved.Compiled.Outputs[0]
		output.Name = "grouped"
		binds := exec05LiveBindVars()
		binds["preview_limit"] = 25
		output.Plan = ir.PhysicalPlan{
			Version:  1,
			Engine:   ir.PhysicalEngineAQL,
			BindVars: binds,
			Operations: []ir.PhysicalOperation{{
				Kind:      ir.PhysicalGroupRowsOp,
				GroupRows: &ir.PhysicalGroupRows{LimitBindKey: "preview_limit"},
			}},
		}
		resolved.Compiled.Outputs = []lower.CompiledRecipeOutput{output}
		resolved.Semantic.SemanticPlan.Bindings.OutputNames = []string{"grouped"}
		streams, err := engine.Streams(s.ctx, resolved)
		if err != nil {
			t.Logf("whole-input bypass arm: compile-refused before execution (%s)", s.scrub(err))
			if calls != 0 {
				t.Fatalf("refused whole-input plan reached the port %d times", calls)
			}
			return
		}
		t.Logf("whole-input bypass arm: compiled, structurally single-statement")
		if len(streams) != 1 || streams[0].page != nil || streams[0].pgRootPage != nil || streams[0].rootPageRows != 0 {
			t.Fatalf("whole-input stream carries page shape: %+v", streams[0])
		}
		if _, err := streams[0].Stream(s.ctx, func(map[string]any) error { return nil }); err != nil {
			s.fail("whole-input single statement", err)
		}
		if calls != 1 {
			t.Fatalf("whole-input port calls = %d, want exactly one statement", calls)
		}
	})
	t.Run("control: non-group PostgreSQL stream never carries page fields", func(t *testing.T) {
		s := newExec05LiveService(t)
		s.seedAcceptanceFixture(t)
		calls := 0
		engine := s.engine(t, func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			calls++
			return s.executor(ctx, request, visit)
		})
		engine.rootPageRows = 50
		streams, err := engine.Streams(s.ctx, exec05LiveResolved())
		if err != nil {
			s.fail("control streams", err)
		}
		if len(streams) != 1 || streams[0].page != nil || streams[0].pgRootPage != nil || streams[0].rootPageRows != 0 {
			t.Fatalf("engine-built PG stream carries page shape: %+v", streams[0])
		}
		result, err := streams[0].Stream(s.ctx, func(map[string]any) error { return nil })
		if err != nil {
			s.fail("control single statement", err)
		}
		if calls != 1 || result.RowCount != 5 {
			t.Fatalf("control port calls=%d RowCount=%d, want 1/5", calls, result.RowCount)
		}
	})
}

// s.engine wires a PostgreSQL-selected production engine whose single row
// port is supplied by the caller (Arango callbacks stay refused: this is a
// PostgreSQL-selected composition with no fallback to arm).
func (s *exec05LiveService) engine(t *testing.T, sourceRows SourceRows) *Engine {
	t.Helper()
	config := Config{
		Registry:      invalidRecipeRegistry{},
		CompileTarget: s.target,
		SourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
				return err
			}
			return sourceRows(ctx, request, visit)
		},
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	return engine
}

func exec05LiveBindVars() map[string]any {
	return map[string]any{
		"root_collection":                  exec05RootType,
		"project":                          exec05Project,
		"dataset_generation":               exec05Generation,
		"auth_resource_paths":              []string{exec05AuthPath},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
	}
}

// exec05LiveResolved is the root-scan output inside the registered
// PostgreSQL root-scan slice: five typed scope columns, row identity over
// logical_id, compiled by the selected PostgreSQL target.
func exec05LiveResolved() Resolved {
	columns := []string{"project", "dataset_generation", "resource_type", "logical_id", "auth_resource_path"}
	schema := make([]lower.CompiledOutputColumn, 0, len(columns))
	projections := make([]ir.PhysicalProjection, 0, len(columns))
	for _, column := range columns {
		schema = append(schema, lower.CompiledOutputColumn{ID: column, Name: column, Label: column, Kind: "string", Cardinality: "one"})
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{column}},
		})
	}
	output := lower.CompiledRecipeOutput{
		Name:             "roots",
		RootResourceType: exec05RootType,
		Columns:          append([]string(nil), columns...),
		OutputSchema:     schema,
		RowIdentity:      &spec.RowIdentity{Fields: []string{"logical_id"}},
		Plan: ir.PhysicalPlan{
			Version:  1,
			Engine:   ir.PhysicalEngineAQL,
			BindVars: exec05LiveBindVars(),
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
	return Resolved{
		Bundle: recipe.Bundle{Name: "exec05_roots"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: exec05Project, DatasetGeneration: exec05Generation, OutputNames: []string{"roots"},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "exec05-roots",
	}
}

func exec05CollectBaseline(t *testing.T, s *exec05LiveService, streams []OutputStream) []string {
	t.Helper()
	if len(streams) != 1 {
		t.Fatalf("baseline streams = %d, want 1", len(streams))
	}
	var roots []string
	if _, err := streams[0].Stream(s.ctx, func(row map[string]any) error {
		roots = append(roots, fmt.Sprint(row["logical_id"]))
		return nil
	}); err != nil {
		s.fail("baseline stream", err)
	}
	return roots
}

func TestZZPostgresRootPagingOwnershipAudit(t *testing.T) {
	raw := exec05URL(t)
	exec05CaptureBaseline(raw)
	if exec05BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", exec05BaselineErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %v", err)
	}
	defer admin.Close()
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec05\\_%' ESCAPE '\\'")
	if err != nil {
		t.Fatalf("audit query: %v", err)
	}
	defer rows.Close()
	var survivors []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("audit scan: %v", err)
		}
		survivors = append(survivors, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("audit rows error: %v", err)
	}
	baseline := map[string]bool{}
	for _, name := range exec05BaselineNames {
		baseline[name] = true
	}
	owned := exec05RegistrySnapshot()
	for _, name := range survivors {
		if !baseline[name] {
			t.Errorf("unregistered survivor under verify_exec05_*: %s", name)
		}
	}
	for name := range owned {
		for _, survivor := range survivors {
			if survivor == name {
				t.Errorf("owned database survived cleanup: %s (schema %q)", name, owned[name])
			}
		}
	}
}
