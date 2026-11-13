package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/clickhouse"
)

type dispatchSpies struct {
	mu                sync.Mutex
	aqlQueries        []string
	aqlPreviewQueries []string
	pgFull            []postgresCallRecord
	pgPreview         []postgresCallRecord
	pgRequests        int
	clickHouseQueries int
	arangoControls    []string
}

func (s *dispatchSpies) recordControl(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.arangoControls = append(s.arangoControls, name)
}

func (s *dispatchSpies) controlSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.arangoControls...)
}

type postgresCallRecord struct {
	SQL     string
	Args    []command.PostgreSQLParameter
	Columns []string
}

func (s *dispatchSpies) arangoEngine() Config {
	record := s.recordControl
	return Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      invalidRecipeRegistry{},
		ScopeDigest:   func(recipe.RuntimeBindings) string { return "exec02-test-scope" },
		SourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			query, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			s.mu.Lock()
			s.aqlQueries = append(s.aqlQueries, query)
			s.mu.Unlock()
			return nil
		},
		PreviewSourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			query, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			s.mu.Lock()
			s.aqlPreviewQueries = append(s.aqlPreviewQueries, query)
			s.mu.Unlock()
			return nil
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			record("preparePreviewIndex")
			return nil
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]PreviewCollectionIndex, error) {
			record("previewCoveringIndexes")
			return nil, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			record("previewCollectionRevision")
			return "rev-1", nil
		},
		PreviewCollectionScans: func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			record("previewCollectionScans")
			return PreviewCollectionScanEvidence{}, nil
		},
		PreviewCollectionCount: func(context.Context, string) (int64, error) {
			record("previewCollectionCount")
			return 1, nil
		},
		ClickHouseQueryRows: func(context.Context, string, []string, func(map[string]any) error, ...any) error {
			s.mu.Lock()
			s.clickHouseQueries++
			s.mu.Unlock()
			return nil
		},
	}
}

func exec02PostgreSQLTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("loom")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("PostgreSQLTarget: %v", err)
	}
	return target
}

func exec02TestResolved() Resolved {
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id", "name"},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one"},
			{ID: "name", Name: "name", Label: "name", Kind: "string", Cardinality: "one"},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		Plan:        exec02TestPlan(exec02TestProjections(false)),
	}
	return Resolved{
		Bundle: recipe.Bundle{Name: "pg_dispatch"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "pg-dispatch-digest",
	}
}

func exec02TestProjections(withSourceID bool) []ir.PhysicalProjection {
	projections := []ir.PhysicalProjection{
		{Name: "id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		{Name: "name", Expression: &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType:  "Patient",
				Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}},
				ExecutionMode: ir.PhysicalSelectorDirectScalar,
			},
		}, Presence: &ir.PhysicalProjectionPresence{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Paths:  [][]string{{"gender"}},
		}},
	}
	if withSourceID {
	}
	return projections
}

func exec02TestPlan(projections []ir.PhysicalProjection) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Patient",
			"project":                          "P1",
			"dataset_generation":               "G1",
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
	}
}

func exec02TestParameters() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "G1"},
	}
}

func exec02PostgreSQLCommand(t *testing.T) command.CompiledCommand {
	t.Helper()
	resolved := exec02TestResolved()
	query, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
		resolved.Compiled.Outputs[0],
		resolved.Semantic.SemanticPlan.Bindings,
		0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("reference CompileRecipeOutput: %v", err)
	}
	if query.Command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("reference command backend = %q, want PostgreSQL", query.Command.Backend())
	}
	return query.Command
}

func exec02pgPort(spies *dispatchSpies, target *[]postgresCallRecord, rows []map[string]any) SourceRows {
	return func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
		sqlText, args, err := request.Command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		spies.mu.Lock()
		*target = append(*target, postgresCallRecord{SQL: sqlText, Args: args, Columns: append([]string(nil), request.DeclaredColumns...)})
		spies.pgRequests++
		spies.mu.Unlock()
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}
}

func exec02pgEngine(t *testing.T, spies *dispatchSpies, rows []map[string]any) (*Engine, Config) {
	t.Helper()
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	config.SourceRows = exec02pgPort(spies, &spies.pgFull, rows)
	config.PreviewSourceRows = exec02pgPort(spies, &spies.pgPreview, rows)
	return config.newEngineForTest(t), config
}

