package arango

import (
	"context"
	"log/slog"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/execution"
	store "github.com/calypr/loom/internal/store/arango"
)

type QueryAdapter struct {
	client    *store.Client
	logger    *slog.Logger
	normalize func(error) error
}

func NewQueryAdapter(client *store.Client, logger *slog.Logger, normalize func(error) error) *QueryAdapter {
	return &QueryAdapter{client: client, logger: logger, normalize: normalize}
}

func (a *QueryAdapter) ReadRows(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
	return ClassifyDataframeQueryError(a.client.QueryRows(ctx, query, batch, binds, visit))
}

func (a *QueryAdapter) PreviewIndexPreparation() func(context.Context, compiler.PreviewCoveringIndexSpec) error {
	return LoggedPreviewIndexPreparation(a.logger, func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
		if len(spec.StoredValues) != 0 {
			return a.client.EnsurePreviewCoveringIndexWithStoredValues(ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues)
		}
		return a.client.EnsurePreviewCoveringIndex(ctx, spec.Collection, spec.Name, spec.Fields)
	})
}

func (a *QueryAdapter) Explain(ctx context.Context, query string, bindVars map[string]any) (store.ExplainResult, error) {
	return a.client.Explain(ctx, store.ExplainRequest{Query: query, BindVars: bindVars})
}

func (a *QueryAdapter) PreviewQueryRows() execution.QueryRows {
	return ConfiguredAQLQueryRows(a.logger, "preview_query_rows", a.normalize, func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
		return a.client.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, PreviewQueryMaxRuntime(ctx), store.RowVisitor(visit))
	})
}

func (a *QueryAdapter) QueryRows() execution.QueryRows {
	return ConfiguredAQLQueryRows(a.logger, "query_rows", a.normalize, func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
		return a.client.QueryRows(ctx, query, batchSize, bindVars, store.RowVisitor(visit))
	})
}

func (a *QueryAdapter) WithPreviewReadSnapshot(ctx context.Context, run func(context.Context, execution.QueryRows) error) error {
	collections, err := a.client.ListReadSnapshotCollections(ctx)
	if err != nil {
		return err
	}
	return a.client.WithReadSnapshot(ctx, collections, func(snapshotCtx context.Context, reader store.RowQueryer) error {
		query := ConfiguredAQLQueryRows(a.logger, "preview_query_rows", a.normalize, WithPreviewQueryTimeout(func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			ctx = store.WithQueryMaxNumberOfPlans(ctx, 1)
			return reader.QueryRows(ctx, query, batchSize, bindVars, store.RowVisitor(visit))
		}))
		return run(snapshotCtx, query)
	})
}
