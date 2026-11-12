package server

// arch-backend-08: the selected backend's row-execution adapter cores.
//
// Before this file, the Arango lane assembled three separate inline
// lifecycleClient.QueryRows callbacks for the FHIR dataframe service, the
// recipe engine query lane, and the recipe engine preview lane, each
// repeating the AQL command unwrap and dispatch, so a single query carried
// two unwraps (once inside configuredAQLSourceRows for the hash policy and
// once inside the lane closure). selectedAQLAdapter plus withConfiguredAQLRows
// is now the one AQL adapter core: exactly one closed-dispatch unwrap per
// request, one dispatch site per lane, and the distinct caller
// instrumentation (logging phases, the preview max-runtime budget, the
// preview-route error mapping) preserved exactly as each lane had it.
// configuredAQLSourceRows remains as the witness-facing seam: it feeds an
// untouched RowsRequest to an injected executor through the same
// withConfiguredAQLRows instrumentation, so the error-boundary, logging, and
// PG-tag-landmine witnesses exercise the production instrumentation path.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

// aqlRowExecutor is the selected-Aango row surface the adapter core
// dispatches through: the one lifecycle client the Arango backend owns. It
// is asserted structurally so the composition passes the factory's client
// without importing the private store package.
type aqlRowExecutor interface {
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit arangofactory.RowVisitor) error
	QueryRowsWithMaxRuntime(ctx context.Context, query string, batchSize int, bindVars map[string]any, maxRuntime time.Duration, visit arangofactory.RowVisitor) error
}

// selectedAQLAdapter is the Arango lane's single selected row-execution
// adapter. It owns the client dispatch half of the AQL row path; the
// instrumentation half lives in withConfiguredAQLRows so every lane keeps
// its own logging phase and budget policy over one shared unwrap.
type selectedAQLAdapter struct {
	exec aqlRowExecutor
}

// serviceRows is the FHIR dataframe query lane: plain dispatch, no
// composition-side logging, no query-memory pin, and no preview-route error
// mapping -- the shape the public dataframe query route has always served.
func (a selectedAQLAdapter) serviceRows(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	query, bindVars, dispatchErr := request.Command.AQLParameters()
	if dispatchErr != nil {
		return dispatchErr
	}
	return a.exec.QueryRows(ctx, query, request.BatchSize, bindVars, arangofactory.RowVisitor(visit))
}

// dispatchRows is the recipe engine query lane's already-unwrapped dispatch.
// It arrives through withConfiguredAQLRows, which performed the single
// closed-dispatch unwrap and applies the preview-route error mapping.
func (a selectedAQLAdapter) dispatchRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, _ dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	return a.exec.QueryRows(ctx, query, batchSize, bindVars, arangofactory.RowVisitor(visit))
}

// dispatchPreviewRows is the recipe engine preview lane's already-unwrapped
// dispatch. The per-request max-runtime budget is this lane's own law
// (preview cancellation): pivot category scans extend it, everything else
// keeps the explorer preview timeout.
func (a selectedAQLAdapter) dispatchPreviewRows(ctx context.Context, query string, batchSize int, bindVars map[string]any, _ dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
	return a.exec.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, previewQueryMaxRuntime(ctx), arangofactory.RowVisitor(visit))
}

// aqlRowDispatch is the already-unwrapped AQL row dispatch: the (query,
// bindVars) pair that cleared the closed-dispatch tag guard, plus the lane's
// original request and visitor.
type aqlRowDispatch func(ctx context.Context, query string, batchSize int, bindVars map[string]any, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error

// configuredAQLSourceRows is the witness-facing instrumentation seam around
// withConfiguredAQLRows: the injected execute receives the untouched
// request, so contract witnesses can substitute fakes while exercising the
// production unwrap guard, logging policy, query-memory pin, and
// preview-route error mapping.
func configuredAQLSourceRows(logger *slog.Logger, phase string, execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return withConfiguredAQLRows(logger, phase, func(ctx context.Context, _ string, _ int, _ map[string]any, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		return execute(ctx, request, visit)
	})
}

// withConfiguredAQLRows is the shared instrumentation half of the AQL
// adapter core. This deployment selected the Arango target, so the source
// row port serves AQL-tagged commands only: the command is unwrapped here
// through closed dispatch exactly once, and a command that is not AQL-tagged
// is rejected before dispatch and long before any driver call; PostgreSQL
// statement SQL is never logged or hashed as if it were AQL. The logging and
// hash policy records the AQL view (hash, byte length, bind-var count); the
// query-memory budget from execution's neutral context is handed to the
// Arango adapter's own mechanism (composition only requests the capability;
// vendor interpretation of failures stays inside the adapter).
func withConfiguredAQLRows(logger *slog.Logger, phase string, dispatch aqlRowDispatch) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		query, bindVars, dispatchErr := request.Command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		// Composition wires execution's neutral query-memory budget into the
		// Arango adapter's own mechanism. Vendor interpretation of failures
		// stays inside the adapter; this only requests a capability.
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

// configuredPostgreSQLSourceRows is the PostgreSQL sibling of
// configuredAQLSourceRows for the deployment that selected the PostgreSQL
// target. The dispatch guard derives the SQL view through closed dispatch:
// a command that is not PostgreSQL-tagged is rejected here, before execute
// and long before any driver call, with the compiler's typed routing
// sentinel. The logging and hash policy records only the SHA-256 of the
// statement text plus its byte length and bind-value count; SQL text and
// bound values are never logged, hashed as if they were AQL, or routed to
// any AQL surface (this branch constructs no ArangoDB adapter at all).
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
