package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

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
		return loomapi.NewGetHealthErrorResponse(status, value)
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
		return loomapi.NewGetReadinessErrorResponse(status, value)
	}
	return nil, unexpectedResponseStatus("getReadiness", status)
}

func (r *HTTPRoutes) GetDatasetGenerationStatus(ctx context.Context, request loomapi.GetDatasetGenerationStatusRequestObject) (loomapi.GetDatasetGenerationStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.GetDatasetGenerationStatus(ctx, request.Project, request.Generation, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.GetDatasetGenerationStatus200JSONResponse(generationStatusResponse(result)), nil
	}
	status, body := mapServiceError(err, ctx)
	return loomapi.NewGetDatasetGenerationStatusErrorResponse(status, body)
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
	return loomapi.NewCreateDatasetGenerationErrorResponse(status, body)
}

func (r *HTTPRoutes) ActivateDatasetGeneration(ctx context.Context, request loomapi.ActivateDatasetGenerationRequestObject) (loomapi.ActivateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.ActivateDatasetGeneration(ctx, request.Project, request.Generation, request.Params.DataframeExecutionId, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.ActivateDatasetGeneration200JSONResponse(generationActivationResponse(result)), nil
	}
	status, body := mapServiceError(err, ctx)
	return loomapi.NewActivateDatasetGenerationErrorResponse(status, body)
}

func (r *HTTPRoutes) GetRecipeExecution(ctx context.Context, request loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	body, status := r.recipeExecution(ctx, request.Id)
	if status == http.StatusOK {
		response, ok := body.(loomapi.RecipeExecutionResponse)
		if !ok {
			return nil, unexpectedResponseStatus("getRecipeExecution", status)
		}
		return loomapi.GetRecipeExecution200JSONResponse(response), nil
	}
	if status == http.StatusServiceUnavailable {
		response, ok := body.(loomapi.ServiceErrorResponse)
		if !ok {
			return nil, unexpectedResponseStatus("getRecipeExecution", status)
		}
		return loomapi.NewGetRecipeExecutionErrorResponse(status, response)
	}
	value, err := rawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		return loomapi.NewGetRecipeExecutionErrorResponse(status, value)
	}
	if status == http.StatusForbidden {
		legacy, legacyErr := legacyErrorFromMap(value)
		if legacyErr != nil {
			return nil, legacyErr
		}
		return loomapi.NewGetRecipeExecutionErrorResponse(status, legacy)
	}
	if status == http.StatusNotFound {
		legacy, legacyErr := legacyErrorFromMap(value)
		if legacyErr != nil {
			return nil, legacyErr
		}
		return loomapi.NewGetRecipeExecutionErrorResponse(status, legacy)
	}
	return nil, unexpectedResponseStatus("getRecipeExecution", status)
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func generationStatusResponse(result *loadapi.GenerationStatusResult) loomapi.GenerationStatusResponse {
	return loomapi.GenerationStatusResponse{
		Project:    result.Project,
		Generation: result.Generation,
		State:      string(result.State),
		Reusable:   result.Reusable,
	}
}

func generationActivationResponse(result *loadapi.GenerationActivationResult) loomapi.GenerationActivationResponse {
	return loomapi.GenerationActivationResponse{
		Project:              result.Project,
		Generation:           result.Generation,
		DataframeExecutionId: result.DataframeExecutionID,
		Activated:            result.Activated,
	}
}

func mapServiceError(err error, ctx context.Context) (int, loomapi.ServiceErrorResponse) {
	mapped := httpapi.MapDataframeError(err, httpapi.RequestIDFromContext(ctx))
	if mapped.Status >= http.StatusInternalServerError {
		logServerDiagnostic(slog.Default(), slog.LevelError, "data request failed",
			httpapi.RequestIDFromContext(ctx), "response", time.Duration(0), err)
	}
	return mapped.Status, serviceErrorResponse(mapped.Body)
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
