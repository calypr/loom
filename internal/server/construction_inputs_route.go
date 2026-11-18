package server

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
)

func (h *explorerHTTPHandlers) getConstructionInputsDirect(ctx context.Context, project, explorerID string, body *constructioninputs.Request) (constructioninputs.Response, error) {
	var result constructioninputs.Response
	if err := h.authoringReadDirect(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("construction-inputs", errors.New("request body is required"))
	}
	return h.constructionInputs.List(ctx, project, explorerID, *body)
}
