package execution

// arch-backend-01 PostgreSQL 18 boundary evidence. These tests drive the
// typed compiled-command dispatch against the real PostgreSQL 18 service
// (LOOM_TEST_POSTGRES_URL, skipped otherwise) and pin the properties the
// migration must preserve at the seam: one server round-trip per dispatched
// command (zero churn), ten rounds of concurrent streams racing on distinct
// real backends with an overlap witness, named fail-closed refusals that
// never touch the server, and a crash injected between dispatch and driver
// that leaves the server untouched and clears preview markers.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

const verifyArch01Rounds = 10

// TestVerifyArch01PostgreSQLDispatchZeroChurnRealServer pins that one engine
// stream of one compiled PostgreSQL command costs exactly one statement
// round-trip through the driver and one server-side transaction commit. Any
// hidden re-execution — an AQL identity fallback leaking into the PostgreSQL
// path, a re-rendered command, or a duplicated dispatch — shows up as churn
// and fails this witness.
func TestVerifyArch01PostgreSQLDispatchZeroChurnRealServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	queries := 0
	var mu sync.Mutex
	counting := &exec02CountingQuerier{inner: s.iso, mu: &mu, count: &queries}
	executor, err := dataframepostgres.NewExecutor(counting)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	config := exec02ForbidArango()
	selection := exec02ScopedSelection(s)
	config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return selection, nil
	}
	config.PostgreSQLQueryRows = func(ctx context.Context, command command.CompiledCommand, columns []string, visit RowVisitor) error {
		return executor.QueryRows(ctx, command, columns, dataframepostgres.RowVisitor(visit))
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Warm the pool so pool startup traffic is outside the measured window.
	if _, err := engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false)); err != nil {
		t.Fatalf("warm-up Run: %v", err)
	}
	mu.Lock()
	warmQueries := queries
	mu.Unlock()
	if warmQueries != 1 {
		t.Fatalf("warm-up stream cost %d statement round-trips, want exactly 1", warmQueries)
	}

	// Server-side churn witness: seq_tup_read on the owned fixture table,
	// which excludes catalog and connection-startup noise entirely. The
	// per-dispatch scan cost is calibrated by one measured stream; every
	// remaining dispatch must cost exactly the same. A duplicated dispatch,
	// hidden identity round-trip, or re-render inflates the per-stream cost
	// and fails the witness. Cumulative stats flush on a timer, so the read
	// polls until the witness settles.
	scanWitnessSQL := `SELECT seq_tup_read FROM pg_stat_user_tables WHERE schemaname = $1 AND relname = 'exec02_resource'`
	readServed := func() int64 {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var served int64
		if err := s.iso.QueryRow(s.ctx, scanWitnessSQL, s.schema).Scan(&served); err != nil {
			t.Fatalf("read seq_tup_read witness: %s", s.scrub(err))
		}
		last := served
		for {
			if time.Now().After(deadline) {
				return last
			}
			time.Sleep(50 * time.Millisecond)
			var current int64
			if err := s.iso.QueryRow(s.ctx, scanWitnessSQL, s.schema).Scan(&current); err != nil {
				t.Fatalf("read seq_tup_read witness: %s", s.scrub(err))
			}
			if current != last {
				return current
			}
		}
	}
	beforeCalibration := readServed()
	if _, err := engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false)); err != nil {
		t.Fatalf("calibration Run: %v", err)
	}
	afterCalibration := readServed()
	perStream := afterCalibration - beforeCalibration
	if perStream <= 0 {
		t.Fatalf("calibration dispatch served %d fixture tuples, want a positive witness", perStream)
	}
	const measured = 4
	for i := 0; i < measured; i++ {
		if _, err := engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false)); err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
	}
	mu.Lock()
	measuredQueries := queries - warmQueries
	mu.Unlock()
	if measuredQueries != 1+measured {
		t.Fatalf("%d dispatched commands cost %d statement round-trips, want exactly %d (zero churn)",
			1+measured, measuredQueries, 1+measured)
	}
	if served := readServed(); served-afterCalibration != int64(measured)*perStream {
		t.Fatalf("the %d measured streams served %d fixture tuples, want exactly %d (%d per dispatch, zero churn)",
			measured, served-afterCalibration, int64(measured)*perStream, perStream)
	}
}

