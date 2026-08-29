package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
)

func (r *HTTPRoutes) GetHealth(ctx context.Context, _ loomapi.GetHealthRequestObject) (loomapi.GetHealthResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.server.HandleHealth)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetHealth200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusServiceUnavailable {
		response, decodeErr := decodeResponse[loomapi.GetHealth503JSONResponse](body)
		return response, decodeErr
	}
	return nil, unexpectedResponseStatus("getHealth", status)
}

func (r *HTTPRoutes) GetLiveness(ctx context.Context, _ loomapi.GetLivenessRequestObject) (loomapi.GetLivenessResponseObject, error) {
	_, body, err := runFiberHandler(ctx, r.server.HandleLiveness)
	if err != nil {
		return nil, err
	}
	response, decodeErr := decodeResponse[loomapi.GetLiveness200JSONResponse](body)
	return response, decodeErr
}

func (r *HTTPRoutes) GetReadiness(ctx context.Context, _ loomapi.GetReadinessRequestObject) (loomapi.GetReadinessResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.server.HandleReadiness)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetReadiness200JSONResponse](body)
		return response, decodeErr
	}
	response, decodeErr := decodeResponse[loomapi.GetReadiness503JSONResponse](body)
	return response, decodeErr
}

func (r *HTTPRoutes) UploadProjectResource(ctx context.Context, _ loomapi.UploadProjectResourceRequestObject) (loomapi.UploadProjectResourceResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleResource)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.UploadProjectResource200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case http.StatusBadRequest:
		return loomapi.UploadProjectResource400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case http.StatusUnauthorized:
		return loomapi.UploadProjectResource401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case http.StatusForbidden:
		return loomapi.UploadProjectResource403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.UploadProjectResource415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(value)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.UploadProjectResource422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case http.StatusInternalServerError:
		return loomapi.UploadProjectResource500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.UploadProjectResource503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadProjectResource", status)
	}
}

func (r *HTTPRoutes) CreateDatasetGeneration(ctx context.Context, _ loomapi.CreateDatasetGenerationRequestObject) (loomapi.CreateDatasetGenerationResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleCreateGeneration)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.CreateDatasetGeneration200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case http.StatusBadRequest:
		return loomapi.CreateDatasetGeneration400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case http.StatusUnauthorized:
		return loomapi.CreateDatasetGeneration401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case http.StatusForbidden:
		return loomapi.CreateDatasetGeneration403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case http.StatusConflict:
		return loomapi.CreateDatasetGeneration409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.CreateDatasetGeneration415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(value)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.CreateDatasetGeneration422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case http.StatusInternalServerError:
		return loomapi.CreateDatasetGeneration500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.CreateDatasetGeneration503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("createDatasetGeneration", status)
	}
}

func (r *HTTPRoutes) ActivateDatasetGeneration(ctx context.Context, _ loomapi.ActivateDatasetGenerationRequestObject) (loomapi.ActivateDatasetGenerationResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleActivateGeneration)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ActivateDatasetGeneration200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case http.StatusBadRequest:
		return loomapi.ActivateDatasetGeneration400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case http.StatusUnauthorized:
		return loomapi.ActivateDatasetGeneration401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case http.StatusForbidden:
		return loomapi.ActivateDatasetGeneration403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case http.StatusConflict:
		return loomapi.ActivateDatasetGeneration409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case http.StatusInternalServerError:
		return loomapi.ActivateDatasetGeneration500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.ActivateDatasetGeneration503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("activateDatasetGeneration", status)
	}
}

func (r *HTTPRoutes) CreateSnapshot(ctx context.Context, _ loomapi.CreateSnapshotRequestObject) (loomapi.CreateSnapshotResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleCreateSnapshot)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.CreateSnapshot200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.CreateSnapshot400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.CreateSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.CreateSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.CreateSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.CreateSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.CreateSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("createSnapshot", status)
	}
}

func (r *HTTPRoutes) GetSnapshot(ctx context.Context, _ loomapi.GetSnapshotRequestObject) (loomapi.GetSnapshotResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleSnapshotStatus)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetSnapshot200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 401:
		return loomapi.GetSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.GetSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getSnapshot", status)
	}
}

