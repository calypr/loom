package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func (r *HTTPRoutes) GetHealth(ctx context.Context, _ loomapi.GetHealthRequestObject) (loomapi.GetHealthResponseObject, error) {
	body, status := r.server.Health(ctx)
	value, err := rawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetHealth200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.GetHealth503JSONResponse(value), nil
	}
	return nil, unexpectedResponseStatus("getHealth", status)
}

func (r *HTTPRoutes) GetLiveness(ctx context.Context, _ loomapi.GetLivenessRequestObject) (loomapi.GetLivenessResponseObject, error) {
	body, status := r.server.Liveness(ctx)
	value, err := rawJSON(body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, unexpectedResponseStatus("getLiveness", status)
	}
	return loomapi.GetLiveness200JSONResponse(value), nil
}

func (r *HTTPRoutes) GetReadiness(ctx context.Context, _ loomapi.GetReadinessRequestObject) (loomapi.GetReadinessResponseObject, error) {
	body, status := r.server.Readiness(ctx)
	value, err := rawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetReadiness200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.GetReadiness503JSONResponse(value), nil
	}
	return nil, unexpectedResponseStatus("getReadiness", status)
}

func (r *HTTPRoutes) UploadProjectResource(ctx context.Context, request loomapi.UploadProjectResourceRequestObject) (loomapi.UploadProjectResourceResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.UploadProjectResource(ctx, request.Project, request.ResourceType, request.Body, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.UploadProjectResource200JSONResponse(value), nil
	}
	status, body := mapServiceError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.UploadProjectResource400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.UploadProjectResource401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.UploadProjectResource403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.UploadProjectResource415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(body)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.UploadProjectResource422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.UploadProjectResource500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.UploadProjectResource503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadProjectResource", status)
	}
}

func (r *HTTPRoutes) CreateDatasetGeneration(ctx context.Context, request loomapi.CreateDatasetGenerationRequestObject) (loomapi.CreateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.CreateDatasetGeneration(ctx, request.Project, request.Generation, request.Body, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.CreateDatasetGeneration200JSONResponse(value), nil
	}
	status, body := mapServiceError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateDatasetGeneration400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.CreateDatasetGeneration401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.CreateDatasetGeneration403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.CreateDatasetGeneration409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.CreateDatasetGeneration415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(body)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.CreateDatasetGeneration422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateDatasetGeneration500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.CreateDatasetGeneration503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("createDatasetGeneration", status)
	}
}

func (r *HTTPRoutes) ActivateDatasetGeneration(ctx context.Context, request loomapi.ActivateDatasetGenerationRequestObject) (loomapi.ActivateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.ActivateDatasetGeneration(ctx, request.Project, request.Generation, request.Params.DataframeExecutionId, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.ActivateDatasetGeneration200JSONResponse(value), nil
	}
	status, body := mapServiceError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ActivateDatasetGeneration400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(body)}, nil
	case http.StatusUnauthorized:
		return loomapi.ActivateDatasetGeneration401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(body)}, nil
	case http.StatusForbidden:
		return loomapi.ActivateDatasetGeneration403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(body)}, nil
	case http.StatusConflict:
		return loomapi.ActivateDatasetGeneration409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(body)}, nil
	case http.StatusInternalServerError:
		return loomapi.ActivateDatasetGeneration500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(body)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ActivateDatasetGeneration503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(body)}, nil
	default:
		return nil, unexpectedResponseStatus("activateDatasetGeneration", status)
	}
}

func (r *HTTPRoutes) CreateSnapshot(ctx context.Context, request loomapi.CreateSnapshotRequestObject) (loomapi.CreateSnapshotResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	body, err := marshalRequestBody(request.Body)
	if err == nil {
		result, operationErr := r.load.CreateSnapshot(ctx, request.Project, request.Generation, body, principal)
		if operationErr == nil {
			value, conversionErr := rawJSON(result)
			if conversionErr != nil {
				return nil, conversionErr
			}
			return loomapi.CreateSnapshot200JSONResponse(value), nil
		}
		err = operationErr
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateSnapshot400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.CreateSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.CreateSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.CreateSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.CreateSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("createSnapshot", status)
	}
}

