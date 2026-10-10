package lifecycle

import (
	"context"
	"errors"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) RowLineage(ctx context.Context, request RowLineageRequest) (RowLineageResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" || strings.TrimSpace(request.RowID) == "" {
		return RowLineageResult{}, malformed("rowLineage", "receiptId, outputId, and rowId are required", nil)
	}
	if request.Offset < 0 {
		return RowLineageResult{}, unprocessable("rowLineage", "INVALID_ROW_LINEAGE_OFFSET", "offset cannot be negative", nil)
	}
	if request.Limit < 0 || request.Limit > compiler.MaxRowLineageContributors {
		return RowLineageResult{}, unprocessable("rowLineage", "INVALID_ROW_LINEAGE_LIMIT", "limit must be between 1 and 100", nil)
	}
	if request.Limit == 0 {
		request.Limit = compiler.DefaultRowLineageLimit
	}
	if s.config.Capability.ForExecution == nil || s.config.RowLineage == nil {
		return RowLineageResult{}, unavailable("rowLineage", "ROW_LINEAGE_UNAVAILABLE", "row lineage is not configured", nil)
	}
	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return RowLineageResult{}, err
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return RowLineageResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return RowLineageResult{}, conflict("rowLineage", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return RowLineageResult{}, conflict("rowLineage", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !receiptHasOutput(receipt.Bundle, request.OutputID) || validateReceiptOutputContract(receipt, request.OutputID) != nil {
		return RowLineageResult{}, unprocessable("rowLineage", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		OutputNames: []string{request.OutputID},
	}
	applyAuthorizedScope(&bindings, authorized, false)
	page, err := s.config.RowLineage(ctx, receipt, bindings, dataframeexecution.RowLineageRequest{
		Output: request.OutputID, RowID: strings.TrimSpace(request.RowID), Offset: request.Offset, Limit: request.Limit,
	})
	if err != nil {
		if errors.Is(err, dataframeexecution.ErrRowLineageRowNotFound) {
			return RowLineageResult{}, notFound("rowLineage", "PREVIEW_ROW_NOT_FOUND", "rowId does not exist in the receipt output", err)
		}
		var unsupported *compiler.RowLineageUnsupportedError
		if errors.As(err, &unsupported) {
			return RowLineageResult{}, unprocessable("rowLineage", unsupported.Capability.ReasonCode,
				"row lineage is unavailable for this construction operation", err)
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
