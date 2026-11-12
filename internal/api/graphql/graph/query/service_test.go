package queryapi

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

const (
	// queryapiBackendDelay is charged through the controlled Service clock, so
	// it is huge without slowing the test.
	queryapiBackendDelay = 90 * time.Second
	// queryapiCursorDelay is charged by the synctest bubble clock: in a bubble,
	// time only advances while every goroutine is durably blocked, so the held
	// cursor is an exact virtual quantum billed to the query span, independent
	// of machine speed.
	queryapiCursorDelay = 9 * time.Second
)

// controlledClock is the deterministic clock behind Service.now. Each read
// moves the clock by one fixed step, so every Run-owned phase reports a fixed
// non-zero duration without racing wall-clock speed; tests advance it
// explicitly to charge a known delay to exactly one region.
type controlledClock struct {
	mu   sync.Mutex
	time time.Time
	step time.Duration
}

func newControlledClock() *controlledClock {
	return &controlledClock{time: time.Date(2026, time.November, 6, 12, 0, 0, 0, time.UTC), step: time.Second}
}

func (c *controlledClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(c.step)
	return c.time
}

func (c *controlledClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

// queryapiBackend is the single execution seam for these tests. It records the
// compiled query it received and can hold the cursor open for a declared
// quantum, so the query-region duration is a declared constant rather than a
// measured race. fieldCalls proves a request reached request preparation even
// when it later failed, so a backend call count of zero is a boundary claim,
// not a tautology.
type queryapiBackend struct {
	mu          sync.Mutex
	calls       int
	fieldCalls  int
	queries     []string
	bindVars    []map[string]any
	rows        []map[string]any
	err         error
	cursorDelay time.Duration
	entered     chan struct{}
	enteredOnce sync.Once
}

// queryRows blocks for cursorDelay before producing any row, which is the
// execution service's SourceQuery span. Inside a synctest bubble that block is
// an exact virtual quantum.
func (b *queryapiBackend) queryRows(_ context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	query, bindVars, deriveErr := request.Command.AQLParameters()
	if deriveErr != nil {
		return deriveErr
	}

	b.mu.Lock()
	b.calls++
	b.queries = append(b.queries, query)
	b.bindVars = append(b.bindVars, bindVars)
	rows := b.rows
	err := b.err
	cursorDelay := b.cursorDelay
	entered := b.entered
	b.mu.Unlock()
	if err != nil {
		return err
	}
	if entered != nil {
		b.enteredOnce.Do(func() { close(entered) })
	}
	if cursorDelay > 0 {
		time.Sleep(cursorDelay)
	}
	for _, row := range rows {
		if visitErr := visit(row); visitErr != nil {
			return visitErr
		}
	}
	return nil
}

// begin opens the entered signal. Call it inside a synctest bubble, before the
// Run under test, so waiting on the cursor is a durably blocking bubble
// operation rather than a wait on an outside channel.
func (b *queryapiBackend) begin() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entered = make(chan struct{})
	b.enteredOnce = sync.Once{}
}

// waitEntered blocks until the cursor has been entered, or fails the test if
// Run finished without ever reaching the backend.
func (b *queryapiBackend) waitEntered(t *testing.T, done <-chan runOutcome) {
	t.Helper()
	b.mu.Lock()
	entered := b.entered
	b.mu.Unlock()
	if entered == nil {
		t.Fatal("backend has no entered signal, call begin() inside the bubble first")
	}
	select {
	case <-entered:
	case outcome := <-done:
		t.Fatalf("Run finished without holding the cursor open, err = %v", outcome.err)
	}
}

func (b *queryapiBackend) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func (b *queryapiBackend) catalogCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fieldCalls
}

func queryapiGenderRows() []map[string]any {
	return []map[string]any{{"_key": "p1", "gender": "female"}}
}

func queryapiSelector(valuePath string) *model.FhirFieldSelectorInput {
	return &model.FhirFieldSelectorInput{ValuePath: valuePath}
}

func queryapiGenderInput() model.FhirDataframeInput {
	return model.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields:       []*model.FhirFieldSelectInput{{Name: "gender", Selector: queryapiSelector("gender")}},
	}
}

func queryapiService(t *testing.T, backend *queryapiBackend, clock *controlledClock) *Service {
	t.Helper()
	service := NewService(Config{CompileTarget: compiler.ArangoTarget(),
		ActiveManifestResolver: &builderActiveManifestResolver{manifest: builderReadyManifest(t, "P1", "generation-1")},
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			backend.mu.Lock()
			backend.fieldCalls++
			backend.mu.Unlock()
			switch options.ResourceType {
			case "", "Patient":
				return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender", Kind: "scalar", DocCount: 1}}, nil
			default:
				return []catalog.PopulatedField{}, nil
			}
		},
		Dataframes: dataframeexecution.NewService(dataframeexecution.ServiceConfig{SourceRows: backend.queryRows}),
	})
	if clock != nil {
		service.now = clock.now
	}
	return service
}

