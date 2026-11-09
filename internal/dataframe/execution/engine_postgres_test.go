package execution

// Offline dispatch tests for pg-exec-02: PostgreSQL plans execute only
// through the PostgreSQL row executor, AQL plans retain the existing query,
// resource-limit, and preview behavior, ClickHouse Combine keeps its
// dedicated callbacks, unsupported engines and cross-tag commands fail
// before any callback runs, and a canceled PostgreSQL preview stops without
// a completed marker. Every Arango callback here is a spy that records its
// invocation and names itself in a failure the moment a PostgreSQL stream
// touches it; every guard has a paired control that must proceed, so a
// guard that refused (or allowed) everything fails the suite.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/store/arango"
)

// dispatchSpies records every backend callback the engine could invoke.
// Recording is mutex-guarded because preview index prewarm is asynchronous.
type dispatchSpies struct {
	mu                sync.Mutex
	aqlQueries        []string
	aqlPreviewQueries []string
	pgFull            []postgresCallRecord
	pgPreview         []postgresCallRecord
	clickHouseQueries int
	arangoControls    []string
	resolverAsks      []string
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
	Args    []compiler.PostgreSQLParameter
	Columns []string
}

func (s *dispatchSpies) arangoEngine() Config {
	record := s.recordControl
	return Config{
		Registry:    invalidRecipeRegistry{},
		ScopeDigest: func(recipe.RuntimeBindings) string { return "exec02-test-scope" },
		QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, _ func(map[string]any) error) error {
			s.mu.Lock()
			s.aqlQueries = append(s.aqlQueries, query)
			s.mu.Unlock()
			return nil
		},
		PreviewQueryRows: func(_ context.Context, query string, _ int, _ map[string]any, _ func(map[string]any) error) error {
			s.mu.Lock()
			s.aqlPreviewQueries = append(s.aqlPreviewQueries, query)
			s.mu.Unlock()
			return nil
		},
		PreparePreviewIndex: func(context.Context, compiler.PreviewCoveringIndexSpec) error {
			record("preparePreviewIndex")
			return nil
		},
		PreviewCoveringIndexes: func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
			record("previewCoveringIndexes")
			return nil, nil
		},
		PreviewCollectionRevision: func(context.Context, string) (string, error) {
			record("previewCollectionRevision")
			return "rev-1", nil
		},
		PreviewExplainQuery: func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
			record("previewExplainQuery")
			return arango.ExplainResult{}, nil
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

const (
	exec02TestSQL = `SELECT logical_id AS "id", payload->>'name' AS "name" FROM loom.resource WHERE project = $1 AND dataset_generation = $2 AND payload ? 'name' ORDER BY resource_seq`
)

func exec02TestParameters() []compiler.PostgreSQLParameter {
	return []compiler.PostgreSQLParameter{
		{Type: compiler.PostgreSQLParameterText, Value: "P1"},
		{Type: compiler.PostgreSQLParameterText, Value: "G1"},
	}
}