func (c Config) newEngineForTest(t *testing.T) *Engine {
	t.Helper()
	engine, err := New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func exec02Rows() []map[string]any {
	return []map[string]any{
		{"id": "patient-one", "name": "Ada"},
		{"id": "patient-two", "name": "Grace"},
	}
}

func exec02AssertUntouchedArango(t *testing.T, spies *dispatchSpies) {
	t.Helper()
	if len(spies.aqlQueries) != 0 || len(spies.aqlPreviewQueries) != 0 || len(spies.controlSnapshot()) != 0 || spies.clickHouseQueries != 0 {
		t.Fatalf("PostgreSQL plan touched Arango/ClickHouse callbacks: aql=%v preview=%v controls=%v clickhouse=%d",
			spies.aqlQueries, spies.aqlPreviewQueries, spies.controlSnapshot(), spies.clickHouseQueries)
	}
}

func TestPostgreSQLPlanExecutesOnlyThroughPostgreSQLAdapter(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, exec02Rows())
	resolved := exec02TestResolved()

	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("streams = %d, want 1", len(streams))
	}
	var got []map[string]any
	result, err := streams[0].Stream(context.Background(), func(row map[string]any) error {
		got = append(got, row)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if result.RowCount != 2 || len(got) != 2 {
		t.Fatalf("rows: result=%d visited=%d, want 2/2", result.RowCount, len(got))
	}
	if got[0]["id"] != "patient-one" || got[0]["name"] != "Ada" || got[1]["id"] != "patient-two" || got[1]["name"] != "Grace" {
		t.Fatalf("rows drifted from golden literals: %v", got)
	}
	if _, present := got[0]["__loom_row_id"]; !present {
		t.Fatalf("stable row identity missing from PostgreSQL stream row: %v", got[0])
	}
	if len(spies.pgFull) != 1 {
		t.Fatalf("PostgreSQL port calls = %d, want 1", len(spies.pgFull))
	}
	call := spies.pgFull[0]
	reference := exec02PostgreSQLCommand(t)
	wantSQL, wantArgs, err := reference.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("reference parameters: %v", err)
	}
	if call.SQL != wantSQL {
		t.Fatalf("statement SQL drifted from the compiler reference render:\n got %q\nwant %q", call.SQL, wantSQL)
	}
	if !reflect.DeepEqual(call.Args, wantArgs) {
		t.Fatalf("ordered arguments drifted:\n got %v\nwant %v", call.Args, wantArgs)
	}
	if !reflect.DeepEqual(call.Columns, []string{"id", "name"}) {
		t.Fatalf("declared columns = %v, want [id name]", call.Columns)
	}
	if len(spies.pgPreview) != 0 {
		t.Fatalf("full stream touched the PostgreSQL preview override: %d calls", len(spies.pgPreview))
	}
	exec02AssertUntouchedArango(t, spies)

	previewSummary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil })
	if err != nil {
		t.Fatalf("PreviewOutput: %v", err)
	}
	if !previewSummary.Complete || previewSummary.Truncated {
		t.Fatalf("short PostgreSQL preview must report Complete=true Truncated=false, got %+v", previewSummary)
	}
	if len(spies.pgPreview) != 1 || len(spies.pgFull) != 1 {
		t.Fatalf("preview must use the preview override only: full=%d preview=%d", len(spies.pgFull), len(spies.pgPreview))
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgreSQLPreviewLimitReportsTruncationNotCompletion(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	config.SourceRows = exec02pgPort(spies, &spies.pgFull, exec02Rows())
	config.PreviewSourceRows = exec02pgPort(spies, &spies.pgPreview, exec02Rows())
	engine := config.newEngineForTest(t)
	var visits int
	summary, err := engine.PreviewOutput(context.Background(), exec02TestResolved(), PreviewRequest{Output: "resources", Limit: 1}, func(map[string]any) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatalf("PreviewOutput: %v", err)
	}
	if visits != 1 || summary.RowCount != 1 || summary.Complete || !summary.Truncated {
		t.Fatalf("bounded preview: visits=%d rowcount=%d complete=%v truncated=%v", visits, summary.RowCount, summary.Complete, summary.Truncated)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgreSQLPreviewCancellationStopsWithoutCompletedMarker(t *testing.T) {
	spies := &dispatchSpies{}
	rows := []map[string]any{
		{"id": "patient-one", "name": "Ada"},
		{"id": "patient-two", "name": "Grace"},
		{"id": "patient-three", "name": "Alan"},
	}
	engine, _ := exec02pgEngine(t, spies, rows)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var visits int
	summary, err := engine.PreviewOutput(ctx, exec02TestResolved(), PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error {
		visits++
		cancel()
		return nil
	})
	if err == nil {
		t.Fatalf("canceled PostgreSQL preview returned nil error with summary %+v", summary)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PostgreSQL preview error must satisfy errors.Is(context.Canceled): %v", err)
	}
	if visits != 1 {
		t.Fatalf("visitor ran %d times after cancellation, want stop at 1", visits)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("canceled preview must carry neither completed nor truncated markers: %+v", summary)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgreSQLPreviewBackendFailureNeverMarksComplete(t *testing.T) {
	spies := &dispatchSpies{}
	failure := errors.New("postgres rows: query: FATAL simulated")
	config := spies.arangoEngine()
	config.CompileTarget = exec02PostgreSQLTarget(t)
	failingPort := func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		if err := visit(exec02Rows()[0]); err != nil {
			return err
		}
		return failure
	}
	config.SourceRows = failingPort
	config.PreviewSourceRows = failingPort
	engine := config.newEngineForTest(t)
	summary, err := engine.PreviewOutput(context.Background(), exec02TestResolved(), PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return nil })
	if err == nil || !errors.Is(err, failure) {
		t.Fatalf("adapter failure must reach the caller via the error chain: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("failed preview must not mark completion or truncation: %+v", summary)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestUnsupportedPhysicalEngineRejectsBeforeAnyCallback(t *testing.T) {
	for _, engineTag := range []ir.PhysicalEngine{"MONGO", "", " CLICKHOUSE ", "clickhouse", "postgres"} {
		if engineTag == "" || engineTag == "clickhouse" || engineTag == " CLICKHOUSE " {
			continue
		}
		spies := &dispatchSpies{}
		engine, _ := exec02pgEngine(t, spies, exec02Rows())
		resolved := exec02TestResolved()
		resolved.Compiled.Outputs[0].Plan.Engine = engineTag
		_, _, err := engine.streamForOutput(context.Background(), resolved, "resources", 5)
		if err == nil || !strings.Contains(err.Error(), "unsupported physical engine") {
			t.Fatalf("unknown plan engine %q must fail closed: %v", engineTag, err)
		}
		if _, err := engine.Run(context.Background(), resolved); err == nil || !strings.Contains(err.Error(), "unsupported physical engine") {
			t.Fatalf("Run with unknown plan engine %q must fail closed: %v", engineTag, err)
		}
		exec02AssertUntouchedArango(t, spies)
		if spies.pgRequests != 0 {
			t.Fatalf("unsupported engine %q reached the PostgreSQL row port", engineTag)
		}
	}
}

func TestPostgreSQLCompileFailurePropagatesWithoutFallback(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, exec02Rows())
	resolved := exec02TestResolved()
	resolved.Compiled.Outputs[0].Plan.BindVars = map[string]any{"root_collection": "Patient"}
	_, _, err := engine.streamForOutput(context.Background(), resolved, "resources", 5)
	if err == nil {
		t.Fatal("unrenderable PostgreSQL plan must fail closed")
	}
	if len(spies.aqlQueries) != 0 || spies.pgRequests != 0 {
		t.Fatalf("compile failure must not fall back to AQL or reach the port: aql=%v pg=%d", spies.aqlQueries, spies.pgRequests)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgreSQLStreamRefusesCrossTagAndZeroTagCommands(t *testing.T) {
	crossTag, err := command.NewAQLCommand("FOR d IN resource RETURN d", map[string]any{"project": "P1"})
	if err != nil {
		t.Fatalf("build AQL command: %v", err)
	}
	zeroTag := command.CompiledCommand{}
	for _, test := range []struct {
		name       string
		command    command.CompiledCommand
		wantIs     error
		wantPhrase string
	}{
		{"AQL-tagged command on the PostgreSQL route", crossTag, command.ErrCompiledCommandBackendMismatch, `command is tagged "AQL" while the selected target serves "POSTGRESQL"`},
		{"zero-tag command on the PostgreSQL route", zeroTag, command.ErrCompiledCommandBackendNotRegistered, "unregistered backend tag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spies := &dispatchSpies{}
			stream := OutputStream{
				Name: "resources", Columns: []string{"id"},
				sourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
					spies.mu.Lock()
					sqlText, args, _ := request.Command.PostgreSQLParameters()
					spies.pgFull = append(spies.pgFull, postgresCallRecord{SQL: sqlText, Args: args, Columns: append([]string(nil), request.DeclaredColumns...)})
					spies.pgRequests++
					spies.mu.Unlock()
					for _, row := range exec02Rows() {
						if err := ctx.Err(); err != nil {
							return err
						}
						if err := visit(row); err != nil {
							return err
						}
					}
					return nil
				},
				command:         test.command,
				expectedBackend: command.CompiledBackendPostgreSQL,
				commandColumns:  []string{"id"},
			}
			err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("error must satisfy errors.Is(%v): %v", test.wantIs, err)
			}
			if !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("refusal must name the routing defect %q: %v", test.wantPhrase, err)
			}
			if len(spies.pgFull) != 0 {
				t.Fatalf("mismatched command reached the PostgreSQL port")
			}
			exec02AssertUntouchedArango(t, spies)
		})
	}
}

func TestPostgreSQLSelectionRefusesClickHousePayloadAndBuildsPrivateCapture(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, exec02Rows())
	combinePlan := exec02TestResolved()
	combine := ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{{RevisionID: "execution-a"}}}
	combinePlan.Compiled.Outputs[0].Plan.ClickHouseCombine = &combine
	_, _, err := engine.streamForOutput(context.Background(), combinePlan, "resources", 5)
	if err == nil || !strings.Contains(err.Error(), "cannot carry a ClickHouse combine payload") {
		t.Fatalf("PostgreSQL target over a ClickHouse payload must fail closed: %v", err)
	}
	resolved := exec02TestResolved()
	stream, _, err := engine.buildCompiledOutputStream(context.Background(), resolved, resolved.Compiled.Outputs[0], 0, map[string]bool{}, true)
	if err != nil {
		t.Fatalf("private capture build over a PostgreSQL-served output: %v", err)
	}
	if stream.expectedBackend != command.CompiledBackendPostgreSQL || stream.command.Backend() != command.CompiledBackendPostgreSQL {
		t.Fatalf("capture stream is not a fully PostgreSQL-tagged stream: expected=%q command=%q", stream.expectedBackend, stream.command.Backend())
	}
	if stream.artifactScopeMode != chartifact.ScopeModeRows || stream.scopeEvidence != nil {
		t.Fatalf("capture scope fields drifted from the evidence-free fixture: mode=%q evidence=%v", stream.artifactScopeMode, stream.scopeEvidence)
	}
	if spies.pgRequests != 0 {
		t.Fatalf("capture build reached the PostgreSQL port")
	}
	catalog := &testArtifactCatalog{items: map[string]chartifact.Manifest{}}
	clickHouse := &testArtifactClickHouse{columns: map[string][]clickhouse.Column{}, rows: map[string][]map[string]any{}}
	manager, err := chartifact.New(chartifact.Config{Catalog: catalog, ClickHouse: clickHouse})
	if err != nil {
		t.Fatalf("chartifact.New: %v", err)
	}
	_, err = stream.MaterializeClickHouseArtifact(context.Background(), manager, "exec-1", stream.stageID, nil)
	if err == nil || !strings.Contains(err.Error(), "private PostgreSQL artifact requires opaque evidence for its exact complete source plan") {
		t.Fatalf("evidence-free PostgreSQL capture must fail closed naming PostgreSQL: %v", err)
	}
	if len(catalog.items) != 0 {
		t.Fatalf("refused PostgreSQL capture created %d artifacts", len(catalog.items))
	}
	if spies.pgRequests != 0 {
		t.Fatalf("refused capture reached the PostgreSQL port")
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestClickHouseCombineUnaffectedAndNeverOfferedSourcePort(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	combine := ir.PhysicalClickHouseCombine{
		Kind: ir.PhysicalCombineAppend,
		Inputs: []ir.PhysicalCombineInputRef{
			{TableID: "table-left", RevisionID: "execution-left", OutputID: "left"},
			{TableID: "table-right", RevisionID: "execution-right", OutputID: "right"},
		},
		Projections: []ir.PhysicalCombineProjection{
			{OutputColumnID: "value", InputIndex: 0, InputColumnID: "left-value"},
			{OutputColumnID: "value", InputIndex: 1, InputColumnID: "right-value"},
		},
		Outputs: []ir.PhysicalCombineOutputColumn{{ID: "value", Name: "value", LogicalType: "string", ClickHouseType: "String"}},
	}
	resolved := exec02TestResolved()
	resolved.Semantic.SemanticPlan.Bindings.Project = "project-a"
	resolved.Semantic.SemanticPlan.Bindings.DatasetGeneration = "generation-a"
	resolved.Compiled.Outputs[0].Plan = ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineClickHouse, ClickHouseCombine: &combine}
	resolved.Compiled.Outputs[0].OutputSchema = nil
	resolved.Compiled.Outputs[0].Columns = []string{"value"}
	plan := ir.ClonePhysicalPlan(resolved.Compiled.Outputs[0].Plan)
	resolved.Compiled.Outputs[0].OptimizedPlan = &plan
	inputs := []ir.ResolvedClickHouseTable{
		engineResolvedClickHouseInput("table-left", "execution-left", "left", "left_table", "left-value", "left_name"),
		engineResolvedClickHouseInput("table-right", "execution-right", "right", "right_table", "right-value", "right_name"),
	}
	config.ClickHouseQueryRows = func(_ context.Context, query string, _ []string, visit func(map[string]any) error, _ ...any) error {
		spies.clickHouseQueries++
		if !strings.Contains(query, "UNION ALL") {
			t.Fatalf("unexpected ClickHouse combine SQL shape: %q", query)
		}
		return visit(map[string]any{"__loom_row_id": "stable-row", "value": "Ada", "auth_resource_path": ""})
	}
	config.ResolveClickHouseInputs = func(context.Context, ir.PhysicalClickHouseCombine, recipe.RuntimeBindings) ([]ir.ResolvedClickHouseTable, error) {
		return inputs, nil
	}
	config.WithExecutionReadPins = func(ctx context.Context, _ []string, visit func(context.Context) error) error {
		return visit(ctx)
	}
	engine := config.newEngineForTest(t)
	rows, err := engine.Run(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spies.clickHouseQueries != 1 {
		t.Fatalf("ClickHouse callbacks = %d, want 1", spies.clickHouseQueries)
	}
	if spies.pgRequests != 0 || len(spies.aqlQueries) != 0 || len(spies.aqlPreviewQueries) != 0 {
		t.Fatalf("ClickHouse combine leaked into a source adapter: pg=%d aql=%v preview=%v", spies.pgRequests, spies.aqlQueries, spies.aqlPreviewQueries)
	}
	if got := rows["resources"]; len(got) != 1 || got[0]["value"] != "Ada" {
		t.Fatalf("ClickHouse combine rows drifted: %v", got)
	}
}

func TestAQLPathUnchangedWhenPostgreSQLUnused(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	engine := config.newEngineForTest(t)
	bundle := testResolvedBundle([]string{})
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := engine.Preview(context.Background(), resolved, 2); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(spies.aqlPreviewQueries) == 0 || spies.pgRequests != 0 {
		t.Fatalf("AQL preview must use the AQL preview port only: preview=%v pg=%d", spies.aqlPreviewQueries, spies.pgRequests)
	}
	streams, err := engine.Streams(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	for _, stream := range streams {
		if _, err := stream.Stream(context.Background(), func(map[string]any) error { return nil }); err != nil {
			t.Fatalf("Stream: %v", err)
		}
	}
	if len(spies.aqlQueries) == 0 || spies.pgRequests != 0 {
		t.Fatalf("AQL stream must use the AQL port only: aql=%v pg=%d", spies.aqlQueries, spies.pgRequests)
	}
}

func TestAQLPreviewStillDrivesArangoControls(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	engine := config.newEngineForTest(t)
	groupOutput := executionGroupPreviewOutput()
	resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                groupOutput.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{groupOutput},
	}, recipe.RuntimeBindings{Project: "P1", DatasetGeneration: "G1"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: groupOutput.Name, Limit: 1}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("PreviewOutput: %v", err)
	}
	if len(spies.aqlPreviewQueries) == 0 {
		t.Fatal("AQL preview stopped using the AQL preview port")
	}
	deadline := time.Now().Add(2 * time.Second)
	sawPrepare := false
	for time.Now().Before(deadline) {
		for _, control := range spies.controlSnapshot() {
			if control == "preparePreviewIndex" {
				sawPrepare = true
			}
		}
		if sawPrepare {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawPrepare {
		t.Fatalf("AQL Group preview stopped driving the Arango index preparation control: %v", spies.controlSnapshot())
	}
	if spies.pgRequests != 0 {
		t.Fatalf("AQL Group preview leaked into the PostgreSQL port: %d calls", spies.pgRequests)
	}
}

func TestGroupPreviewOptimizerNeverEvaluatesPostgreSQLStream(t *testing.T) {
	spies := &dispatchSpies{}
	reference := exec02PostgreSQLCommand(t)
	evaluate := func(stream *OutputStream) error {
		engine := &Engine{compileTarget: compiler.ArangoTarget(),
			registry: invalidRecipeRegistry{},
			sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
				_, _, dispatchErr := request.Command.AQLParameters()
				if dispatchErr != nil {
					return dispatchErr
				}
				return nil
			},
		}
		engine.previewCoveringIndexes = func(context.Context, string) ([]PreviewCollectionIndex, error) {
			spies.recordControl("previewCoveringIndexes")
			return nil, nil
		}
		engine.previewCollectionScans = func(context.Context, command.CompiledCommand, string) (PreviewCollectionScanEvidence, error) {
			spies.recordControl("previewCollectionScans")
			return PreviewCollectionScanEvidence{}, nil
		}
		engine.previewCollectionCount = func(context.Context, string) (int64, error) {
			spies.recordControl("previewCollectionCount")
			return 7, nil
		}
		query := &compiler.CompiledQuery{
			Command: mustAQLCommand("FOR d IN Patient RETURN d", map[string]any{"project": "P1"}),
			PreviewGroupScan: &compiler.PreviewGroupScanSpec{
				Collection: "Patient", Sequential: mustAQLCommand("FOR d IN Patient RETURN d", nil),
				ScopeCount: mustAQLCommand("RETURN 1", nil),
			},
			PreviewCoveringIndex: &compiler.PreviewCoveringIndexSpec{Collection: "Patient", Name: "loom_pivot_preview_a", Fields: []string{"project"}},
		}
		return engine.chooseGroupPreviewScan(context.Background(), query, stream)
	}
	aqlStream := &OutputStream{
		Name: "resources", sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return nil
		},
	}
	if err := evaluate(aqlStream); err != nil {
		t.Fatalf("AQL group preview scan: %v", err)
	}
	controlsBefore := len(spies.controlSnapshot())
	if controlsBefore == 0 {
		t.Fatal("control failed: the AQL-shaped stream must reach the Arango optimizer callbacks")
	}
	postgresStream := &OutputStream{
		Name:           "resources",
		sourceRows:     exec02pgPort(spies, &spies.pgFull, exec02Rows()),
		command:        reference,
		commandColumns: []string{"id", "name"},
	}
	if err := evaluate(postgresStream); err != nil {
		t.Fatalf("PostgreSQL group preview scan: %v", err)
	}
	after := spies.controlSnapshot()
	if len(after) != controlsBefore {
		t.Fatalf("PostgreSQL stream invoked Arango optimizer controls: %v", after[controlsBefore:])
	}
	if spies.pgRequests != 0 {
		t.Fatalf("optimizer ran the PostgreSQL port: %d calls", spies.pgRequests)
	}
}

func TestPostgreSQLStreamConsumesOnlyTaggedCommand(t *testing.T) {
	spies := &dispatchSpies{}
	reference := exec02PostgreSQLCommand(t)
	rows := exec02Rows()
	aqlExpectingStream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		sourceRows: func(_ context.Context, request RowsRequest, _ RowVisitor) error {
			_, _, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return errors.New("AQL port must never run for a PostgreSQL stream")
		},
		command:        reference,
		commandColumns: []string{"id", "name"},
	}
	if err := aqlExpectingStream.streamRaw(context.Background(), func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-expecting port on a PostgreSQL stream must report the envelope mismatch: %v", err)
	}

	stream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		sourceRows:     exec02pgPort(spies, &spies.pgFull, rows),
		command:        reference,
		commandColumns: []string{"id", "name"},
	}
	var visited int
	if err := stream.streamRaw(context.Background(), func(map[string]any) error { visited++; return nil }); err != nil {
		t.Fatalf("streamRaw: %v", err)
	}
	if visited != len(rows) {
		t.Fatalf("visited=%d want %d", visited, len(rows))
	}
	wantSQL, _, err := reference.PostgreSQLParameters()
	if err != nil {
		t.Fatalf("reference parameters: %v", err)
	}
	if spies.pgRequests != 1 || spies.pgFull[0].SQL != wantSQL {
		t.Fatalf("PostgreSQL port did not receive the tagged command: %+v", spies.pgFull)
	}
}

