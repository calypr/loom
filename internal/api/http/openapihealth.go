package httpapi

import (
	"context"
	"net/http"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func (r *HealthHandlers) GetHealth(ctx context.Context, _ loomapi.GetHealthRequestObject) (loomapi.GetHealthResponseObject, error) {
	body, status := r.server.Health(ctx)
	value, err := RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetHealth200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetHealthErrorResponse(status, value)
	}
	return nil, UnexpectedResponseStatus("getHealth", status)
}

func (r *HealthHandlers) GetLiveness(ctx context.Context, _ loomapi.GetLivenessRequestObject) (loomapi.GetLivenessResponseObject, error) {
	body, status := r.server.Liveness(ctx)
	value, err := RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, UnexpectedResponseStatus("getLiveness", status)
	}
	return loomapi.GetLiveness200JSONResponse(value), nil
}

func (r *HealthHandlers) GetReadiness(ctx context.Context, _ loomapi.GetReadinessRequestObject) (loomapi.GetReadinessResponseObject, error) {
	body, status := r.server.Readiness(ctx)
	value, err := RawJSON(body)
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return loomapi.GetReadiness200JSONResponse(value), nil
	}
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetReadinessErrorResponse(status, value)
	}
	return nil, UnexpectedResponseStatus("getReadiness", status)
}

type HealthHandlers struct{ server *HTTPServer }

func NewHealthHandlers(server *HTTPServer) *HealthHandlers { return &HealthHandlers{server: server} }