func exec02TestCommand(t *testing.T) compiler.CompiledCommand {
	t.Helper()
	command, err := compiler.NewPostgreSQLCommand(exec02TestSQL, exec02TestParameters())
	if err != nil {
		t.Fatalf("build PostgreSQL test command: %v", err)
	}
	return command
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
		Plan:        ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
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

func exec02Resolver(spies *dispatchSpies, selection *ResolvedPostgreSQLCommand, selectionErr error) ResolvePostgreSQLCommands {
	return func(_ context.Context, output lower.CompiledRecipeOutput, _ ir.PhysicalPlan, _ recipe.RuntimeBindings, _ int) (*ResolvedPostgreSQLCommand, error) {
		s2 := spies
		s2.mu.Lock()
		s2.resolverAsks = append(s2.resolverAsks, output.Name)
		s2.mu.Unlock()
		return selection, selectionErr
	}
}

func exec02RowExecutor(spies *dispatchSpies, target *[]postgresCallRecord, rows []map[string]any) PostgreSQLQueryRows {
	return exec02RowExecutorWithSpies(spies, target, rows)
}

func exec02RowExecutorWithSpies(spies *dispatchSpies, target *[]postgresCallRecord, rows []map[string]any) PostgreSQLQueryRows {
	return func(ctx context.Context, command compiler.CompiledCommand, columns []string, visit PostgreSQLRowVisitor) error {
		sqlText, args, err := command.PostgreSQLParameters()
		if err != nil {
			return err
		}
		spies.mu.Lock()
		*target = append(*target, postgresCallRecord{SQL: sqlText, Args: args, Columns: append([]string(nil), columns...)})
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

// Acceptance 1: a PostgreSQL plan executes only through the PostgreSQL
// adapter with the command payload carried unchanged, and never invokes any
// Arango query/index callback.
func TestPostgreSQLPlanExecutesOnlyThroughPostgreSQLAdapter(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	config.PreviewPostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgPreview, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
		t.Fatalf("PostgreSQL executor calls = %d, want 1", len(spies.pgFull))
	}
	call := spies.pgFull[0]
	if call.SQL != exec02TestSQL {
		t.Fatalf("statement SQL drifted from the tagged command:\n got %q\nwant %q", call.SQL, exec02TestSQL)
	}
	if !reflect.DeepEqual(call.Args, exec02TestParameters()) {
		t.Fatalf("ordered arguments drifted: %v", call.Args)
	}
	if !reflect.DeepEqual(call.Columns, []string{"id", "name"}) {
		t.Fatalf("declared columns = %v, want [id name]", call.Columns)
	}
	if len(spies.pgPreview) != 0 {
		t.Fatalf("full stream touched the PostgreSQL preview executor: %d calls", len(spies.pgPreview))
	}
	if !reflect.DeepEqual(spies.resolverAsks, []string{"resources"}) {
		t.Fatalf("resolver asks = %v", spies.resolverAsks)
	}
	exec02AssertUntouchedArango(t, spies)

	// The same plan as a preview keeps every Arango control dark and uses
	// the PostgreSQL preview capability (acceptance: separate preview
	// capability; preview override).
	previewSummary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "resources", Limit: 5}, func(map[string]any) error { return nil })
	if err != nil {
		t.Fatalf("PreviewOutput: %v", err)
	}
	if !previewSummary.Complete || previewSummary.Truncated {
		t.Fatalf("short PostgreSQL preview must report Complete=true Truncated=false, got %+v", previewSummary)
	}
	if len(spies.pgPreview) != 1 || len(spies.pgFull) != 1 {
		t.Fatalf("preview must use the PostgreSQL preview executor only: full=%d preview=%d", len(spies.pgFull), len(spies.pgPreview))
	}
	exec02AssertUntouchedArango(t, spies)
}

// Acceptance 1/2 boundary: a preview limited below the fixture row count is
// Truncated, never Complete; the Arango preview controls stay dark.
func TestPostgreSQLPreviewLimitReportsTruncationNotCompletion(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

// Acceptance 5: a canceled PostgreSQL preview stops without marking the
// result complete and without delivering further rows.
func TestPostgreSQLPreviewCancellationStopsWithoutCompletedMarker(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil)
	rows := []map[string]any{
		{"id": "patient-one", "name": "Ada"},
		{"id": "patient-two", "name": "Grace"},
		{"id": "patient-three", "name": "Alan"},
	}
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, rows)
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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

// A mid-stream PostgreSQL failure is reported as a backend error and the
// summary never claims completion (no completed marker for an interrupted
// stream).
func TestPostgreSQLPreviewBackendFailureNeverMarksComplete(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil)
	failure := errors.New("postgres rows: query: FATAL simulated")
	config.PostgreSQLQueryRows = func(ctx context.Context, command compiler.CompiledCommand, columns []string, visit PostgreSQLRowVisitor) error {
		if err := visit(exec02Rows()[0]); err != nil {
			return err
		}
		return failure
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	summary, err := engine.PreviewOutput(context.Background(), exec02TestResolved(), PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return nil })
	if err == nil || !errors.Is(err, failure) {
		t.Fatalf("adapter failure must reach the caller via the error chain: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("failed preview must not mark completion or truncation: %+v", summary)
	}
	exec02AssertUntouchedArango(t, spies)
}

