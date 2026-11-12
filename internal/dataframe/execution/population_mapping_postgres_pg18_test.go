package execution_test

// pg-exec-07 live acceptance battery: Engine.PopulationMapping over the
// merged pg-sql-22 witness terminals (check-2: go test -count=1
// ./internal/dataframe/execution -run PostgresPopulationMapping under
// LOOM_TEST_POSTGRES_URL against the isolated PostgreSQL 18 service).
//
// Every live leg is production-compiled end to end: an authored
// recipe.Bundle (with its PopulationConstraint) goes through the real
// Engine.CompileResolvedBundle on the selected PostgreSQL compile target;
// membership rides the real ExplorerStore staging lifecycle (Begin, Append,
// Digest, Complete) in the owned schema; execution streams the
// parent-tagged witness terminal onto the real server through the
// production internal/backends/postgres row adapter wrapped by a recorder,
// so statement counts, ordered typed arguments, the backend tag, and the
// declared witness column contract are witnessed at the port, never
// asserted by a mock. The stale-receipt leg drives the real explorer
// lifecycle Service with the engine-backed PopulationMapping hook in the
// shape the PostgreSQL runtime wires (internal/server/runtime.go:732-749),
// refusing with zero hook invocations and a statement-count-0 witness.
//
// AQL-parity note on the count literals: the flat and construction
// PostgreSQL witness terminals (the registered slice in
// render/postgres/population_mapping.go and population.go) join members to
// root rows on the UNIQUE (project, dataset_generation, resource_type,
// logical_id) identity, so each selected member contributes at most one
// witness row per terminal identity; the engine-level guarantee with a
// member mapping several witness rows (selected=3, mapped=2, unmapped=1,
// emittedRows=3 with de-duplication) is pinned by the offline battery,
// where the port stands in for outputs whose witness fan-out the AQL route
// arm legitimately produces. Here the live legs pin the honest registered
// numbers: flat 3/2/1/2, and the keyed-group construction terminal proving
// engine-side identity de-duplication live (two mapped members, one group
// row: 3/2/1/1). Shapes outside the registered slice (member-collecting
// root with an expansion, endpoint-lookup route steps) refuse with the
// typed capability sentinel at zero statements — a named capability gap of
// the renderer, witnessed live below.
//
// Fixture databases and schemas are uniquely owned as verify_exec07_cs_*
// and dropped by the test that created them; the TestZZ audit is
// ownership-scoped to this file's own prefix.

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
	exec07Prefix  = "verify_exec07_cs_"
	exec07NameFmt = exec07Prefix + "%d_%s"

	exec07Project     = "project-a"
	exec07Generation  = "gen-a"
	exec07AuthPath    = "FHIR/Observation"
	exec07Explorer    = "observations"
	exec07Selection   = "sel-exec07"
	exec07MembersColl = "loom_explorer_selection_members"
)

var exec07NamePattern = regexp.MustCompile(`^verify_exec07_cs_[0-9]+_[0-9a-f]+$`)

var (
	exec07OwnedMu     sync.Mutex
	exec07OwnedByName = map[string]string{}
)

func exec07Register(db, schema string) {
	exec07OwnedMu.Lock()
	defer exec07OwnedMu.Unlock()
	exec07OwnedByName[db] = schema
}

func exec07RegistrySnapshot() map[string]string {
	exec07OwnedMu.Lock()
	defer exec07OwnedMu.Unlock()
	out := make(map[string]string, len(exec07OwnedByName))
	for db, schema := range exec07OwnedByName {
		out[db] = schema
	}
	return out
}

var (
	exec07BaselineOnce  sync.Once
	exec07BaselineNames []string
	exec07BaselineErr   error
)

func exec07CaptureBaseline(raw string) {
	exec07BaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			exec07BaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec07\_cs\_%' ESCAPE '\' UNION ALL SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec07\_cs\_%' ESCAPE '\'`)
		if err != nil {
			exec07BaselineErr = fmt.Errorf("read baseline: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				exec07BaselineErr = fmt.Errorf("scan baseline: %w", err)
				return
			}
			exec07BaselineNames = append(exec07BaselineNames, name)
		}
		exec07BaselineErr = rows.Err()
	})
}

func exec07RandomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("read name entropy: %v", err)
	}
	return hex.EncodeToString(buf)
}

// ---------------------------------------------------------------------------
// Service harness: isolated database, owned schema with the store history
// plus the ledger-free explorer-family DDL, the real ExplorerStore, the
// PostgreSQL compile target, and the production row adapter.
// ---------------------------------------------------------------------------

