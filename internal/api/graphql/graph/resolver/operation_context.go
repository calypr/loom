package resolver

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// WithOperationContext installs all request-scoped loaders and aggregate
// scheduling state at gqlgen's operation boundary.
func (r *Resolver) WithOperationContext(ctx context.Context) context.Context {
	ctx = withFHIRReferenceLoader(ctx, r)
	if r.dataframes != nil {
		ctx = r.dataframes.WithOperationContext(ctx, aggregateRootFieldCount(ctx))
	}
	return ctx
}

func aggregateRootFieldCount(ctx context.Context) int {
	if !graphql.HasOperationContext(ctx) {
		return 0
	}
	op := graphql.GetOperationContext(ctx)
	if op == nil || op.Operation == nil || op.Operation.Operation != ast.Query {
		return 0
	}
	fields := graphql.CollectFields(op, op.Operation.SelectionSet, []string{"Query"})
	count := 0
	for _, field := range fields {
		if field.Name == "dataframeAggregate" || field.Name == "dataframeAggregations" {
			count++
		}
	}
	return count
}