// Acceptance 4: an unsupported physical engine is rejected before any
// backend callback runs.
func TestUnsupportedPhysicalEngineRejectsBeforeAnyCallback(t *testing.T) {
	for _, engineTag := range []ir.PhysicalEngine{"MONGO", "", " CLICKHOUSE ", "clickhouse", "postgres"} {
		if engineTag == "" || engineTag == "clickhouse" || engineTag == " CLICKHOUSE " {
			continue
		}
		spies := &dispatchSpies{}
		config := spies.arangoEngine()
		command := exec02TestCommand(t)
		config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil)
		config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
		engine, err := New(config)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		resolved := exec02TestResolved()
		resolved.Compiled.Outputs[0].Plan.Engine = engineTag
		_, _, err = engine.streamForOutput(context.Background(), resolved, "resources", 5)
		if err == nil || !strings.Contains(err.Error(), "unsupported physical engine") {
			t.Fatalf("unknown plan engine %q must fail closed: %v", engineTag, err)
		}
		if _, err := engine.Run(context.Background(), resolved); err == nil || !strings.Contains(err.Error(), "unsupported physical engine") {
			t.Fatalf("Run with unknown plan engine %q must fail closed: %v", engineTag, err)
		}
		if len(spies.resolverAsks) != 0 {
			t.Fatalf("unsupported engine %q consulted the PostgreSQL resolver: %v", engineTag, spies.resolverAsks)
		}
		exec02AssertUntouchedArango(t, spies)
		if len(spies.pgFull) != 0 {
			t.Fatalf("unsupported engine %q reached the PostgreSQL executor", engineTag)
		}
	}
}

// A PostgreSQL selection without a registered PostgreSQL executor is a
// capability gap: New refuses the misconfiguration outright.
func TestPostgreSQLResolverWithoutExecutorFailsAtConstruction(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, nil, nil)
	if _, err := New(config); err == nil || !strings.Contains(err.Error(), "requires a PostgreSQL row executor") {
		t.Fatalf("resolver without executor must fail construction: %v", err)
	}
	// Belt and braces: an engine assembled with the seam but no executor
	// still fails before any adapter callback.
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	if _, err := New(config); err != nil {
		t.Fatalf("New with executor: %v", err)
	}
	command := exec02TestCommand(t)
	bypass := &Engine{
		registry:                  invalidRecipeRegistry{},
		queryRows:                 config.QueryRows,
		resolvePostgreSQLCommands: exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}, nil),
	}
	_, _, err := bypass.streamForOutput(context.Background(), exec02TestResolved(), "resources", 5)
	if !errors.Is(err, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("missing PostgreSQL executor must report the not-registered capability gap: %v", err)
	}
	if len(spies.pgFull) != 0 || len(spies.aqlQueries) != 0 {
		t.Fatalf("capability gap must not invoke any adapter: pg=%d aql=%v", len(spies.pgFull), spies.aqlQueries)
	}
}

// Acceptance 1/4: commands whose tag does not name PostgreSQL are refused
// by the tagged envelope before the adapter is invoked, with exact named
// refusals.
func TestPostgreSQLSelectionRejectsCrossTagAndZeroTagCommands(t *testing.T) {
	crossTag, err := compiler.NewAQLCommand("FOR d IN resource RETURN d", map[string]any{"project": "P1"})
	if err != nil {
		t.Fatalf("build AQL command: %v", err)
	}
	zeroTag := compiler.CompiledCommand{}
	for _, test := range []struct {
		name       string
		command    compiler.CompiledCommand
		wantIs     error
		wantPhrase string
	}{
		{"AQL-tagged command on the PostgreSQL route", crossTag, compiler.ErrCompiledCommandBackendMismatch, "refusing to pass AQL query to the PostgreSQL statement executor"},
		{"zero-tag command on the PostgreSQL route", zeroTag, compiler.ErrCompiledCommandBackendNotRegistered, "unregistered backend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			spies := &dispatchSpies{}
			config := spies.arangoEngine()
			config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: test.command, Columns: []string{"id"}}, nil)
			// The spy executor does not inspect the command itself: if the
			// engine's pre-callback tag check were dropped, this executor
			// would record and stream the mismatched command and the
			// zero-call assertion below would fail.
			config.PostgreSQLQueryRows = func(ctx context.Context, command compiler.CompiledCommand, columns []string, visit PostgreSQLRowVisitor) error {
				spies.mu.Lock()
				sqlText, args, _ := command.PostgreSQLParameters()
				spies.pgFull = append(spies.pgFull, postgresCallRecord{SQL: sqlText, Args: args, Columns: append([]string(nil), columns...)})
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
			}
			engine, err := New(config)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, _, err = engine.streamForOutput(context.Background(), exec02TestResolved(), "resources", 5)
			if !errors.Is(err, test.wantIs) {
				t.Fatalf("error must satisfy errors.Is(%v): %v", test.wantIs, err)
			}
			if !strings.Contains(err.Error(), test.wantPhrase) {
				t.Fatalf("refusal must name the routing defect %q: %v", test.wantPhrase, err)
			}
			if len(spies.pgFull) != 0 {
				t.Fatalf("mismatched command reached the PostgreSQL executor")
			}
			exec02AssertUntouchedArango(t, spies)
		})
	}
}

