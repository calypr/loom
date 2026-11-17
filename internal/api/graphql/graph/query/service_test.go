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
	queryapiBackendDelay = 90 * time.Second
	queryapiCursorDelay  = 9 * time.Second
)

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

func (b *queryapiBackend) begin() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entered = make(chan struct{})
	b.enteredOnce = sync.Once{}
}

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

type runOutcome struct {
	result *dataframeexecution.Result
	err    error
}

func runAsync(service *Service, input model.FhirDataframeInput) <-chan runOutcome {
	done := make(chan runOutcome, 1)
	go func() {
		result, err := service.Run(context.Background(), input, nil)
		done <- runOutcome{result: result, err: err}
	}()
	return done
}

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

		if baselineDiagnostics.Compilation <= 0 {
			t.Fatalf("baseline compilation = %v, want a non-zero compile interval", baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation != baselineDiagnostics.Compilation {
			t.Fatalf("compilation changed with the cursor hold: held %v, baseline %v; query execution is being charged to compilation", heldDiagnostics.Compilation, baselineDiagnostics.Compilation)
		}
		if heldDiagnostics.Compilation >= queryapiBackendDelay {
			t.Fatalf("compilation %v still covers the %v backend hold", heldDiagnostics.Compilation, queryapiBackendDelay)
		}

		if heldDiagnostics.SourceQuery != queryapiCursorDelay {
			t.Fatalf("held arango query = %v, want exactly the %v held cursor quantum", heldDiagnostics.SourceQuery, queryapiCursorDelay)
		}
		if charged := heldDiagnostics.SourceQuery + heldDiagnostics.RowMaterialization; charged != queryapiCursorDelay {
			t.Fatalf("cursor quantum %v charged %v across query %v and materialization %v", queryapiCursorDelay, charged, heldDiagnostics.SourceQuery, heldDiagnostics.RowMaterialization)
		}
		if heldDiagnostics.SourceQuery >= heldDiagnostics.Total {
			t.Fatalf("held arango query %v is not bounded by total %v", heldDiagnostics.SourceQuery, heldDiagnostics.Total)
		}

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

func TestRunFailedCompileNeverInvokesBackend(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    model.FhirDataframeInput
		wantCode dataframeerrors.ErrorCode
	}{
		{
			name: "compiler rejects reserved projection name",
			input: model.FhirDataframeInput{
				Project: "P1", RootResourceType: "Patient",
				RootFields: []*model.FhirFieldSelectInput{{Name: "_key", Selector: queryapiSelector("gender")}},
			},
			wantCode: dataframeerrors.CodeBackendUnavailable,
		},
		{
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
