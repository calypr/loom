package execution_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
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
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	exec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	exec12exPrefix  = "verify_exec12_ex_"
	exec12exNameFmt = exec12exPrefix + "%d_%s"
)

var exec12exNamePattern = regexp.MustCompile(`^verify_exec12_ex_[0-9]+_[0-9a-f]+$`)

var (
	exec12exOwnedMu     sync.Mutex
	exec12exOwnedByName = map[string]string{}
)

func exec12exRegister(db, schema string) {
	exec12exOwnedMu.Lock()
	defer exec12exOwnedMu.Unlock()
	exec12exOwnedByName[db] = schema
}

func exec12exRegistrySnapshot() map[string]string {
	exec12exOwnedMu.Lock()
	defer exec12exOwnedMu.Unlock()
	out := make(map[string]string, len(exec12exOwnedByName))
	for db, schema := range exec12exOwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec12exBaselineOnce  sync.Once
	exec12exBaselineNames []string
	exec12exBaselineErr   error
)

func exec12exCaptureBaseline(raw string) {
	exec12exBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec12exBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec12\\_ex\\_%' ESCAPE '\\'")
		if err != nil {
			exec12exBaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec12exBaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec12exBaselineNames = append(exec12exBaselineNames, name)
		}
	})
}

func exec12exRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func exec12exScrubberFor(raw string) (func(any) string, func(string)) {
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

type exec12exService struct {
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

func exec12exRequireURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 table-shape exclusion acceptance tests")
	}
	return raw
}

func exec12exNewService(t *testing.T) *exec12exService {
	t.Helper()
	raw := exec12exRequireURL(t)
	scrub, addSecret := exec12exScrubberFor(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}
	exec12exCaptureBaseline(raw)
	if exec12exBaselineErr != nil {
		fail("baseline census", exec12exBaselineErr)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatalf("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
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

	dbName := fmt.Sprintf(exec12exNameFmt, time.Now().UnixNano(), exec12exRandomHex(t))
	if !exec12exNamePattern.MatchString(dbName) {
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
	exec12exRegister(dbName, "")
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

	schema := fmt.Sprintf(exec12exNameFmt, time.Now().UnixNano(), exec12exRandomHex(t))
	if !exec12exNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec12exRegister(dbName, schema)
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
	return &exec12exService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, target: target, scrub: scrub, fail: fail, executor: executor}
}

func (s *exec12exService) migrate(t *testing.T) {
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

type exec12exFixtureRow struct {
	project, generation, resourceType, logicalID, authPath, payload string
}

func (s *exec12exService) seed(t *testing.T, rows []exec12exFixtureRow) {
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

type exec12exCall struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec12exRecorder struct {
	s     *exec12exService
	calls []exec12exCall
}

func (r *exec12exRecorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.calls = append(r.calls, exec12exCall{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	return r.s.executor(ctx, request, visit)
}

func (r *exec12exRecorder) counts() int { return len(r.calls) }

type exec12exInvalidRegistry struct{}

func (exec12exInvalidRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exclusion battery compiles authored bundles only")
}

func (exec12exInvalidRegistry) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, errors.New("the exclusion battery compiles authored bundles only")
}

func exec12exForbidArango() dataframeexecution.Config {
	return dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      exec12exInvalidRegistry{},
		SourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL QueryRows must never run for a PostgreSQL-served evidence read")
		},
		PreviewSourceRows: func(_ context.Context, request dataframeexecution.RowsRequest, _ dataframeexecution.RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL PreviewQueryRows must never run for a PostgreSQL-served evidence read")
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			return errors.New("preparePreviewIndex must never run for a PostgreSQL-served evidence read")
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]dataframeexecution.PreviewCollectionIndex, error) {
			return nil, errors.New("previewCoveringIndexes must never run for a PostgreSQL-served evidence read")
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			return "", errors.New("previewCollectionRevision must never run for a PostgreSQL-served evidence read")
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (dataframeexecution.PreviewCollectionScanEvidence, error) {
			return dataframeexecution.PreviewCollectionScanEvidence{}, errors.New("previewCollectionScans must never run for a PostgreSQL-served evidence read")
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			return 0, errors.New("previewCollectionCount must never run for a PostgreSQL-served evidence read")
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			return errors.New("ClickHouse callbacks must never run for a PostgreSQL-served evidence read")
		},
	}
}

func (s *exec12exService) engine(t *testing.T, port dataframeexecution.SourceRows) *dataframeexecution.Engine {
	t.Helper()
	config := exec12exForbidArango()
	config.CompileTarget = s.target
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec12-ex-scope" }
	config.SourceRows = port
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %s", s.scrub(err))
	}
	return engine
}

const (
	exec12exProject    = "P1"
	exec12exGeneration = "gen-a"
	exec12exAuthPath   = "FHIR/Observation"
	exec12exListed     = "alpha"
)

func exec12exReshape(policy recipe.PivotUnlistedCategoryPolicy) *recipe.TableReshape {
	listed := exec12exListed
	return &recipe.TableReshape{Kind: recipe.TableReshapeGroupedPivot, GroupedPivot: &recipe.GroupedPivot{
		ConstructionID: "exec12-pivot", GroupKeys: []string{"obs_group"},
		CategoryColumn: "obs_category", ValueColumn: "obs_value",
		Categories:      []recipe.GroupedPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &listed}, Output: "alpha_cell", Label: "Alpha"}},
		DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
		UnlistedCategoryPolicy: policy,
	}}
}