// A resolver failure propagates as a typed failure without touching any
// adapter (no silent AQL fallback for a PostgreSQL decision point).
func TestPostgreSQLResolverFailurePropagatesWithoutFallback(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	selectionFailure := fmt.Errorf("%w: no PostgreSQL command renderer is implemented or registered", compiler.ErrCompiledCommandBackendNotRegistered)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, nil, selectionFailure)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = engine.streamForOutput(context.Background(), exec02TestResolved(), "resources", 5)
	if !errors.Is(err, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("resolver capability gap must propagate: %v", err)
	}
	if len(spies.aqlQueries) != 0 || len(spies.pgFull) != 0 {
		t.Fatalf("resolver failure must not fall back to AQL or reach PostgreSQL: aql=%v pg=%d", spies.aqlQueries, len(spies.pgFull))
	}
}

// A PostgreSQL command declared with an empty result column contract is
// refused before the adapter runs (the adapter would refuse too; the engine
// refuses earlier).
func TestPostgreSQLSelectionRequiresDeclaredColumns(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: nil}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _, err = engine.streamForOutput(context.Background(), exec02TestResolved(), "resources", 5)
	if err == nil || !strings.Contains(err.Error(), "declared result column contract") {
		t.Fatalf("empty column contract must be refused: %v", err)
	}
	if len(spies.pgFull) != 0 {
		t.Fatalf("empty column contract reached the PostgreSQL executor")
	}
}

// A PostgreSQL-served output cannot carry a ClickHouse combine payload, and
// private workspace artifact capture refuses PostgreSQL-served dependencies
// instead of falling back to an AQL capture stream.
func TestPostgreSQLSelectionRefusesClickHousePayloadAndPrivateCapture(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	selection := &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id", "name"}}
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, selection, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	combinePlan := exec02TestResolved()
	combine := ir.PhysicalClickHouseCombine{Inputs: []ir.PhysicalCombineInputRef{{RevisionID: "execution-a"}}}
	combinePlan.Compiled.Outputs[0].Plan.ClickHouseCombine = &combine
	_, _, err = engine.streamForOutput(context.Background(), combinePlan, "resources", 5)
	if err == nil || !strings.Contains(err.Error(), "cannot carry a ClickHouse combine payload") {
		t.Fatalf("PostgreSQL selection over a ClickHouse payload must fail closed: %v", err)
	}
	resolved := exec02TestResolved()
	_, _, err = engine.buildCompiledOutputStream(context.Background(), resolved, resolved.Compiled.Outputs[0], 0, map[string]bool{}, true)
	if err == nil || !strings.Contains(err.Error(), "private ClickHouse workspace artifact capture is unsupported") {
		t.Fatalf("private capture over a PostgreSQL-served output must fail closed: %v", err)
	}
	if len(spies.pgFull) != 0 {
		t.Fatalf("refused selections reached the PostgreSQL executor")
	}
	exec02AssertUntouchedArango(t, spies)
}

// Acceptance 3: a ClickHouse combine still uses its dedicated ClickHouse
// callbacks; the PostgreSQL seam is never consulted for a ClickHouse-engine
// plan.
func TestClickHouseCombineUnaffectedAndNeverOfferedPostgreSQL(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	command := exec02TestCommand(t)
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{Command: command, Columns: []string{"id"}}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
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
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rows, err := engine.Run(context.Background(), resolved)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if spies.clickHouseQueries != 1 {
		t.Fatalf("ClickHouse callbacks = %d, want 1", spies.clickHouseQueries)
	}
	if len(spies.pgFull) != 0 || len(spies.aqlQueries) != 0 || len(spies.aqlPreviewQueries) != 0 {
		t.Fatalf("ClickHouse combine leaked into a source adapter: pg=%d aql=%v preview=%v", len(spies.pgFull), spies.aqlQueries, spies.aqlPreviewQueries)
	}
	if len(spies.resolverAsks) != 0 {
		t.Fatalf("PostgreSQL seam consulted for a ClickHouse-engine plan: %v", spies.resolverAsks)
	}
	if got := rows["resources"]; len(got) != 1 || got[0]["value"] != "Ada" {
		t.Fatalf("ClickHouse combine rows drifted: %v", got)
	}
}

