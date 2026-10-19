package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/api/columncapabilities"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

// recipeExecution returns the legacy execution document consumed by ETL. A
// denied execution remains indistinguishable from a missing catalog entry.
func (r *HTTPRoutes) recipeExecution(ctx context.Context, id string) (any, int) {
	if r.releases == nil {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	id = strings.TrimSpace(id)
	execution, err := r.releases.GetExecution(ctx, id)
	if err != nil || execution.ID == "" {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	if r.scopes != nil {
		principal, _ := authscope.PrincipalFromContext(ctx)
		if _, scopeErr := r.scopes.ResolveReadScopeForGeneration(ctx, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths); scopeErr != nil {
			return recipeExecutionAuthorizationResponse(ctx, scopeErr)
		}
	}
	outputs := make([]loomapi.RecipeExecutionOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		columns := make([]loomapi.RecipeExecutionColumn, 0, len(output.Columns))
		for _, column := range output.Columns {
			capabilities := columncapabilities.FromClickHouse(column.ClickHouse)
			logical, nullable, repeated := capabilities.Logical, capabilities.Nullable, capabilities.Repeated
			if column.LogicalType != "" {
				logical = column.LogicalType
			}
			nullable = nullable || column.Nullable
			repeated = repeated || column.Repeated
			columns = append(columns, loomapi.RecipeExecutionColumn{
				Name:           column.Name,
				SemanticPath:   column.SemanticPath,
				ClickhouseType: column.ClickHouse,
				LogicalType:    logical,
				Nullable:       nullable,
				Repeated:       repeated,
				Filterable:     capabilities.Filterable,
				Sortable:       !repeated,
				Aggregatable:   !repeated && logical != "json",
			})
		}
		outputs = append(outputs, loomapi.RecipeExecutionOutput{
			Name:     output.Name,
			State:    recipeExecutionHTTPState(output.State),
			RowCount: output.RowCount,
			Columns:  columns,
		})
	}
	return loomapi.RecipeExecutionResponse{
		Id:                   execution.ID,
		ProjectId:            execution.Project,
		DatasetGeneration:    execution.DatasetGeneration,
		RecipeDigest:         execution.RecipeDigest,
		SchemaDigest:         execution.SchemaDigest,
		ResolvedSchemaDigest: execution.SchemaDigest,
		State:                recipeExecutionHTTPState(execution.State),
		Outputs:              outputs,
	}, http.StatusOK
}

// recipeExecutionAuthorizationResponse keeps concealment for denials while
// exposing a retryable shared service envelope for an authorization outage.
// The generated OpenAPI adapter owns the final 503 response type; this seam
// keeps the semantic route behavior testable without hand-editing generated
// code.
func recipeExecutionAuthorizationResponse(ctx context.Context, err error) (any, int) {
	status := recipeExecutionAuthorizationStatus(err)
	if status != http.StatusServiceUnavailable {
		return map[string]any{"error": "recipe execution not found"}, status
	}
	_, response := mapServiceError(err, ctx)
	return response, status
}

func recipeExecutionAuthorizationStatus(err error) int {
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, authscope.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, authscope.ErrAuthorizationBackendUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusServiceUnavailable
	}
}

func recipeExecutionHTTPState(state publication.BundleState) string {
	if state == publication.BundlePublished {
		return string(publication.BundleReady)
	}
	return string(state)
}
