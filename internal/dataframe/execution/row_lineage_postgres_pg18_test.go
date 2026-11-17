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
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/projectid"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec08Prefix  = "verify_exec08_rl_"
	exec08NameFmt = exec08Prefix + "%d_%s"

	exec08Project    = "project-a"
	exec08Generation = "gen-a"
	exec08AuthPath   = "FHIR/Observation"
	exec08Explorer   = "observations"
)

const exec08GroupRowID = `[["construction","exec08_group"],["operation","GROUP"],["grouped_status_id","final"]]`

var exec08NamePattern = regexp.MustCompile(`^verify_exec08_rl_[0-9]+_[0-9a-f]+$`)

var (
	exec08OwnedMu     sync.Mutex
	exec08OwnedByName = map[string]string{}
)

func exec08Register(db, schema string) {
	exec08OwnedMu.Lock()
	defer exec08OwnedMu.Unlock()
	exec08OwnedByName[db] = schema
}

func exec08RegistrySnapshot() map[string]string {
	exec08OwnedMu.Lock()
	defer exec08OwnedMu.Unlock()
	out := make(map[string]string, len(exec08OwnedByName))
	for db, schema := range exec08OwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec08BaselineOnce  sync.Once
	exec08BaselineNames []string
	exec08BaselineErr   error
)

func exec08CaptureBaseline(raw string) {
	exec08BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec08BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec08\_rl\_%' ESCAPE '\' UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec08\_rl\_%' ESCAPE '\'`)
		if err != nil {
			exec08BaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec08BaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec08BaselineNames = append(exec08BaselineNames, name)
		}
		exec08BaselineErr = rows.Err()
	})
}

func exec08RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

type exec08Service struct {
	ctx     context.Context
	iso     *pgxpool.Pool
	schema  string
	target  compiler.Target
	scrub   func(any) string
	fail    func(step string, err error)
	adapter dataframeexecution.SourceRows
}

func newExec08Service(t *testing.T) *exec08Service {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 row-lineage acceptance tests")
	}
	scrub, addSecret := exec04pvScrubberFor(raw)
	exec08CaptureBaseline(raw)
	if exec08BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec08BaselineErr))
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

	dbName := fmt.Sprintf(exec08NameFmt, time.Now().UnixNano(), exec08RandomHex(t))
	if !exec08NamePattern.MatchString(dbName) {
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
	exec08Register(dbName, "")
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

	schema := fmt.Sprintf(exec08NameFmt, time.Now().UnixNano(), exec08RandomHex(t))
	if !exec08NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec08Register(dbName, schema)
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
	return &exec08Service{ctx: ctx, iso: iso, schema: schema, target: target, scrub: scrub, fail: fail, adapter: adapter}
}

func exec08Bundle() recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "exec08_row_lineage",
		TranslationVersion:  "exec08",
		Outputs: []recipe.Output{{
			Name: exec08Explorer, RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
				Steps: []recipe.ConstructionStep{{
					ID:     "group_status",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID:   "exec08_group",
						MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys:             []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
						Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "grouped_status_id", Name: "status"}, {ID: "rows_id", Name: "rows", Type: "integer"}},
				}},
			},
		}},
	}
}

func exec08Bindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: []string{exec08AuthPath},
	}
}

type exec08SeedRow struct {
	project, generation, logicalID, authPath, status string
}

func (s *exec08Service) seed(t *testing.T, rows []exec08SeedRow) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	for _, row := range rows {
		encoded, err := json.Marshal(map[string]any{"id": row.logicalID, "status": row.status})
		if err != nil {
			t.Fatalf("encode payload for %s: %v", row.logicalID, err)
		}
		if _, err := repo.InsertResource(s.ctx, source.Resource{
			Project:           row.project,
			DatasetGeneration: row.generation,
			ResourceType:      "Observation",
			LogicalID:         row.logicalID,
			AuthResourcePath:  row.authPath,
			Payload:           json.RawMessage(encoded),
		}); err != nil {
			s.fail("insert fixture resource "+row.logicalID, err)
		}
	}
}

