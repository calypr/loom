package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
)

type strictFiberContextKey struct{}

type HTTPRoutes struct {
	server   *httpapi.HTTPServer
	load     *loadapi.Handler
	recipe   httpapi.RecipeExecutionHandler
	graphql  graphapi.RouteConfig
	explorer *explorerHTTPHandlers
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)

func strictFiberContextMiddleware(next loomapi.StrictHandlerFunc, _ string) loomapi.StrictHandlerFunc {
	return func(c fiber.Ctx, args any) (any, error) {
		c.SetContext(context.WithValue(c.Context(), strictFiberContextKey{}, c))
		return next(c, args)
	}
}

func fiberContext(ctx context.Context) (fiber.Ctx, error) {
	c, ok := ctx.Value(strictFiberContextKey{}).(fiber.Ctx)
	if !ok || c == nil {
		return nil, fmt.Errorf("generated route is missing Fiber request context")
	}
	return c, nil
}

func runFiberHandler(ctx context.Context, handler fiber.Handler) (int, []byte, error) {
	c, err := fiberContext(ctx)
	if err != nil {
		return 0, nil, err
	}
	if handler == nil {
		return http.StatusNotFound, []byte(`{"error":"not found"}`), nil
	}
	if err := handler(c); err != nil {
		return 0, nil, err
	}
	status := c.Response().StatusCode()
	body := append([]byte(nil), c.Response().Body()...)
	c.Response().ResetBody()
	return status, body, nil
}

func decodeResponse[T any](body []byte) (T, error) {
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		return value, fmt.Errorf("decode generated response: %w", err)
	}
	return value, nil
}

func authoringError(body []byte, status int) (loomapi.ErrorResponse, int, error) {
	value, err := decodeResponse[loomapi.ErrorResponse](body)
	return value, status, err
}

func serviceError(body []byte) (loomapi.ServiceErrorResponse, error) {
	return decodeResponse[loomapi.ServiceErrorResponse](body)
}

func legacyError(body []byte) (loomapi.LegacyErrorResponse, error) {
	return decodeResponse[loomapi.LegacyErrorResponse](body)
}

func unexpectedResponseStatus(operation string, status int) error {
	return fmt.Errorf("%s returned undocumented HTTP status %d", operation, status)
}

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

func (r *HTTPRoutes) UploadRawNDJSON(ctx context.Context, _ loomapi.UploadRawNDJSONRequestObject) (loomapi.UploadRawNDJSONResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.load.HandleLoadRaw)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.UploadRawNDJSON200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := serviceError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case http.StatusBadRequest:
		return loomapi.UploadRawNDJSON400JSONResponse{ServiceBadRequestJSONResponse: loomapi.ServiceBadRequestJSONResponse(value)}, nil
	case http.StatusUnauthorized:
		return loomapi.UploadRawNDJSON401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, nil
	case http.StatusForbidden:
		return loomapi.UploadRawNDJSON403JSONResponse{ServiceForbiddenJSONResponse: loomapi.ServiceForbiddenJSONResponse(value)}, nil
	case http.StatusUnsupportedMediaType:
		return loomapi.UploadRawNDJSON415JSONResponse{ServiceUnsupportedMediaTypeJSONResponse: loomapi.ServiceUnsupportedMediaTypeJSONResponse(value)}, nil
	case http.StatusUnprocessableEntity:
		return loomapi.UploadRawNDJSON422JSONResponse{ServiceUnprocessableJSONResponse: loomapi.ServiceUnprocessableJSONResponse(value)}, nil
	case http.StatusInternalServerError:
		return loomapi.UploadRawNDJSON500JSONResponse{ServiceInternalErrorJSONResponse: loomapi.ServiceInternalErrorJSONResponse(value)}, nil
	case http.StatusServiceUnavailable:
		return loomapi.UploadRawNDJSON503JSONResponse{ServiceUnavailableJSONResponse: loomapi.ServiceUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("uploadRawNDJSON", status)
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

func (r *HTTPRoutes) runGraphQL(ctx context.Context, handler http.Handler) (int, []byte, error) {
	if handler == nil {
		return http.StatusNotFound, []byte(`{"error":"not found"}`), nil
	}
	return runFiberHandler(ctx, fiberadaptor.HTTPHandlerWithContext(handler))
}

func (r *HTTPRoutes) ExecuteGraphQL(ctx context.Context, _ loomapi.ExecuteGraphQLRequestObject) (loomapi.ExecuteGraphQLResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Handler)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ExecuteGraphQL200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusUnauthorized {
		value, decodeErr := serviceError(body)
		return loomapi.ExecuteGraphQL401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, decodeErr
	}
	value, decodeErr := decodeResponse[loomapi.RawJSON](body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ExecuteGraphQL400JSONResponse{GraphQLBadRequestJSONResponse: loomapi.GraphQLBadRequestJSONResponse(value)}, nil
	case 422:
		return loomapi.ExecuteGraphQL422JSONResponse{GraphQLUnprocessableJSONResponse: loomapi.GraphQLUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ExecuteGraphQL500JSONResponse{GraphQLInternalErrorJSONResponse: loomapi.GraphQLInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("executeGraphQL", status)
	}
}

func (r *HTTPRoutes) ExecuteDataframeGraphQL(ctx context.Context, _ loomapi.ExecuteDataframeGraphQLRequestObject) (loomapi.ExecuteDataframeGraphQLResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Handler)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ExecuteDataframeGraphQL200JSONResponse](body)
		return response, decodeErr
	}
	if status == http.StatusUnauthorized {
		value, decodeErr := serviceError(body)
		return loomapi.ExecuteDataframeGraphQL401JSONResponse{ServiceUnauthorizedJSONResponse: loomapi.ServiceUnauthorizedJSONResponse(value)}, decodeErr
	}
	value, decodeErr := decodeResponse[loomapi.RawJSON](body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.ExecuteDataframeGraphQL400JSONResponse{GraphQLBadRequestJSONResponse: loomapi.GraphQLBadRequestJSONResponse(value)}, nil
	case 422:
		return loomapi.ExecuteDataframeGraphQL422JSONResponse{GraphQLUnprocessableJSONResponse: loomapi.GraphQLUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ExecuteDataframeGraphQL500JSONResponse{GraphQLInternalErrorJSONResponse: loomapi.GraphQLInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("executeDataframeGraphQL", status)
	}
}

func (r *HTTPRoutes) GetGraphQLPlayground(ctx context.Context, _ loomapi.GetGraphQLPlaygroundRequestObject) (loomapi.GetGraphQLPlaygroundResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Playground)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetGraphQLPlayground200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}

