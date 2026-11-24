package explorerapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer/publication"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/population"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
)

func (r *Handlers) createExplorer(ctx context.Context, rawProject, authResourcePath string, body *loomapi.CreateExplorerJSONRequestBody) (loomapi.CreateExplorer201JSONResponse, error) {
	var result loomapi.CreateExplorer201JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	if body == nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", nil)
	}
	var request struct {
		Name             string `json:"name"`
		Title            string `json:"title,omitempty"`
		SourceExplorerID string `json:"sourceExplorerId,omitempty"`
	}
	raw, err := json.Marshal(*body)
	if err != nil || httpapi.DecodeStrict(raw, &request) != nil {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "name is required", err)
	}
	value, err := r.application.Create(ctx, lifecycle.CreateRequest{Project: project, Name: request.Name, Title: request.Title, SourceExplorerID: request.SourceExplorerID, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Handlers) getExplorer(ctx context.Context, rawProject, rawID string) (loomapi.GetExplorer200JSONResponse, error) {
	var result loomapi.GetExplorer200JSONResponse
	if r == nil || r.application == nil || r.authorizeRead == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(rawProject)
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	value, err := r.application.Get(ctx, project, rawID)
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	if id := value.Generated.Publication.ExecutionID; id != "" && r.authorizeExecution != nil {
		if err := r.authorizeExecution(ctx, id); err != nil {
			userErr, concealed := dataframeerrors.AsUserError(err)
			if concealed && dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
				return result, legacyOperationFailure(http.StatusNotFound, "EXPLORER_NOT_FOUND", "Explorer not found", err)
			}
			status, body := httpapi.MapServiceError(err, ctx)
			if status == http.StatusNotFound {
				return result, legacyOperationFailure(status, "EXPLORER_NOT_FOUND", "Explorer not found", err)
			}
			if status == http.StatusServiceUnavailable {
				return result, legacyOperationFailure(status, "EXPLORER_UNAVAILABLE", body.Error.Message, err)
			}
			return result, legacyOperationFailure(http.StatusInternalServerError, "EXPLORER_UNAVAILABLE", body.Error.Message, err)
		}
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Handlers) publishRepositoryExplorerConfig(ctx context.Context, request loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfig200JSONResponse, error) {
	var result loomapi.PublishRepositoryExplorerConfig200JSONResponse
	if r == nil || r.application == nil || r.authorizer == nil {
		return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
	}
	project := projectid.Canonical(string(request.Project))
	generation := strings.TrimSpace(string(request.Generation))
	if project == "" || generation == "" {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "project and generation are required", nil)
	}
	authResourcePath := ""
	if request.Params.AuthResourcePath != nil {
		authResourcePath = authscope.NormalizeAuthResourcePath(strings.TrimSpace(*request.Params.AuthResourcePath))
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := r.authorizer.AuthorizeWrite(ctx, principal, project, authResourcePath); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
		}
		return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
	}
	if request.Body == nil {
		return result, legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_WORKSPACE", "workspace is required", nil)
	}
	workspace := ws.Workspace(*request.Body)
	if err := workspace.ValidateForPublication(); err != nil {
		return result, legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_WORKSPACE", err.Error(), err)
	}
	commit := strings.TrimSpace(request.Params.XLoomSourceCommit)
	if commit == "" {
		return result, legacyOperationFailure(http.StatusBadRequest, "MALFORMED_REQUEST", "X-Loom-Source-Commit is required", nil)
	}
	value, err := r.owners.Publication.PublishRepository(ctx, publication.RepositoryPublishRequest{Project: project, Generation: generation, Workspace: workspace, Commit: commit, Actor: authscope.SubjectFromContext(ctx)})
	if err != nil {
		return result, lifecycleLegacyFailure(err)
	}
	return loomapi.PublishRepositoryExplorerConfig200JSONResponse(loomapi.RawJSON{"project": project, "generation": generation, "explorerId": "default", "receiptId": value.Receipt.ID, "revisionId": value.Revision.ID, "executionId": value.Execution.ID, "recipe": value.Receipt.Bundle.Name, "translationVersion": value.Receipt.Bundle.TranslationVersion, "activated": true}), nil
}

func (r *Handlers) ListExplorers(ctx context.Context, request loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	value, err := func(r *Handlers, ctx context.Context, rawProject string) (loomapi.ListExplorers200JSONResponse, error) {
		var result loomapi.ListExplorers200JSONResponse
		if r == nil || r.application == nil || r.authorizeRead == nil {
			return result, legacyOperationFailure(http.StatusInternalServerError, "INTERNAL_ERROR", "Explorer lifecycle is not configured", nil)
		}
		project := projectid.Canonical(rawProject)
		principal, _ := authscope.PrincipalFromContext(ctx)
		if err := r.authorizeRead(ctx, principal, project); err != nil {
			if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
				return result, legacyOperationFailure(http.StatusServiceUnavailable, authoringBackendUnavailableCode, authoringBackendUnavailableMessage, err)
			}
			return result, legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "forbidden", err)
		}
		value, err := r.application.List(ctx, project)
		if err != nil {
			return result, lifecycleLegacyFailure(err)
		}
		raw, err := json.Marshal(value.Summaries)
		if err != nil {
			return result, err
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return result, err
		}
		return result, nil
	}(r, ctx, string(request.Project))
	if err == nil {
		return loomapi.ListExplorers200JSONResponse(value), nil
	}
	status, failure := explorerErrorResponse(err)
	return loomapi.NewListExplorersErrorResponse(status, failure)
}