func (s *exec08Service) seedStoredNullID(t *testing.T, project, generation, logicalID, authPath, status string) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	if _, err := repo.InsertResource(s.ctx, source.Resource{
		Project: project, DatasetGeneration: generation, ResourceType: "Observation",
		LogicalID: logicalID, AuthResourcePath: authPath,
		Payload: json.RawMessage(`{"id":null,"status":"` + status + `"}`),
	}); err != nil {
		s.fail("insert null-id fixture resource "+logicalID, err)
	}
}

type exec08Call struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec08Recorder struct {
	adapter dataframeexecution.SourceRows
	mu      sync.Mutex
	calls   []exec08Call
}

func (r *exec08Recorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.calls = append(r.calls, exec08Call{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	r.mu.Unlock()
	return r.adapter(ctx, request, visit)
}

func (r *exec08Recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *exec08Recorder) last() exec08Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

func (s *exec08Service) engine(t *testing.T, recorder *exec08Recorder) *dataframeexecution.Engine {
	t.Helper()
	config := exec04pvForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec08-scope" }
	config.SourceRows = recorder.port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

func (s *exec08Service) previewIdentity(t *testing.T, engine *dataframeexecution.Engine, resolved dataframeexecution.Resolved, wantStatus string) string {
	t.Helper()
	var identity string
	matched := 0
	_, err := engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: exec08Explorer, Limit: 50, IncludeRowIdentity: true}, func(row map[string]any) error {
		if row["status"] == wantStatus {
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
		t.Fatalf("preview published %d rows for status %q, want exactly one", matched, wantStatus)
	}
	return identity
}

type contributorTriple struct {
	resourceType string
	resourceID   string
	occurrence   string
}

func exec08TriplesFrom(contributors []dataframeexecution.RowLineageContributor) []contributorTriple {
	out := make([]contributorTriple, 0, len(contributors))
	for _, contributor := range contributors {
		out = append(out, contributorTriple{contributor.ResourceType, contributor.ResourceID, contributor.OccurrenceKey})
	}
	return out
}

func exec08Triples(result dataframeexecution.RowLineageResult) []contributorTriple {
	return exec08TriplesFrom(result.Contributors)
}

func exec08WantTriples(ids ...string) []contributorTriple {
	out := make([]contributorTriple, 0, len(ids))
	for _, id := range ids {
		out = append(out, contributorTriple{"Observation", id, id})
	}
	return out
}

func exec08TriplesEqual(got, want []contributorTriple) bool {
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

func TestPostgresRowLineageKnownRowExplainsContributors(t *testing.T) {
	s := newExec08Service(t)
	s.seed(t, []exec08SeedRow{
		{exec08Project, exec08Generation, "obs-a", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-b", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-c", exec08AuthPath, "amended"},
	})
	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(exec08Project, exec08Generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	identity := s.previewIdentity(t, engine, resolved, "final")
	before := recorder.count()
	result, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: identity, Offset: 0, Limit: 25})
	if err != nil {
		t.Fatalf("RowLineage: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("lineage ran %d statements, want exactly one", got)
	}
	call := recorder.last()
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("lineage command tag = %q", call.tag)
	}
	if want := []string{"found", "contributors", "hasMore"}; strings.Join(call.declared, ",") != strings.Join(want, ",") {
		t.Fatalf("declared column contract = %v, want %v", call.declared, want)
	}
	if want := exec08WantTriples("obs-a", "obs-b"); !exec08TriplesEqual(exec08Triples(result), want) {
		t.Fatalf("contributors = %v, want %v", exec08Triples(result), want)
	}
	if result.HasMore || result.NextOffset != 0 {
		t.Fatalf("paging summary = %#v, want complete page", result)
	}
	again, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: identity, Offset: 0, Limit: 25})
	if err != nil || !exec08TriplesEqual(exec08Triples(again), exec08WantTriples("obs-a", "obs-b")) {
		t.Fatalf("page drifted between executions: %#v (%s)", again, s.scrub(err))
	}
	for _, leaked := range []string{exec08Project, exec08Generation, exec08AuthPath, "final", "obs-a", identity} {
		if strings.Contains(call.sql, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, call.sql)
		}
	}
	boundIdentity := false
	for _, arg := range call.args {
		if text, ok := arg.Value.(string); ok && text == identity {
			boundIdentity = true
		}
	}
	if !boundIdentity {
		t.Fatalf("the requested row identity never arrived as a bound parameter")
	}
}

