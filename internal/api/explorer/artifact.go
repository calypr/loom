package explorerapi

import (
	"cmp"
	"context"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/publication"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (r *Handlers) PrepareExplorerArtifact(ctx context.Context, request loomapi.PrepareExplorerArtifactRequestObject) (loomapi.PrepareExplorerArtifactResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PrepareExplorerArtifactJSONRequestBody) (loomapi.Artifact, error) {
		var response loomapi.Artifact
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return response, err
		}
		if h == nil || h.application == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
		}
		if body == nil {
			return response, explorer.MalformedRouteError("artifact", errors.New("request body is required"))
		}
		result, err := h.owners.Publication.PrepareArtifact(ctx, publication.ArtifactRequest{
			Project: project, ExplorerID: explorerID, RevisionID: body.RevisionId,
			OutputID: body.OutputId, IdempotencyKey: body.IdempotencyKey,
		})
		if err != nil {
			return response, err
		}
		if result.Record == nil || result.Record.State != explorer.ArtifactComplete || result.Record.CompletedAt == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact preparation did not produce a complete archive")
		}
		return directAuthoringJSON[loomapi.Artifact](*result.Record)
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PrepareExplorerArtifact200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "prepareExplorerArtifact", err)
	return loomapi.NewPrepareExplorerArtifactErrorResponse(status, failure)
}

func (r *Handlers) DownloadExplorerArtifact(ctx context.Context, request loomapi.DownloadExplorerArtifactRequestObject) (loomapi.DownloadExplorerArtifactResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, artifactID string) (loomapi.DownloadExplorerArtifact200ApplicationzipResponse, error) {
		var response loomapi.DownloadExplorerArtifact200ApplicationzipResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return response, err
		}
		if h == nil || h.application == nil {
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured")
		}
		reader, record, err := h.owners.Publication.OpenArtifact(ctx, publication.ArtifactOpenRequest{Project: project, ExplorerID: explorerID, ArtifactID: artifactID})
		if err != nil {
			return response, err
		}
		if reader == nil || record == nil || record.State != explorer.ArtifactComplete {
			if reader != nil {
				_ = reader.Close()
			}
			return response, explorer.ExplorerUnavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete")
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": record.Filename})
		digest := record.ArchiveSHA256
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse{
			Body: reader, ContentLength: record.Bytes,
			Headers: loomapi.DownloadExplorerArtifact200ResponseHeaders{ContentDisposition: &disposition, XLoomArtifactSHA256: &digest},
		}, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.ArtifactId)
	if err == nil {
		return loomapi.DownloadExplorerArtifact200ApplicationzipResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "downloadExplorerArtifact", err)
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Code == "ARTIFACT_EXPIRED" {
		status = http.StatusGone
	}
	return loomapi.NewDownloadExplorerArtifactErrorResponse(status, failure)
}

func (h *Handlers) previewSavedReceipt(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody) (loomapi.PreviewResponse, error) {
	return h.previewSavedReceiptWithTimeout(ctx, project, explorerID, body, executionarango.ExplorerPreviewTimeout, false)
}

func (h *Handlers) previewSavedReceiptWithTimeout(ctx context.Context, project, explorerID string, body *loomapi.PreviewExplorerJSONRequestBody, timeout time.Duration, allowSampledAggregate bool) (loomapi.PreviewResponse, error) {
	var result loomapi.PreviewResponse
	if err := h.authorizeAuthoringRead(ctx, project); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("preview", errors.New("receiptId and outputId are required"))
	}
	limit := dataframeexecution.DefaultPreviewLimit
	if body.Limit != nil {
		limit = *body.Limit
		if limit == 0 {
			limit = -1
		}
	}
	previewCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var encoder *previewResponseEncoder
	preview, err := h.owners.Core.Preview(previewCtx, authoringcore.PreviewRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, OutputID: body.OutputId, Limit: limit, AllowSampledAggregatePreview: allowSampledAggregate, SinkFactory: func(receipt *receiptmodel.CompilationReceipt, columns []viewer.EmittedColumn) (func(map[string]any) error, error) {
		var encoderErr error
		encoder, encoderErr = newPreviewResponseEncoder(receipt, body.OutputId, columns, maxExplorerPreviewResponseBytes)
		if encoderErr != nil {
			return nil, encoderErr
		}
		return encoder.Visit, nil
	}})
	if err != nil {
		return result, previewRouteError(err)
	}
	if encoder == nil {
		return result, previewRouteError(errors.New("native preview response sink was not configured"))
	}
	return previewResponseFromEncoder(encoder, preview.Summary)
}

func (h *Handlers) publishReceipt(ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.PublishExplorerJSONRequestBody) (loomapi.PublishResponse, error) {
	var result loomapi.PublishResponse
	if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
		return result, err
	}
	if body == nil {
		return result, explorer.MalformedRouteError("publish", errors.New("receiptId is required"))
	}
	value, err := h.owners.Publication.Publish(ctx, publication.PublishRequest{Project: project, ExplorerID: explorerID, ReceiptID: body.ReceiptId, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, err
	}
	outputs := make([]loomapi.PublicationOutput, 0, len(value.Revision.Materializations))
	for _, materialization := range value.Revision.Materializations {
		outputs = append(outputs, loomapi.PublicationOutput{OutputId: cmp.Or(loomtext.FirstNonBlank(materialization.OutputID, materialization.Output), "Explorer"), State: "READY", MaterializationId: materialization.MaterializationID})
	}
	return loomapi.PublishResponse{ApiVersion: loomapi.LoomCalyprOrgexplorerAuthoringv2, Kind: loomapi.ExplorerBuilderPublication, ReceiptId: value.Receipt.ID, RevisionId: value.Revision.ID, State: string(value.Revision.Status), Outputs: outputs, Diagnostics: []loomapi.Diagnostic{}}, nil
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
