package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
)

type recipeExecutionCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution
}

func (c recipeExecutionCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return c.execution, nil
}

func TestRecipeExecutionHTTPStatePreservesExplorerReadyContract(t *testing.T) {
	if got := recipeExecutionHTTPState(publication.BundlePublished); got != "READY" {
		t.Fatalf("published HTTP state = %q, want READY", got)
	}
	if got := recipeExecutionHTTPState(publication.BundleRunning); got != "RUNNING" {
		t.Fatalf("running HTTP state = %q, want RUNNING", got)
	}
}

func TestRecipeExecutionAuthorizationStatusDistinguishesDenialFromOutage(t *testing.T) {
	if got := recipeExecutionAuthorizationStatus(authscope.ErrForbidden); got != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want %d", got, http.StatusForbidden)
	}
	if got := recipeExecutionAuthorizationStatus(authscope.ErrAuthorizationBackendUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("backend outage status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	if got := recipeExecutionAuthorizationStatus(errors.New("unexpected scope failure")); got != http.StatusServiceUnavailable {
		t.Fatalf("unknown scope status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestRecipeAuthorizationAdapterUsesOperationAuthPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		code      string
		retryable bool
	}{
		{name: "forbidden", err: authscope.ErrForbidden, code: "UNAUTHORIZED_PROJECT"},
		{name: "authorization outage", err: authscope.ErrAuthorizationBackendUnavailable, code: "BACKEND_UNAVAILABLE", retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			mapped := recipeAuthorizationError(test.err)
			userErr, ok := dataframeerrors.AsUserError(mapped)
			if !ok || userErr.Code() != test.code || userErr.Retryable() != test.retryable {
				t.Fatalf("mapped error = %#v, want code=%s retryable=%t", mapped, test.code, test.retryable)
			}
		})
	}
}

func TestRecipeExecutionAuthorizationResponseConcealsDenialAndStructuresOutage(t *testing.T) {
	denial, status := recipeExecutionAuthorizationResponse(context.Background(), authscope.ErrForbidden)
	denialBody, ok := denial.(map[string]any)
	if status != http.StatusForbidden || !ok || denialBody["error"] != "recipe execution not found" {
		t.Fatalf("denial response = %#v, %d; want concealed 403", denial, status)
	}

	outage, status := recipeExecutionAuthorizationResponse(context.Background(), authscope.ErrAuthorizationBackendUnavailable)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("outage status = %d, want 503", status)
	}
	response, ok := outage.(loomapi.ServiceErrorResponse)
	if !ok || response.Error.Code != "BACKEND_UNAVAILABLE" || response.Error.Retryable == nil || !*response.Error.Retryable {
		t.Fatalf("outage response = %#v, want structured retryable service error", outage)
	}
}

func TestGetRecipeExecutionReturnsGeneratedRetryableOutage(t *testing.T) {
	routes := &HTTPRoutes{
		releases: recipeExecutionCatalog{execution: publication.BundleExecution{
			ID: "execution-a", BundleIdentity: publication.BundleIdentity{
				Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"path-a"},
			},
		}},
		scopes: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
				return nil, authscope.ErrAuthorizationBackendUnavailable
			},
		}),
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"path-a"}})
	response, err := routes.GetRecipeExecution(ctx, loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := response.(loomapi.GetRecipeExecution503JSONResponse)
	if !ok || typed.Error.Code != "BACKEND_UNAVAILABLE" || typed.Error.Retryable == nil || !*typed.Error.Retryable {
		t.Fatalf("generated 503 response = %#v, want retryable BACKEND_UNAVAILABLE", response)
	}
}