type exec07Service struct {
	ctx     context.Context
	iso     *pgxpool.Pool
	schema  string
	target  compiler.Target
	scrub   func(any) string
	addSec  func(string)
	fail    func(string, error)
	adapter dataframeexecution.SourceRows
	store   *storepg.ExplorerStore
	// scopeDigest is the engine compile-identity scope digest; the receipt
	// legs align it with the authorized read scope's policy digest.
	scopeDigest string
}

func newExec07Service(t *testing.T) *exec07Service {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 population-mapping acceptance tests")
	}
	scrub, addSecret := exec04pvScrubberFor(raw)
	exec07CaptureBaseline(raw)
	if exec07BaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %s", scrub(exec07BaselineErr))
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

	dbName := fmt.Sprintf(exec07NameFmt, time.Now().UnixNano(), exec07RandomHex(t))
	if !exec07NamePattern.MatchString(dbName) {
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
	exec07Register(dbName, "")
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

	schema := fmt.Sprintf(exec07NameFmt, time.Now().UnixNano(), exec07RandomHex(t))
	if !exec07NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	exec07Register(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	client, err := storepg.OpenPool(ctx, storepg.ClientConfig{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := storepg.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	// The explorer-family membership relations ride beside the store tables,
	// ledger-free (the coordinator-ruled composition of
	// internal/dataframe/compiler/render/postgres/population_live_test.go):
	// the authoritative migration bodies with search_path pinned to the
	// owned schema, skipping version 1 (the store-history-owned ledger).
	migrations, err := storepg.OwnerMigrations()
	if err != nil {
		fail("load owner migrations", err)
	}
	conn, err := iso.Acquire(ctx)
	if err != nil {
		fail("acquire DDL connection", err)
	}
	if _, err := conn.Exec(ctx, `SET search_path TO "`+schema+`"`); err != nil {
		conn.Release()
		fail("pin search_path for membership DDL", err)
	}
	applied := 0
	for _, migration := range migrations {
		if migration.Version == 1 {
			continue
		}
		if _, err := conn.Exec(ctx, migration.SQL); err != nil {
			conn.Release()
			fail(fmt.Sprintf("apply explorer DDL %d_%s", migration.Version, migration.Name), err)
		}
		applied++
	}
	conn.Release()
	if applied != len(migrations)-1 {
		t.Fatalf("applied %d explorer migrations, want %d", applied, len(migrations)-1)
	}

	pgStore, err := storepg.NewExplorerStore(client, schema)
	if err != nil {
		fail("open explorer store", err)
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
	return &exec07Service{ctx: ctx, iso: iso, schema: schema, target: target, scrub: scrub, addSec: addSecret, fail: fail, adapter: adapter, store: pgStore, scopeDigest: "exec07-scope"}
}

// ---------------------------------------------------------------------------
// Fixtures: production-shaped resources and the real selection lifecycle.
// ---------------------------------------------------------------------------

func (s *exec07Service) seedObservation(t *testing.T, project, generation, logicalID, status string) {
	t.Helper()
	repo, err := storepg.NewResourceRepository(s.iso, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	encoded, err := json.Marshal(map[string]any{"id": logicalID, "status": status})
	if err != nil {
		t.Fatalf("encode payload for %s: %v", logicalID, err)
	}
	if _, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      "Observation",
		LogicalID:         logicalID,
		AuthResourcePath:  exec07AuthPath,
		Payload:           json.RawMessage(encoded),
	}); err != nil {
		s.fail("insert fixture resource "+logicalID, err)
	}
}

// seedSelection drives the real staging lifecycle — Begin, Append, Digest,
// Complete — and returns the completed header, whose frozen digest and
// count the receipt's PopulationConstraint must quote exactly (the
// lifecycle's POPULATION_MAPPING_STALE guard).
func (s *exec07Service) seedSelection(t *testing.T, memberIDs []string, scopeDigest string) explorer.SelectionRevision {
	t.Helper()
	project := projectid.Canonical(exec07Project)
	writer := "exec07-writer"
	now := time.Now().UTC()
	header, err := s.store.BeginSelection(s.ctx, explorer.SelectionRevision{
		ID: exec07Selection, Project: project, Generation: exec07Generation, ResourceType: "Observation",
		Rule:        explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:      explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: scopeDigest, RuleDigest: "exec07-rule", CreatedAt: now,
	}, writer)
	if err != nil {
		s.fail("begin selection", err)
	}
	members := make([]explorer.SelectionMember, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		members = append(members, explorer.SelectionMember{Ref: explorer.ResourceRef{
			Project: project, Generation: exec07Generation, ResourceType: "Observation", ID: memberID}})
	}
	if _, err := s.store.AppendSelectionMembers(s.ctx, header.ID, writer, members); err != nil {
		s.fail("append selection members", err)
	}
	digest, count, bytes, err := s.store.DigestSelectionMembers(s.ctx, project, header.ID)
	if err != nil {
		s.fail("digest selection", err)
	}
	completed, err := s.store.CompleteSelection(s.ctx, header.ID, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		s.fail("complete selection", err)
	}
	if completed.MembershipDigest != digest || completed.MemberCount != count {
		t.Fatalf("completed header facts drifted: %v", completed)
	}
	return *completed
}

// ---------------------------------------------------------------------------
// Bundles. The PopulationConstraint identity fields are filled from the
// completed selection header so the lifecycle guards pass with real facts.
// ---------------------------------------------------------------------------

func exec07Population(header explorer.SelectionRevision) *recipe.PopulationConstraint {
	return &recipe.PopulationConstraint{
		SelectionRevisionID: header.ID, MembershipDigest: header.MembershipDigest,
		MemberCount: header.MemberCount, ResourceType: header.ResourceType,
	}
}

func exec07FlatBundle(name string, header explorer.SelectionRevision) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "exec07",
		Outputs: []recipe.Output{{
			Name: exec07Explorer, RootResourceType: "Observation", RowGrain: "observation",
			Fields:     []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Population: exec07Population(header),
		}},
	}
}

func exec07GroupBundle(name string, header explorer.SelectionRevision) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "exec07",
		Outputs: []recipe.Output{{
			Name: exec07Explorer, RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
				Steps: []recipe.ConstructionStep{{
					ID:     "group_status",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID:   "exec07_group",
						MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys:             []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
						Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{{ID: "grouped_status_id", Name: "status"}, {ID: "rows_id", Name: "rows", Type: "integer"}},
				}},
			},
			Population: exec07Population(header),
		}},
	}
}