func TestPostgreSQLStreamWithoutPortNeverFallsBackToAQL(t *testing.T) {
	aqlCalls := 0
	stream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		sourceRows:     nil,
		command:        exec02PostgreSQLCommand(t),
		commandColumns: []string{"id", "name"},
	}
	_ = aqlCalls
	err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
	if !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("missing source row port must fail closed with the named sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "no source row port") {
		t.Fatalf("refusal must name the missing-port defect: %v", err)
	}
}

func TestPostgreSQLPlanFingerprintIsStableAndSensitivityProbed(t *testing.T) {
	compile := func(t *testing.T, resolved Resolved) string {
		t.Helper()
		query, err := compiler.CompileRecipeOutput(exec02PostgreSQLTarget(t),
			resolved.Compiled.Outputs[0],
			resolved.Semantic.SemanticPlan.Bindings,
			0, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("reference compile: %v", err)
		}
		return query.PlanDiagnostics.Fingerprint
	}
	base := compile(t, exec02TestResolved())
	if base == "" || base != compile(t, exec02TestResolved()) {
		t.Fatalf("fingerprint must be deterministic and non-empty: %q", base)
	}
	scopeChange := exec02TestResolved()
	scopeChange.Compiled.Outputs[0].Plan.BindVars["project"] = "P2"
	if got := compile(t, scopeChange); got == base {
		t.Fatal("fingerprint did not react to a scope argument change")
	}
	projectionChange := exec02TestResolved()
	returnReturn := func(resolved *Resolved) *ir.PhysicalReturn {
		t.Helper()
		for index := range resolved.Compiled.Outputs[0].Plan.Operations {
			if operation := &resolved.Compiled.Outputs[0].Plan.Operations[index]; operation.Kind == ir.PhysicalReturnOp {
				return operation.Return
			}
		}
		t.Fatal("fixture plan has no RETURN")
		return nil
	}
	returnReturn(&projectionChange).Projections = returnReturn(&projectionChange).Projections[:1]
	projectionChange.Compiled.Outputs[0].Columns = []string{"id"}
	projectionChange.Compiled.Outputs[0].OutputSchema = projectionChange.Compiled.Outputs[0].OutputSchema[:1]
	if got := compile(t, projectionChange); got == base {
		t.Fatal("fingerprint did not react to a statement shape change")
	}
	nullBehaviorChange := exec02TestResolved()
	returnReturn(&nullBehaviorChange).Projections[1].Expression.NullBehavior = ir.PhysicalEmptyOnNull
	if got := compile(t, nullBehaviorChange); got == base {
		t.Fatal("fingerprint did not react to a null-behavior change")
	}
}

