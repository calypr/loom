package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func (r *Handlers) CreateExplorer(ctx context.Context, request loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	value, err := r.createExplorer(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateExplorer201JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewCreateExplorerErrorResponse(status, failure)
}

func (r *Handlers) GetExplorer(ctx context.Context, request loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	value, err := r.getExplorer(ctx, string(request.Project), string(request.ExplorerId))
	if err == nil {
		return loomapi.GetExplorer200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewGetExplorerErrorResponse(status, failure)
}

func (r *Handlers) PublishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	value, err := r.publishRepositoryExplorerConfig(ctx, request)
	if err == nil {
		return loomapi.PublishRepositoryExplorerConfig200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewPublishRepositoryExplorerConfigErrorResponse(status, failure)
}

func (r *Handlers) PreviewExplorer(ctx context.Context, request loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.previewSavedReceipt(ctx, project, explorerID, request.Body)
	if err == nil {
		return loomapi.PreviewExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewExplorer", err)
	return loomapi.NewPreviewExplorerErrorResponse(status, failure)
}

func (r *Handlers) PublishExplorer(ctx context.Context, request loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	project := string(request.Project)
	explorerID := string(request.ExplorerId)
	value, err := r.publishReceipt(ctx, project, explorerID, authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.PublishExplorer200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "publishExplorer", err)
	return loomapi.NewPublishExplorerErrorResponse(status, failure)
}