func exec07Bindings(project, generation string) recipe.RuntimeBindings {
	return recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		SelectionProject:           projectid.Canonical(exec07Project),
		SelectionMembersCollection: exec07MembersColl,
		AuthResourcePaths:          []string{exec07AuthPath},
	}
}

// ---------------------------------------------------------------------------
// Recorder around the production adapter: statement counts, the backend
// tag, the ordered typed parameters, and the declared witness contract are
// witnessed here.
// ---------------------------------------------------------------------------

type exec07Call struct {
	sql      string
	args     []command.PostgreSQLParameter
	declared []string
	tag      command.CompiledBackend
}

type exec07Recorder struct {
	adapter    dataframeexecution.SourceRows
	mu         sync.Mutex
	calls      []exec07Call
	visits     int
	afterVisit func(visits int)
	failNext   error
}

func (r *exec07Recorder) port(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	sqlText, args, err := request.Command.PostgreSQLParameters()
	if err != nil {
		return err
	}
	r.mu.Lock()
	fail := r.failNext
	r.failNext = nil
	r.calls = append(r.calls, exec07Call{sql: sqlText, args: args, declared: append([]string(nil), request.DeclaredColumns...), tag: request.Command.Backend()})
	r.mu.Unlock()
	if fail != nil {
		return fail
	}
	return r.adapter(ctx, request, func(row map[string]any) error {
		r.mu.Lock()
		r.visits++
		seen := r.visits
		hook := r.afterVisit
		r.mu.Unlock()
		if err := visit(row); err != nil {
			return err
		}
		if hook != nil {
			hook(seen)
		}
		return nil
	})
}

func (r *exec07Recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *exec07Recorder) last() exec07Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

// ---------------------------------------------------------------------------
// Engine wiring: PostgreSQL compile target, the recorder as the single
// source row port, and the exec04 landmine set armed on every Arango and
// ClickHouse control.
// ---------------------------------------------------------------------------

func (s *exec07Service) engine(t *testing.T, recorder dataframeexecution.SourceRows) *dataframeexecution.Engine {
	t.Helper()
	config := exec04pvForbidArango()
	config.CompileTarget = s.target
	config.PreviewSourceRows = nil
	config.ScopeDigest = func(recipe.RuntimeBindings) string { return s.scopeDigest }
	config.SourceRows = recorder
	engine, err := dataframeexecution.New(config)
	if err != nil {
		t.Fatalf("engine: %v", s.scrub(err))
	}
	return engine
}

