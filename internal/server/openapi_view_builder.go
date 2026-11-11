package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

// View writes share the project authorizer with the dataframe Builder, but
// persist presentation only. No compilation or materialization adapter is used.
func (h *explorerHTTPHandlers) authorizeViewBuilder(ctx context.Context, project, resource string, write bool) error {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}
	if h == nil || h.views == nil || h.authorizeRead == nil || h.authorizer == nil {
		return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "View editing is unavailable", nil)
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := h.authorizeRead(ctx, principal, project); err != nil {
		if isAuthorizerStorageFault(err) {
			return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
		}
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot read this project", err)
	}
	if write {
		if err := h.authorizer.AuthorizeWrite(ctx, principal, project, resource); err != nil {
			if isAuthorizerStorageFault(err) {
				return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
			}
			return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "You cannot edit views in this project", err)
		}
	}
	return nil
}

func (h *explorerHTTPHandlers) authorizedViewBuilderState(ctx context.Context, project, resource string) (explorer.ViewBuilderState, error) {
	if strings.TrimSpace(resource) == "" {
		resource = projectid.Legacy(project)
	}
	value, err := h.views.ViewBuilderState(ctx, project)
	if err != nil {
		return value, viewBuilderFailure(err)
	}
	// Catalog access retains the exact generation-scoped execution checks used
	// by the viewer. Project membership alone must not disclose another scope.
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
			return explorer.ViewBuilderState{}, err
		}
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	err = h.authorizer.AuthorizeWrite(ctx, principal, project, resource)
	if err != nil && isAuthorizerStorageFault(err) {
		return explorer.ViewBuilderState{}, legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Project authorization is temporarily unavailable", err)
	}
	value.CanEdit = err == nil
	return value, nil
}

func (h *explorerHTTPHandlers) authorizeViewBuilderExecution(ctx context.Context, id string) error {
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
	status, _ := mapServiceError(err, ctx)
	if status == http.StatusForbidden {
		return legacyOperationFailure(http.StatusForbidden, "FORBIDDEN", "Published data is unavailable in this scope", err)
	}
	return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "Published data authorization is temporarily unavailable", err)
}

func viewBuilderFailure(err error) error {
	switch {
	case errors.Is(err, explorer.ErrDraftConflict):
		return legacyOperationFailure(http.StatusConflict, "VIEW_CONFLICT", "This view or its published data changed. Reload before saving; your unsaved edits have been kept.", err)
	case errors.Is(err, explorer.ErrNotFound):
		return legacyOperationFailure(http.StatusNotFound, "VIEW_NOT_FOUND", "The saved view or published dataframe was not found", err)
	case errors.Is(err, explorer.ErrInvalidViewBuilder):
		return legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_VIEW", err.Error(), err)
	default:
		return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "View storage is temporarily unavailable; retry the request", err)
	}
}

func decodeViewBuilderBody(body any, target any) error {
	if body == nil {
		return legacyOperationFailure(http.StatusBadRequest, "INVALID_REQUEST", "A view request body is required", nil)
	}
	raw, err := json.Marshal(body)
	if err == nil {
		err = decodeStrict(raw, target)
	}
	if err != nil {
		return legacyOperationFailure(http.StatusBadRequest, "INVALID_REQUEST", "The view request contains invalid or unknown fields", err)
	}
	return nil
}

func viewBuilderJSON(value explorer.ViewBuilderState) (loomapi.RawJSON, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result loomapi.RawJSON
	err = json.Unmarshal(raw, &result)
	return result, err
}

