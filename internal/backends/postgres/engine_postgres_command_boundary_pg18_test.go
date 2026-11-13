package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	dataframepostgres "github.com/calypr/loom/internal/backends/postgres/internal/rows"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/execution"
)

const verifyArch01Rounds = 10

func TestVerifyArch01PostgreSQLDispatchZeroChurnRealServer(t *testing.T) {
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
	config := exec02EngineConfig(s)
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		return executor.SourceRows(ctx, request, visit)
	}
	engine, err := execution.New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := engine.Run(s.ctx, exec02Resolved("resources", "P1", "gen-a", false)); err != nil {
		t.Fatalf("warm-up Run: %v", err)
	}
	mu.Lock()
	warmQueries := queries
	mu.Unlock()
	if warmQueries != 1 {
		t.Fatalf("warm-up stream cost %d statement round-trips, want exactly 1", warmQueries)
	}

	scanWitnessSQL := `SELECT COALESCE(seq_tup_read, 0) + COALESCE(idx_tup_fetch, 0) FROM pg_stat_user_tables WHERE schemaname = $1 AND relname = 'fhir_resource'`
	readServed := func() int64 {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var served int64
		if err := s.iso.QueryRow(s.ctx, scanWitnessSQL, s.schema).Scan(&served); err != nil {
			t.Fatalf("read scan-churn witness: %s", s.scrub(err))
		}
		last := served
		for {
			if time.Now().After(deadline) {
				return last
			}
			time.Sleep(50 * time.Millisecond)
			var current int64
			if err := s.iso.QueryRow(s.ctx, scanWitnessSQL, s.schema).Scan(&current); err != nil {
				t.Fatalf("read scan-churn witness: %s", s.scrub(err))
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

func TestVerifyArch01PostgreSQLBoundaryRaceDistinctBackendsOverlapRounds(t *testing.T) {
	s := newExec02Service(t)
	s.migrate(t)
	s.seedFixture(t, "fhir_resource")
	for round := 0; round < verifyArch01Rounds; round++ {
		var bothInFlight sync.WaitGroup
		bothInFlight.Add(2)
		barrier := func() {
			bothInFlight.Done()
			bothInFlight.Wait()
		}
		type sideResult struct {
			backend int64
			err     error
		}
		leftDone := make(chan sideResult, 1)
		rightDone := make(chan sideResult, 1)
		go func() {
			ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
			defer cancel()
			backend, _, err := s.raceStream(ctx, fmt.Sprintf("boundary-round-%d-left", round), barrier)
			leftDone <- sideResult{backend, err}
		}()
		go func() {
			ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
			defer cancel()
			backend, _, err := s.raceStream(ctx, fmt.Sprintf("boundary-round-%d-right", round), barrier)
			rightDone <- sideResult{backend, err}
		}()
		left, right := <-leftDone, <-rightDone
		if left.err != nil {
			t.Fatalf("round %d left: %v", round, left.err)
		}
		if right.err != nil {
			t.Fatalf("round %d right: %v", round, right.err)
		}
		if left.backend == right.backend || left.backend <= 0 || right.backend <= 0 {
			t.Fatalf("round %d: concurrent dispatches shared a backend (%d, %d), want two distinct server pids",
				round, left.backend, right.backend)
		}
	}
}

func TestVerifyArch01PostgreSQLNamedRefusalsTouchNoServer(t *testing.T) {
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

	crossTag, err := command.NewAQLCommand("FOR d IN x RETURN d", nil)
	if err != nil {
		t.Fatalf("forge AQL command: %v", err)
	}
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: crossTag, DeclaredColumns: []string{"id"}}, func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("adapter-level cross tag: err = %v, want %v", err, command.ErrCompiledCommandBackendMismatch)
	}
	if err := executor.SourceRows(s.ctx, execution.RowsRequest{Command: command.CompiledCommand{}, DeclaredColumns: []string{"id"}}, func(map[string]any) error { return nil }); !errors.Is(err, command.ErrCompiledCommandBackendNotRegistered) {
		t.Fatalf("adapter-level zero tag: err = %v, want %v", err, command.ErrCompiledCommandBackendNotRegistered)
	}

	mu.Lock()
	touched := queries
	mu.Unlock()
	if touched != 0 {
		t.Fatalf("refused commands reached the real server %d time(s), want 0", touched)
	}
}

func TestVerifyArch01PostgreSQLCrashBeforeDriverNeverReachesServer(t *testing.T) {
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
	config := exec02EngineConfig(s)
	config.SourceRows = func(ctx context.Context, request execution.RowsRequest, visit execution.RowVisitor) error {
		if _, _, err := request.Command.PostgreSQLParameters(); err != nil {
			return err
		}
		crashCtx, cancel := context.WithCancel(ctx)
		cancel()
		if err := crashCtx.Err(); err != nil {
			return err
		}
		return executor.SourceRows(crashCtx, request, visit)
	}
	engine, err := execution.New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	summary, err := engine.PreviewOutput(s.ctx, exec02Resolved("resources", "P1", "gen-a", false),
		execution.PreviewRequest{Output: "resources", Limit: 10}, func(map[string]any) error { return nil })
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
	clean, err := execution.New(exec02EngineConfig(s))
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
