package proto

import (
	"context"

	"arangodb-proto/internal/querysvc"
)

type BuildScalarIndexOptions = querysvc.BuildScalarIndexOptions
type BuildScalarIndexSummary = querysvc.BuildScalarIndexSummary
type ExecuteQueryOptions = querysvc.ExecuteQueryOptions
type PrepareCaseAssayOptions = querysvc.PrepareCaseAssayOptions
type PrepareCaseAssaySummary = querysvc.PrepareCaseAssaySummary
type QueryOptions = querysvc.QueryOptions

func BuildScalarIndex(ctx context.Context, opts BuildScalarIndexOptions) (BuildScalarIndexSummary, error) {
	return querysvc.BuildScalarIndex(ctx, opts)
}

func DefaultBulkIndex() string {
	return querysvc.DefaultBulkIndex()
}

func DefaultCaseAssayQueryPath() string {
	return querysvc.DefaultCaseAssayQueryPath()
}

func DefaultCaseAssayQueryPathForBackend(backend string) string {
	return querysvc.DefaultCaseAssayQueryPathForBackend(backend)
}

func ExecuteQueryRows(ctx context.Context, opts ExecuteQueryOptions, query string, bindVars map[string]any, visit func(map[string]any) error) error {
	return querysvc.ExecuteQueryRows(ctx, opts, query, bindVars, visit)
}

func PrepareGDCCaseAssayMatrix(ctx context.Context, opts PrepareCaseAssayOptions) (PrepareCaseAssaySummary, error) {
	return querysvc.PrepareGDCCaseAssayMatrix(ctx, opts)
}

func Query(ctx context.Context, opts QueryOptions) (int, error) {
	return querysvc.Query(ctx, opts)
}
