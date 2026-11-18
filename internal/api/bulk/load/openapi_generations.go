package load

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func (r *StrictHandlers) GetDatasetGenerationStatus(ctx context.Context, request loomapi.GetDatasetGenerationStatusRequestObject) (loomapi.GetDatasetGenerationStatusResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.service.GetDatasetGenerationStatus(ctx, request.Project, request.Generation, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.GetDatasetGenerationStatus200JSONResponse(generationStatusResponse(result)), nil
	}
	status, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewGetDatasetGenerationStatusErrorResponse(status, body)
}

func (r *StrictHandlers) CreateDatasetGeneration(ctx context.Context, request loomapi.CreateDatasetGenerationRequestObject) (loomapi.CreateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.service.CreateDatasetGeneration(ctx, request.Project, request.Generation, request.Body, principal)
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

func (r *StrictHandlers) ActivateDatasetGeneration(ctx context.Context, request loomapi.ActivateDatasetGenerationRequestObject) (loomapi.ActivateDatasetGenerationResponseObject, error) {
	principal, _ := authscope.PrincipalFromContext(ctx)
	result, err := r.service.ActivateDatasetGeneration(ctx, request.Project, request.Generation, request.Params.DataframeExecutionId, optionalString(request.Params.AuthResourcePath), principal)
	if err == nil {
		return loomapi.ActivateDatasetGeneration200JSONResponse(generationActivationResponse(result)), nil
	}
	status, body := httpapi.MapServiceError(err, ctx)
	return loomapi.NewActivateDatasetGenerationErrorResponse(status, body)
}

func generationStatusResponse(result *GenerationStatusResult) loomapi.GenerationStatusResponse {
	return loomapi.GenerationStatusResponse{
		Project:    result.Project,
		Generation: result.Generation,
		State:      string(result.State),
		Reusable:   result.Reusable,
	}
}

func generationActivationResponse(result *GenerationActivationResult) loomapi.GenerationActivationResponse {
	return loomapi.GenerationActivationResponse{
		Project:              result.Project,
		Generation:           result.Generation,
		DataframeExecutionId: result.DataframeExecutionID,
		Activated:            result.Activated,
	}
}

type StrictHandlers struct{ service *Handler }

func NewStrictHandlers(service *Handler) *StrictHandlers { return &StrictHandlers{service: service} }

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
