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
	executionresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

// recipeExecution returns the durable record after the shared project and
// generation-scope checks. REST applies its own legacy state projection below.
func (r *HTTPRoutes) recipeExecution(ctx context.Context, id string) (publication.BundleExecution, error) {
	if r.releases == nil {
		return publication.BundleExecution{}, dataframeerrors.NewError(dataframeerrors.CodeRecipeExecutionNotFound, "")
	}
	resolveScope := executionresolver.RecipeExecutionScopeResolver(func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, generation string, declared []string) (authscope.ReadScope, error) {
		return recipeExecutionReadScope(ctx, scopes, principal, project, generation, declared)
	})
	reader := executionresolver.NewAuthorizedRecipeExecutionRecordReader(r.releases, r.scopes, resolveScope, true)
	return reader(ctx, strings.TrimSpace(id))
}

func recipeExecutionResponse(execution publication.BundleExecution) loomapi.RecipeExecutionResponse {
	outputs := make([]loomapi.RecipeExecutionOutput, 0, len(execution.Outputs))
	for _, output := range execution.Outputs {
		columns := make([]loomapi.RecipeExecutionColumn, 0, len(output.Columns))
		for _, column := range output.Columns {
			capabilities := columncapabilities.Resolve(column.ClickHouse, column.LogicalType, column.Nullable, column.Repeated)
			columns = append(columns, loomapi.RecipeExecutionColumn{
				Name:           column.Name,
				SemanticPath:   column.SemanticPath,
				ClickhouseType: column.ClickHouse,
				LogicalType:    capabilities.Logical,
				Nullable:       capabilities.Nullable,
				Repeated:       capabilities.Repeated,
				Filterable:     capabilities.Filterable,
				Sortable:       capabilities.Sortable,
				Aggregatable:   capabilities.Aggregatable,
			})
		}
		item := loomapi.RecipeExecutionOutput{
			Name:     output.Name,
			State:    recipeExecutionHTTPState(output.State),
			RowCount: output.RowCount,
			Columns:  columns,
		}
		mirroredError := executionresolver.RecipeExecutionOutputMessage(execution.Error, output.State)
		item.Error, item.ErrorCode, item.ErrorRetryable = dataframeapi.PersistedFailure(mirroredError, output.FailureCode, output.FailureRetryable)
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
	return response
}

// recipeExecutionReadScope consults the wired resolver for the auth paths an
// execution declares. The variable is the test seam for the CoversExecutionPaths
// defense-in-depth gate against resolver drift: a production resolver's
// Restricted success always echoes the requested path set back (scope_resolver.go
// ResolveReadScopeForGeneration), so the subset loop that makes Covers the
// sole disclosure gate is otherwise unreachable in e2e. The default remains
// the authentic resolver call; production behavior is unchanged.
var recipeExecutionReadScope = func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, datasetGeneration string, declared []string) (authscope.ReadScope, error) {
	return scopes.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, declared)
}

func recipeExecutionErrorResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	var failure *executionresolver.RecipeExecutionReadFailure
	if errors.As(err, &failure) {
		if failure.Authorization {
			return recipeExecutionAuthorizationResponse(ctx, failure.Err)
		}
		return recipeExecutionLookupResponse(ctx, failure.Err)
	}
	return recipeExecutionLookupResponse(ctx, err)
}

func recipeExecutionLookupResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	if err == nil || errors.Is(err, publication.ErrBundleNotFound) {
		return recipeExecutionNotFoundResponse(), nil
	}
	classified := authpolicy.Classify(err, authpolicy.OperationConcealedLookup)
	if userErr, ok := dataframeerrors.AsUserError(classified); ok {
		if dataframeerrors.ErrorCode(userErr.Code()) == dataframeerrors.CodeRecipeExecutionNotFound && !userErr.Retryable() {
			return recipeExecutionNotFoundResponse(), nil
		}
	} else {
		classified = dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	status, response := mapServiceError(classified, ctx)
	if status != http.StatusServiceUnavailable {
		status, response = mapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return recipeExecutionServiceResponse(status, response)
}

func recipeExecutionAuthorizationResponse(ctx context.Context, err error) (loomapi.GetRecipeExecutionResponseObject, error) {
	status := recipeExecutionAuthorizationStatus(err)
	if status == http.StatusUnauthorized {
		return loomapi.GetRecipeExecution401JSONResponse{"error": "recipe execution not found"}, nil
	}
	if status == http.StatusNotFound {
		return recipeExecutionNotFoundResponse(), nil
	}
	mappedStatus, response := mapServiceError(err, ctx)
	if mappedStatus != http.StatusServiceUnavailable {
		_, response = mapServiceError(dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true)), ctx)
	}
	return loomapi.NewGetRecipeExecutionErrorResponse(http.StatusServiceUnavailable, response)
}

func recipeExecutionAuthorizationStatus(err error) int {
	switch {
	case errors.Is(err, authscope.ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, authscope.ErrForbidden):
		return http.StatusNotFound
	default:
		return http.StatusServiceUnavailable
	}
}

// Preserve the mixed service envelope and reject non-string legacy403/404
// bodies exactly as the previous adapter did, without a JSON round trip.
func recipeExecutionServiceResponse(status int, response loomapi.ServiceErrorResponse) (loomapi.GetRecipeExecutionResponseObject, error) {
	if status == http.StatusServiceUnavailable {
		return loomapi.NewGetRecipeExecutionErrorResponse(status, response)
	}
	properties := map[string]any{}
	body := response.Error
	if body.FieldPath != nil {
		properties["fieldPath"] = *body.FieldPath
	}
	if body.Details != nil {
		properties["details"] = *body.Details
	}
	if body.RequestId != nil {
		properties["requestId"] = *body.RequestId
	}
	if body.Retryable != nil {
		properties["retryable"] = *body.Retryable
	}
	if status == http.StatusUnauthorized {
		properties["code"], properties["message"] = body.Code, body.Message
		return loomapi.NewGetRecipeExecutionErrorResponse(status, loomapi.RawJSON{"error": properties})
	}
	if status == http.StatusForbidden || status == http.StatusNotFound {
		return nil, errors.New("legacy error response is missing a string error")
	}
	return nil, unexpectedResponseStatus("getRecipeExecution", status)
}

func recipeExecutionNotFoundResponse() loomapi.GetRecipeExecution404JSONResponse {
	var response loomapi.LegacyErrorResponse
	_ = response.Error.FromLegacyErrorResponseError0("recipe execution not found")
	return loomapi.GetRecipeExecution404JSONResponse{LegacyNotFoundJSONResponse: loomapi.LegacyNotFoundJSONResponse(response)}
}

func recipeExecutionHTTPState(state publication.BundleState) string {
	if state == publication.BundlePublished {
		return string(publication.BundleReady)
	}
	return string(state)
}
