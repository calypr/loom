package execution

import "context"

// This file holds execution-owned query resource policy. The budget is a
// neutral value: execution decides when a source-query memory budget applies
// (preview and group-scan assessment paths) and adapters decide whether and
// how to honor it. No vendor type or driver mechanism crosses this boundary;
// backend composition wires the neutral value into the adapter's mechanism.

type queryMemoryBudgetContextKey struct{}

// WithQueryMemoryBudget attaches a per-source-query memory budget in bytes to
// ctx for adapters that support a query memory cap. Adapters without such a
// capability ignore it; the request never changes execution behavior itself.
func WithQueryMemoryBudget(ctx context.Context, bytes int64) context.Context {
	return context.WithValue(ctx, queryMemoryBudgetContextKey{}, bytes)
}

// QueryMemoryBudgetFromContext reports the memory budget execution requested
// for queries run with ctx, if any.
func QueryMemoryBudgetFromContext(ctx context.Context) (int64, bool) {
	bytes, ok := ctx.Value(queryMemoryBudgetContextKey{}).(int64)
	return bytes, ok
}
