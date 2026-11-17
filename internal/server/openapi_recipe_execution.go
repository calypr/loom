package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/api/authpolicy"
	"github.com/calypr/loom/internal/api/columncapabilities"
	dataframeapi "github.com/calypr/loom/internal/api/graphql/graph/dataframe"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func (r *HTTPRoutes) recipeExecution(ctx context.Context, id string) (any, int) {
	if r.releases == nil {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	id = strings.TrimSpace(id)
	execution, err := r.releases.GetExecution(ctx, id)
	if err != nil {
		return recipeExecutionLookupResponse(ctx, err)
	}
	if execution.ID == "" {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := authscope.AuthorizeProject(principal, execution.Project, false); err != nil {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	if r.scopes != nil {
		scope, scopeErr := recipeExecutionReadScope(ctx, r.scopes, principal, execution.Project, execution.DatasetGeneration, execution.AuthResourcePaths)
		if scopeErr != nil {
			return recipeExecutionAuthorizationResponse(ctx, scopeErr)
		}
		if scope.Mode == authscope.ReadScopeRestricted && !scope.CoversExecutionPaths(execution.AuthResourcePaths) {
			return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
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
		item := loomapi.RecipeExecutionOutput{
			Name:     output.Name,
			State:    recipeExecutionHTTPState(output.State),
			RowCount: output.RowCount,
			Columns:  columns,
		}
		mirroredError := ""
		if item.State != string(publication.BundleReady) {
			mirroredError = execution.Error
		}
		message, code, retryable := dataframeapi.PersistedFailure(mirroredError, output.FailureCode, output.FailureRetryable)
		item.Error, item.ErrorCode, item.ErrorRetryable = message, code, retryable
		if output.FailurePhase != "" {
			item.Phase = &output.FailurePhase
		}
		outputs = append(outputs, item)
	}
	response := loomapi.RecipeExecutionResponse{
		Id:                   execution.ID,
		ProjectId:            execution.Project,
		DatasetGeneration:    execution.DatasetGeneration,
		RecipeDigest:         execution.RecipeDigest,
		SchemaDigest:         execution.SchemaDigest,
		ResolvedSchemaDigest: execution.SchemaDigest,
		State:                recipeExecutionHTTPState(execution.State),
		Outputs:              outputs,
	}
	response.Error, response.ErrorCode, response.ErrorRetryable = dataframeapi.PersistedFailure(execution.Error, execution.FailureCode, execution.FailureRetryable)
	if execution.FailurePhase != "" {
		response.Phase = &execution.FailurePhase
	}
	return response, http.StatusOK
}

var recipeExecutionReadScope = func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, datasetGeneration string, declared []string) (authscope.ReadScope, error) {
	return scopes.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, declared)
}

func recipeExecutionLookupResponse(ctx context.Context, err error) (any, int) {
	if err == nil || errors.Is(err, publication.ErrBundleNotFound) {
		return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
	}
	classified := authpolicy.Classify(err, authpolicy.OperationConcealedLookup)
	if userErr, ok := dataframeerrors.AsUserError(classified); ok {
		if dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
			return map[string]any{"error": "recipe execution not found"}, http.StatusNotFound
		}
	} else {
		classified = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	status, response := mapServiceError(classified, ctx)
	if status != http.StatusServiceUnavailable {
		status, response = mapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return response, status
}

func recipeExecutionAuthorizationResponse(ctx context.Context, err error) (any, int) {
	status := recipeExecutionAuthorizationStatus(err)
	if status != http.StatusServiceUnavailable {
		return map[string]any{"error": "recipe execution not found"}, status
	}
	mappedStatus, response := mapServiceError(err, ctx)
	if mappedStatus != http.StatusServiceUnavailable {
		_, response = mapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return response, status
}

func recipeExecutionAuthorizationStatus(err error) int {
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, authscope.ErrForbidden):
		return http.StatusNotFound
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