func (r *HTTPRoutes) GetProjectViewBuilder(ctx context.Context, request loomapi.GetProjectViewBuilderRequestObject) (loomapi.GetProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.GetProjectViewBuilderResponseObject, error) {
		status, legacy := explorerLegacyResponse(err)
		switch status {
		case http.StatusBadRequest:
			return loomapi.GetProjectViewBuilder400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
		case http.StatusForbidden:
			return loomapi.GetProjectViewBuilder403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
		case http.StatusNotFound:
			return loomapi.GetProjectViewBuilder404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
		case http.StatusConflict:
			return loomapi.GetProjectViewBuilder409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
		case http.StatusUnprocessableEntity:
			return loomapi.GetProjectViewBuilder422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
		case http.StatusInternalServerError:
			return loomapi.GetProjectViewBuilder500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
		case http.StatusServiceUnavailable:
			return loomapi.GetProjectViewBuilder503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
		default:
			return nil, unexpectedResponseStatus("GetProjectViewBuilder", status)
		}
	}
	project := projectid.Canonical(string(request.Project))
	resource := ""
	h := r.explorer
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

func (r *HTTPRoutes) AdoptProjectViewBuilder(ctx context.Context, request loomapi.AdoptProjectViewBuilderRequestObject) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
	fail := func(err error) (loomapi.AdoptProjectViewBuilderResponseObject, error) {
		status, legacy := explorerLegacyResponse(err)
		switch status {
		case http.StatusBadRequest:
			return loomapi.AdoptProjectViewBuilder400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
		case http.StatusForbidden:
			return loomapi.AdoptProjectViewBuilder403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
		case http.StatusNotFound:
			return loomapi.AdoptProjectViewBuilder404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
		case http.StatusConflict:
			return loomapi.AdoptProjectViewBuilder409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
		case http.StatusUnprocessableEntity:
			return loomapi.AdoptProjectViewBuilder422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
		case http.StatusInternalServerError:
			return loomapi.AdoptProjectViewBuilder500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
		case http.StatusServiceUnavailable:
			return loomapi.AdoptProjectViewBuilder503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
		default:
			return nil, unexpectedResponseStatus("AdoptProjectViewBuilder", status)
		}
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	h := r.explorer
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	// Existing projects can explicitly attach retained publication metadata.
	// Validate every adopted execution before writing its durable catalog anchor.
	owners, err := h.views.List(ctx, project)
	if err != nil {
		return fail(viewBuilderFailure(err))
	}
	for _, owner := range owners {
		if owner.ProjectConfig == nil || owner.ActiveRevisionID == "" {
			continue
		}
		revision, err := h.views.GetRevision(ctx, owner.ActiveRevisionID)
		if err != nil {
			return fail(viewBuilderFailure(err))
		}
		if err := h.authorizeViewBuilderExecution(ctx, revision.Publication.ExecutionID); err != nil {
			return fail(err)
		}
	}
	_, err = h.views.AdoptViewBuilderCatalogs(ctx, project)
	if err != nil {
		return fail(viewBuilderFailure(err))
	}
	value, err = h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.AdoptProjectViewBuilder200JSONResponse(result), nil
}