func TestPostgreSQLSelectionRefusesAllInternalDeclaredColumns(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, exec02Rows())
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id", "name"},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one", Internal: true},
			{ID: "name", Name: "name", Label: "name", Kind: "string", Cardinality: "one", Internal: true},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		Plan:        exec02TestPlan(exec02TestProjections(false)),
	}
	resolved := Resolved{
		Bundle: recipe.Bundle{Name: "pg_dispatch"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "pg-internal-only",
	}
	_, err := engine.Streams(context.Background(), resolved)
	if err == nil || !strings.Contains(err.Error(), "internal projection") {
		t.Fatalf("all-internal declared contract must be refused, got %v", err)
	}
	if spies.pgRequests != 0 {
		t.Fatalf("refused selection must not reach the row port: %d calls", spies.pgRequests)
	}
	exec02AssertUntouchedArango(t, spies)
}

func TestPostgreSQLPreviewReportsSingleSourceIdentityWithResourceType(t *testing.T) {
	spies := &dispatchSpies{}
	sourceID := ir.PreviewSourceResourceIDColumn
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id", "name", sourceID},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one"},
			{ID: "name", Name: "name", Label: "name", Kind: "string", Cardinality: "one"},
			{ID: sourceID, Name: sourceID, Label: sourceID, Kind: "string", Cardinality: "one", Internal: true},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		Plan:        exec02TestPlan(exec02TestProjections(true)),
	}
	resolved := Resolved{
		Bundle: recipe.Bundle{Name: "pg_dispatch"},
		Semantic: semantic.ResolvedRecipePlan{
			SemanticPlan: semantic.RecipePlan{Bindings: recipe.RuntimeBindings{
				Project: "P1", DatasetGeneration: "G1", OutputNames: []string{"resources"},
				IncludeSourceIdentity: true,
			}},
		},
		Compiled:           lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}},
		StoredRecipeDigest: "pg-source-identity",
	}
	rows := []map[string]any{
		{"id": "patient-one", "name": "Ada", sourceID: "Patient/p1"},
	}
	engine, _ := exec02pgEngine(t, spies, rows)
	var public []map[string]any
	if _, err := engine.PreviewOutput(context.Background(), resolved,
		PreviewRequest{Output: "resources", Limit: 5}, func(row map[string]any) error {
			public = append(public, row)
			return nil
		}); err != nil {
		t.Fatalf("PreviewOutput: %v", err)
	}
	if len(public) != 1 {
		t.Fatalf("visited=%d want 1", len(public))
	}
	source, ok := public[0][previewSourceMetadataKey].(map[string]any)
	if !ok {
		t.Fatalf("preview row is missing the source metadata key: %v", public[0])
	}
	if source["kind"] != "SINGLE" || source["resourceType"] != "Patient" || source["id"] != "Patient/p1" {
		t.Fatalf("PostgreSQL preview source identity must be SINGLE with the root resource type, got %v (RootResourceType dropped from the compiled query?)", source)
	}
	if _, leaked := public[0][sourceID]; leaked {
		t.Fatalf("internal source-id column leaked into the public row: %v", public[0])
	}
}