func (s *exec07Service) resolve(t *testing.T, engine *dataframeexecution.Engine, bundle recipe.Bundle, generation string) dataframeexecution.Resolved {
	t.Helper()
	resolved, err := engine.CompileResolvedBundle(s.ctx, bundle, exec07Bindings(projectid.Legacy(exec07Project), generation))
	if err != nil {
		t.Fatalf("CompileResolvedBundle: %s", s.scrub(err))
	}
	return resolved
}

// ---------------------------------------------------------------------------
// Lifecycle wiring for the receipt-bound legs (the shape the PostgreSQL
// runtime composes at internal/server/runtime.go:732-749).
// ---------------------------------------------------------------------------

type exec07ExplorerStore struct {
	explorer.Store
	pg *storepg.ExplorerStore
}

func (s exec07ExplorerStore) GetSelection(ctx context.Context, project, id string) (*explorer.SelectionRevision, error) {
	return s.pg.GetSelection(ctx, project, id)
}

func (s exec07ExplorerStore) VisitSelectionMembers(ctx context.Context, project, id, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	return s.pg.VisitSelectionMembers(ctx, project, id, after, limit, visit)
}

type exec07Authorized struct {
	mu        sync.Mutex
	snapshot  capability.Snapshot
	scope     authscope.ReadScope
	hookCalls int
}

func (a *exec07Authorized) set(snapshot capability.Snapshot, scope authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.snapshot = snapshot
	a.scope = scope
}

func (a *exec07Authorized) authorized() (capability.Snapshot, authscope.ReadScope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshot, a.scope
}

func (a *exec07Authorized) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hookCalls
}

// exec07ScopeDigest reproduces the lifecycle policy digest over a read
// scope exactly as internal/explorer/lifecycle/policy.go computes it.
func exec07ScopeDigest(scope authscope.ReadScope) string {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return hex.EncodeToString(sum[:])
}

func exec07Snapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	return capability.Snapshot{
		Identity: capability.SnapshotIdentity{
			Project: project, Generation: generation,
			AuthorizationScopeDigest: exec07ScopeDigest(scope),
			SchemaDigest:             "exec07-schema", ShapeDigest: "exec07-shape",
		},
		Status: capability.StatusReady, Complete: true, Token: token,
	}
}

// exec07Receipt builds a native execution receipt for the bundle, bound to
// the snapshot identity exactly as the compile path binds it, with the
// public output contract emitted columns the receipt validation re-reads.
func exec07Receipt(t *testing.T, snapshot capability.Snapshot, bundle recipe.Bundle, columns []explorer.EmittedColumn, contract json.RawMessage) explorer.CompilationReceipt {
	t.Helper()
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:     explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion:  explorer.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               exec07Explorer,
		IntentDigest:             "exec07-intent",
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		CompiledConfig:           json.RawMessage(`{}`),
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		EmittedColumns:           columns,
	}
	var err error
	receipt.RecipeDigest, err = bundle.Digest()
	if err != nil {
		t.Fatalf("bundle digest: %v", err)
	}
	receipt.ResolvedRecipeDigest = receipt.RecipeDigest
	receipt.ResolvedSchemaDigest = "exec07-resolved-schema"
	provenance := map[string]string{}
	for _, column := range columns {
		provenance[column.PublicColumn] = "EXPLICIT"
	}
	receipt.OutputColumnProvenance = map[string]map[string]string{exec07Explorer: provenance}
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

func exec07StatusColumns() []explorer.EmittedColumn {
	return []explorer.EmittedColumn{
		{OutputID: exec07Explorer, EmissionID: "em_status", PublicColumn: "status", Label: "Status", LogicalType: "string", Filterable: true, Chartable: true},
	}
}

func exec07StatusContract() json.RawMessage {
	return json.RawMessage(`{"outputs":[{"outputId":"observations","columns":[{"column":"status","label":"Status","logicalType":"string","filterable":true,"chartable":true}]}]}`)
}

func exec07GroupColumns() []explorer.EmittedColumn {
	return []explorer.EmittedColumn{
		{OutputID: exec07Explorer, EmissionID: "em_status", PublicColumn: "status", Label: "Status", LogicalType: "string", Filterable: true, Chartable: true},
		{OutputID: exec07Explorer, EmissionID: "em_rows", PublicColumn: "rows", Label: "Rows", LogicalType: "integer", Filterable: true},
	}
}