func (r *HTTPRoutes) SaveProjectViewDefaults(ctx context.Context, request loomapi.SaveProjectViewDefaultsRequestObject) (loomapi.SaveProjectViewDefaultsResponseObject, error) {
	fail := func(err error) (loomapi.SaveProjectViewDefaultsResponseObject, error) {
		status, legacy := explorerLegacyResponse(err)
		switch status {
		case http.StatusBadRequest:
			return loomapi.SaveProjectViewDefaults400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
		case http.StatusForbidden:
			return loomapi.SaveProjectViewDefaults403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
		case http.StatusNotFound:
			return loomapi.SaveProjectViewDefaults404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
		case http.StatusConflict:
			return loomapi.SaveProjectViewDefaults409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
		case http.StatusUnprocessableEntity:
			return loomapi.SaveProjectViewDefaults422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
		case http.StatusInternalServerError:
			return loomapi.SaveProjectViewDefaults500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
		case http.StatusServiceUnavailable:
			return loomapi.SaveProjectViewDefaults503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
		default:
			return nil, unexpectedResponseStatus("SaveProjectViewDefaults", status)
		}
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	h := r.explorer
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	var body explorer.ViewBuilderDefaultsRequest
	if err = decodeViewBuilderBody(request.Body, &body); err != nil {
		return fail(err)
	}
	_, err = h.views.SaveViewBuilderDefaults(ctx, project, body)
	if err != nil {
		return fail(viewBuilderFailure(err))
	}
	value, err = h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.SaveProjectViewDefaults200JSONResponse(result), nil
}

func (r *HTTPRoutes) SaveProjectViewConfig(ctx context.Context, request loomapi.SaveProjectViewConfigRequestObject) (loomapi.SaveProjectViewConfigResponseObject, error) {
	fail := func(err error) (loomapi.SaveProjectViewConfigResponseObject, error) {
		status, legacy := explorerLegacyResponse(err)
		switch status {
		case http.StatusBadRequest:
			return loomapi.SaveProjectViewConfig400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
		case http.StatusForbidden:
			return loomapi.SaveProjectViewConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
		case http.StatusNotFound:
			return loomapi.SaveProjectViewConfig404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
		case http.StatusConflict:
			return loomapi.SaveProjectViewConfig409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
		case http.StatusUnprocessableEntity:
			return loomapi.SaveProjectViewConfig422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
		case http.StatusInternalServerError:
			return loomapi.SaveProjectViewConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
		case http.StatusServiceUnavailable:
			return loomapi.SaveProjectViewConfig503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
		default:
			return nil, unexpectedResponseStatus("SaveProjectViewConfig", status)
		}
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	h := r.explorer
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	var body explorer.ViewBuilderSaveRequest
	if err = decodeViewBuilderBody(request.Body, &body); err != nil {
		return fail(err)
	}
	_, err = h.views.SaveViewBuilderConfig(ctx, project, string(request.ExplorerId), body, subjectFromContext(ctx))
	if err != nil {
		return fail(viewBuilderFailure(err))
	}
	value, err = h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.SaveProjectViewConfig200JSONResponse(result), nil
}

func (r *HTTPRoutes) DeleteProjectViewConfig(ctx context.Context, request loomapi.DeleteProjectViewConfigRequestObject) (loomapi.DeleteProjectViewConfigResponseObject, error) {
	fail := func(err error) (loomapi.DeleteProjectViewConfigResponseObject, error) {
		status, legacy := explorerLegacyResponse(err)
		switch status {
		case http.StatusBadRequest:
			return loomapi.DeleteProjectViewConfig400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(legacy)}, nil
		case http.StatusForbidden:
			return loomapi.DeleteProjectViewConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
		case http.StatusNotFound:
			return loomapi.DeleteProjectViewConfig404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
		case http.StatusConflict:
			return loomapi.DeleteProjectViewConfig409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(legacy)}, nil
		case http.StatusUnprocessableEntity:
			return loomapi.DeleteProjectViewConfig422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(legacy)}, nil
		case http.StatusInternalServerError:
			return loomapi.DeleteProjectViewConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(legacy)}, nil
		case http.StatusServiceUnavailable:
			return loomapi.DeleteProjectViewConfig503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(legacy)}, nil
		default:
			return nil, unexpectedResponseStatus("DeleteProjectViewConfig", status)
		}
	}
	project := projectid.Canonical(string(request.Project))
	resource := authResourcePathFromParam(request.Params.AuthResourcePath)
	h := r.explorer
	if err := h.authorizeViewBuilder(ctx, project, resource, true); err != nil {
		return fail(err)
	}
	value, err := h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	_, err = h.views.DeleteViewBuilderConfig(ctx, project, string(request.ExplorerId), request.Params.ExpectedRevisionId)
	if err != nil {
		return fail(viewBuilderFailure(err))
	}
	value, err = h.authorizedViewBuilderState(ctx, project, resource)
	if err != nil {
		return fail(err)
	}
	result, err := viewBuilderJSON(value)
	if err != nil {
		return fail(err)
	}
	return loomapi.DeleteProjectViewConfig200JSONResponse(result), nil
}