func TestPostgresRowLineageUnknownRowReturnsTypedNotFound(t *testing.T) {
	s := newExec08Service(t)
	s.seed(t, []exec08SeedRow{
		{exec08Project, exec08Generation, "obs-a", exec08AuthPath, "final"},
	})
	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(exec08Project, exec08Generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	unknown := `[["construction","exec08_group"],["operation","GROUP"],["grouped_status_id","cancelled"]]`
	before := recorder.count()
	result, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: unknown, Offset: 0, Limit: 25})
	if !errors.Is(err, dataframeexecution.ErrRowLineageRowNotFound) {
		t.Fatalf("unknown row = %#v, %v, want the typed not-found", result, err)
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("unknown-row lineage ran %d statements, want exactly one", got)
	}
	if len(result.Contributors) != 0 || result.HasMore {
		t.Fatalf("unknown-row result leaked a page: %#v", result)
	}
}

func TestPostgresRowLineagePagingAdvancesWithoutDuplicatesAndStopsAtTerminalPage(t *testing.T) {
	s := newExec08Service(t)
	s.seed(t, []exec08SeedRow{
		{exec08Project, exec08Generation, "obs-a", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-b", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-c", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-d", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-e", exec08AuthPath, "final"},
	})
	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(exec08Project, exec08Generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	identity := s.previewIdentity(t, engine, resolved, "final")

	var pages [][]contributorTriple
	var hasMore []bool
	seen := map[contributorTriple]int{}
	offset := 0
	for {
		before := recorder.count()
		result, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: identity, Offset: offset, Limit: 2})
		if err != nil {
			t.Fatalf("RowLineage offset %d: %s", offset, s.scrub(err))
		}
		if got := recorder.count() - before; got != 1 {
			t.Fatalf("offset %d ran %d statements, want exactly one", offset, got)
		}
		triples := exec08Triples(result)
		for _, triple := range triples {
			if prior, dup := seen[triple]; dup {
				t.Fatalf("contributor %v repeated on page offset %d (first at offset %d)", triple, offset, prior)
			}
			seen[triple] = offset
		}
		pages = append(pages, triples)
		hasMore = append(hasMore, result.HasMore)
		if !result.HasMore {
			if result.NextOffset != 0 {
				t.Fatalf("terminal page set NextOffset %d, want 0", result.NextOffset)
			}
			break
		}
		if result.NextOffset != offset+len(triples) {
			t.Fatalf("offset %d: NextOffset = %d, want %d", offset, result.NextOffset, offset+len(triples))
		}
		if len(triples) == 0 {
			t.Fatalf("offset %d: HasMore=true with an empty page — the loop cannot advance", offset)
		}
		offset = result.NextOffset
		if len(pages) > 10 {
			t.Fatalf("paging did not converge to the terminal page")
		}
	}
	if len(pages) != 3 {
		t.Fatalf("pages served = %d, want three", len(pages))
	}
	if len(pages[0]) != 2 || len(pages[1]) != 2 || len(pages[2]) != 1 {
		t.Fatalf("page sizes = %d,%d,%d, want 2,2,1", len(pages[0]), len(pages[1]), len(pages[2]))
	}
	if !hasMore[0] || !hasMore[1] || hasMore[2] {
		t.Fatalf("hasMore progression = %v, want true,true,false", hasMore)
	}
	var union []contributorTriple
	for _, page := range pages {
		union = append(union, page...)
	}
	if want := exec08WantTriples("obs-a", "obs-b", "obs-c", "obs-d", "obs-e"); !exec08TriplesEqual(union, want) {
		t.Fatalf("page union = %v, want %v", union, want)
	}
}

