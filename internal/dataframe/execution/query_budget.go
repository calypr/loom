package execution

import "context"

type queryMemoryBudgetContextKey struct{}

func WithQueryMemoryBudget(ctx context.Context, bytes int64) context.Context {
	return context.WithValue(ctx, queryMemoryBudgetContextKey{}, bytes)
}

func QueryMemoryBudgetFromContext(ctx context.Context) (int64, bool) {
	bytes, ok := ctx.Value(queryMemoryBudgetContextKey{}).(int64)
	return bytes, ok
}
