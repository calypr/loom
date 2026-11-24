package explorerapi

import (
	"context"
	"encoding/json"
	"errors"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func (r *Handlers) CreateInterpretationRevision(ctx context.Context, request loomapi.CreateInterpretationRevisionRequestObject) (loomapi.CreateInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, authResourcePath string, body *loomapi.CreateInterpretationRevisionJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretations", errors.New("request body is required"))
		}
		applicability, err := directAuthoringJSON[interpretation.InterpretationApplicability](body.Applicability)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		rules, err := directAuthoringJSON[[]interpretation.InterpretationRule](body.Rules)
		if err != nil {
			return result, explorer.MalformedRouteError("interpretations", err)
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.owners.Interpretation.CreateInterpretationRevision(ctx, interpretation.CreateInterpretationRevisionRequest{
			Project: project, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Applicability: applicability, Rules: rules, Explanation: body.Explanation,
			Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevision", err)
	return loomapi.NewCreateInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) CreateInterpretationRevisionFromColumn(ctx context.Context, request loomapi.CreateInterpretationRevisionFromColumnRequestObject) (loomapi.CreateInterpretationRevisionFromColumnResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID, authResourcePath string, body *loomapi.CreateInterpretationRevisionFromColumnJSONRequestBody) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringWrite(ctx, project, authResourcePath); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-create", errors.New("request body is required"))
		}
		parentRevisionID := ""
		if body.ParentRevisionId != nil {
			parentRevisionID = *body.ParentRevisionId
		}
		value, err := h.application.CreateInterpretationRevisionFromColumn(ctx, lifecycle.CreateInterpretationRevisionFromColumnRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, Column: body.Column, LibraryID: body.LibraryId, ParentRevisionID: parentRevisionID,
			Explanation: body.Explanation, Author: authscope.SubjectFromContext(ctx),
		})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), string(request.ExplorerId), authResourcePathFromParam(request.Params.AuthResourcePath), request.Body)
	if err == nil {
		return loomapi.CreateInterpretationRevisionFromColumn201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createInterpretationRevisionFromColumn", err)
	return loomapi.NewCreateInterpretationRevisionFromColumnErrorResponse(status, failure)
}

func (r *Handlers) GetInterpretationRevision(ctx context.Context, request loomapi.GetInterpretationRevisionRequestObject) (loomapi.GetInterpretationRevisionResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, revisionID string) (loomapi.InterpretationRevision, error) {
		var result loomapi.InterpretationRevision
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.owners.Interpretation.GetInterpretationRevision(ctx, interpretation.GetInterpretationRevisionRequest{Project: project, RevisionID: revisionID})
		if err != nil {
			return result, err
		}
		return directAuthoringJSON[loomapi.InterpretationRevision](value)
	}(r, ctx, string(request.Project), request.RevisionId)
	if err == nil {
		return loomapi.GetInterpretationRevision200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "getInterpretationRevision", err)
	return loomapi.NewGetInterpretationRevisionErrorResponse(status, failure)
}

func (r *Handlers) ListInterpretationLibraries(ctx context.Context, request loomapi.ListInterpretationLibrariesRequestObject) (loomapi.ListInterpretationLibrariesResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project string) (loomapi.InterpretationLibraryListResponse, error) {
		var result loomapi.InterpretationLibraryListResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		value, err := h.owners.Interpretation.ListInterpretationLibraries(ctx, project)
		if err != nil {
			return result, err
		}
		result.Project = value.Project
		result.Libraries = make([]loomapi.InterpretationLibraryView, 0, len(value.Libraries))
		for _, view := range value.Libraries {
			library, convertErr := directAuthoringJSON[loomapi.InterpretationLibrary](view.Library)
			if convertErr != nil {
				return result, convertErr
			}
			wire := loomapi.InterpretationLibraryView{Library: library}
			if view.Head != nil {
				head, convertErr := directAuthoringJSON[loomapi.InterpretationRevision](*view.Head)
				if convertErr != nil {
					return result, convertErr
				}
				wire.Head = &head
			}
			result.Libraries = append(result.Libraries, wire)
		}
		return result, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.ListInterpretationLibraries200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "listInterpretationLibraries", err)
	return loomapi.NewListInterpretationLibrariesErrorResponse(status, failure)
}

func (r *Handlers) PreviewInterpretationCandidate(ctx context.Context, request loomapi.PreviewInterpretationCandidateRequestObject) (loomapi.PreviewInterpretationCandidateResponseObject, error) {
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PreviewInterpretationCandidateJSONRequestBody) (loomapi.InterpretationPreviewResponse, error) {
		var result loomapi.InterpretationPreviewResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("interpretation-preview", errors.New("request body is required"))
		}
		limit := dataframeexecution.DefaultPreviewLimit
		if body.Limit != nil {
			limit = *body.Limit
		}
		previewCtx, cancel := context.WithTimeout(ctx, executionarango.ExplorerPreviewTimeout)
		defer cancel()
		value, err := h.application.PreviewInterpretationCandidate(previewCtx, interpretation.PreviewInterpretationCandidateRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, Column: body.Column, RevisionID: body.RevisionId, Limit: limit,
		})
		if err != nil {
			var lifecycleErr *ports.Error
			if errors.As(err, &lifecycleErr) {
				return result, err
			}
			return result, previewRouteError(err)
		}
		result, err = directAuthoringJSON[loomapi.InterpretationPreviewResponse](value)
		if err != nil {
			return result, err
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return result, err
		}
		if len(encoded) > maxExplorerPreviewResponseBytes {
			return result, previewRouteError(&previewResponseTooLargeError{Limit: maxExplorerPreviewResponseBytes})
		}
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.PreviewInterpretationCandidate200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "previewInterpretationCandidate", err)
	return loomapi.NewPreviewInterpretationCandidateErrorResponse(status, failure)
}