func (r *HTTPRoutes) GetSnapshot(ctx context.Context, request loomapi.GetSnapshotRequestObject) (loomapi.GetSnapshotResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.SnapshotStatus(ctx, request.Project, request.Generation, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.GetSnapshot200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusUnauthorized:
		return loomapi.GetSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.GetSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.GetSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("getSnapshot", status)
	}
}

func (r *HTTPRoutes) AbortSnapshot(ctx context.Context, request loomapi.AbortSnapshotRequestObject) (loomapi.AbortSnapshotResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.AbortSnapshot(ctx, request.Project, request.Generation, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.AbortSnapshot200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusUnauthorized:
		return loomapi.AbortSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.AbortSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.AbortSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.AbortSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.AbortSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.AbortSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("abortSnapshot", status)
	}
}

func (r *HTTPRoutes) UploadSnapshotResource(ctx context.Context, request loomapi.UploadSnapshotResourceRequestObject) (loomapi.UploadSnapshotResourceResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.UploadSnapshotResource(ctx, request.Project, request.Generation, request.ResourceType, optionalString(request.Params.XContentSHA256), request.Body, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.UploadSnapshotResource200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.UploadSnapshotResource400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.UploadSnapshotResource401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.UploadSnapshotResource403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.UploadSnapshotResource404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.UploadSnapshotResource409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.UploadSnapshotResource422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.UploadSnapshotResource500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.UploadSnapshotResource503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadSnapshotResource", status)
	}
}

func (r *HTTPRoutes) FinalizeSnapshot(ctx context.Context, request loomapi.FinalizeSnapshotRequestObject) (loomapi.FinalizeSnapshotResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	generation, result, err := r.load.FinalizeSnapshot(ctx, request.Project, request.Generation, principal)
	if err == nil {
		value, conversionErr := rawJSON(map[string]any{"generation": generation, "load": result})
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.FinalizeSnapshot200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.FinalizeSnapshot400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.FinalizeSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.FinalizeSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.FinalizeSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.FinalizeSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.FinalizeSnapshot422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.FinalizeSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.FinalizeSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("finalizeSnapshot", status)
	}
}

func (r *HTTPRoutes) CreateRelease(ctx context.Context, request loomapi.CreateReleaseRequestObject) (loomapi.CreateReleaseResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	body, err := marshalRequestBody(request.Body)
	if err == nil {
		result, operationErr := r.load.CreateRelease(ctx, request.Project, body, principal)
		if operationErr == nil {
			value, conversionErr := rawJSON(result)
			if conversionErr != nil {
				return nil, conversionErr
			}
			return loomapi.CreateRelease200JSONResponse(value), nil
		}
		err = operationErr
	}
	return r.createReleaseError(err, ctx)
}

func (r *HTTPRoutes) createReleaseError(err error, ctx context.Context) (loomapi.CreateReleaseResponseObject, error) {
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateRelease400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.CreateRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.CreateRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.CreateRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.CreateRelease409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.CreateRelease422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.CreateRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("createRelease", status)
	}
}

func (r *HTTPRoutes) GetActiveRelease(ctx context.Context, request loomapi.GetActiveReleaseRequestObject) (loomapi.GetActiveReleaseResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.ActiveRelease(ctx, request.Project, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.GetActiveRelease200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusUnauthorized:
		return loomapi.GetActiveRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.GetActiveRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.GetActiveRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetActiveRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetActiveRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("getActiveRelease", status)
	}
}

func (r *HTTPRoutes) GetRelease(ctx context.Context, request loomapi.GetReleaseRequestObject) (loomapi.GetReleaseResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.ReleaseStatus(ctx, request.Project, request.Release, principal)
	if err == nil {
		value, conversionErr := rawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.GetRelease200JSONResponse(value), nil
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusUnauthorized:
		return loomapi.GetRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.GetRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.GetRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.GetRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.GetRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("getRelease", status)
	}
}