// TestVerifyArch01PostgreSQLBoundaryRaceDistinctBackendsOverlapRounds runs
// ten rounds of two engine streams racing through the dispatch at the same
// time. Each stream reads its real pg_backend_pid from the served rows; the
// pids must differ every round, and an in-stream barrier must prove both
// streams were live on the server simultaneously (overlap witness), not
// serialized by the seam.
func TestVerifyArch01PostgreSQLBoundaryRaceDistinctBackendsOverlapRounds(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	raceSQL := `SELECT pg_backend_pid() AS backend, id, logical_id, score, active, payload, note FROM ` + s.qualifiedTable("exec02_resource") + ` WHERE project = $1 AND dataset_generation = $2 ORDER BY id`
	raceCommand, err := command.NewPostgreSQLCommand(raceSQL, []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "P1"},
		{Type: command.PostgreSQLParameterText, Value: "gen-a"},
	})
	if err != nil {
		t.Fatalf("build race command: %v", err)
	}
	raceSelection := &ResolvedPostgreSQLCommand{
		Command: raceCommand,
		Columns: []string{"backend", "id", "logical_id", "score", "active", "payload", "note"},
	}
	resolved := exec02Resolved("resources", "P1", "gen-a", true)
	for round := 0; round < verifyArch01Rounds; round++ {
		var (
			mu       sync.Mutex
			backends = map[int64]bool{}
			arrived  sync.WaitGroup
			release  = make(chan struct{})
		)
		arrived.Add(2)
		errs := make([]error, 2)
		var streamWG sync.WaitGroup
		for i := 0; i < 2; i++ {
			streamWG.Add(1)
			go func(i int) {
				defer streamWG.Done()
				var once sync.Once
				arrive := func() { once.Do(arrived.Done) }
				defer arrive()
				ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
				defer cancel()
				engine, engineErr := New(exec02EngineConfig(s, raceSelection))
				if engineErr != nil {
					errs[i] = engineErr
					return
				}
				streams, streamErr := engine.Streams(ctx, resolved)
				if streamErr != nil {
					errs[i] = streamErr
					return
				}
				if len(streams) != 1 {
					errs[i] = fmt.Errorf("round %d side %d: %d streams, want 1", round, i, len(streams))
					return
				}
				_, streamErr = streams[0].Stream(ctx, func(row map[string]any) error {
					if pid, ok := exec02AsInt64(row["backend"]); ok {
						mu.Lock()
						backends[pid] = true
						mu.Unlock()
						arrive()
						// Overlap witness: hold the stream open until
						// both dispatches are inside their visit phase.
						select {
						case <-release:
						case <-time.After(30 * time.Second):
							return fmt.Errorf("overlap barrier timed out")
						}
					}
					return nil
				})
				errs[i] = streamErr
			}(i)
		}
		arrived.Wait()
		close(release)
		streamWG.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d stream %d: %v", round, i, err)
			}
		}
		mu.Lock()
		distinct := len(backends)
		mu.Unlock()
		if distinct != 2 {
			t.Fatalf("round %d: concurrent dispatches shared a backend (%d distinct pids: %v), want 2", round, distinct, backends)
		}
	}
}