func TestPostgresRowLineageForeignWitnessesAreExcluded(t *testing.T) {
	s := newExec08Service(t)
	s.seed(t, []exec08SeedRow{
		{exec08Project, exec08Generation, "obs-a", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-b", exec08AuthPath, "final"},
		{"project-b", exec08Generation, "obs-foreign-project", exec08AuthPath, "final"},
		{exec08Project, "gen-b", "obs-foreign-generation", exec08AuthPath, "final"},
		{exec08Project, exec08Generation, "obs-foreign-auth", "FHIR/Restricted", "final"},
	})
	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(exec08Project, exec08Generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	identity := s.previewIdentity(t, engine, resolved, "final")
	before := recorder.count()
	result, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: identity, Offset: 0, Limit: 25})
	if err != nil {
		t.Fatalf("RowLineage: %s", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("lineage ran %d statements, want exactly one", got)
	}
	if want := exec08WantTriples("obs-a", "obs-b"); !exec08TriplesEqual(exec08Triples(result), want) {
		t.Fatalf("foreign witnesses leaked: contributors = %v, want %v", exec08Triples(result), want)
	}
	if result.HasMore {
		t.Fatalf("foreign witnesses inflated hasMore: %#v", result)
	}
	groups := 0
	rows := 0
	_, err = engine.PreviewOutput(s.ctx, resolved, dataframeexecution.PreviewRequest{Output: exec08Explorer, Limit: 50}, func(row map[string]any) error {
		if row["status"] != "final" {
			t.Fatalf("out-of-scope witness row reached the preview: %#v", row)
		}
		groups++
		if count, ok := row["rows"].(int64); ok {
			rows = int(count)
		}
		return nil
	})
	if err != nil || groups != 1 || rows != 2 {
		t.Fatalf("in-scope preview affected by foreign witnesses: groups=%d rows=%d err=%s", groups, rows, s.scrub(err))
	}
}

func TestPostgresRowLineageMalformedContributorIdentityIsContractError(t *testing.T) {
	s := newExec08Service(t)
	s.seed(t, []exec08SeedRow{
		{exec08Project, exec08Generation, "obs-clean", exec08AuthPath, "final-clean"},
	})
	s.seedStoredNullID(t, exec08Project, exec08Generation, "obs-null-id", exec08AuthPath, "final-null")
	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(exec08Project, exec08Generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	nullIdentity := s.previewIdentity(t, engine, resolved, "final-null")
	before := recorder.count()
	_, err = engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: nullIdentity, Offset: 0, Limit: 25})
	if err == nil || !strings.Contains(err.Error(), "without a FHIR resource identity") {
		t.Fatalf("null-id contributor accepted: %v, want the contract error", s.scrub(err))
	}
	if got := recorder.count() - before; got != 1 {
		t.Fatalf("malformed page ran %d statements, want exactly one (refused at decode, not before execution)", got)
	}
	cleanIdentity := s.previewIdentity(t, engine, resolved, "final-clean")
	result, err := engine.RowLineage(s.ctx, resolved, dataframeexecution.RowLineageRequest{Output: exec08Explorer, RowID: cleanIdentity, Offset: 0, Limit: 25})
	if err != nil || !exec08TriplesEqual(exec08Triples(result), exec08WantTriples("obs-clean")) {
		t.Fatalf("clean control failed: %#v %s", result, s.scrub(err))
	}
}

type exec08ExplorerStore struct {
	explorer.Store
}

type exec08Authorized struct {
	mu        sync.Mutex
	snapshot  capability.Snapshot
	scope     authscope.ReadScope
	hookCalls int
}

func (a *exec08Authorized) set(snapshot capability.Snapshot, scope authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot = snapshot
	a.scope = scope
}

func (a *exec08Authorized) authorized() (capability.Snapshot, authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot, a.scope
}

func (a *exec08Authorized) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hookCalls
}

func exec08ScopeDigest(scope authscope.ReadScope) string {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:])
}

func exec08Snapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	return capability.Snapshot{
		Identity: capability.SnapshotIdentity{
			Project: project, Generation: generation,
			AuthorizationScopeDigest: exec08ScopeDigest(scope),
			SchemaDigest:             "exec08-schema", ShapeDigest: "exec08-shape",
		},
		Status: capability.StatusReady, Complete: true, Token: token,
	}
}

