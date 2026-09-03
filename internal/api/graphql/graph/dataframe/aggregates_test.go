package dataframe

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAggregateSchedulerDispatchesExpectedConcurrentCallsOnce(t *testing.T) {
	batches := make(chan int, 2)
	state := &aggregateOperationState{operationCtx: context.Background(), expectedCalls: 158}
	state.dispatchCalls = func(_ context.Context, calls []*aggregateCall) {
		batches <- len(calls)
		for _, call := range calls {
			call.result <- aggregateCallResult{}
		}
	}
	var wait sync.WaitGroup
	for range 158 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			call := &aggregateCall{result: make(chan aggregateCallResult, 1)}
			state.enqueue(call)
			select {
			case <-call.result:
			case <-time.After(time.Second):
				t.Error("aggregate call timed out")
			}
		}()
	}
	wait.Wait()
	select {
	case size := <-batches:
		if size != 158 {
			t.Fatalf("batch size = %d, want 158", size)
		}
	default:
		t.Fatal("scheduler did not dispatch")
	}
	select {
	case size := <-batches:
		t.Fatalf("unexpected second batch of %d calls", size)
	default:
	}
}

func TestAggregateSchedulerFallbackAndLateCallUseSeparateBatches(t *testing.T) {
	batches := make(chan int, 2)
	state := &aggregateOperationState{operationCtx: context.Background(), expectedCalls: 2}
	state.dispatchCalls = func(_ context.Context, calls []*aggregateCall) {
		batches <- len(calls)
		for _, call := range calls {
			call.result <- aggregateCallResult{}
		}
	}
	first := &aggregateCall{result: make(chan aggregateCallResult, 1)}
	state.enqueue(first)
	select {
	case size := <-batches:
		if size != 1 {
			t.Fatalf("fallback batch size = %d", size)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("fallback did not dispatch")
	}
	second := &aggregateCall{result: make(chan aggregateCallResult, 1)}
	state.enqueue(second)
	select {
	case size := <-batches:
		if size != 1 {
			t.Fatalf("late batch size = %d", size)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("late call did not dispatch")
	}
}

func TestAggregateSchedulerCancellationReleasesPendingCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &aggregateOperationState{operationCtx: ctx, expectedCalls: 2}
	call := &aggregateCall{result: make(chan aggregateCallResult, 1)}
	state.mu.Lock()
	state.pending = append(state.pending, call)
	state.mu.Unlock()
	cancel()
	state.cancelPending(ctx.Err())
	select {
	case result := <-call.result:
		if result.err != context.Canceled {
			t.Fatalf("cancellation error = %v", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled call remained blocked")
	}
}
