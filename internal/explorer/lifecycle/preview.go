package lifecycle

import (
	"context"
	"strings"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/projectid"
)

func (s *Service) Preview(ctx context.Context, request PreviewRequest) (PreviewResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
		return PreviewResult{}, malformed("preview", "receiptId and outputId are required", nil)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if request.Limit > dataframeexecution.MaxPreviewLimit {
		return PreviewResult{}, unprocessable("preview", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil)
	}
	if s.config.Capability.ForExecution == nil {
		return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured", nil)
	}
	if s.config.PreviewReceipt == nil {
		return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured", nil)
	}
	receipt, err := s.lookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := s.validateReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PreviewResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PreviewResult{}, conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return PreviewResult{}, conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !receiptHasOutput(receipt.Bundle, request.OutputID) || validateReceiptOutputContract(receipt, request.OutputID) != nil {
		return PreviewResult{}, unprocessable("preview", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{Project: projectid.Legacy(receipt.Project), DatasetGeneration: receipt.SourceGeneration, PreviewLimit: request.Limit, OutputNames: []string{request.OutputID}}
	applyAuthorizedScope(&bindings, authorized, false)
	columns := emittedColumnsForOutput(receipt, request.OutputID)
	result := PreviewResult{Receipt: receipt, Columns: columns}
	if request.SinkFactory == nil {
		return PreviewResult{}, unavailable("preview", "PREVIEW_UNAVAILABLE", "native preview sink is not configured", nil)
	}
	sink, err := request.SinkFactory(receipt, columns)
	if err != nil {
		return PreviewResult{}, err
	}
	result.Summary, err = s.config.PreviewReceipt(ctx, receipt, bindings, sink)
	if err != nil {
		return PreviewResult{}, err
	}
	return result, nil
}
