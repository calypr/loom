package lifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

func receiptCellTraceFeature(receipt *receiptmodel.CompilationReceipt, outputID, column string) (CellTraceFeature, bool) {
	if receipt == nil {
		return CellTraceFeature{}, false
	}
	for _, emitted := range receipt.EmittedColumns {
		if emitted.OutputID == outputID && emitted.PublicColumn == column {
			authoredColumn := emitted.PublicColumn
			if len(emitted.AuthoredColumns) > 0 {
				authoredColumn = emitted.AuthoredColumns[0]
			}
			label := strings.TrimSpace(emitted.Label)
			if label == "" {
				label = emitted.PublicColumn
			}
			occurrenceID := strings.TrimSpace(emitted.OccurrenceID)
			if occurrenceID == "" {
				occurrenceID = "base"
			}
			return CellTraceFeature{
				OutputID: emitted.OutputID, Column: emitted.PublicColumn, AuthoredColumn: authoredColumn,
				OccurrenceID: occurrenceID, Label: label, LogicalType: emitted.LogicalType,
				SourceResourceType: emitted.SourceResourceType, SourcePath: emitted.SourcePath,
				ProjectionMode: emitted.ProjectionMode, Lossless: emitted.Lossless,
				LossReasons: append([]string(nil), emitted.LossReasons...),
			}, true
		}
	}
	return CellTraceFeature{}, false
}

func (s *Service) CellTrace(ctx context.Context, request CellTraceRequest) (CellTraceResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RowID) == "" || strings.TrimSpace(request.Column) == "" {
		return CellTraceResult{}, support.Malformed("cellTrace", "receiptId, outputId, rowId, and column are required", nil)
	}
	if request.Offset < 0 {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "INVALID_CELL_TRACE_OFFSET", "offset cannot be negative", nil)
	}
	if request.Limit < 0 || request.Limit > facade.MaxCellTraceContributions {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "INVALID_CELL_TRACE_LIMIT", "limit must be between 1 and 100", nil)
	}
	if s.config.Capability.ForExecution == nil || s.config.CellTrace == nil {
		return CellTraceResult{}, support.Unavailable("cellTrace", "CELL_TRACE_UNAVAILABLE", "cell explanation is not configured", nil)
	}
	receipt, err := s.core.LookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return CellTraceResult{}, err
	}
	if err := s.core.ValidateExecutableReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return CellTraceResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil {
		return CellTraceResult{}, authoringcore.ReceiptCapabilityFailure("cellTrace", "the receipt's capability snapshot is no longer authorized or retained", err)
	}
	if authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return CellTraceResult{}, support.Conflict("cellTrace", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if err := authoringcore.ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return CellTraceResult{}, support.Conflict("cellTrace", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !authoringcore.ReceiptHasOutput(receipt.Bundle, request.OutputID) || authoringcore.ValidateReceiptOutputContract(receipt, request.OutputID) != nil {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	feature, ok := receiptCellTraceFeature(receipt, request.OutputID, request.Column)
	if !ok {
		return CellTraceResult{}, support.Unprocessable("cellTrace", "UNKNOWN_OUTPUT_COLUMN", "column is not in the receipt output", nil)
	}

	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	authoringcore.ApplyAuthorizedScope(&bindings, authorized, false)
	trace, err := s.config.CellTrace(ctx, receipt, bindings, dataframeexecution.CellTraceRequest{
		Output: request.OutputID, RowID: strings.TrimSpace(request.RowID), Column: strings.TrimSpace(request.Column),
		Offset: request.Offset, Limit: request.Limit,
	})
	if err != nil {
		if errors.Is(err, dataframeexecution.ErrCellTraceRowNotFound) {
			return CellTraceResult{}, support.NotFound("cellTrace", "PREVIEW_ROW_NOT_FOUND", "rowId does not exist in the receipt output", err)
		}
		var typed *ports.Error
		if errors.As(err, &typed) {
			return CellTraceResult{}, typed
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			return CellTraceResult{}, support.FailureDetails(ports.ClassUnprocessable, "cellTrace", userErr.Code(), dataframeerrors.PublicMessage(err), userErr.Details(), err)
		}
		return CellTraceResult{}, err
	}
	return CellTraceResult{
		Binding: CellTraceBinding{ReceiptID: receipt.ID, OutputID: request.OutputID, Project: projectid.Canonical(receipt.Project), ExplorerID: receipt.ExplorerID, Generation: receipt.SourceGeneration, ScopeDigest: receipt.AuthorizationScopeDigest},
		Feature: feature,
		Trace:   trace,
	}, nil
}
