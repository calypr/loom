package authoringcore

import (
	"context"
	"errors"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
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
	prepared, err := support.PrepareReceiptOutput(ctx, s, s.config.Capability.ForExecution, support.ReceiptOutputRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: request.ReceiptID, OutputID: request.OutputID, Stage: "preview",
	})
	if err != nil {
		return PreviewResult{}, err
	}
	receipt := prepared.Receipt
	bindings := prepared.Bindings(s.config.SelectionMembersCollection)
	bindings.OutputNames = []string{prepared.OutputID}
	bindings.PreviewLimit = request.Limit
	bindings.IncludeRowIdentity = true
	bindings.IncludeSourceIdentity = true
	bindings.AllowSampledAggregatePreview = request.AllowSampledAggregatePreview
	columns := EmittedColumnsForOutput(receipt, prepared.OutputID)
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
