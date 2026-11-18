package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	explorer "github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

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
			groups := make([]lifecycle.ExplicitGroupInput, len(request.Body.Groups))
			for i, group := range request.Body.Groups {
				groups[i] = lifecycle.ExplicitGroupInput{ID: group.Id, Label: group.Label, Ordinal: group.Ordinal, MemberIDs: group.MemberIds}
			}
			value, err := h.application.CreateExplicitGroupRevision(ctx, lifecycle.ExplicitGroupCreateRequest{
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
