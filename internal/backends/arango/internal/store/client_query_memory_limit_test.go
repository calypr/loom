package arango

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryRowsWithMaxRuntimeAppliesMemoryLimitAndClampsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	ctx = WithQueryMemoryLimit(ctx, 256<<20)
	queryer := &fakeQueryer{cursor: &fakeCursor{}}

	if err := queryRowsWithMaxRuntime(ctx, queryer, "RETURN 1", 3, nil, 30*time.Second, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queryer.called != 1 || queryer.options == nil {
		t.Fatalf("query calls=%d options=%#v, want one query", queryer.called, queryer.options)
	}
	if queryer.options.MemoryLimit != 256<<20 {
		t.Fatalf("query memory limit=%d, want %d", queryer.options.MemoryLimit, int64(256<<20))
	}
	if got := queryer.options.Options.MaxRuntime; got <= 0 || got >= 30 {
		t.Fatalf("query max runtime=%v seconds, want positive and below supplied 30 seconds", got)
	}
}

func TestQueryRowsWithMaxRuntimeLeavesUncappedCallsUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	queryer := &fakeQueryer{cursor: &fakeCursor{}}

	if err := queryRowsWithMaxRuntime(ctx, queryer, "RETURN 1", 3, nil, 10*time.Second, func(map[string]any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if queryer.options == nil || queryer.options.MemoryLimit != 0 || queryer.options.Options.MaxRuntime != 10 {
		t.Fatalf("ordinary query options=%#v, want no memory cap and maxRuntime 10 seconds", queryer.options)
	}
}

func TestQueryRowsWithMaxRuntimeMemoryLimitStillPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}, {"id": "2"}}}
	queryer := &fakeQueryer{cursor: cursor}
	visited := 0
	err := queryRowsWithMaxRuntime(WithQueryMemoryLimit(ctx, 256<<20), queryer, "RETURN 1", 2, nil, time.Second, func(map[string]any) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatalf("error=%v visited=%d, want cancellation after one row", err, visited)
	}
	if queryer.options == nil || queryer.options.MemoryLimit != 256<<20 {
		t.Fatalf("query options=%#v, want memory cap applied before cancellation", queryer.options)
	}
}

func TestQueryRowsWithMaxRuntimeRejectsInvalidMemoryLimit(t *testing.T) {
	queryer := &fakeQueryer{cursor: &fakeCursor{}}
	err := queryRowsWithMaxRuntime(WithQueryMemoryLimit(context.Background(), 0), queryer, "RETURN 1", 1, nil, time.Second, func(map[string]any) error { return nil })
	if err == nil || queryer.called != 0 {
		t.Fatalf("error=%v query calls=%d, want invalid memory limit rejected before querying", err, queryer.called)
	}
}