func TestPostgreSQLPreviewPostLoopCancellationNeverMarksComplete(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, []map[string]any{
		{"id": "patient-one", "name": "Ada"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	summary, err := engine.PreviewOutput(ctx, exec02TestResolved(),
		PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error {
			cancel()
			return nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("post-exhaustion cancellation must report the context cause: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("post-loop cancelled preview emitted a completion marker: %+v", summary)
	}
}

func TestPostgreSQLStreamCarriesNoArangoOnlyPolicy(t *testing.T) {
	spies := &dispatchSpies{}
	reference := exec02PostgreSQLCommand(t)
	var observed RowsRequest
	budgetSawPostgreSQL := false
	pgStream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		sourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			observed = request
			if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
				return err
			}
			if _, ok := QueryMemoryBudgetFromContext(ctx); ok {
				budgetSawPostgreSQL = true
			}
			for _, row := range exec02Rows() {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
		command:          reference,
		commandColumns:   []string{"id", "name"},
		batchSize:        64,
		queryMemoryLimit: 1 << 30,
	}
	if err := pgStream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("PostgreSQL streamRaw: %v", err)
	}
	if budgetSawPostgreSQL {
		t.Fatal("query-memory budget context attached to a PostgreSQL stream")
	}
	if observed.BatchSize != 64 {
		t.Fatalf("batch hint = %d, want 64", observed.BatchSize)
	}
	if _, _, err := observed.Command.PostgreSQLParameters(); err != nil {
		t.Fatalf("forwarded command must be the tagged PostgreSQL statement: %v", err)
	}

	budgetSawAQL := false
	aqlStream := OutputStream{
		Name: "resources", Columns: []string{"id"},
		sourceRows: func(ctx context.Context, request RowsRequest, visit RowVisitor) error {
			if _, _, err := request.Command.AQLParameters(); err != nil {
				return err
			}
			_, budgetSawAQL = QueryMemoryBudgetFromContext(ctx)
			return visit(map[string]any{"id": "patient-one"})
		},
		command:          mustAQLCommand("FOR d IN Patient RETURN d", map[string]any{"project": "P1"}),
		commandColumns:   []string{"id"},
		queryMemoryLimit: 1 << 30,
	}
	if err := aqlStream.streamRaw(context.Background(), func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("AQL streamRaw: %v", err)
	}
	if !budgetSawAQL {
		t.Fatal("control failed: the AQL stream stopped attaching the query-memory budget")
	}
	exec02AssertUntouchedArango(t, spies)
}

func exec02StableRowIdentity(parts ...any) string {
	encoded, err := json.Marshal(parts)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func TestPostgreSQLStreamRowIdentityAndPublicColumns(t *testing.T) {
	spies := &dispatchSpies{}
	engine, _ := exec02pgEngine(t, spies, exec02Rows())
	streams, err := engine.Streams(context.Background(), exec02TestResolved())
	if err != nil {
		t.Fatalf("Streams: %v", err)
	}
	want := exec02StableRowIdentity("patient-one")
	var got []map[string]any
	if _, err := streams[0].Stream(context.Background(), func(row map[string]any) error {
		got = append(got, row)
		return nil
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got[0]["__loom_row_id"] != want {
		t.Fatalf("stable row identity = %v, want %s", got[0]["__loom_row_id"], want)
	}
	for _, row := range got {
		for column := range row {
			switch column {
			case "id", "name", "__loom_row_id":
			default:
				t.Fatalf("PostgreSQL stream row carries unexpected column %q: %v", column, row)
			}
		}
	}
}