func (r *Handlers) CreateExplorerExplicitGroupRevision(ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.CreateExplorerExplicitGroupRevisionResponseObject, error) {
	var value loomapi.ExplicitGroupRevisionSummary
	var err error
	if r.application == nil {
		err = explorer.ExplorerUnavailable("explicit-groups", "AUTHORING_UNAVAILABLE", "explicit group authoring is not configured")
	} else {
		value, err = func(h *Handlers, ctx context.Context, request loomapi.CreateExplorerExplicitGroupRevisionRequestObject) (loomapi.ExplicitGroupRevisionSummary, error) {
			if err := h.authorizeAuthoringWrite(ctx, string(request.Project), authResourcePathFromParam(request.Params.AuthResourcePath)); err != nil {
				return loomapi.ExplicitGroupRevisionSummary{}, err
			}
			if request.Body == nil {
				return loomapi.ExplicitGroupRevisionSummary{}, explorer.MalformedRouteError("explicit-groups", nil)
			}
			groups := make([]explicitgroup.ExplicitGroupInput, len(request.Body.Groups))
			for i, group := range request.Body.Groups {
				groups[i] = explicitgroup.ExplicitGroupInput{ID: group.Id, Label: group.Label, Ordinal: group.Ordinal, MemberIDs: group.MemberIds}
			}
			value, err := h.owners.ExplicitGroup.CreateExplicitGroupRevision(ctx, explicitgroup.ExplicitGroupCreateRequest{
				Project: string(request.Project), ExplorerID: string(request.ExplorerId), SnapshotToken: request.Body.SnapshotToken,
				SelectionID: request.SelectionRevision, IdempotencyKey: request.Body.IdempotencyKey, Groups: groups,
			})
			if err != nil {
				return loomapi.ExplicitGroupRevisionSummary{}, err
			}
			response := loomapi.ExplicitGroupRevisionSummary{
				RevisionId: value.RevisionID, SourceSelectionRevisionId: value.SourceSelectionRevisionID,
				GroupCount: value.GroupCount, MemberCount: value.MemberCount, CreatedAt: value.CreatedAt,
				Groups: make([]loomapi.ExplicitGroupSummary, 0, len(value.Groups)),
			}
			for _, group := range value.Groups {
				response.Groups = append(response.Groups, loomapi.ExplicitGroupSummary{Id: group.ID, Label: group.Label, Ordinal: group.Ordinal, MemberCount: group.MemberCount})
			}
			return response, nil
		}(r, ctx, request)
	}
	if err == nil {
		return loomapi.CreateExplorerExplicitGroupRevision201JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "createExplorerExplicitGroupRevision", err)
	return loomapi.NewCreateExplorerExplicitGroupRevisionErrorResponse(status, failure)
}

func (r *Handlers) ProposeExplorerPopulationMemberRemoval(ctx context.Context, request loomapi.ProposeExplorerPopulationMemberRemovalRequestObject) (loomapi.ProposeExplorerPopulationMemberRemovalResponseObject, error) {
	if r.application == nil {
		_, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", explorer.ExplorerUnavailable("population-member-proposal", "AUTHORING_UNAVAILABLE", "Explorer authoring is not configured"))
		return loomapi.ProposeExplorerPopulationMemberRemoval503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	}
	value, err := func(h *Handlers, ctx context.Context, project, explorerID string, body *loomapi.PopulationMemberRemovalProposalRequest) (loomapi.PopulationMemberRemovalProposalResponse, error) {
		var result loomapi.PopulationMemberRemovalProposalResponse
		if err := h.authorizeAuthoringRead(ctx, project); err != nil {
			return result, err
		}
		if body == nil {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		removedMember, err := directAuthoringJSON[explorerselection.ResourceRef](body.RemovedMember)
		if err != nil {
			return result, explorer.MalformedRouteError("population-member-proposal", err)
		}
		request := population.PopulationMemberRemovalProposalRequest{
			Project: project, ExplorerID: explorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputId, BaseSelectionID: body.BaseSelectionRevisionId, RemovedMember: removedMember,
		}
		if body.Limit != nil {
			request.Limit = *body.Limit
		}
		var proposalID string
		options, encoder := h.proposalPreviewOptions(project, body.OutputId, body.Limit, executionarango.ExplorerPreviewTimeout, false)
		run, err := h.application.ProposePopulationMemberRemovalAndPreview(ctx, request, options, func(value population.PopulationMemberRemovalProposalResponse) error {
			if value.ProposalID == "" || value.BaseSelection == nil || value.CandidateSelection == nil || value.PreviewStatus != "PREVIEW_PENDING" {
				return explorer.MalformedRouteError("population-member-proposal", nil)
			}
			proposalID = value.ProposalID
			var err error
			result, err = directAuthoringJSON[loomapi.PopulationMemberRemovalProposalResponse](value)
			return err
		})
		if err != nil {
			return result, proposalPreviewFailure(err)
		}
		preview, err := encoder.response(run)
		result.PreviewDurationMs = time.Since(run.Started).Milliseconds()
		if err != nil {
			return result, err
		}
		if preview.ReceiptId != proposalID || preview.OutputId != body.OutputId {
			return result, explorer.MalformedRouteError("population-member-proposal", nil)
		}
		result.Preview = preview
		result.PreviewStatus = loomapi.PopulationMemberRemovalProposalResponsePreviewStatusREADY
		return result, nil
	}(r, ctx, string(request.Project), string(request.ExplorerId), request.Body)
	if err == nil {
		return loomapi.ProposeExplorerPopulationMemberRemoval200JSONResponse(value), nil
	}
	status, failure := authoringErrorForOpenAPI(ctx, "proposeExplorerPopulationMemberRemoval", err)
	return loomapi.NewProposeExplorerPopulationMemberRemovalErrorResponse(status, failure)
}

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