func exec07GroupContract() json.RawMessage {
	return json.RawMessage(`{"outputs":[{"outputId":"observations","columns":[{"column":"status","label":"Status","logicalType":"string","filterable":true,"chartable":true},{"column":"rows","label":"Rows","logicalType":"integer","filterable":true,"chartable":false}]}]}`)
}

// exec07LifecycleService wires the real lifecycle Service with the
// engine-backed PopulationMapping hook in the runtime shape.
func exec07LifecycleService(t *testing.T, store *storepg.ExplorerStore, authorized *exec07Authorized, engine *dataframeexecution.Engine, receipts ...explorer.CompilationReceipt) *lifecycle.Service {
	t.Helper()
	byID := map[string]explorer.CompilationReceipt{}
	for _, receipt := range receipts {
		byID[receipt.ID] = receipt
	}
	domain, err := explorer.NewService(&exec07ExplorerStore{pg: store})
	if err != nil {
		t.Fatalf("explorer service: %v", err)
	}
	config := lifecycle.Config{
		SelectionMembersCollection: exec07MembersColl,
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
	codec, err := lifecycle.NewHMACPopulationMappingCursorCodec("exec07-cursor-secret")
	if err != nil {
		t.Fatalf("cursor codec: %v", err)
	}
	config.PopulationMappingCursorCodec = codec
	config.PopulationMapping = func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
		authorized.mu.Lock()
		authorized.hookCalls++
		authorized.mu.Unlock()
		resolved, err := engine.CompileResolvedBundle(ctx, receipt.Bundle, bindings)
		if err != nil {
			return dataframeexecution.PopulationMappingResult{}, err
		}
		reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
			for _, id := range memberIDs {
				if err := visit(id); err != nil {
					return err
				}
			}
			return nil
		})
		return engine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
	}
	service, err := lifecycle.New(domain, config)
	if err != nil {
		t.Fatalf("lifecycle service: %v", err)
	}
	return service
}

func exec07LifecycleRequest(receipt explorer.CompilationReceipt, limit int) lifecycle.PopulationMappingRequest {
	return lifecycle.PopulationMappingRequest{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID,
		OutputID: exec07Explorer, Limit: limit,
	}
}

// ---------------------------------------------------------------------------
// Live legs.
// ---------------------------------------------------------------------------

// TestPostgresPopulationMappingLiveFlatExactMembership is the primary
// acceptance leg: three selected members against two stored resources
// through the real witness terminal.
func TestPostgresPopulationMappingLiveFlatExactMembership(t *testing.T) {
	s := newExec07Service(t)
	seedProject := projectid.Legacy(exec07Project)
	s.seedObservation(t, seedProject, exec07Generation, "obs-1", "final")
	s.seedObservation(t, seedProject, exec07Generation, "obs-2", "preliminary")
	header := s.seedSelection(t, []string{"obs-1", "obs-2", "obs-3"}, "exec07-scope")

	recorder := &exec07Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder.port)
	resolved := s.resolve(t, engine, exec07FlatBundle("exec07-flat", header), exec07Generation)
	result, err := engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer, MaxUnmapped: 10},
		dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
			for _, id := range []string{"obs-1", "obs-2", "obs-3"} {
				if err := visit(id); err != nil {
					return err
				}
			}
			return nil
		}))
	if err != nil {
		t.Fatalf("live flat mapping: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 || result.EmittedRows != 2 {
		t.Fatalf("live flat mapping result = %#v", result)
	}
	if len(result.UnmappedMemberIDs) != 1 || result.UnmappedMemberIDs[0] != "obs-3" {
		t.Fatalf("live unmapped member IDs = %#v, want the phantom member", result.UnmappedMemberIDs)
	}
	if recorder.count() != 1 {
		t.Fatalf("statements executed = %d, want the single witness terminal", recorder.count())
	}
	call := recorder.last()
	if call.tag != command.CompiledBackendPostgreSQL {
		t.Fatalf("dispatched tag = %q", call.tag)
	}
	if want := []string{ir.PhysicalPopulationMappingMemberField, ir.PhysicalPopulationMappingIdentityPartsField}; len(call.declared) != 2 || call.declared[0] != want[0] || call.declared[1] != want[1] {
		t.Fatalf("declared witness contract at the port = %#v", call.declared)
	}
	if !strings.Contains(call.sql, `"`+s.schema+`"."selection_members"`) || strings.Contains(call.sql, "@") {
		t.Fatalf("witness statement is not the registered PostgreSQL terminal:\n%s", call.sql)
	}
	if len(call.args) == 0 {
		t.Fatal("witness statement is not parameterized")
	}
	for _, arg := range call.args {
		if arg.Type == command.PostgreSQLParameterText && arg.Value == exec07Selection {
			return
		}
	}
	t.Fatalf("selection revision identity is not a bound text parameter: %#v", call.args)
}