// Acceptance 2 (control): with the seam configured but abstaining, the AQL
// path is byte-identical: AQL compile runs, the AQL preview executor is
// used for previews, and the ordinary AQL executor for streams.
func TestAQLPathUnchangedWhenResolverAbstains(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, nil, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bundle := testResolvedBundle([]string{})
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "P1"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := engine.Preview(context.Background(), resolved, 2); err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(spies.aqlPreviewQueries) == 0 || len(spies.pgFull) != 0 {
		t.Fatalf("AQL preview must use the AQL preview executor only: preview=%v pg=%d", spies.aqlPreviewQueries, len(spies.pgFull))
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
	if len(spies.aqlQueries) == 0 || len(spies.pgFull) != 0 {
		t.Fatalf("AQL stream must use the AQL executor only: aql=%v pg=%d", spies.aqlQueries, len(spies.pgFull))
	}
}

// Control for the PostgreSQL-dark assertion: with the PostgreSQL seam
// configured (abstaining), the very same engine shape still drives the
// Arango preview controls for an AQL Group preview — preparePreviewIndex
// fires and the AQL preview executor is used. Guards are not blanket
// refusals.
func TestAQLPreviewStillDrivesArangoControls(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, nil, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
		t.Fatal("AQL preview stopped using the AQL preview executor")
	}
	// Group index preparation is scheduled asynchronously after the
	// preview query; give the prewarm goroutine a bounded window.
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
	if len(spies.pgFull) != 0 {
		t.Fatalf("AQL Group preview leaked into the PostgreSQL executor: %d calls", len(spies.pgFull))
	}
}

// The Group preview optimizer (index listing, EXPLAIN, counts) never
// evaluates a PostgreSQL-served stream, while the identical AQL-shaped
// stream still reaches it (non-vacuity control).
func TestGroupPreviewOptimizerNeverEvaluatesPostgreSQLStream(t *testing.T) {
	spies := &dispatchSpies{}
	command := exec02TestCommand(t)
	evaluate := func(stream *OutputStream) error {
		engine := &Engine{
			registry:  invalidRecipeRegistry{},
			queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
		}
		engine.previewCoveringIndexes = func(context.Context, string) ([]arango.PreviewCoveringIndex, error) {
			record := "previewCoveringIndexes"
			spies.recordControl(record)
			return nil, nil
		}
		engine.previewExplainQuery = func(context.Context, string, map[string]any) (arango.ExplainResult, error) {
			spies.recordControl("previewExplainQuery")
			return arango.ExplainResult{}, nil
		}
		engine.previewCollectionCount = func(context.Context, string) (int64, error) {
			spies.recordControl("previewCollectionCount")
			return 7, nil
		}
		query := &compiler.CompiledQuery{
			Query:    "FOR d IN Patient RETURN d",
			BindVars: map[string]any{"project": "P1"},
			PreviewGroupScan: &compiler.PreviewGroupScanSpec{
				Collection: "Patient", SequentialQuery: "FOR d IN Patient RETURN d",
				ScopeCountQuery: "RETURN 1",
			},
			PreviewCoveringIndex: &compiler.PreviewCoveringIndexSpec{Collection: "Patient", Name: "loom_pivot_preview_a", Fields: []string{"project"}},
		}
		return engine.chooseGroupPreviewScan(context.Background(), query, stream)
	}
	aqlStream := &OutputStream{
		Name: "resources", stream: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	}
	if err := evaluate(aqlStream); err != nil {
		t.Fatalf("AQL group preview scan: %v", err)
	}
	controlsBefore := len(spies.controlSnapshot())
	if controlsBefore == 0 {
		t.Fatal("control failed: the AQL-shaped stream must reach the Arango optimizer callbacks")
	}
	postgresStream := &OutputStream{
		Name:                "resources",
		stream:              aqlStream.stream,
		postgreSQLQueryRows: exec02RowExecutor(spies, &spies.pgFull, exec02Rows()),
		postgreSQLCommand:   command,
		postgreSQLColumns:   []string{"id", "name"},
	}
	if err := evaluate(postgresStream); err != nil {
		t.Fatalf("PostgreSQL group preview scan: %v", err)
	}
	after := spies.controlSnapshot()
	if len(after) != controlsBefore {
		t.Fatalf("PostgreSQL stream invoked Arango optimizer controls: %v", after[controlsBefore:])
	}
	if len(spies.pgFull) != 0 {
		t.Fatalf("optimizer ran the PostgreSQL executor: %d calls", len(spies.pgFull))
	}
}

