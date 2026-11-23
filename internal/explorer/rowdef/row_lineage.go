package rowdef

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) RowLineage(ctx context.Context, request RowLineageRequest) (RowLineageResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RowID) == "" {
		return RowLineageResult{}, support.Malformed("rowLineage", "receiptId, outputId, and rowId are required", nil)
	}
	if request.Offset < 0 {
		return RowLineageResult{}, support.Unprocessable("rowLineage", "INVALID_ROW_LINEAGE_OFFSET", "offset cannot be negative", nil)
	}
	if request.Limit < 0 || request.Limit > facade.MaxRowLineageContributors {
		return RowLineageResult{}, support.Unprocessable("rowLineage", "INVALID_ROW_LINEAGE_LIMIT", "limit must be between 1 and 100", nil)
	}
	if request.Limit == 0 {
		request.Limit = facade.DefaultRowLineageLimit
	}
	if s.config.Capability.ForExecution == nil || s.config.RowLineage == nil {
		return RowLineageResult{}, support.Unavailable("rowLineage", "ROW_LINEAGE_UNAVAILABLE", "row lineage is not configured", nil)
	}
	receipt, err := s.config.LookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return RowLineageResult{}, err
	}
	if err := s.config.ValidateExecutableReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return RowLineageResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil {
		return RowLineageResult{}, support.ReceiptCapabilityFailure("rowLineage", "the receipt's capability snapshot is no longer authorized or retained", err)
	}
	if authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return RowLineageResult{}, support.Conflict("rowLineage", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return RowLineageResult{}, support.Conflict("rowLineage", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !support.ReceiptHasOutput(receipt.Bundle, request.OutputID) || support.ValidateReceiptOutputContract(receipt, request.OutputID) != nil {
		return RowLineageResult{}, support.Unprocessable("rowLineage", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	support.ApplyAuthorizedScope(&bindings, authorized, false)
	page, err := s.config.RowLineage(ctx, receipt, bindings, dataframeexecution.RowLineageRequest{
		Output: request.OutputID, RowID: strings.TrimSpace(request.RowID), Offset: request.Offset, Limit: request.Limit,
	})
	if err != nil {
		if errors.Is(err, dataframeexecution.ErrRowLineageRowNotFound) {
			return RowLineageResult{}, support.NotFound("rowLineage", "PREVIEW_ROW_NOT_FOUND", "rowId does not exist in the receipt output", err)
		}
		var unsupported *facade.RowLineageUnsupportedError
		if errors.As(err, &unsupported) {
			return RowLineageResult{}, support.Unprocessable("rowLineage", unsupported.Capability.ReasonCode,
				"row lineage is unavailable for this construction operation", err)
		}
		var typed *ports.Error
		if errors.As(err, &typed) {
			return RowLineageResult{}, typed
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			return RowLineageResult{}, support.FailureDetails(ports.ClassUnprocessable, "rowLineage", userErr.Code(), dataframeerrors.PublicMessage(err), userErr.Details(), err)
		}
		return RowLineageResult{}, err
	}
	result := RowLineageResult{
		ReceiptID: receipt.ID, OutputID: request.OutputID, RowID: strings.TrimSpace(request.RowID),
		Status: "COMPLETE", Contributors: page.Contributors, HasMore: page.HasMore,
	}
	if result.Contributors == nil {
		result.Contributors = []dataframeexecution.RowLineageContributor{}
	}
	if page.HasMore {
		next := page.NextOffset
		result.NextOffset = &next
	}
	return result, nil
}