// TestPostgresPopulationMappingLiveGroupDedupe witnesses engine-side
// identity de-duplication against real SQL: two mapped members contribute
// to one keyed group row, so the two witness rows collapse to one emitted
// identity.
func TestPostgresPopulationMappingLiveGroupDedupe(t *testing.T) {
	s := newExec07Service(t)
	seedProject := projectid.Legacy(exec07Project)
	s.seedObservation(t, seedProject, exec07Generation, "obs-1", "final")
	s.seedObservation(t, seedProject, exec07Generation, "obs-2", "final")
	header := s.seedSelection(t, []string{"obs-1", "obs-2", "obs-3"}, "exec07-scope")

	recorder := &exec07Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder.port)
	resolved := s.resolve(t, engine, exec07GroupBundle("exec07-group", header), exec07Generation)
	result, err := engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer, MaxUnmapped: 10},
		dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
			for _, id := range []string{"obs-1", "obs-2", "obs-3"} {
				if err := visit(id); err != nil {
					return err
				}
			}
			return nil
		}))
	if err != nil {
		t.Fatalf("live group mapping: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.PopulationMappingComplete || result.SelectedCount != 3 || result.MappedCount != 2 || result.UnmappedCount != 1 {
		t.Fatalf("live group mapping result = %#v", result)
	}
	if result.EmittedRows != 1 {
		t.Fatalf("group witness rows did not collapse onto the single group row identity: %#v", result)
	}
	if recorder.count() != 1 {
		t.Fatalf("statements executed = %d", recorder.count())
	}
}

// TestPostgresPopulationMappingLiveCapAndCancellationAreIncomplete proves
// the two honest-INTCOMPLETE legs against real streaming: a witness cap and
// a mid-stream cancellation both report INCOMPLETE with zero counts — never
// a misleading partial.
func TestPostgresPopulationMappingLiveCapAndCancellationAreIncomplete(t *testing.T) {
	s := newExec07Service(t)
	seedProject := projectid.Legacy(exec07Project)
	s.seedObservation(t, seedProject, exec07Generation, "obs-1", "final")
	s.seedObservation(t, seedProject, exec07Generation, "obs-2", "preliminary")
	header := s.seedSelection(t, []string{"obs-1", "obs-2", "obs-3"}, "exec07-scope")
	reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		for _, id := range []string{"obs-1", "obs-2", "obs-3"} {
			if err := visit(id); err != nil {
				return err
			}
		}
		return nil
	})

	// Cap leg: one witness row allowed, two exist.
	recorder := &exec07Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder.port)
	resolved := s.resolve(t, engine, exec07FlatBundle("exec07-cap", header), exec07Generation)
	result, err := engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer, MaxWitnessRows: 1}, reader)
	if err != nil {
		t.Fatalf("cap mapping: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.PopulationMappingIncomplete || result.SelectedCount != 0 || result.MappedCount != 0 || result.UnmappedCount != 0 || result.EmittedRows != 0 {
		t.Fatalf("cap produced a misleading partial result: %#v", result)
	}
	if recorder.count() != 1 {
		t.Fatalf("cap leg executed %d statements, want the single witness terminal", recorder.count())
	}

	// Cancellation leg: cancel after the first visited witness row.
	ctx, cancel := context.WithCancel(s.ctx)
	t.Cleanup(cancel)
	recorder = &exec07Recorder{adapter: s.adapter}
	recorder.afterVisit = func(visits int) {
		if visits == 1 {
			cancel()
		}
	}
	engine = s.engine(t, recorder.port)
	result, err = engine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer, MaxUnmapped: 10}, reader)
	if err != nil {
		t.Fatalf("cancellation mapping: %s", s.scrub(err))
	}
	if result.Status != dataframeexecution.PopulationMappingIncomplete || result.SelectedCount != 0 || result.MappedCount != 0 || result.EmittedRows != 0 {
		t.Fatalf("cancellation produced a misleading partial result: %#v", result)
	}
	if recorder.visits == 0 {
		t.Fatal("cancellation leg never streamed a witness row")
	}
}

