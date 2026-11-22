package explorerapi

import (
	"context"
	"errors"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

type proposalPreviewEncoder struct {
	encoder *previewResponseEncoder
}

func (h *Handlers) proposalPreviewOptions(project, outputID string, limit *int, timeout time.Duration, sampled bool) (lifecycle.ProposalPreviewOptions, *proposalPreviewEncoder) {
	output := &proposalPreviewEncoder{}
	return lifecycle.ProposalPreviewOptions{
		Limit: limit, Timeout: timeout, AllowSampledAggregate: sampled,
		Authorize: func(ctx context.Context) error { return h.authorizeAuthoringRead(ctx, project) },
		Sink: func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
			var err error
			output.encoder, err = newPreviewResponseEncoder(receipt, outputID, columns, maxExplorerPreviewResponseBytes)
			if err != nil {
				return nil, err
			}
			return output.encoder.Visit, nil
		},
	}, output
}

func (e *proposalPreviewEncoder) response(value lifecycle.ProposalPreview) (loomapi.PreviewResponse, error) {
	if e.encoder == nil {
		return loomapi.PreviewResponse{}, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	return previewResponseFromEncoder(e.encoder, value.Result.Summary)
}

func proposalPreviewFailure(err error) error {
	var execution *lifecycle.ProposalPreviewExecutionError
	if errors.As(err, &execution) {
		return previewRouteError(execution.Cause)
	}
	return err
}