// The PostgreSQL stream never consumes the loose AQL query/bind fields: a
// PostgreSQL stream built with an armed-but-forbidden AQL adapter and a
// nil AQL adapter both deliver identical rows through the tagged adapter.
func TestPostgreSQLStreamIgnoresLooseQueryBindFields(t *testing.T) {
	spies := &dispatchSpies{}
	command := exec02TestCommand(t)
	rows := exec02Rows()
	stream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		query: "FOR d IN Patient RETURN d",
		stream: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return errors.New("AQL QueryRows must never run for a PostgreSQL stream")
		},
		postgreSQLQueryRows: exec02RowExecutor(spies, &spies.pgFull, rows),
		postgreSQLCommand:   command,
		postgreSQLColumns:   []string{"id", "name"},
	}
	var visited int
	if err := stream.streamRaw(context.Background(), func(map[string]any) error { visited++; return nil }); err != nil {
		t.Fatalf("streamRaw: %v", err)
	}
	if visited != len(rows) {
		t.Fatalf("visited=%d want %d", visited, len(rows))
	}
	if len(spies.pgFull) != 1 || spies.pgFull[0].SQL != exec02TestSQL {
		t.Fatalf("PostgreSQL adapter did not receive the tagged command: %+v", spies.pgFull)
	}
}

// The streamRaw boundary invariant: a PostgreSQL-tagged stream that somehow
// carries no PostgreSQL row executor fails closed with the compiler's
// named unregistered-backend sentinel instead of falling through to the AQL
// executor. Selection normally refuses this configuration first; this test
// exercises the boundary itself with a hand-built stream.
func TestPostgreSQLStreamWithoutExecutorNeverFallsBackToAQL(t *testing.T) {
	aqlCalls := 0
	stream := OutputStream{
		Name: "resources", Columns: []string{"id", "name"},
		query:    "FOR d IN Patient RETURN d",
		bindVars: map[string]any{"project": "P1"},
		stream: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			aqlCalls++
			return errors.New("AQL QueryRows ran as a PostgreSQL fallback")
		},
		postgreSQLCommand: exec02TestCommand(t),
		postgreSQLColumns: []string{"id", "name"},
	}
	err := stream.streamRaw(context.Background(), func(map[string]any) error { return nil })
	if !errors.Is(err, compiler.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("missing PostgreSQL executor must fail closed with the named sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "never fall back to AQL") {
		t.Fatalf("refusal must name the fallback defect: %v", err)
	}
	if aqlCalls != 0 {
		t.Fatalf("AQL executor ran %d time(s) for a PostgreSQL stream", aqlCalls)
	}
}

