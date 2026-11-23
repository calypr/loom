package explorerapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/calypr/loom/internal/explorer/viewer"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	"github.com/calypr/loom/internal/projectid"
)

func (h *Handlers) authorizeViewBuilder(ctx context.Context, project, resource string, write bool) error {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}
	if h == nil || h.views == nil || h.authorizeRead == nil || h.authorizer == nil {
		return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "View editing is unavailable", nil)
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
			return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
		}
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot read this project", err)
	}
	if write {
		if err := h.authorizer.AuthorizeWrite(ctx, principal, project, resource); err != nil {
			if authorizerResponseStatus(err) == http.StatusServiceUnavailable {
				return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
			}
			return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot edit views in this project", err)
		}
	}
	return nil
}

func (h *Handlers) authorizedViewBuilderState(ctx context.Context, project, resource string) (viewer.ViewBuilderState, error) {
	value, err := h.views.ViewBuilderState(ctx, project)
	if err != nil {
		return value, viewBuilderFailure(err)
	}
	return h.checkViewBuilderState(ctx, project, resource, value)
}

func (h *Handlers) viewMutationAccess(project, resource string) projectconfig.MutationAccess {
	return projectconfig.MutationAccess{
		CheckState: func(ctx context.Context, value viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
			return h.checkViewBuilderState(ctx, project, resource, value)
		},
		CheckExecution: h.authorizeViewBuilderExecution,
		StorageError:   viewBuilderFailure,
	}
}

func (h *Handlers) checkViewBuilderState(ctx context.Context, project, resource string, value viewer.ViewBuilderState) (viewer.ViewBuilderState, error) {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}

	executionIDs := make(map[string]bool)
	for _, config := range value.Configs {
		if config.ExecutionID != "" {
			executionIDs[config.ExecutionID] = true
		}
	}
	for _, catalog := range value.Catalogs {
		executionIDs[catalog.ExecutionID] = true
	}
	for id := range executionIDs {
		if err := h.authorizeViewBuilderExecution(ctx, id); err != nil {
			return viewer.ViewBuilderState{}, err
		}
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	err := h.authorizer.AuthorizeWrite(ctx, principal, project, resource)
	if err != nil && authorizerResponseStatus(err) == http.StatusServiceUnavailable {
		return viewer.ViewBuilderState{}, legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
	}
	value.CanEdit = err == nil
	return value, nil
}

func (h *Handlers) authorizeViewBuilderExecution(ctx context.Context, id string) error {
	if h.authorizeExecution == nil {
		return nil
	}
	err := h.authorizeExecution(ctx, id)
	if err == nil {
		return nil
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if ok && dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
		return legacyOperationFailure(http.StatusNotFound, "VIEW_NOT_FOUND", "Published data is unavailable in this scope", err)
	}
	status, _ := httpapi.MapServiceError(err, ctx)
	if status == http.StatusForbidden {
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "Published data is unavailable in this scope", err)
	}
	return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Published data authorization is temporarily unavailable", err)
}

func (h *Handlers) GetProjectViewBuilder(ctx context.Context, request loomapi.GetProjectViewBuilderRequestObject) (loomapi.GetProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.GetProjectViewBuilderResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewGetProjectViewBuilderErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, false); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.GetProjectViewBuilder200JSONResponse(result), nil
}

func (h *Handlers) AdoptProjectViewBuilder(ctx context.Context, request loomapi.AdoptProjectViewBuilderRequestObject) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
		status, failure := explorerErrorResponse(err)
		return loomapi.NewAdoptProjectViewBuilderErrorResponse(status, failure)
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.views.AdoptCatalogs(ctx, project, h.viewMutationAccess(project, resource))
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.AdoptProjectViewBuilder200JSONResponse(result), nil
}