func (r *HTTPRoutes) AbortSnapshot(ctx context.Context, _ loomapi.AbortSnapshotRequestObject) (loomapi.AbortSnapshotResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleAbortSnapshot)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.AbortSnapshot200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 401:
		return loomapi.AbortSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.AbortSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.AbortSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.AbortSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.AbortSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.AbortSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("abortSnapshot", status)
	}
}

func (r *HTTPRoutes) UploadSnapshotResource(ctx context.Context, _ loomapi.UploadSnapshotResourceRequestObject) (loomapi.UploadSnapshotResourceResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleUploadSnapshotResource)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.UploadSnapshotResource200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.UploadSnapshotResource400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.UploadSnapshotResource401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.UploadSnapshotResource403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.UploadSnapshotResource404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.UploadSnapshotResource409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.UploadSnapshotResource422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.UploadSnapshotResource500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.UploadSnapshotResource503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadSnapshotResource", status)
	}
}

func (r *HTTPRoutes) FinalizeSnapshot(ctx context.Context, _ loomapi.FinalizeSnapshotRequestObject) (loomapi.FinalizeSnapshotResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleFinalizeSnapshot)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.FinalizeSnapshot200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.FinalizeSnapshot400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.FinalizeSnapshot401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.FinalizeSnapshot403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.FinalizeSnapshot404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.FinalizeSnapshot409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.FinalizeSnapshot422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.FinalizeSnapshot500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.FinalizeSnapshot503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("finalizeSnapshot", status)
	}
}

func (r *HTTPRoutes) CreateRelease(ctx context.Context, _ loomapi.CreateReleaseRequestObject) (loomapi.CreateReleaseResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleCreateRelease)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.CreateRelease200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.CreateRelease400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.CreateRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.CreateRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.CreateRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.CreateRelease409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.CreateRelease422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.CreateRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.CreateRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("createRelease", status)
	}
}

func (r *HTTPRoutes) GetActiveRelease(ctx context.Context, _ loomapi.GetActiveReleaseRequestObject) (loomapi.GetActiveReleaseResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleActiveRelease)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetActiveRelease200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 401:
		return loomapi.GetActiveRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.GetActiveRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetActiveRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetActiveRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetActiveRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getActiveRelease", status)
	}
}

func (r *HTTPRoutes) GetRelease(ctx context.Context, _ loomapi.GetReleaseRequestObject) (loomapi.GetReleaseResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleReleaseStatus)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetRelease200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 401:
		return loomapi.GetRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.GetRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getRelease", status)
	}
}

func (r *HTTPRoutes) ActivateRelease(ctx context.Context, _ loomapi.ActivateReleaseRequestObject) (loomapi.ActivateReleaseResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleActivateRelease)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ActivateRelease200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ActivateRelease400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.ActivateRelease401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.ActivateRelease403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.ActivateRelease404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.ActivateRelease409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.ActivateRelease422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ActivateRelease500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.ActivateRelease503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("activateRelease", status)
	}
}

func (r *HTTPRoutes) ActivateReleaseCompatibility(ctx context.Context, _ loomapi.ActivateReleaseCompatibilityRequestObject) (loomapi.ActivateReleaseCompatibilityResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleActivateReleaseCompatibility)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ActivateReleaseCompatibility200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ActivateReleaseCompatibility400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case 401:
		return loomapi.ActivateReleaseCompatibility401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case 403:
		return loomapi.ActivateReleaseCompatibility403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.ActivateReleaseCompatibility404JSONResponse{ServiceNotFoundJSONResponse: loomapi.ServiceNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.ActivateReleaseCompatibility409JSONResponse{ServiceConflictJSONResponse: loomapi.ServiceConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.ActivateReleaseCompatibility422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ActivateReleaseCompatibility500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.ActivateReleaseCompatibility503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("activateReleaseCompatibility", status)
	}
}

func (r *HTTPRoutes) GetRecipeExecution(ctx context.Context, _ loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.recipe.Handle)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetRecipeExecution200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusUnauthorized {
		response, decodeErr := decodeResponse[loomapi.GetRecipeExecution401JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.GetRecipeExecution403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetRecipeExecution404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getRecipeExecution", status)
	}
}
