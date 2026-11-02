package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/api/authpolicy"
	"github.com/calypr/loom/internal/api/columncapabilities"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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

// recipeExecutionLookupResponse is the HTTP twin of the GraphQL reader's
// recipeExecutionLookupError (recipe_execution_reader.go:81-99). The durable
// not-found sentinel is the only signal that maps to a permanent, concealed
// not-found; every other store failure stays an explicit retryable outage so a
// transient Arango fault never collapses into a non-retryable 404. Unclassified
// faults fail safe as retryable dependency faults per
// docs/loom-reliability-contract.md, and denial-concealment per authpolicy
// keeps the not-found envelope. No new response shape is introduced: retryable
// faults reuse the same shared service error the authorization outage emits.
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
	// F-4: the default 503 arm must keep this function's docstring promise (and
	// the GraphQL twin recipe_execution_reader.go:98 fail-safe) of a retryable
	// shared service envelope for an authorization outage. A known sentinel
	// outage already maps to the 503 BACKEND_UNAVAILABLE envelope and is
	// untouched; an arbitrary catalog/authorization fault that would otherwise
	// map to a non-retryable INTERNAL_ERROR gets the same fail-safe wrapper the
	// lookup twin applies just above (mapServiceError then re-map when the
	// shared mapper does not yield 503), so no new response shape is invented.
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
		// F-B32: a per-generation scope denial (ResolveReadScopeForGeneration ->
		// ErrForbidden for a restricted caller missing an auth_resource_path) is
		// an unauthorized-to-see execution and must conceal to 404, matching the
		// GraphQL twin (recipe_execution_reader.go:39-41 -> recipeExecutionLookupError:94
		// -> authpolicy Classify(ErrForbidden, OperationConcealedLookup)=CodeRecipeExecutionNotFound,
		// policy.go:40-41,47-52) and this route's sibling uncovered-path branch
		// (openapi_recipe_execution.go:40-42). A 403 here was a status-only
		// existence oracle over the shared "recipe execution not found" body.
		// Unauthenticated (401) and authorization-backend failure (503) arms are
		// preserved unchanged.
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