func (r *HTTPRoutes) GetApolloSandbox(ctx context.Context, _ loomapi.GetApolloSandboxRequestObject) (loomapi.GetApolloSandboxResponseObject, error) {
	status, body, err := r.runGraphQL(ctx, r.graphql.Sandbox)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fiber.ErrNotFound
	}
	return loomapi.GetApolloSandbox200TexthtmlResponse{HTMLTexthtmlResponse: loomapi.HTMLTexthtmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}}, nil
}

func (r *HTTPRoutes) ListExplorers(ctx context.Context, _ loomapi.ListExplorersRequestObject) (loomapi.ListExplorersResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.list)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.ListExplorers200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.ListExplorers403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 500:
		return loomapi.ListExplorers500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("listExplorers", status)
	}
}

func (r *HTTPRoutes) CreateExplorer(ctx context.Context, _ loomapi.CreateExplorerRequestObject) (loomapi.CreateExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.create)
	if err != nil {
		return nil, err
	}
	if status == http.StatusCreated {
		response, decodeErr := decodeResponse[loomapi.CreateExplorer201JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.CreateExplorer400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.CreateExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.CreateExplorer409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.CreateExplorer422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.CreateExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("createExplorer", status)
	}
}

func (r *HTTPRoutes) GetExplorer(ctx context.Context, _ loomapi.GetExplorerRequestObject) (loomapi.GetExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.lifecycle.get)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetExplorer200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.GetExplorer403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorer404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorer500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorer", status)
	}
}

func (r *HTTPRoutes) PublishRepositoryExplorerConfig(ctx context.Context, _ loomapi.PublishRepositoryExplorerConfigRequestObject) (loomapi.PublishRepositoryExplorerConfigResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.publishRepositoryConfig)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.PublishRepositoryExplorerConfig200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 400:
		return loomapi.PublishRepositoryExplorerConfig400JSONResponse{LegacyBadRequestJSONResponse: loomapi.LegacyBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PublishRepositoryExplorerConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.PublishRepositoryExplorerConfig409JSONResponse{LegacyConflictJSONResponse: loomapi.LegacyConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.PublishRepositoryExplorerConfig422JSONResponse{LegacyUnprocessableJSONResponse: loomapi.LegacyUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.PublishRepositoryExplorerConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PublishRepositoryExplorerConfig503JSONResponse{LegacyUnavailableJSONResponse: loomapi.LegacyUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("publishRepositoryExplorerConfig", status)
	}
}

func (r *HTTPRoutes) GetRepositoryExplorerConfig(ctx context.Context, _ loomapi.GetRepositoryExplorerConfigRequestObject) (loomapi.GetRepositoryExplorerConfigResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.getRepositoryConfig)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		response, decodeErr := decodeResponse[loomapi.GetRepositoryExplorerConfig200JSONResponse](body)
		return response, decodeErr
	}
	value, decodeErr := legacyError(body)
	if decodeErr != nil {
		return nil, decodeErr
	}
	switch status {
	case 403:
		return loomapi.GetRepositoryExplorerConfig403JSONResponse{LegacyForbiddenJSONResponse: loomapi.LegacyForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetRepositoryExplorerConfig404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(value)}, nil
	case 500:
		return loomapi.GetRepositoryExplorerConfig500JSONResponse{LegacyInternalErrorJSONResponse: loomapi.LegacyInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getRepositoryExplorerConfig", status)
	}
}

func (r *HTTPRoutes) GetExplorerAuthoringCapability(ctx context.Context, _ loomapi.GetExplorerAuthoringCapabilityRequestObject) (loomapi.GetExplorerAuthoringCapabilityResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getCapability)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.AuthoringCapability](body)
		return loomapi.GetExplorerAuthoringCapability200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.GetExplorerAuthoringCapability403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerAuthoringCapability500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerAuthoringCapability", status)
	}
}

