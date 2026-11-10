package execution

import (
	"context"
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"reflect"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestFlattenPivotFieldsCreatesStableFlattenedKeys(t *testing.T) {
	row := flattenPivotFields(map[string]any{
		"pivot": map[string]any{
			"zeta":  "z",
			"alpha": "a",
		},
		"keep": "value",
	}, []string{"pivot"})

	if _, ok := row["pivot"]; ok {
		t.Fatalf("pivot object was not removed: %#v", row)
	}
	want := map[string]any{
		"pivot__alpha": "a",
		"pivot__zeta":  "z",
		"keep":         "value",
	}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("flattenPivotFields() = %#v, want %#v", row, want)
	}
}

func TestRunQueryAppendsDynamicColumnsInStableOrder(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, visit RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			for _, row := range []map[string]any{
				{"_key": "first", "pivot": map[string]any{"zeta": "z", "alpha": "a"}},
				{"_key": "second", "pivot": map[string]any{"beta": "b"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})

	result, err := svc.runQuery(context.Background(), CompiledQuery{
		Command:     mustAQLCommand("RETURN []", nil),
		Columns:     []string{"_key", "pivot"},
		PivotFields: []string{"pivot"},
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("runQuery() error = %v", err)
	}
	wantColumns := []string{"_key", "pivot__alpha", "pivot__beta", "pivot__zeta"}
	if !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}
	if result.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", result.RowCount)
	}
}

func TestStreamQueryDeliversFlattenedRowsWithoutCollectingResultRows(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, visit RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			for _, row := range []map[string]any{
				{"_key": "first", "pivot": map[string]any{"zeta": "z", "alpha": "a"}},
				{"_key": "second", "pivot": map[string]any{"beta": "b"}},
			} {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		},
	})

	gotRows := []map[string]any{}
	result, err := svc.streamQuery(context.Background(), CompiledQuery{
		Command:     mustAQLCommand("RETURN []", nil),
		Columns:     []string{"_key", "pivot"},
		PivotFields: []string{"pivot"},
		Limit:       2,
	}, func(row map[string]any) error {
		gotRows = append(gotRows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("streamQuery() error = %v", err)
	}
	if result.RowCount != 2 {
		t.Fatalf("RowCount = %d, want 2", result.RowCount)
	}
	wantColumns := []string{"_key", "pivot__alpha", "pivot__beta", "pivot__zeta"}
	if !reflect.DeepEqual(result.Columns, wantColumns) {
		t.Fatalf("Columns = %#v, want %#v", result.Columns, wantColumns)
	}
	wantRows := []map[string]any{
		{"_key": "first", "pivot__alpha": "a", "pivot__zeta": "z"},
		{"_key": "second", "pivot__beta": "b"},
	}
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Fatalf("streamed rows = %#v, want %#v", gotRows, wantRows)
	}
}

func TestStreamQueryReturnsPartialProgressWhenVisitorStops(t *testing.T) {
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, visit RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			if err := visit(map[string]any{"_key": "first"}); err != nil {
				return err
			}
			return visit(map[string]any{"_key": "second"})
		},
	})
	stop := assertError("stop")
	result, err := svc.streamQuery(context.Background(), CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}}, func(map[string]any) error {
		return stop
	})
	if err != stop {
		t.Fatalf("streamQuery() error = %v, want visitor error %v", err, stop)
	}
	if result.RowCount != 0 {
		t.Fatalf("RowCount = %d, want 0 because the visitor rejected the first row", result.RowCount)
	}
}

// compiledQueryAdapter is the test-owned backend seam for the generic
// compiled-query path. It records each invocation so a test can prove no query
// is issued for an abandoned request, and it keeps visiting rows after the
// caller cancels the way a lagging driver cursor does.
type compiledQueryAdapter struct {
	calls       int
	rows        []map[string]any
	afterCancel func()
}

func (a *compiledQueryAdapter) queryRows(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
	a.calls++
	for _, row := range a.rows {
		if err := visit(row); err != nil {
			return err
		}
		if a.afterCancel != nil {
			a.afterCancel()
		}
	}
	return nil
}

func TestRunCompiledAbandonsCanceledRequestWithoutQueryingBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	adapter := &compiledQueryAdapter{rows: []map[string]any{{"_key": "first"}}}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	result, err := svc.RunCompiled(ctx, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCompiled() error = %v, want context.Canceled", err)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("cancellation error = %v, want typed CLIENT_CANCELED", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("backend calls = %d, want 0 for an already canceled request", adapter.calls)
	}
	if result != nil {
		t.Fatalf("RunCompiled() = %#v, want nil result on cancellation", result)
	}
}

