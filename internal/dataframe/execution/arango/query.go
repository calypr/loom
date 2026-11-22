package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func ClassifyDataframeQueryError(err error) error {
	if err == nil {
		return err
	}
	switch {
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotCellCardinality)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotCellCardinality, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotUnlistedCategory)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotUnlistedCategory, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeConstructionRowValueMultipleValues)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionRowValueMultipleValues, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeConstructionExpansionEmpty)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionExpansionEmpty, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeExplicitGroupUnassignedMember)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeExplicitGroupUnassignedMember, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeRelationshipCardinalityViolation)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeRelationshipCardinalityViolation, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalAnchorInvalid)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalAnchorInvalid, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalPrecisionUnsupported)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalPrecisionUnsupported, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalTieAmbiguous)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalTieAmbiguous, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeUnitIdentityUnknown)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitIdentityUnknown, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeUnitDimensionIncompatible)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitDimensionIncompatible, "")
	case arangostore.IsQueryMemoryLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query_memory"}),
		)
	case arangostore.IsQueryResourceLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryResourceLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query"}),
		)
	case arangostore.IsQueryOutOfMemory(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryBackendOutOfMemory,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "memory"}),
		)
	default:
		return err
	}
}

func ConfiguredAQLQueryRows(logger *slog.Logger, phase string, normalizeError func(error) error, execute dataframeexecution.QueryRows) dataframeexecution.QueryRows {
	return func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
		started := time.Now()
		digest := sha256.Sum256([]byte(query))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe AQL started", "request_id", httpctx.RequestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "query_bytes", len(query), "bind_vars", len(bindVars), "cursor_batch_size", batchSize)
		}
		err := normalizeError(execute(ctx, query, batchSize, bindVars, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		httpctx.LogDiagnostic(logger, level, "dataframe AQL execution", httpctx.RequestIDFromContext(ctx), phase, time.Since(started), err, classifyAQLDiagnostic,
			"query_hash", queryHash)
		return err
	}
}

func classifyAQLDiagnostic(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		return userErr.Code(), dataframeerrors.PublicMessage(err)
	}
	var arangoErr shared.ArangoError
	if errors.As(err, &arangoErr) {
		cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", arangoErr.ErrorNum, arangoErr.Code)
		if arangoErr.ErrorMessage != "" {
			cause += ": " + arangoErr.ErrorMessage
		}
		return "", cause
	}
	var arangoErrPtr *shared.ArangoError
	if errors.As(err, &arangoErrPtr) && arangoErrPtr != nil {
		cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", arangoErrPtr.ErrorNum, arangoErrPtr.Code)
		if arangoErrPtr.ErrorMessage != "" {
			cause += ": " + arangoErrPtr.ErrorMessage
		}
		return "", cause
	}
	return "", err.Error()
}

func LoggedPreviewIndexPreparation(logger *slog.Logger, prepare func(context.Context, compiler.PreviewCoveringIndexSpec) error) func(context.Context, compiler.PreviewCoveringIndexSpec) error {
	return func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
		started := time.Now()
		err := prepare(ctx, spec)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		httpctx.LogDiagnostic(logger, level, "dataframe preview index preparation", httpctx.RequestIDFromContext(ctx), "index_prepare", time.Since(started), err, classifyAQLDiagnostic,
			"collection", spec.Collection, "index", spec.Name, "prewarm", spec.PrepareAfterPreview)
		return err
	}
}

const ExplorerPreviewTimeout = 10 * time.Second

const PivotCategoryScanTimeout = 30 * time.Second

type pivotPreviewQueryBudgetContextKey struct{}

func WithPivotPreviewQueryBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, pivotPreviewQueryBudgetContextKey{}, true)
}

func PreviewQueryMaxRuntime(ctx context.Context) time.Duration {
	if ctx != nil {
		if extended, _ := ctx.Value(pivotPreviewQueryBudgetContextKey{}).(bool); extended {
			return PivotCategoryScanTimeout
		}
	}
	return ExplorerPreviewTimeout
}

func WithPreviewQueryTimeout(query dataframeexecution.QueryRows) dataframeexecution.QueryRows {
	return func(ctx context.Context, text string, batchSize int, binds map[string]any, visit func(map[string]any) error) error {
		queryCtx, cancel := context.WithTimeout(ctx, PreviewQueryMaxRuntime(ctx))
		defer cancel()
		return query(queryCtx, text, batchSize, binds, visit)
	}
}