func (r *HTTPRoutes) SearchExplorerCandidates(ctx context.Context, _ loomapi.SearchExplorerCandidatesRequestObject) (loomapi.SearchExplorerCandidatesResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.searchSuggestions)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CandidateSearchResponse](body)
		return loomapi.SearchExplorerCandidates200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.SearchExplorerCandidates400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.SearchExplorerCandidates403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.SearchExplorerCandidates409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.SearchExplorerCandidates500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.SearchExplorerCandidates503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("searchExplorerCandidates", status)
	}
}

func (r *HTTPRoutes) GetExplorerCandidateSuggestions(ctx context.Context, _ loomapi.GetExplorerCandidateSuggestionsRequestObject) (loomapi.GetExplorerCandidateSuggestionsResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getCandidateSuggestions)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CandidateSuggestions](body)
		return loomapi.GetExplorerCandidateSuggestions200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.GetExplorerCandidateSuggestions400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.GetExplorerCandidateSuggestions403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorerCandidateSuggestions404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.GetExplorerCandidateSuggestions409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerCandidateSuggestions500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetExplorerCandidateSuggestions503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerCandidateSuggestions", status)
	}
}

func (r *HTTPRoutes) GetExplorerBuilder(ctx context.Context, _ loomapi.GetExplorerBuilderRequestObject) (loomapi.GetExplorerBuilderResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.getBuilder)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.BuilderState](body)
		return loomapi.GetExplorerBuilder200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.GetExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.GetExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.GetExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.GetExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.GetExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("getExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) SaveExplorerDraft(ctx context.Context, _ loomapi.SaveExplorerDraftRequestObject) (loomapi.SaveExplorerDraftResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.saveDraft)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.SaveDraftResponse](body)
		return loomapi.SaveExplorerDraft200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.SaveExplorerDraft400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.SaveExplorerDraft403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.SaveExplorerDraft404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.SaveExplorerDraft409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.SaveExplorerDraft422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.SaveExplorerDraft500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.SaveExplorerDraft503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("saveExplorerDraft", status)
	}
}

func (r *HTTPRoutes) ExportExplorerWorkspace(ctx context.Context, _ loomapi.ExportExplorerWorkspaceRequestObject) (loomapi.ExportExplorerWorkspaceResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.exportWorkspace)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.Workspace](body)
		return loomapi.ExportExplorerWorkspace200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 403:
		return loomapi.ExportExplorerWorkspace403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.ExportExplorerWorkspace404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.ExportExplorerWorkspace409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 500:
		return loomapi.ExportExplorerWorkspace500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("exportExplorerWorkspace", status)
	}
}

func (r *HTTPRoutes) ApplyExplorerBuilderCommands(ctx context.Context, _ loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.applyCommands)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.ApplyCommandsResponse](body)
		return loomapi.ApplyExplorerBuilderCommands200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.ApplyExplorerBuilderCommands400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.ApplyExplorerBuilderCommands403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 409:
		return loomapi.ApplyExplorerBuilderCommands409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.ApplyExplorerBuilderCommands422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.ApplyExplorerBuilderCommands500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.ApplyExplorerBuilderCommands503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("applyExplorerBuilderCommands", status)
	}
}

func (r *HTTPRoutes) compileExplorer(ctx context.Context, handler fiber.Handler) (loomapi.CompileResponse, loomapi.ErrorResponse, int, error) {
	status, body, err := runFiberHandler(ctx, handler)
	if err != nil {
		return loomapi.CompileResponse{}, loomapi.ErrorResponse{}, 0, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.CompileResponse](body)
		return value, loomapi.ErrorResponse{}, status, e
	}
	value, _, e := authoringError(body, status)
	return loomapi.CompileResponse{}, value, status, e
}