func TestStreamQueryStopsDeliveryAfterCancellationDuringCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited := []any{}
	adapter := &compiledQueryAdapter{
		rows: []map[string]any{{"_key": "first"}, {"_key": "second"}},
		// Cancellation lands after the first row is delivered. A missing check
		// would hand the second row to the public visitor and report success.
		afterCancel: cancel,
	}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	result, err := svc.streamQuery(ctx, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}}, func(row map[string]any) error {
		visited = append(visited, row["_key"])
		return nil
	})
	// The boundary reports the caller's own context error, wrapped as a typed
	// user error, rather than inventing a fresh sentinel.
	if !errors.Is(err, ctx.Err()) {
		t.Fatalf("streamQuery() error = %v, want the caller context error %v", err, ctx.Err())
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeClientCanceled) {
		t.Fatalf("cancellation error = %v, want typed CLIENT_CANCELED", err)
	}
	if !reflect.DeepEqual(visited, []any{"first"}) {
		t.Fatalf("visited keys = %#v, want only the row delivered before cancellation", visited)
	}
	if result.RowCount != 1 {
		t.Fatalf("RowCount = %d, want 1 delivered row reported with the cancellation", result.RowCount)
	}
}

func TestRunCompiledRejectsNilAdapterErrorAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	adapter := &compiledQueryAdapter{
		rows:        []map[string]any{{"_key": "first"}},
		afterCancel: cancel,
	}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	result, err := svc.RunCompiled(ctx, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunCompiled() error = %v, want context.Canceled when the adapter returns nil", err)
	}
	if result != nil {
		t.Fatalf("RunCompiled() = %#v, want nil result on cancellation", result)
	}
}

func TestRunCompiledCompletesZeroRowsWhileContextIsLive(t *testing.T) {
	adapter := &compiledQueryAdapter{}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	result, err := svc.RunCompiled(context.Background(), CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}})
	if err != nil {
		t.Fatalf("RunCompiled() error = %v, want success for a legitimate empty result", err)
	}
	if result == nil || result.RowCount != 0 {
		t.Fatalf("RunCompiled() = %#v, want an empty successful result", result)
	}
	if adapter.calls != 1 {
		t.Fatalf("backend calls = %d, want 1", adapter.calls)
	}
}

func TestStreamQueryReportsExpiredDeadlineInsteadOfSuccess(t *testing.T) {
	expired, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	adapter := &compiledQueryAdapter{rows: []map[string]any{{"_key": "first"}}}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	if _, err := svc.streamQuery(expired, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}}, func(map[string]any) error {
		t.Fatal("visitor must not run for an expired request")
		return nil
	}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("streamQuery() error = %v, want context.DeadlineExceeded", err)
	}
	if adapter.calls != 0 {
		t.Fatalf("backend calls = %d, want 0 for an expired request", adapter.calls)
	}
}

func TestStreamQueryPreservesBackendErrorOverCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backendErr := assertError("arango cursor failed")
	svc := NewService(ServiceConfig{
		QueryRows: func(_ context.Context, command compiler.CompiledCommand, _ int, _ RowVisitor) error {
			_, _, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}

			cancel()
			return backendErr
		},
	})

	if _, err := svc.streamQuery(ctx, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}}, func(map[string]any) error {
		t.Fatal("visitor must not run when the adapter delivers no row")
		return nil
	}); !errors.Is(err, backendErr) {
		t.Fatalf("streamQuery() error = %v, want the backend error to stay authoritative", err)
	}
}

func TestStreamQueryKeepsVisitorErrorWhenItCancelsMidStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := assertError("stop")
	visited := []any{}
	adapter := &compiledQueryAdapter{rows: []map[string]any{{"_key": "first"}, {"_key": "second"}}}
	svc := NewService(ServiceConfig{QueryRows: aqlRows(adapter.queryRows)})

	result, err := svc.streamQuery(ctx, CompiledQuery{Command: mustAQLCommand("RETURN []", nil), Columns: []string{"_key"}}, func(row map[string]any) error {
		visited = append(visited, row["_key"])
		// The consumer abandons the stream and cancels the request in the same
		// step; its own error stays authoritative over the new cancellation.
		cancel()
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("streamQuery() error = %v, want the visitor error to stay authoritative", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("streamQuery() error = %v, want the visitor error, not a cancellation", err)
	}
	if !reflect.DeepEqual(visited, []any{"first"}) {
		t.Fatalf("visited keys = %#v, want the visitor to see only the first row", visited)
	}
	if result.RowCount != 0 {
		t.Fatalf("RowCount = %d, want 0 because the visitor rejected the first row", result.RowCount)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