// runOutcome carries a Run result across the goroutine boundary.
type runOutcome struct {
	result *dataframeexecution.Result
	err    error
}

// runAsync starts Run on its own goroutine so the test goroutine can advance
// the controlled clock while the cursor is held open.
func runAsync(service *Service, input model.FhirDataframeInput) <-chan runOutcome {
	done := make(chan runOutcome, 1)
	go func() {
		result, err := service.Run(context.Background(), input, nil)
		done <- runOutcome{result: result, err: err}
	}()
	return done
}

// TestRunDoesNotChargeQueryExecutionToCompilation is the boundary gate. A
// cursor held open for a declared quantum must be charged to the query and
// total durations and never to Compilation. The cursor block runs inside a
// synctest bubble, so its whole duration is an exact virtual quantum: the
// assertions compare declared durations, never a wall-clock speed threshold.
func TestRunDoesNotChargeQueryExecutionToCompilation(t *testing.T) {
	baselineBackend := &queryapiBackend{rows: queryapiGenderRows()}
	baseline, err := queryapiService(t, baselineBackend, newControlledClock()).Run(context.Background(), queryapiGenderInput(), nil)
	if err != nil {
		t.Fatalf("Run() baseline error = %v", err)
	}

	heldBackend := &queryapiBackend{
		rows:        queryapiGenderRows(),
		cursorDelay: queryapiCursorDelay,
	}
	heldClock := newControlledClock()
	heldService := queryapiService(t, heldBackend, heldClock)

	var heldDiagnostics dataframeexecution.QueryDiagnostics
	synctest.Test(t, func(t *testing.T) {
		heldBackend.begin()
		done := runAsync(heldService, queryapiGenderInput())
		heldBackend.waitEntered(t, done)
		// The compiler returned before the cursor was entered, so this advance
		// charges the query region and the total, never the compile interval.
		heldClock.advance(queryapiBackendDelay)

		outcome := <-done
		if outcome.err != nil {
			t.Fatalf("Run() held error = %v", outcome.err)
		}
		if baselineBackend.callCount() != 1 || heldBackend.callCount() != 1 {
			t.Fatalf("backend calls = baseline %d held %d, want one each", baselineBackend.callCount(), heldBackend.callCount())
		}

		heldDiagnostics = outcome.result.Diagnostics
		baselineDiagnostics := baseline.Diagnostics

		// The compilation interval must be invariant to how long the cursor is
		// held open, and non-zero so invariance cannot be met by dropping the phase.
		if baselineDiagnostics.Compilation <= 0 {
			t.Fatalf("baseline compilation = %v, want a non-zero compile interval", baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation != baselineDiagnostics.Compilation {
			t.Fatalf("compilation changed with the cursor hold: held %v, baseline %v; query execution is being charged to compilation", heldDiagnostics.Compilation, baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation >= queryapiBackendDelay {
			t.Fatalf("compilation %v still covers the %v backend hold", heldDiagnostics.Compilation, queryapiBackendDelay)
		}

		// The same hold must be visible to the separately measured query
		// diagnostics: the bubble charged the whole cursor quantum there, and
		// none of it moved into the per-row materialization span. The two spans
		// must account for the quantum exactly, so a phase cannot both absorb
		// and hide it.
		if heldDiagnostics.SourceQuery != queryapiCursorDelay {
			t.Fatalf("held arango query = %v, want exactly the %v held cursor quantum", heldDiagnostics.SourceQuery, queryapiCursorDelay)
		}
		if charged := heldDiagnostics.SourceQuery + heldDiagnostics.RowMaterialization; charged != queryapiCursorDelay {
			t.Fatalf("cursor quantum %v charged %v across query %v and materialization %v", queryapiCursorDelay, charged, heldDiagnostics.SourceQuery, heldDiagnostics.RowMaterialization)
		}
		if heldDiagnostics.SourceQuery >= heldDiagnostics.Total {
			t.Fatalf("held arango query %v is not bounded by total %v", heldDiagnostics.SourceQuery, heldDiagnostics.Total)
		}

		// The same delay must land in the total.
		if want := baselineDiagnostics.Total + queryapiBackendDelay; heldDiagnostics.Total != want {
			t.Fatalf("held total = %v, want %v (baseline %v + cursor delay %v)", heldDiagnostics.Total, want, baselineDiagnostics.Total, queryapiBackendDelay)
		}
	})

	for _, diagnostics := range []dataframeexecution.QueryDiagnostics{baseline.Diagnostics, heldDiagnostics} {
		if diagnostics.InputResolution <= 0 || diagnostics.RequestPreparation <= 0 {
			t.Fatalf("unpopulated run phases: input resolution %v, request preparation %v", diagnostics.InputResolution, diagnostics.RequestPreparation)
		}
		if owned := diagnostics.InputResolution + diagnostics.RequestPreparation + diagnostics.Compilation; diagnostics.Total < owned {
			t.Fatalf("total %v is below the sum %v of the Run-owned phases", diagnostics.Total, owned)
		}
		if diagnostics.SourceQuery < 0 || diagnostics.RowMaterialization < 0 || diagnostics.ResultAssembly < 0 {
			t.Fatalf("negative backend diagnostics: %+v", diagnostics)
		}
	}
	if heldDiagnostics.InputResolution != baseline.Diagnostics.InputResolution ||
		heldDiagnostics.RequestPreparation != baseline.Diagnostics.RequestPreparation {
		t.Fatalf("run-phase durations drifted: held input=%v preparation=%v, baseline input=%v preparation=%v",
			heldDiagnostics.InputResolution, heldDiagnostics.RequestPreparation,
			baseline.Diagnostics.InputResolution, baseline.Diagnostics.RequestPreparation)
	}
}

// TestRunChargesCursorQuantumOnceAcrossRows pins the other half of the query
// diagnostic: the held cursor is one span billed once to SourceQuery, however
// many rows it delivers, and it never leaks into the per-row materialization
// span. The quantum is an exact bubble-time constant.
func TestRunChargesCursorQuantumOnceAcrossRows(t *testing.T) {
	backend := &queryapiBackend{
		rows: []map[string]any{
			{"_key": "p1", "gender": "female"},
			{"_key": "p2", "gender": "male"},
			{"_key": "p3", "gender": "female"},
		},
		cursorDelay: queryapiCursorDelay,
	}
	service := queryapiService(t, backend, nil)

	synctest.Test(t, func(t *testing.T) {
		backend.begin()
		done := runAsync(service, queryapiGenderInput())
		backend.waitEntered(t, done)
		outcome := <-done
		if outcome.err != nil {
			t.Fatalf("Run() error = %v", outcome.err)
		}
		if backend.callCount() != 1 {
			t.Fatalf("backend calls = %d, want 1", backend.callCount())
		}
		if outcome.result.RowCount != 3 {
			t.Fatalf("row count = %d, want the 3 delivered rows", outcome.result.RowCount)
		}
		diagnostics := outcome.result.Diagnostics
		if diagnostics.SourceQuery != queryapiCursorDelay {
			t.Fatalf("arango query = %v, want the single %v cursor quantum, not one per row", diagnostics.SourceQuery, queryapiCursorDelay)
		}
		if diagnostics.RowMaterialization >= queryapiCursorDelay {
			t.Fatalf("row materialization = %v, want the cursor quantum charged only to the query span", diagnostics.RowMaterialization)
		}
	})
}

// TestRunPreservesResultAndSeparateDiagnostics confirms the boundary fix keeps
// the public row shape and the separately reported query, materialization, and
// plan diagnostics intact.
func TestRunPreservesResultAndSeparateDiagnostics(t *testing.T) {
	backend := &queryapiBackend{rows: queryapiGenderRows()}
	result, err := queryapiService(t, backend, newControlledClock()).Run(context.Background(), queryapiGenderInput(), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.callCount() != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.callCount())
	}
	if result.RowCount != 1 || len(result.Rows) != 1 || result.Rows[0]["gender"] != "female" {
		t.Fatalf("unexpected dataframe payload: %#v", result)
	}
	if len(result.Columns) == 0 {
		t.Fatal("columns are empty, want the compiled projection")
	}
	if backend.queries[0] == "" {
		t.Fatal("backend received an empty query")
	}
	if got := backend.bindVars[0]["dataset_generation"]; got != "generation-1" {
		t.Fatalf("backend generation bind = %#v, want generation-1", got)
	}
	diagnostics := result.Diagnostics
	if diagnostics.SourceQuery < 0 || diagnostics.RowMaterialization < 0 || diagnostics.ResultAssembly < 0 {
		t.Fatalf("negative backend diagnostics: %+v", diagnostics)
	}
	if diagnostics.Plan.Fingerprint == "" {
		t.Fatal("plan diagnostics fingerprint is empty, want the compiled plan identity")
	}
	if diagnostics.Compilation >= diagnostics.Total {
		t.Fatalf("compilation %v is not bounded by total %v", diagnostics.Compilation, diagnostics.Total)
	}
}

// TestRunFailedCompileNeverInvokesBackend pins the compile boundary: a request
// that reaches the compiler and fails there must not reach the backend, and the
// error keeps its existing classification.
func TestRunFailedCompileNeverInvokesBackend(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    model.FhirDataframeInput
		wantCode dataframeerrors.ErrorCode
	}{
		{
			// The compiler, not the recipe validator, rejects this projection
			// name; the failure is a compiler-internal fault under the current
			// classification contract.
			name: "compiler rejects reserved projection name",
			input: model.FhirDataframeInput{
				Project: "P1", RootResourceType: "Patient",
				RootFields: []*model.FhirFieldSelectInput{{Name: "_key", Selector: queryapiSelector("gender")}},
			},
			wantCode: dataframeerrors.CodeBackendUnavailable,
		},
		{
			// The request-preparation boundary rejects this before compilation.
			name: "preparation rejects cross-grain row grain",
			input: func() model.FhirDataframeInput {
				grain := "specimen"
				input := queryapiGenderInput()
				input.RowGrain = &grain
				return input
			}(),
			wantCode: dataframeerrors.CodeInvalidRequest,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &queryapiBackend{rows: queryapiGenderRows()}
			result, err := queryapiService(t, backend, nil).Run(context.Background(), test.input, nil)
			if err == nil {
				t.Fatalf("Run() result = %#v, error = nil, want a failure", result)
			}
			if result != nil {
				t.Fatalf("Run() result = %#v, want nil on failure", result)
			}
			// The request really reached catalog-backed preparation, so the zero
			// backend count below is a boundary claim rather than an early exit.
			if backend.catalogCallCount() == 0 {
				t.Fatal("catalog discovery was never called, want a request that reached preparation")
			}
			if backend.callCount() != 0 {
				t.Fatalf("backend calls = %d, want 0 when the request fails before execution", backend.callCount())
			}
			userErr, ok := dataframeerrors.AsUserError(err)
			if !ok {
				t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
			}
			if userErr.Code() != string(test.wantCode) {
				t.Fatalf("Run() error code = %q, want %q", userErr.Code(), test.wantCode)
			}
		})
	}
}

// TestRunBackendErrorKeepsClassification confirms the compiler-boundary change
// did not absorb backend failures into the compile path.
func TestRunBackendErrorKeepsClassification(t *testing.T) {
	t.Run("unknown cause becomes retryable backend unavailable", func(t *testing.T) {
		backend := &queryapiBackend{err: errors.New("cursor exploded")}
		_, err := queryapiService(t, backend, nil).Run(context.Background(), queryapiGenderInput(), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("Run() error = %q retryable=%t, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		if backend.callCount() != 1 {
			t.Fatalf("backend calls = %d, want 1", backend.callCount())
		}
	})

	t.Run("semantic backend error keeps its code", func(t *testing.T) {
		backend := &queryapiBackend{err: dataframeerrors.NewError(dataframeerrors.CodeQueryResourceLimitExceeded, "", dataframeerrors.WithRetryable(false))}
		_, err := queryapiService(t, backend, nil).Run(context.Background(), queryapiGenderInput(), nil)
		userErr, ok := dataframeerrors.AsUserError(err)
		if !ok {
			t.Fatalf("Run() error = %v (%T), want a classified UserError", err, err)
		}
		if userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
			t.Fatalf("Run() error code = %q, want QUERY_RESOURCE_LIMIT_EXCEEDED", userErr.Code())
		}
	})
}

// TestQueryDiagnosticsFieldShape pins the diagnostic field set the public
// response is rendered from, so closing the compile interval cannot smuggle in
// a reporting expansion. SourceQuery is the backend-neutral cursor interval
// for either source and SourceBackend names the adapter that produced it;
// there is deliberately no separate ArangoQuery timer — the legacy
// arangoQueryMs response field is a transport projection of SourceQuery.
func TestQueryDiagnosticsFieldShape(t *testing.T) {
	structure := reflect.TypeOf(dataframeexecution.QueryDiagnostics{})
	got := make([]string, 0, structure.NumField())
	for index := 0; index < structure.NumField(); index++ {
		got = append(got, structure.Field(index).Name)
	}
	want := []string{
		"InputResolution", "RequestPreparation", "Compilation",
		"SourceQuery", "SourceBackend", "RowMaterialization", "ResultAssembly", "Total", "Plan",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("QueryDiagnostics fields = %#v, want %#v", got, want)
	}
}