func (r *HTTPRoutes) CompileExplorerBuilder(ctx context.Context, _ loomapi.CompileExplorerBuilderRequestObject) (loomapi.CompileExplorerBuilderResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.compileBuilder)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.CompileExplorerBuilder200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.CompileExplorerBuilder400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.CompileExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 409:
		return loomapi.CompileExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.CompileExplorerBuilder422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.CompileExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.CompileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("compileExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) CompileExplorer(ctx context.Context, _ loomapi.CompileExplorerRequestObject) (loomapi.CompileExplorerResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.compile)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.CompileExplorer200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.CompileExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.CompileExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 409:
		return loomapi.CompileExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.CompileExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.CompileExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.CompileExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("compileExplorer", status)
	}
}

func (r *HTTPRoutes) ReconcileExplorerBuilder(ctx context.Context, _ loomapi.ReconcileExplorerBuilderRequestObject) (loomapi.ReconcileExplorerBuilderResponseObject, error) {
	value, failure, status, err := r.compileExplorer(ctx, r.explorer.authoring.reconcile)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.ReconcileExplorerBuilder200JSONResponse(value), nil
	}
	switch status {
	case 400:
		return loomapi.ReconcileExplorerBuilder400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(failure)}, nil
	case 403:
		return loomapi.ReconcileExplorerBuilder403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(failure)}, nil
	case 404:
		return loomapi.ReconcileExplorerBuilder404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(failure)}, nil
	case 409:
		return loomapi.ReconcileExplorerBuilder409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(failure)}, nil
	case 422:
		return loomapi.ReconcileExplorerBuilder422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(failure)}, nil
	case 500:
		return loomapi.ReconcileExplorerBuilder500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(failure)}, nil
	case 503:
		return loomapi.ReconcileExplorerBuilder503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(failure)}, nil
	default:
		return nil, unexpectedResponseStatus("reconcileExplorerBuilder", status)
	}
}

func (r *HTTPRoutes) PreviewExplorer(ctx context.Context, _ loomapi.PreviewExplorerRequestObject) (loomapi.PreviewExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.preview)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.PreviewResponse](body)
		return loomapi.PreviewExplorer200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.PreviewExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PreviewExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.PreviewExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.PreviewExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 413:
		return loomapi.PreviewExplorer413JSONResponse{AuthoringPayloadTooLargeJSONResponse: loomapi.AuthoringPayloadTooLargeJSONResponse(value)}, nil
	case 422:
		return loomapi.PreviewExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 429:
		return loomapi.PreviewExplorer429JSONResponse{AuthoringTooManyRequestsJSONResponse: loomapi.AuthoringTooManyRequestsJSONResponse(value)}, nil
	case 499:
		return loomapi.PreviewExplorer499JSONResponse{AuthoringClientClosedRequestJSONResponse: loomapi.AuthoringClientClosedRequestJSONResponse(value)}, nil
	case 500:
		return loomapi.PreviewExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PreviewExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	case 504:
		return loomapi.PreviewExplorer504JSONResponse{AuthoringGatewayTimeoutJSONResponse: loomapi.AuthoringGatewayTimeoutJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("previewExplorer", status)
	}
}

func (r *HTTPRoutes) PublishExplorer(ctx context.Context, _ loomapi.PublishExplorerRequestObject) (loomapi.PublishExplorerResponseObject, error) {
	status, body, err := runFiberHandler(ctx, r.explorer.authoring.publish)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		value, e := decodeResponse[loomapi.PublishResponse](body)
		return loomapi.PublishExplorer200JSONResponse(value), e
	}
	value, _, e := authoringError(body, status)
	if e != nil {
		return nil, e
	}
	switch status {
	case 400:
		return loomapi.PublishExplorer400JSONResponse{AuthoringBadRequestJSONResponse: loomapi.AuthoringBadRequestJSONResponse(value)}, nil
	case 403:
		return loomapi.PublishExplorer403JSONResponse{AuthoringForbiddenJSONResponse: loomapi.AuthoringForbiddenJSONResponse(value)}, nil
	case 404:
		return loomapi.PublishExplorer404JSONResponse{AuthoringNotFoundJSONResponse: loomapi.AuthoringNotFoundJSONResponse(value)}, nil
	case 409:
		return loomapi.PublishExplorer409JSONResponse{AuthoringConflictJSONResponse: loomapi.AuthoringConflictJSONResponse(value)}, nil
	case 422:
		return loomapi.PublishExplorer422JSONResponse{AuthoringUnprocessableJSONResponse: loomapi.AuthoringUnprocessableJSONResponse(value)}, nil
	case 500:
		return loomapi.PublishExplorer500JSONResponse{AuthoringInternalErrorJSONResponse: loomapi.AuthoringInternalErrorJSONResponse(value)}, nil
	case 503:
		return loomapi.PublishExplorer503JSONResponse{AuthoringUnavailableJSONResponse: loomapi.AuthoringUnavailableJSONResponse(value)}, nil
	default:
		return nil, unexpectedResponseStatus("publishExplorer", status)
	}
}
