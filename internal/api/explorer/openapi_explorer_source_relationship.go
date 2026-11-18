package explorerapi

import (
	"context"
	"errors"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func (r *Handlers) ProposeExplorerSourceRelationship(ctx context.Context, request loomapi.ProposeExplorerSourceRelationshipRequestObject) (loomapi.ProposeExplorerSourceRelationshipResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.SourceRelationshipProposalRequest) (loomapi.SourceRelationshipProposal, error) {
		var empty loomapi.SourceRelationshipProposal
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return empty, err
		}
		if body == nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", nil)
		}
		input, err := directAuthoringJSON[lifecycle.SourceRelationshipProposalRequest](*body)
		if err != nil {
			return empty, explorer.MalformedRouteError("proposeExplorerSourceRelationship", err)
		}
		input.Project, input.ExplorerID = project, explorerID
		var encoder *previewResponseEncoder
		if input.IncludePreview {
			input.PreviewSinkFactory = func(receipt *explorer.CompilationReceipt, columns []explorer.EmittedColumn) (func(map[string]any) error, error) {
				var err error
				encoder, err = newPreviewResponseEncoder(receipt, input.OutputID, columns, maxExplorerPreviewResponseBytes)
				if err != nil {
					return nil, err
				}
				return encoder.Visit, nil
			}
		}
		value, err := h.application.ProposeSourceRelationship(ctx, input)
		if err != nil {
			var authoringError *explorer.AuthoringError
			if input.IncludePreview && !errors.As(err, &authoringError) {
				return empty, previewRouteError(err)
			}
			return empty, err
		}
		result, err := directAuthoringJSON[loomapi.SourceRelationshipProposal](value)
		if err != nil {
			return empty, err
		}
		if input.IncludePreview {
			if value.NativePreview == nil || encoder == nil {
				return empty, previewRouteError(errors.New("source proposal preview sink was not configured"))
			}
			preview, err := previewResponseFromEncoder(encoder, value.NativePreview.Summary)
			if err != nil {
				return empty, err
			}
			result.Preview = &preview
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerSourceRelationship200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerSourceRelationship", err)
	return loomapi.NewProposeExplorerSourceRelationshipErrorResponse(status, failure)
}