// TestVerifyArch01PostgreSQLNamedRefusalsTouchNoServer drops each outer
// guard in turn and pins that the next layer still refuses with its exact
// named sentinel while the counting driver observes zero statements.
func TestVerifyArch01PostgreSQLNamedRefusalsTouchNoServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	queries := 0
	var mu sync.Mutex
	counting := &exec02CountingQuerier{inner: s.iso, mu: &mu, count: &queries}
	executor, err := dataframepostgres.NewExecutor(counting)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	// Guard drop 1: no resolver-time tag check can run because the selection
	// itself carries a zero tag. The dispatch seam must refuse.
	zeroEngine, err := New(func() Config {
		config := exec02ForbidArango()
		config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
			return &ResolvedPostgreSQLCommand{Command: command.CompiledCommand{}, Columns: []string{"id"}}, nil
		}
		config.PostgreSQLQueryRows = func(ctx context.Context, command command.CompiledCommand, columns []string, visit RowVisitor) error {
			return executor.QueryRows(ctx, command, columns, dataframepostgres.RowVisitor(visit))
		}
		return config
	}())
	if err != nil {
		t.Fatalf("New zero-tag engine: %v", err)
	}
	if _, _, err := zeroEngine.streamForOutput(s.ctx, exec02Resolved("resources", "P1", "gen-a", false), "resources", 5); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("zero-tag selection: err = %v, want %v", err, command.ErrCompiledCommandBackendNotRegistered)
	}

	// Guard drop 2: bypass the engine seam entirely and hand the real
	// adapter an AQL-tagged command. The adapter itself must refuse by name.
	crossTag, err := command.NewAQLCommand("FOR d IN x RETURN d", nil)
	if err != nil {
		t.Fatalf("forge AQL command: %v", err)
	}
	if err := executor.QueryRows(s.ctx, crossTag, []string{"id"}, func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("adapter-level cross tag: err = %v, want %v", err, command.ErrCompiledCommandBackendMismatch)
	}
	if err := executor.QueryRows(s.ctx, command.CompiledCommand{}, []string{"id"}, func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("adapter-level zero tag: err = %v, want %v", err, command.ErrCompiledCommandBackendNotRegistered)
	}

	mu.Lock()
	touched := queries
	mu.Unlock()
	if touched != 0 {
		t.Fatalf("refused commands reached the real server %d time(s), want 0", touched)
	}
}

// TestVerifyArch01PostgreSQLCrashBeforeDriverNeverReachesServer injects a
// crash in the sliver between the dispatch seam and the driver: the adapter
// cancels the context just before invoking the real executor. The preview
// must surface the cancellation, carry no completion marker, and the driver
// must have seen zero statements.
func TestVerifyArch01PostgreSQLCrashBeforeDriverNeverReachesServer(t *testing.T) {
	s := newExec02Service(t)
	s.seedFixture(t, "exec02_resource")
	queries := 0
	var mu sync.Mutex
	counting := &exec02CountingQuerier{inner: s.iso, mu: &mu, count: &queries}
	executor, err := dataframepostgres.NewExecutor(counting)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	config := exec02ForbidArango()
	selection := exec02ScopedSelection(s)
	config.ResolvePostgreSQLCommands = func(context.Context, lower.CompiledRecipeOutput, ir.PhysicalPlan, recipe.RuntimeBindings, int) (*ResolvedPostgreSQLCommand, error) {
		return selection, nil
	}
	config.PostgreSQLQueryRows = func(ctx context.Context, command command.CompiledCommand, columns []string, visit RowVisitor) error {
		// Crash in the sliver after the seam selected this adapter and
		// before the driver is invoked: the command is in hand, the context
		// dies, and the driver call never happens.
		crashCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := crashCtx.Err(); err != nil {
			return err
		}
		return executor.QueryRows(crashCtx, command, columns, dataframepostgres.RowVisitor(visit))
	}
	engine, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	summary, err := engine.PreviewOutput(s.ctx, exec02Resolved("resources", "P1", "gen-a", false),
		PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return nil })
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("crashed dispatch must surface cancellation: %v", err)
	}
	if summary.Complete || summary.Truncated {
		t.Fatalf("crashed preview must carry neither completion marker: %+v", summary)
	}
	mu.Lock()
	touched := queries
	mu.Unlock()
	if touched != 0 {
		t.Fatalf("crashed dispatch reached the driver %d time(s), want 0", touched)
	}
	// Convergence: a fresh engine on the same seam streams the committed
	// golden witness again after the injected crash.
	clean, err := New(exec02EngineConfig(s, selection))
	if err != nil {
		t.Fatalf("New clean: %v", err)
	}
	rows, err := clean.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false))
	if err != nil {
		t.Fatalf("post-crash Run: %v", err)
	}
	if len(rows["resources"]) != 3 {
		t.Fatalf("post-crash stream carried %d rows, want 3", len(rows["resources"]))
	}
}
