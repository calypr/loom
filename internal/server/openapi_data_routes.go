package server

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func (r *HTTPRoutes) GetHealth(ctx context.Context, _ loomapi.GetHealthRequestObject) (loomapi.GetHealthResponseObject, error) {
	body, status := r.server.Health(ctx)
	value, err := httpapi.RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetHealth200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetHealthErrorResponse(status, value)
	}
	return nil, httpapi.UnexpectedResponseStatus("getHealth", status)
}

func (r *HTTPRoutes) GetLiveness(ctx context.Context, _ loomapi.GetLivenessRequestObject) (loomapi.GetLivenessResponseObject, error) {
	body, status := r.server.Liveness(ctx)
	value, err := httpapi.RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, httpapi.UnexpectedResponseStatus("getLiveness", status)
	}
	return loomapi.GetLiveness200JSONResponse(value), nil
}

func (r *HTTPRoutes) GetReadiness(ctx context.Context, _ loomapi.GetReadinessRequestObject) (loomapi.GetReadinessResponseObject, error) {
	body, status := r.server.Readiness(ctx)
	value, err := httpapi.RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetReadiness200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetReadinessErrorResponse(status, value)
	}
	return nil, httpapi.UnexpectedResponseStatus("getReadiness", status)
}

func (r *HTTPRoutes) GetDatasetGenerationStatus(ctx context.Context, request loomapi.GetDatasetGenerationStatusRequestObject) (loomapi.GetDatasetGenerationStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.GetDatasetGenerationStatus(ctx, request.Project, request.Generation, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.GetDatasetGenerationStatus200JSONResponse(generationStatusResponse(result)), nil
	}
	status, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewGetDatasetGenerationStatusErrorResponse(status, body)
}

func (r *HTTPRoutes) CreateDatasetGeneration(ctx context.Context, request loomapi.CreateDatasetGenerationRequestObject) (loomapi.CreateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.CreateDatasetGeneration(ctx, request.Project, request.Generation, request.Body, principal)
	if err == nil {
		value, conversionErr := httpapi.RawJSON(result)
		if conversionErr != nil {
			return nil, conversionErr
		}
		return loomapi.CreateDatasetGeneration200JSONResponse(value), nil
	}
	status, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewCreateDatasetGenerationErrorResponse(status, body)
}

func (r *HTTPRoutes) ActivateDatasetGeneration(ctx context.Context, request loomapi.ActivateDatasetGenerationRequestObject) (loomapi.ActivateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.load.ActivateDatasetGeneration(ctx, request.Project, request.Generation, request.Params.DataframeExecutionId, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.ActivateDatasetGeneration200JSONResponse(generationActivationResponse(result)), nil
	}
	status, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewActivateDatasetGenerationErrorResponse(status, body)
}

func (r *HTTPRoutes) GetRecipeExecution(ctx context.Context, request loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	execution, err := r.recipeExecution(ctx, request.Id)
	if err != nil {
		return recipeExecutionErrorResponse(ctx, err)
	}
	return loomapi.GetRecipeExecution200JSONResponse(recipeExecutionResponse(execution)), nil
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