func exec12exBundle(policy recipe.PivotUnlistedCategoryPolicy, withExtraFilter bool) recipe.Bundle {
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "obs_group", ColumnID: "obs_group", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "obs_category", ColumnID: "obs_category", Expr: recipe.Expression{Select: "root.code.text"}},
			{Name: "obs_value", ColumnID: "obs_value", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		TableReshape: exec12exReshape(policy),
	}
	if withExtraFilter {
		value := "provisional"
		output.Filters = append(output.Filters, recipe.Filter{
			Select: "root.status", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &value}},
		})
	}
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "exec12_exclusions", TranslationVersion: "exec12",
		Outputs: []recipe.Output{output},
	}
}

func exec12exBindings(project, generation string, authPaths ...string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthResourcePaths: append([]string(nil), authPaths...),
	}
}

func exec12exDoc(logicalID, group, category string) string {
	if category == "__missing__" {
		return fmt.Sprintf(`{"resourceType":"Observation","id":%q,"status":%q}`, logicalID, group)
	}
	if category == "__null__" {
		return fmt.Sprintf(`{"resourceType":"Observation","id":%q,"status":%q,"code":{"text":null}}`, logicalID, group)
	}
	return fmt.Sprintf(`{"resourceType":"Observation","id":%q,"status":%q,"code":{"text":%q}}`, logicalID, group, category)
}

func exec12exDocNoIdentity(group, category string) string {
	return fmt.Sprintf(`{"status":%q,"code":{"text":%q}}`, group, category)
}

func exec12exAcceptanceFixture() []exec12exFixtureRow {
	return []exec12exFixtureRow{
		{exec12exProject, exec12exGeneration, "Observation", "obs-ada", exec12exAuthPath, exec12exDoc("obs-ada", "g1", "beta")},
		{exec12exProject, exec12exGeneration, "Observation", "obs-alan", exec12exAuthPath, exec12exDoc("obs-alan", "g2", "gamma")},
		{exec12exProject, exec12exGeneration, "Observation", "obs-listed", exec12exAuthPath, exec12exDoc("obs-listed", "g1", exec12exListed)},
		{exec12exProject, exec12exGeneration, "Observation", "obs-missing", exec12exAuthPath, exec12exDoc("obs-missing", "g3", "__missing__")},
		{"P2", "gen-c", "Observation", "obs-intruder-1", exec12exAuthPath, exec12exDoc("obs-intruder-1", "g1", "beta")},
		{"P2", "gen-c", "Observation", "obs-intruder-2", exec12exAuthPath, exec12exDocNoIdentity("g2", "gamma")},
	}
}

func exec12exCanonical(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("unencodable:%v", err)
	}
	return string(encoded)
}

func exec12exRowsDigest(exclusions []dataframeexecution.TableShapeExclusion) []string {
	out := make([]string, 0, len(exclusions))
	for _, exclusion := range exclusions {
		identity := "<none>"
		if exclusion.SourceIdentity != nil {
			identity = exclusion.SourceIdentity.ResourceType + "/" + exclusion.SourceIdentity.ResourceID
		}
		out = append(out, fmt.Sprintf("%s|%s|present=%t|value=%s|type=%s|reason=%s|omission=%s",
			identity, exclusion.OutputRowID, exclusion.Category.Present, exec12exCanonical(exclusion.Category.Value),
			exclusion.CategoryType, exclusion.Reason, exclusion.OmissionCode))
	}
	return out
}

func TestPostgresExclusionsExactEvidenceLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	s.seed(t, exec12exAcceptanceFixture())
	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, resolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("TableShapeExclusions: %s", s.scrub(err))
	}
	if recorder.counts() != 1 {
		t.Fatalf("expected exactly one statement, got %d", recorder.counts())
	}
	call := recorder.calls[0]
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("statement tag = %q", call.tag)
	}
	wantContract := []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}
	if !reflect.DeepEqual(call.declared, wantContract) {
		t.Fatalf("declared contract = %v, want %v", call.declared, wantContract)
	}
	if len(call.args) < 2 {
		t.Fatalf("statement carries only %d ordered arguments", len(call.args))
	}
	if offset, ok := call.args[len(call.args)-2].Value.(int64); !ok || offset != 0 {
		t.Fatalf("offset parameter = %#v", call.args[len(call.args)-2])
	}
	if fetch, ok := call.args[len(call.args)-1].Value.(int64); !ok || fetch != 11 {
		t.Fatalf("probe-limit parameter must be limit+1, got %#v", call.args[len(call.args)-1])
	}
	if result.Status != dataframeexecution.TableShapeExclusionComplete || !result.Complete || result.HasMore || result.NextOffset != 0 {
		t.Fatalf("result metadata = %#v", result)
	}
	want := []string{
		"Observation/obs-ada|row-|present=true|value=\"beta\"|type=STRING|reason=UNLISTED_CATEGORY|omission=",
		"Observation/obs-alan|row-|present=true|value=\"gamma\"|type=STRING|reason=UNLISTED_CATEGORY|omission=",
		"Observation/obs-missing|row-|present=false|value=null|type=STRING|reason=UNLISTED_CATEGORY|omission=",
	}
	got := exec12exRowsDigest(result.Exclusions)
	for i := range want {
		if i >= len(got) {
			t.Fatalf("missing evidence row %d: want %q, got %v", i, want[i], got)
		}
		gotFields := strings.Split(got[i], "|")
		wantFields := strings.Split(want[i], "|")
		for j := range wantFields {
			if j == 1 {
				continue
			}
			if gotFields[j] != wantFields[j] {
				t.Fatalf("evidence row %d field %d = %q, want %q (row: %q)", i, j, gotFields[j], wantFields[j], got[i])
			}
		}
		if gotFields[1] == "" || gotFields[1] == "row-" {
			t.Fatalf("row identity must be the pivot-computed identity, got %q", got[i])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("evidence count = %d, want %d: %v", len(got), len(want), got)
	}
}

func TestPostgresExclusionsBoundedPagingLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	rows := exec12exAcceptanceFixture()
	for i, category := range []string{"delta", "epsilon", "zeta", "eta", "theta"} {
		logicalID := fmt.Sprintf("obs-page-%d", i)
		rows = append(rows, exec12exFixtureRow{exec12exProject, exec12exGeneration, "Observation", logicalID, exec12exAuthPath, exec12exDoc(logicalID, "gm", category)})
	}
	s.seed(t, rows)
	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	const total = 8
	seen := map[string]bool{}
	offset := 0
	pages := 0
	for {
		before := recorder.counts()
		result, err := engine.TableShapeExclusions(s.ctx, resolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Offset: offset, Limit: 3})
		if err != nil {
			t.Fatalf("page at %d: %s", offset, s.scrub(err))
		}
		if recorder.counts() != before+1 {
			t.Fatalf("page at %d executed %d statements, want exactly 1", offset, recorder.counts()-before)
		}
		if len(result.Exclusions) > 3 {
			t.Fatalf("page at %d delivered %d rows, over the limit", offset, len(result.Exclusions))
		}
		for _, exclusion := range result.Exclusions {
			key := exclusion.OutputRowID + "|" + exec12exCanonical(exclusion.Category.Value)
			if seen[key] {
				t.Fatalf("pages overlap at offset %d on %s", offset, key)
			}
			seen[key] = true
		}
		pages++
		if !result.HasMore {
			if result.Status != dataframeexecution.TableShapeExclusionComplete || !result.Complete {
				t.Fatalf("last page must be COMPLETE, got %#v", result)
			}
			break
		}
		if result.NextOffset <= offset {
			t.Fatalf("NextOffset %d did not advance from %d", result.NextOffset, offset)
		}
		offset = result.NextOffset
		if pages > total {
			t.Fatalf("paging did not converge: %d pages", pages)
		}
	}
	if len(seen) != total {
		t.Fatalf("paged %d distinct exclusions, want %d", len(seen), total)
	}
}

func TestPostgresExclusionsIdentityOmissionLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	rows := []exec12exFixtureRow{
		{exec12exProject, exec12exGeneration, "Observation", "obs-anon", exec12exAuthPath, exec12exDocNoIdentity("ga", "beta")},
		{exec12exProject, exec12exGeneration, "Observation", "obs-exact", exec12exAuthPath, exec12exDoc("obs-exact", "gb", "gamma")},
	}
	s.seed(t, rows)
	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, resolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("TableShapeExclusions: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionIncomplete || result.Complete || len(result.Exclusions) != 2 {
		t.Fatalf("identity omission must report INCOMPLETE, never complete: %#v", result)
	}
	var anon, exact *dataframeexecution.TableShapeExclusion
	for i := range result.Exclusions {
		switch result.Exclusions[i].Category.Value.(type) {
		case json.RawMessage:
			if exec12exCanonical(result.Exclusions[i].Category.Value) == `"beta"` {
				anon = &result.Exclusions[i]
			} else {
				exact = &result.Exclusions[i]
			}
		}
	}
	if anon == nil || exact == nil {
		t.Fatalf("fixture rows not distinguished: %#v", result.Exclusions)
	}
	if anon.SourceIdentity != nil || anon.OmissionCode != "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("missing identity must be an explicit omission, never synthesized: %#v", anon)
	}
	if exact.SourceIdentity == nil || exact.SourceIdentity.ResourceID != "obs-exact" || exact.OmissionCode != "" {
		t.Fatalf("exact row must carry the decoded identity and SQL-NULL omission (parsed as absent): %#v", exact)
	}
}

func TestPostgresExclusionsCategoryThreeStateLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	rows := []exec12exFixtureRow{
		{exec12exProject, exec12exGeneration, "Observation", "obs-m", exec12exAuthPath, exec12exDoc("obs-m", "g3", "__missing__")},
		{exec12exProject, exec12exGeneration, "Observation", "obs-n", exec12exAuthPath, exec12exDoc("obs-n", "g3", "__null__")},
		{exec12exProject, exec12exGeneration, "Observation", "obs-e", exec12exAuthPath, exec12exDoc("obs-e", "g3", "")},
	}
	s.seed(t, rows)
	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, resolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("TableShapeExclusions: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionComplete || len(result.Exclusions) != 3 {
		t.Fatalf("result = %#v", result)
	}
	states := map[string]string{}
	for _, exclusion := range result.Exclusions {
		id := exclusion.SourceIdentity.ResourceID
		states[id] = fmt.Sprintf("present=%t value=%s", exclusion.Category.Present, exec12exCanonical(exclusion.Category.Value))
	}
	if states["obs-m"] != "present=false value=null" {
		t.Fatalf("missing member must read present=false value=SQL NULL(nil): %q", states["obs-m"])
	}
	if states["obs-n"] != `present=true value=null` {
		t.Fatalf("explicit JSON null must read present=true with the raw JSON null carried, never flattened to nil: %q", states["obs-n"])
	}
	for _, exclusion := range result.Exclusions {
		if exclusion.SourceIdentity.ResourceID == "obs-m" && exclusion.Category.Value != nil {
			t.Fatalf("SQL NULL missing-category value must stay nil, got %#v", exclusion.Category.Value)
		}
		if exclusion.SourceIdentity.ResourceID == "obs-n" {
			raw, ok := exclusion.Category.Value.(json.RawMessage)
			if !ok || string(raw) != "null" {
				t.Fatalf("JSON-null category must survive as raw JSON, got %#v", exclusion.Category.Value)
			}
		}
	}
	if states["obs-e"] != `present=true value=""` {
		t.Fatalf("empty-string category must stay a present empty string: %q", states["obs-e"])
	}
}

func TestPostgresExclusionsCancellationLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	rows := exec12exAcceptanceFixture()
	for i, category := range []string{"delta", "epsilon", "zeta"} {
		logicalID := fmt.Sprintf("obs-cancel-%d", i)
		rows = append(rows, exec12exFixtureRow{exec12exProject, exec12exGeneration, "Observation", logicalID, exec12exAuthPath, exec12exDoc(logicalID, "gm", category)})
	}
	s.seed(t, rows)
	recorder := &exec12exRecorder{s: s}
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	cancelling := func(inner dataframeexecution.SourceRows) dataframeexecution.SourceRows {
		return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			seen := 0
			return inner(ctx, request, func(row map[string]any) error {
				err := visit(row)
				seen++
				if err == nil && seen == 1 {
					cancel()
				}
				return err
			})
		}
	}(recorder.port)
	engine := s.engine(t, cancelling)
	resolved, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(ctx, resolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Offset: 0, Limit: 10})
	if err != nil {
		t.Fatalf("cancelled read must report INCOMPLETE, not fail: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionIncomplete || result.Complete || result.NextOffset != 0 {
		t.Fatalf("cancellation must report INCOMPLETE at the requested offset, never complete or complete-empty-as-complete: %#v", result)
	}
	if recorder.counts() == 0 {
		t.Fatal("cancellation leg must actually execute SQL")
	}
}

func TestPostgresExclusionsUnsupportedShapeLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	s.seed(t, exec12exAcceptanceFixture())
	bindings := exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath)

	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	filtered, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, true), bindings)
	if err != nil {
		t.Fatalf("filtered bundle compile: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, filtered, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("outside-slice read must report, not fail: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionUnsupported || result.Complete || len(result.Exclusions) != 0 {
		t.Fatalf("outside-slice result = %#v", result)
	}
	if recorder.counts() != 0 {
		t.Fatalf("UNSUPPORTED must execute zero statements, got %d", recorder.counts())
	}
	registered, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), bindings)
	if err != nil {
		t.Fatalf("registered bundle compile: %s", s.scrub(err))
	}
	control, err := engine.TableShapeExclusions(s.ctx, registered, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("registered control: %s", s.scrub(err))
	}
	if control.Status != dataframeexecution.TableShapeExclusionComplete || len(control.Exclusions) == 0 || recorder.counts() != 1 {
		t.Fatalf("registered control = %#v after %d statements", control, recorder.counts())
	}
}

func TestZZPostgresExclusionsZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 table-shape exclusion acceptance tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	scrub, _ := exec12exScrubberFor(raw)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for audit: %s", scrub(err))
	}
	defer admin.Close()
	owned := exec12exRegistrySnapshot()
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
	for _, name := range exec12exBaselineNames {
		baseline[name] = true
	}
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'verify\\_exec12\\_ex\\_%' ESCAPE '\\'")
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
}

func TestPostgresExclusionsAuthScopeLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	s.seed(t, exec12exAcceptanceFixture())

	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)
	authorized, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath))
	if err != nil {
		t.Fatalf("authorized compile: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, authorized, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("authorized read: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionComplete || len(result.Exclusions) != 3 {
		t.Fatalf("authorized read = %#v", result)
	}
	for _, exclusion := range result.Exclusions {
		if exclusion.SourceIdentity != nil && exclusion.SourceIdentity.ResourceID == "obs-intruder-1" {
			t.Fatalf("P2/gen-c intruder leaked into the P1/gen-a evidence read: %#v", exclusion)
		}
	}
	if recorder.counts() != 1 {
		t.Fatalf("authorized read statement count = %d", recorder.counts())
	}

	unauthorized, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), exec12exBindings(exec12exProject, exec12exGeneration, "FHIR/Encounter"))
	if err != nil {
		t.Fatalf("unauthorized compile: %s", s.scrub(err))
	}
	recorder = &exec12exRecorder{s: s}
	engine = s.engine(t, recorder.port)
	denied, err := engine.TableShapeExclusions(s.ctx, unauthorized, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("unauthorized read: %s", s.scrub(err))
	}
	if denied.Status != dataframeexecution.TableShapeExclusionComplete || len(denied.Exclusions) != 0 {
		t.Fatalf("unauthorized read must yield zero evidence, got %#v", denied)
	}
	if recorder.counts() != 1 {
		t.Fatalf("the negative leg must execute real SQL, got %d statements", recorder.counts())
	}
	foundAuthArg := false
	for _, arg := range recorder.calls[0].args {
		if raw, ok := arg.Value.(json.RawMessage); ok && strings.Contains(string(raw), "FHIR/Encounter") {
			foundAuthArg = true
		}
	}
	if !foundAuthArg {
		t.Fatalf("the unauthorized path set must cross SQL as an ordered parameter: %#v", recorder.calls[0].args)
	}
}