// TestPostgresPopulationMappingLivePortFailureIsHardError keeps the
// routing/transport honesty boundary against the production adapter stack:
// a generic port failure (and the composition's unregistered-adapter
// sentinel) must surface as errors — never INCOMPLETE, and the unregistered
// adapter case as the typed capability refusal with zero rows visited.
func TestPostgresPopulationMappingLivePortFailureIsHardError(t *testing.T) {
	s := newExec07Service(t)
	seedProject := projectid.Legacy(exec07Project)
	s.seedObservation(t, seedProject, exec07Generation, "obs-1", "final")
	header := s.seedSelection(t, []string{"obs-1"}, "exec07-scope")
	reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
		return visit("obs-1")
	})

	// Generic transport failure: hard error, no status exposed.
	recorder := &exec07Recorder{adapter: s.adapter, failNext: errors.New("connection reset by peer")}
	engine := s.engine(t, recorder.port)
	resolved := s.resolve(t, engine, exec07FlatBundle("exec07-fail", header), exec07Generation)
	result, err := engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer}, reader)
	if err == nil || result.Status != "" {
		t.Fatalf("generic port failure was not a hard error: result=%#v err=%v", result, s.scrub(err))
	}

	// Unregistered adapter: the command package's capability-gap sentinel
	// becomes the typed refusal, with zero rows visited at the port.
	recorder = &exec07Recorder{adapter: s.adapter}
	recorder.failNext = fmt.Errorf("postgres rows: %w", command.ErrCompiledCommandBackendNotRegistered)
	engine = s.engine(t, recorder.port)
	result, err = engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer}, reader)
	if !errors.Is(err, dataframeexecution.ErrPopulationMappingPostgresUnsupported) {
		t.Fatalf("unregistered adapter error = %v, want the typed capability refusal", s.scrub(err))
	}
	if result.Status != "" || result.SelectedCount != 0 || result.MappedCount != 0 {
		t.Fatalf("capability refusal exposed counts: %#v", result)
	}
	recorder.mu.Lock()
	visited := recorder.visits
	recorder.mu.Unlock()
	if visited != 0 {
		t.Fatalf("capability refusal visited %d rows, want zero", visited)
	}

	// Routing mismatch: hard error, not a capability gap.
	recorder = &exec07Recorder{adapter: s.adapter}
	recorder.failNext = fmt.Errorf("dispatch: %w for %q", command.ErrCompiledCommandBackendMismatch, "POSTGRESQL")
	engine = s.engine(t, recorder.port)
	_, err = engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer}, reader)
	if err == nil || !errors.Is(err, command.ErrCompiledCommandBackendMismatch) || errors.Is(err, dataframeexecution.ErrPopulationMappingPostgresUnsupported) {
		t.Fatalf("routing mismatch error = %v", s.scrub(err))
	}
}

