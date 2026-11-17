package dataframe

import (
	"context"
	"sync"
	"testing"
	"time"
)

type aggregateDispatchRecorder struct {
	t       *testing.T
	batches chan int
	mu      sync.Mutex
	seen    map[*aggregateCall]int
}

func newAggregateSchedulerHarness(t *testing.T, expectedCalls int) (*aggregateOperationState, *aggregateDispatchRecorder) {
	recorder := &aggregateDispatchRecorder{
		t:       t,
		batches: make(chan int, expectedCalls),
		seen:    make(map[*aggregateCall]int),
	}
	state := &aggregateOperationState{operationCtx: context.Background(), expectedCalls: expectedCalls}
	state.dispatchCalls = recorder.dispatch
	return state, recorder
}

func (r *aggregateDispatchRecorder) dispatch(_ context.Context, calls []*aggregateCall) {
	r.mu.Lock()
	for _, call := range calls {
		r.seen[call]++
		if count := r.seen[call]; count > 1 {
			r.t.Errorf("aggregate call dispatched %d times, want exactly once", count)
		}
	}
	r.mu.Unlock()
	r.batches <- len(calls)
	for _, call := range calls {
		select {
		case call.result <- aggregateCallResult{}:
		default:
		}
	}
}

func (r *aggregateDispatchRecorder) nextBatch(timeout time.Duration) int {
	r.t.Helper()
	select {
	case size := <-r.batches:
		if size <= 0 {
			r.t.Fatalf("dispatcher delivered batch of %d calls", size)
		}
		return size
	case <-time.After(timeout):
		r.t.Fatal("scheduler did not dispatch within timeout")
		return 0
	}
}

func (r *aggregateDispatchRecorder) assertContract(preDrained []int, wantCalls int) {
	r.t.Helper()
	total := 0
	for _, size := range preDrained {
		total += size
	}
drain:
	for {
		select {
		case size := <-r.batches:
			if size <= 0 {
				r.t.Fatalf("dispatcher delivered batch of %d calls", size)
			}
			total += size
		default:
			break drain
		}
	}
	if total != wantCalls {
		r.t.Fatalf("scheduler dispatched %d calls across all batches, want %d", total, wantCalls)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if distinct := len(r.seen); distinct != wantCalls {
		r.t.Errorf("dispatcher handled %d distinct calls, want %d", distinct, wantCalls)
	}
}

func newResolvedAggregateCall() *aggregateCall {
	return &aggregateCall{result: make(chan aggregateCallResult, 1)}
}

func enqueueAndAwait(t *testing.T, wait *sync.WaitGroup, state *aggregateOperationState, call *aggregateCall) {
	wait.Add(1)
	go func() {
		defer wait.Done()
		state.enqueue(call)
		select {
		case <-call.result:
		case <-time.After(time.Second):
			t.Error("aggregate call timed out")
		}
	}()
}

// The 10ms fallback debounce may legitimately split the burst under
// preemption; wall-clock single-batch behavior is not a product guarantee.
// Synchronize on completion and pin the observable contract: every call
// resolves and each call is dispatched exactly once across all batches.
func TestAggregateSchedulerDispatchesExpectedConcurrentCallsOnce(t *testing.T) {
	const burstCalls = 158
	state, recorder := newAggregateSchedulerHarness(t, burstCalls)
	var wait sync.WaitGroup
	for range burstCalls {
		enqueueAndAwait(t, &wait, state, newResolvedAggregateCall())
	}
	wait.Wait()
	recorder.assertContract(nil, burstCalls)
}

func TestAggregateSchedulerBurstSplitsAtFallbackAndRemainderDispatchesAtExpectedCount(t *testing.T) {
	const burstCalls = 158
	state, recorder := newAggregateSchedulerHarness(t, burstCalls)
	var wait sync.WaitGroup
	for range burstCalls - 8 {
		enqueueAndAwait(t, &wait, state, newResolvedAggregateCall())
	}
	firstBatch := recorder.nextBatch(time.Second)
	for range 8 {
		enqueueAndAwait(t, &wait, state, newResolvedAggregateCall())
	}
	wait.Wait()
	recorder.assertContract([]int{firstBatch}, burstCalls)
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