// The fingerprint of a PostgreSQL-served plan is deterministic over the
// tagged statement, its ordered typed arguments, and the declared column
// contract — and distinguishes every meaningful variation (load-bearing:
// changing any pinned component must change the digest).
func TestPostgreSQLCommandFingerprintIsStableAndSensitivityProbed(t *testing.T) {
	base := postgreSQLCommandFingerprint(exec02TestSQL, exec02TestParameters(), []string{"id", "name"})
	if base == "" || base != postgreSQLCommandFingerprint(exec02TestSQL, exec02TestParameters(), []string{"id", "name"}) {
		t.Fatalf("fingerprint must be deterministic and non-empty: %q", base)
	}
	variants := []struct {
		name    string
		sql     string
		args    []compiler.PostgreSQLParameter
		columns []string
	}{
		{"sql punctuation change", exec02TestSQL + " LIMIT 1", exec02TestParameters(), []string{"id", "name"}},
		{"argument value change", exec02TestSQL, []compiler.PostgreSQLParameter{
			{Type: compiler.PostgreSQLParameterText, Value: "P2"},
			{Type: compiler.PostgreSQLParameterText, Value: "G1"},
		}, []string{"id", "name"}},
		{"argument type change (SQL NULL vs JSON null)", exec02TestSQL, []compiler.PostgreSQLParameter{
			{Type: compiler.PostgreSQLParameterText, Value: "P1"},
			{Type: compiler.PostgreSQLParameterJSON, Value: json.RawMessage(`{"generation":"G1"}`)},
		}, []string{"id", "name"}},
		{"column contract change", exec02TestSQL, exec02TestParameters(), []string{"id"}},
	}
	for _, variant := range variants {
		if got := postgreSQLCommandFingerprint(variant.sql, variant.args, variant.columns); got == base {
			t.Fatalf("fingerprint did not react to %s", variant.name)
		}
	}
}

// Review round 2 (public-column leak): when every declared column is marked
// internal in the compiler-owned output schema, the PostgreSQL selection is
// refused outright. The public row must never fall back to publishing the
// declared contract, because row-identity source and auth-path projections
// are exactly such internal columns.
func TestPostgreSQLSelectionRefusesAllInternalDeclaredColumns(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{
		Command: exec02TestCommand(t),
		Columns: []string{"id", "name"},
	}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, exec02Rows())
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	output := lower.CompiledRecipeOutput{
		Name:             "resources",
		RootResourceType: "Patient",
		Columns:          []string{"id", "name"},
		OutputSchema: []lower.CompiledOutputColumn{
			{ID: "id", Name: "id", Label: "id", Kind: "string", Cardinality: "one", Internal: true},
			{ID: "name", Name: "name", Label: "name", Kind: "string", Cardinality: "one", Internal: true},
		},
		RowIdentity: &spec.RowIdentity{Fields: []string{"id"}},
		Plan:        ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
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
	_, err = engine.Streams(context.Background(), resolved)
	if err == nil || !strings.Contains(err.Error(), "internal projection") {
		t.Fatalf("all-internal declared contract must be refused, got %v", err)
	}
	if len(spies.pgFull) != 0 {
		t.Fatalf("refused selection must not reach the row executor: %d calls", len(spies.pgFull))
	}
	exec02AssertUntouchedArango(t, spies)
}

// Review round 2 (source identity): the PostgreSQL-served plan must carry
// RootResourceType into the CompiledQuery, or an authorized Builder preview
// with IncludeSourceIdentity degrades every row's source to UNAVAILABLE in
// SINGLE mode (which the schema earns via the internal source-id column).
func TestPostgreSQLPreviewReportsSingleSourceIdentityWithResourceType(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	sourceID := ir.PreviewSourceResourceIDColumn
	command, err := compiler.NewPostgreSQLCommand(
		`SELECT logical_id AS "id", payload->>'name' AS "name", resource_id AS "`+sourceID+`" FROM loom.resource ORDER BY resource_seq`,
		exec02TestParameters())
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{
		Command: command,
		Columns: []string{"id", "name", sourceID},
	}, nil)
	rows := []map[string]any{
		{"id": "patient-one", "name": "Ada", sourceID: "Patient/p1"},
	}
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, rows)
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
		Plan:        ir.PhysicalPlan{Version: 1, Engine: ir.PhysicalEngineAQL},
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
	source, ok := public[0]["__loom_preview_source"].(map[string]any)
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

// The third PostgreSQL preview failure-marker site: the context is
// cancelled after the row stream exhausted cleanly, so the post-loop
// context check (not the visitor or the stream error) is what aborts the
// preview. That path must also strip both completion markers.
func TestPostgreSQLPreviewPostLoopCancellationNeverMarksComplete(t *testing.T) {
	spies := &dispatchSpies{}
	config := spies.arangoEngine()
	config.ResolvePostgreSQLCommands = exec02Resolver(spies, &ResolvedPostgreSQLCommand{
		Command: exec02TestCommand(t),
		Columns: []string{"id", "name"},
	}, nil)
	config.PostgreSQLQueryRows = exec02RowExecutor(spies, &spies.pgFull, []map[string]any{
		{"id": "patient-one", "name": "Ada"},
	})
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
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