func TestPostgresExclusionsNoPolicyStatusDistinctLivePG18(t *testing.T) {
	s := exec12exNewService(t)
	s.migrate(t)
	s.seed(t, exec12exAcceptanceFixture())
	bindings := exec12exBindings(exec12exProject, exec12exGeneration, exec12exAuthPath)

	recorder := &exec12exRecorder{s: s}
	engine := s.engine(t, recorder.port)

	noPolicy, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryError, false), bindings)
	if err != nil {
		t.Fatalf("no-policy compile: %s", s.scrub(err))
	}
	result, err := engine.TableShapeExclusions(s.ctx, noPolicy, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("no-policy read: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.TableShapeExclusionNoExclusionPolicy || result.Complete || len(result.Exclusions) != 0 {
		t.Fatalf("no-policy status = %#v", result)
	}
	if recorder.counts() != 0 {
		t.Fatalf("no-policy must execute zero statements, got %d", recorder.counts())
	}

	evidence, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, false), bindings)
	if err != nil {
		t.Fatalf("evidence compile: %s", s.scrub(err))
	}
	withEvidence, err := engine.TableShapeExclusions(s.ctx, evidence, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("evidence read: %s", s.scrub(err))
	}
	if withEvidence.Status != dataframeexecution.TableShapeExclusionComplete || len(withEvidence.Exclusions) == 0 {
		t.Fatalf("evidence control = %#v", withEvidence)
	}
	if withEvidence.Status == result.Status || result.Status == dataframeexecution.TableShapeExclusionComplete {
		t.Fatalf("NO_EXCLUSION_POLICY must be distinct from COMPLETE-with-evidence")
	}
	filtered, err := engine.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryExcludeWithEvidence, true), bindings)
	if err != nil {
		t.Fatalf("filtered compile: %s", s.scrub(err))
	}
	unsupported, err := engine.TableShapeExclusions(s.ctx, filtered, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("unsupported read: %s", s.scrub(err))
	}
	if unsupported.Status != dataframeexecution.TableShapeExclusionUnsupported || unsupported.Status == result.Status {
		t.Fatalf("NO_EXCLUSION_POLICY must be distinct from UNSUPPORTED: %#v", unsupported)
	}
	if recorder.counts() != 1 {
		t.Fatalf("only the COMPLETE-with-evidence witness may run statements (the NO_POLICY and UNSUPPORTED witnesses run zero), got %d", recorder.counts())
	}

	twinConfig := exec12exForbidArango()
	twinConfig.SourceRows = func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		return errors.New("the no-policy twin must never execute a query")
	}
	twinConfig.ScopeDigest = func(recipe.RuntimeBindings) string { return "exec12-ex-twin-scope" }
	twin, err := dataframeexecution.New(twinConfig)
	if err != nil {
		t.Fatalf("twin engine: %s", s.scrub(err))
	}
	twinResolved, err := twin.CompileResolvedBundle(s.ctx, exec12exBundle(recipe.PivotUnlistedCategoryError, false), bindings)
	if err != nil {
		t.Fatalf("twin compile: %s", s.scrub(err))
	}
	twinResult, err := twin.TableShapeExclusions(s.ctx, twinResolved, dataframeexecution.TableShapeExclusionRequest{Output: "observations", Limit: 10})
	if err != nil {
		t.Fatalf("twin read: %s", s.scrub(err))
	}
	if twinResult.Status != dataframeexecution.TableShapeExclusionNoExclusionPolicy || twinResult.Status != result.Status {
		t.Fatalf("AQL twin status %q drifted from the PostgreSQL arm %q", twinResult.Status, result.Status)
	}
}
