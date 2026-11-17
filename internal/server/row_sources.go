package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

type aqlRowExecutor interface {
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit arangofactory.RowVisitor) error
	QueryRowsWithMaxRuntime(ctx context.Context, query string, batchSize int, bindVars map[string]any, maxRuntime time.Duration, visit arangofactory.RowVisitor) error
}

type selectedAQLAdapter struct {
	exec aqlRowExecutor
}

func (a selectedAQLAdapter) serviceRows(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	query, bindVars, dispatchErr := request.Command.AQLParameters()
	if dispatchErr != nil {
		return dispatchErr
	}
	return a.exec.QueryRows(ctx, query, request.BatchSize, bindVars, arangofactory.RowVisitor(visit))
}

func (a selectedAQLAdapter) dispatchRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, _ dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	return a.exec.QueryRows(ctx, query, batchSize, bindVars, arangofactory.RowVisitor(visit))
}

func (a selectedAQLAdapter) dispatchPreviewRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, _ dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	return a.exec.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, previewQueryMaxRuntime(ctx), arangofactory.RowVisitor(visit))
}

type aqlRowDispatch func(ctx context.Context, query string, batchSize int, bindVars map[string]any, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error

func configuredAQLSourceRows(logger *slog.Logger, phase string, execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return withConfiguredAQLRows(logger, phase, func(ctx context.Context, _ string, _ int, _ map[string]any, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		return execute(ctx, request, visit)
	})
}

func withConfiguredAQLRows(logger *slog.Logger, phase string, dispatch aqlRowDispatch) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		query, bindVars, dispatchErr := request.Command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		if budget, ok := dataframeexecution.QueryMemoryBudgetFromContext(ctx); ok && budget > 0 {
			ctx = arangofactory.WithQueryMemoryLimit(ctx, budget)
		}
		started := time.Now()
		digest := sha256.Sum256([]byte(query))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe AQL started", "request_id", requestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "query_bytes", len(query), "bind_vars", len(bindVars), "cursor_batch_size", request.BatchSize)
		}
		err := preserveConfiguredQueryError(dispatch(ctx, query, request.BatchSize, bindVars, request, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "dataframe AQL execution", requestIDFromContext(ctx), phase, time.Since(started), err,
			"query_hash", queryHash)
		return err
	}
}

func configuredPostgreSQLSourceRows(logger *slog.Logger, phase string, execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		sqlText, bindValues, dispatchErr := request.Command.PostgreSQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		started := time.Now()
		digest := sha256.Sum256([]byte(sqlText))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe SQL started", "request_id", requestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "statement_bytes", len(sqlText), "bind_values", len(bindValues))
		}
		err := preserveConfiguredQueryError(execute(ctx, request, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "dataframe SQL execution", requestIDFromContext(ctx), phase, time.Since(started), err,
			"query_hash", queryHash)
		return err
	}
}