// TestPostgresPopulationMappingLiveStaleReceiptRejectsBeforeExecution
// drives the real lifecycle Service with the runtime-shaped engine hook:
// moving the capability snapshot past the receipt must refuse with the
// existing typed rejection, zero hook invocations, and a statement-count-0
// witness at the production adapter, while the fresh control reports exact
// counts on the same service and recovers after restoration.
func TestPostgresPopulationMappingLiveStaleReceiptRejectsBeforeExecution(t *testing.T) {
	s := newExec07Service(t)
	seedProject := projectid.Legacy(exec07Project)
	s.seedObservation(t, seedProject, exec07Generation, "obs-1", "final")
	s.seedObservation(t, seedProject, exec07Generation, "obs-2", "final")
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{exec07AuthPath}}
	fresh := exec07Snapshot(exec07Project, exec07Generation, "token-1", scope)
	// The header's frozen scope digest must be the authorized read scope's
	// policy digest: the lifecycle's POPULATION_MAPPING_STALE guard compares
	// the stored header, the receipt, and the capability snapshot.
	s.scopeDigest = fresh.Identity.AuthorizationScopeDigest
	header := s.seedSelection(t, []string{"obs-1", "obs-2", "obs-3"}, fresh.Identity.AuthorizationScopeDigest)

	recorder := &exec07Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder.port)
	bundle := exec07FlatBundle("exec07-receipt", header)
	receipt := exec07Receipt(t, fresh, bundle, exec07StatusColumns(), exec07StatusContract())
	authorized := &exec07Authorized{snapshot: fresh, scope: scope}
	service := exec07LifecycleService(t, s.store, authorized, engine, receipt)

	// Fresh control: the receipt publishes exact counts from live state.
	report, err := service.PopulationMapping(s.ctx, exec07LifecycleRequest(receipt, 10))
	if err != nil {
		t.Fatalf("fresh receipt: %s", s.scrub(err))
	}
	if report.Report.Status != dataframeexecution.PopulationMappingComplete || report.Report.Counts == nil ||
		report.Report.Counts.Selected != 3 || report.Report.Counts.Mapped != 2 || report.Report.Counts.Unmapped != 1 || report.Report.Counts.EmittedRows != 2 {
		t.Fatalf("fresh receipt report = %#v", report.Report)
	}
	if len(report.Report.Unmapped) != 1 || report.Report.Unmapped[0].ID != "obs-3" {
		t.Fatalf("fresh receipt unmapped refs = %#v", report.Report.Unmapped)
	}
	if authorized.calls() != 1 || recorder.count() < 1 {
		t.Fatalf("fresh control witnesses: hooks=%d statements=%d", authorized.calls(), recorder.count())
	}

	// Stale: the snapshot generation moved on past the receipt.
	stale := fresh
	stale.Identity.Generation = "gen-b"
	authorized.set(stale, scope)
	hooksBefore := authorized.calls()
	statementsBefore := recorder.count()
	_, err = service.PopulationMapping(s.ctx, exec07LifecycleRequest(receipt, 10))
	var lifecycleErr *lifecycle.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != lifecycle.ClassConflict || lifecycleErr.Code != "RECEIPT_STALE" {
		t.Fatalf("stale receipt error = %v, want ClassConflict/RECEIPT_STALE", s.scrub(err))
	}
	if authorized.calls() != hooksBefore {
		t.Fatalf("stale receipt reached the population-mapping hook: %d extra invocations", authorized.calls()-hooksBefore)
	}
	if got := recorder.count() - statementsBefore; got != 0 {
		t.Fatalf("stale receipt executed %d statements, want the statement-count-0 witness at the production adapter", got)
	}

	// Recovery control: restoring the fresh snapshot proceeds again.
	authorized.set(fresh, scope)
	if _, err := service.PopulationMapping(s.ctx, exec07LifecycleRequest(receipt, 10)); err != nil {
		t.Fatalf("restored fresh receipt: %s", s.scrub(err))
	}
	if authorized.calls() != hooksBefore+1 {
		t.Fatalf("restored receipt did not reach the hook: calls=%d", authorized.calls())
	}
}

// TestPostgresPopulationMappingLiveNamedGapRefusesAtZeroStatements takes
// the renderer's named capability gap (an expansion on the
// member-collecting root) to the real compile target: the typed capability
// sentinel must arrive with zero statements executed.
func TestPostgresPopulationMappingLiveNamedGapRefusesAtZeroStatements(t *testing.T) {
	s := newExec07Service(t)
	header := s.seedSelection(t, []string{"obs-1"}, "exec07-scope")
	recorder := &exec07Recorder{adapter: s.adapter}
	engine := s.engine(t, recorder.port)
	bundle := exec07FlatBundle("exec07-gap", header)
	bundle.Outputs[0].Expand = &recipe.Expansion{From: recipe.Expression{Select: "root.extension[]"}, As: "item"}
	bundle.Outputs[0].RowGrain = "expanded"
	bundle.Outputs[0].Identity = &recipe.Identity{Name: "row", Expr: recipe.Expression{Select: "root.id"}}
	resolved := s.resolve(t, engine, bundle, exec07Generation)
	_, err := engine.PopulationMapping(s.ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: exec07Explorer},
		dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
			return visit("obs-1")
		}))
	if !errors.Is(err, dataframeexecution.ErrPopulationMappingPostgresUnsupported) {
		t.Fatalf("named gap error = %v, want the typed capability refusal", s.scrub(err))
	}
	if recorder.count() != 0 {
		t.Fatalf("named-gap refusal executed %d statements, want the statement-count-0 witness", recorder.count())
	}
}

// ---------------------------------------------------------------------------
// Zero-leftover audit, ownership-scoped to this file's prefix.
// ---------------------------------------------------------------------------

func TestZZPostgresPopulationMappingZeroLeftoverFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return // no service was used; nothing could have been created
	}
	exec07CaptureBaseline(raw)
	if exec07BaselineErr != nil {
		t.Fatalf("baseline: %v", exec07BaselineErr)
	}
	baseline := map[string]bool{}
	for _, name := range exec07BaselineNames {
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
	rows, err := admin.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE 'verify\_exec07\_cs\_%' ESCAPE '\'`)
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
	rows, err = admin.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'verify\_exec07\_cs\_%' ESCAPE '\'`)
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
		t.Fatalf("verify_exec07_cs_* fixtures survived the run: %v (owned registry: %v)", survivors, exec07RegistrySnapshot())
	}
}
