package authoringcore

import (
	"context"
	"errors"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	"github.com/calypr/loom/internal/projectid"
)

type PreviewRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Limit      int

	AllowSampledAggregatePreview bool

	SinkFactory func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error)
}

func (s *Core) Preview(ctx context.Context, request PreviewRequest) (PreviewResult, error) {
	if strings.TrimSpace(request.ReceiptID) == "" || strings.TrimSpace(request.OutputID) == "" {
		return PreviewResult{}, support.Malformed("preview", "receiptId and outputId are required", nil)
	}
	if request.Limit == 0 {
		request.Limit = dataframeexecution.DefaultPreviewLimit
	}
	if request.Limit > dataframeexecution.MaxPreviewLimit {
		return PreviewResult{}, support.Unprocessable("preview", "INVALID_PREVIEW_LIMIT", "limit must be between 1 and 1000", nil)
	}
	if s.config.Capability.ForExecution == nil {
		return PreviewResult{}, support.Unavailable("preview", "PREVIEW_UNAVAILABLE", "authorized receipt execution is not configured", nil)
	}
	if s.config.PreviewReceipt == nil {
		return PreviewResult{}, support.Unavailable("preview", "PREVIEW_UNAVAILABLE", "Explorer preview is not configured", nil)
	}
	receipt, err := s.LookupReceipt(ctx, request.Project, request.ExplorerID, request.ReceiptID)
	if err != nil {
		return PreviewResult{}, err
	}
	if err := s.ValidateExecutableReceiptRoute(receipt, request.Project, request.ExplorerID); err != nil {
		return PreviewResult{}, err
	}
	authorized, err := s.config.Capability.ForExecution(ctx, receipt.Project, receipt.SnapshotToken)
	if err != nil {
		return PreviewResult{}, ReceiptCapabilityFailure("preview", "the receipt's capability snapshot is no longer authorized or retained", err)
	}
	if authorized.Snapshot.ValidateToken(receipt.SnapshotToken) != nil || strings.TrimSpace(authorized.Snapshot.Identity.Generation) != strings.TrimSpace(receipt.SourceGeneration) {
		return PreviewResult{}, support.Conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, nil)
	}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return PreviewResult{}, support.Conflict("preview", "RECEIPT_STALE", "the receipt's capability snapshot is no longer authorized or retained", nil, err)
	}
	if !support.ReceiptHasOutput(receipt.Bundle, request.OutputID) || support.ValidateReceiptOutputContract(receipt, request.OutputID) != nil {
		return PreviewResult{}, support.Unprocessable("preview", "UNKNOWN_AUTHORING_OUTPUT", "outputId is not in the receipt", nil)
	}
	bindings := recipe.RuntimeBindings{
		Project: projectid.Legacy(receipt.Project), SelectionProject: projectid.Canonical(receipt.Project),
		DatasetGeneration: receipt.SourceGeneration, SelectionMembersCollection: s.config.SelectionMembersCollection,
		PreviewLimit: request.Limit, OutputNames: []string{request.OutputID}, IncludeRowIdentity: true, IncludeSourceIdentity: true,
		AllowSampledAggregatePreview: request.AllowSampledAggregatePreview,
	}
	support.ApplyAuthorizedScope(&bindings, authorized, false)
	columns := EmittedColumnsForOutput(receipt, request.OutputID)
	result := PreviewResult{Receipt: receipt, Columns: columns}
	if request.SinkFactory == nil {
		return PreviewResult{}, support.Unavailable("preview", "PREVIEW_UNAVAILABLE", "native preview sink is not configured", nil)
	}
	sink, err := request.SinkFactory(receipt, columns)
	if err != nil {
		return PreviewResult{}, err
	}
	result.Summary, err = s.config.PreviewReceipt(ctx, receipt, bindings, sink)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return PreviewResult{}, typed
		}
		if userErr, ok := dataframeerrors.AsUserError(err); ok {
			return PreviewResult{}, support.FailureDetails(ports.ClassUnprocessable, "preview", userErr.Code(), dataframeerrors.PublicMessage(err), userErr.Details(), err)
		}
		return PreviewResult{}, err
	}
	return result, nil
}