func (r *HTTPRoutes) ActivateRelease(ctx context.Context, request loomapi.ActivateReleaseRequestObject) (loomapi.ActivateReleaseResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	body, err := marshalRequestBody(request.Body)
	if err == nil {
		result, operationErr := r.load.ActivateRelease(ctx, request.Project, request.Release, body, principal)
		if operationErr == nil {
			value, conversionErr := rawJSON(result)
			if conversionErr != nil {
				return nil, conversionErr
			}
			return loomapi.ActivateRelease200JSONResponse(value), nil
		}
		err = operationErr
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ActivateRelease400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.ActivateRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.ActivateRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.ActivateRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.ActivateRelease409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ActivateRelease422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.ActivateRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ActivateRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("activateRelease", status)
	}
}

func (r *HTTPRoutes) ActivateReleaseCompatibility(ctx context.Context, request loomapi.ActivateReleaseCompatibilityRequestObject) (loomapi.ActivateReleaseCompatibilityResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	body, err := marshalRequestBody(request.Body)
	if err == nil {
		result, operationErr := r.load.ActivateReleaseCompatibility(ctx, request.Project, body, principal)
		if operationErr == nil {
			value, conversionErr := rawJSON(result)
			if conversionErr != nil {
				return nil, conversionErr
			}
			return loomapi.ActivateReleaseCompatibility200JSONResponse(value), nil
		}
		err = operationErr
	}
	status, mapped := snapshotError(err, ctx)
	switch status {
	case http.StatusBadRequest:
		return loomapi.ActivateReleaseCompatibility400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(mapped)}, nil
	case http.StatusUnauthorized:
		return loomapi.ActivateReleaseCompatibility401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(mapped)}, nil
	case http.StatusForbidden:
		return loomapi.ActivateReleaseCompatibility403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(mapped)}, nil
	case http.StatusNotFound:
		return loomapi.ActivateReleaseCompatibility404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(mapped)}, nil
	case http.StatusConflict:
		return loomapi.ActivateReleaseCompatibility409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(mapped)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.ActivateReleaseCompatibility422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(mapped)}, nil
	case http.StatusInternalServerError:
		return loomapi.ActivateReleaseCompatibility500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(mapped)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ActivateReleaseCompatibility503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(mapped)}, nil
	default:
		return nil, unexpectedResponseStatus("activateReleaseCompatibility", status)
	}
}

func (r *HTTPRoutes) GetRecipeExecution(ctx context.Context, request loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	body, status := r.recipe.Execute(ctx, request.Id)
	value, err := rawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetRecipeExecution200JSONResponse(value), nil
	}
	if status == http.StatusUnauthorized {
		return loomapi.GetRecipeExecution401JSONResponse(value), nil
	}
	if status == http.StatusForbidden {
		legacy, legacyErr := legacyErrorFromMap(value)
		if legacyErr != nil {
			return nil, legacyErr
		}
		return loomapi.GetRecipeExecution403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(legacy)}, nil
	}
	if status == http.StatusNotFound {
		legacy, legacyErr := legacyErrorFromMap(value)
		if legacyErr != nil {
			return nil, legacyErr
		}
		return loomapi.GetRecipeExecution404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(legacy)}, nil
	}
	return nil, unexpectedResponseStatus("getRecipeExecution", status)
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func marshalRequestBody(body any) ([]byte, error) {
	if body == nil {
		return nil, errors.New("request body is required")
	}
	return json.Marshal(body)
}

func mapServiceError(err error, ctx context.Context) (int, loomapi.ServiceErrorResponse) {
	mapped := httpapi.MapDataframeError(err, httpapi.RequestIDFromContext(ctx))
	return mapped.Status, serviceErrorResponse(mapped.Body)
}

func snapshotError(err error, ctx context.Context) (int, loomapi.ServiceErrorResponse) {
	status, body := loadapi.MapSnapshotError(err, httpapi.RequestIDFromContext(ctx))
	return status, serviceErrorResponse(body)
}

func rawJSON(value any) (loomapi.RawJSON, error) {
	if value == nil {
		return loomapi.RawJSON{}, nil
	}
	if raw, ok := value.(loomapi.RawJSON); ok {
		return raw, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result loomapi.RawJSON
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}