func exec08Receipt(t *testing.T, snapshot capability.Snapshot) explorer.CompilationReceipt {
	t.Helper()
	bundle := exec08Bundle()
	contract := json.RawMessage(`{"outputs":[{"outputId":"observations","columns":[{"column":"status","label":"Status","logicalType":"string","filterable":true,"chartable":true},{"column":"rows","label":"Rows","logicalType":"integer","filterable":true,"chartable":false}]}]}`)
	emitted := []explorer.EmittedColumn{
		{OutputID: exec08Explorer, EmissionID: "em_status", PublicColumn: "status", Label: "Status", LogicalType: "string", Filterable: true, Chartable: true},
		{OutputID: exec08Explorer, EmissionID: "em_rows", PublicColumn: "rows", Label: "Rows", LogicalType: "integer", Filterable: true},
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatalf("bundle digest: %v", err)
	}
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:     explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion:  explorer.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               exec08Explorer,
		IntentDigest:             "exec08-intent",
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		RecipeDigest:             bundleDigest,
		ResolvedRecipeDigest:     bundleDigest,
		ResolvedSchemaDigest:     "exec08-resolved-schema",
		CompiledConfig:           json.RawMessage(`{}`),
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		EmittedColumns:           emitted,
		OutputColumnProvenance:   map[string]map[string]string{exec08Explorer: {"status": "EXPLICIT", "rows": "EXPLICIT"}},
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

func exec08LifecycleService(t *testing.T, authorized *exec08Authorized, engine *dataframeexecution.Engine, receipts ...explorer.CompilationReceipt) *lifecycle.Service {
	t.Helper()
	byID := map[string]explorer.CompilationReceipt{}
	for _, receipt := range receipts {
		byID[receipt.ID] = receipt
	}
	domain, err := explorer.NewService(&exec08ExplorerStore{})
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
	config.RowLineage = func(ctx context.Context, got *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		authorized.mu.Lock()
		authorized.hookCalls++
		authorized.mu.Unlock()
		resolved, err := engine.CompileResolvedBundle(ctx, got.Bundle, bindings)
		if err != nil {
			return dataframeexecution.RowLineageResult{}, err
		}
		return engine.RowLineage(ctx, resolved, request)
	}
	service, err := lifecycle.New(domain, config)
	if err != nil {
		t.Fatalf("lifecycle service: %v", err)
	}
	return service
}

func exec08LifecycleRequest(receipt explorer.CompilationReceipt, rowID string, offset, limit int) lifecycle.RowLineageRequest {
	return lifecycle.RowLineageRequest{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID,
		OutputID: exec08Explorer, RowID: rowID, Offset: offset, Limit: limit,
	}
}

func exec08OfflineEngine(t *testing.T, calls *[]exec08Call, mu *sync.Mutex) *dataframeexecution.Engine {
	t.Helper()
	fakeAdapter := func(_ context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		sqlText, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		mu.Lock()
		*calls = append(*calls, exec08Call{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
		mu.Unlock()
		return visit(map[string]any{"found": true, "contributors": json.RawMessage(`[{"resourceType":"Observation","resourceId":"obs-a","occurrenceKey":"obs-a"}]`), "hasMore": false})
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
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec08-scope" }
	config.SourceRows = fakeAdapter
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return engine
}

func TestPostgresRowLineageStaleReceiptClassification(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{exec08AuthPath}}
	fresh := exec08Snapshot(exec08Project, exec08Generation, "token-1", scope)
	receipt := exec08Receipt(t, fresh)
	authorized := &exec08Authorized{snapshot: fresh, scope: scope}
	var calls []exec08Call
	var mu sync.Mutex
	engine := exec08OfflineEngine(t, &calls, &mu)
	service := exec08LifecycleService(t, authorized, engine, receipt)

	result, err := service.RowLineage(context.Background(), exec08LifecycleRequest(receipt, exec08GroupRowID, 0, 25))
	if err != nil || len(result.Contributors) != 1 || result.Contributors[0].ResourceID != "obs-a" {
		t.Fatalf("fresh receipt failed: %#v %v", result, err)
	}
	if authorized.calls() != 1 {
		t.Fatalf("fresh control hook witness: %d invocations, want 1", authorized.calls())
	}
	mu.Lock()
	freshStatements := len(calls)
	mu.Unlock()
	if freshStatements < 1 {
		t.Fatalf("fresh control executed %d statements, want at least one", freshStatements)
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
			snapshot.Identity.AuthorizationScopeDigest = exec08ScopeDigest(scope)
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
			mu.Lock()
			statementsBefore := len(calls)
			mu.Unlock()
			_, err := service.RowLineage(context.Background(), exec08LifecycleRequest(receipt, exec08GroupRowID, 0, 25))
			var lifecycleErr *lifecycle.Error
			if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != lifecycle.ClassConflict || lifecycleErr.Code != "RECEIPT_STALE" {
				t.Fatalf("stale receipt error = %v, want ClassConflict/RECEIPT_STALE", err)
			}
			if authorized.calls() != hooksBefore {
				t.Fatalf("stale receipt reached the row-lineage hook: %d extra invocations", authorized.calls()-hooksBefore)
			}
			mu.Lock()
			executed := len(calls) - statementsBefore
			mu.Unlock()
			if executed != 0 {
				t.Fatalf("stale receipt executed %d statements, want the statement-count-0 witness", executed)
			}
		})
	}

	authorized.set(fresh, scope)
	if _, err := service.RowLineage(context.Background(), exec08LifecycleRequest(receipt, exec08GroupRowID, 0, 25)); err != nil {
		t.Fatalf("restored fresh receipt failed: %v", err)
	}
}

func TestPostgresRowLineageStaleReceiptRejectsBeforeExecution(t *testing.T) {
	s := newExec08Service(t)
	seedProject := projectid.Legacy(exec08Project)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{exec08AuthPath}}
	fresh := exec08Snapshot(exec08Project, exec08Generation, "token-1", scope)
	receipt := exec08Receipt(t, fresh)

	s.seed(t, []exec08SeedRow{
		{seedProject, exec08Generation, "obs-a", exec08AuthPath, "final"},
		{seedProject, exec08Generation, "obs-b", exec08AuthPath, "final"},
		{seedProject, "gen-b", "obs-new", exec08AuthPath, "final"},
	})

	recorder := &exec08Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder)
	authorized := &exec08Authorized{snapshot: fresh, scope: scope}
	current := exec08Snapshot(exec08Project, "gen-b", "token-2", scope)
	currentReceipt := exec08Receipt(t, current)
	service := exec08LifecycleService(t, authorized, engine, receipt, currentReceipt)

	identity := s.previewIdentity(t, engine, mustExec08Resolve(t, s, engine, seedProject, exec08Generation), "final")
	result, err := service.RowLineage(s.ctx, exec08LifecycleRequest(receipt, identity, 0, 25))
	if err != nil || !exec08TriplesEqual(exec08TriplesFrom(result.Contributors), exec08WantTriples("obs-a", "obs-b")) {
		t.Fatalf("fresh receipt failed on live state: %#v %s", result, s.scrub(err))
	}

	stale := fresh
	stale.Identity.Generation = "gen-b"
	authorized.set(stale, scope)
	hooksBefore := authorized.calls()
	statementsBefore := recorder.count()
	_, err = service.RowLineage(s.ctx, exec08LifecycleRequest(receipt, identity, 0, 25))
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
	newIdentity := s.previewIdentity(t, engine, mustExec08Resolve(t, s, engine, seedProject, "gen-b"), "final")
	freshResult, err := service.RowLineage(s.ctx, exec08LifecycleRequest(currentReceipt, newIdentity, 0, 25))
	if err != nil {
		t.Fatalf("current receipt failed: %s", s.scrub(err))
	}
	if !exec08TriplesEqual(exec08TriplesFrom(freshResult.Contributors), exec08WantTriples("obs-new")) {
		t.Fatalf("current receipt contributors = %v, want the gen-b row", exec08TriplesFrom(freshResult.Contributors))
	}
}

func mustExec08Resolve(t *testing.T, s *exec08Service, engine *dataframeexecution.Engine, project, generation string) dataframeexecution.Resolved {
	t.Helper()
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec08Bundle(), exec08Bindings(project, generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle(%s/%s): %s", project, generation, s.scrub(err))
	}
	return resolved
}

func TestZZPostgresRowLineageZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return
	}
	exec08CaptureBaseline(raw)
	if exec08BaselineErr != nil {
		t.Fatalf("baseline: %v", exec08BaselineErr)
	}
	baseline := map[string]bool{}
	for _, name := range exec08BaselineNames {
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
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec08\_rl\_%' ESCAPE '\'`)
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
	rows, err = admin.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec08\_rl\_%' ESCAPE '\'`)
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
		t.Fatalf("verify_exec08_rl_* fixtures survived the run: %v (owned registry: %v)", survivors, exec08RegistrySnapshot())
	}
}
