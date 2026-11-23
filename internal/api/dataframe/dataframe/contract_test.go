package dataframe_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipeauth"
	"github.com/gofiber/fiber/v3"
)

type recipeExecutionCatalog struct {
	publication.BundleCatalog
	execution publication.BundleExecution
}

func (c recipeExecutionCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return c.execution, nil
}

type recipeExecutionFaultCatalog struct {
	publication.BundleCatalog
	err error
}

func (c recipeExecutionFaultCatalog) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, c.err
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
			mapped := recipeauth.AuthorizationError(test.err)
			userErr, ok := dataframeerrors.AsUserError(mapped)
			if !ok || userErr.Code() != test.code || userErr.Retryable() != test.retryable {
				t.Fatalf("mapped error = %#v, want code=%s retryable=%t", mapped, test.code, test.retryable)
			}
		})
	}
}

func TestGetRecipeExecutionAuthorizationArbitraryFaultIsRetryableOutage(t *testing.T) {
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{
		releases: recipeExecutionCatalog{execution: publication.BundleExecution{
			ID: "execution-a", BundleIdentity: publication.BundleIdentity{
				Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"path-a"},
			},
		}},
		scopes: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
				return nil, errors.New("catalog auth path listing exploded")
			},
		}),
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"path-a"}})
	response, err := routes.GetRecipeExecution(ctx, loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "arbitrary authorization fault")
}

func TestGetRecipeExecutionReturnsGeneratedRetryableOutage(t *testing.T) {
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{
		releases: recipeExecutionCatalog{execution: publication.BundleExecution{
			ID: "execution-a", BundleIdentity: publication.BundleIdentity{
				Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"path-a"},
			},
		}},
		scopes: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, authscope.AuthResourcePathOptions) ([]string, error) {
				return nil, authscope.ErrAuthorizationBackendUnavailable
			},
		}),
	})
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

func assertRecipeExecutionRetryableOutage(t *testing.T, response loomapi.GetRecipeExecutionResponseObject, cause string) {
	t.Helper()
	typed, ok := response.(loomapi.GetRecipeExecution503JSONResponse)
	if !ok {
		t.Fatalf("%s: response = %T, want the generated retryable 503 outage envelope", cause, response)
	}
	if typed.Error.Code != "BACKEND_UNAVAILABLE" || typed.Error.Retryable == nil || !*typed.Error.Retryable {
		t.Fatalf("%s: service error = %#v, want retryable BACKEND_UNAVAILABLE", cause, typed.Error)
	}
	payload, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	var wire loomapi.ServiceErrorResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Error.Code != "BACKEND_UNAVAILABLE" || wire.Error.Retryable == nil || !*wire.Error.Retryable {
		t.Fatalf("%s: marshaled body = %s, want explicit retryable BACKEND_UNAVAILABLE", cause, payload)
	}
}

func TestGetRecipeExecutionStoreFaultIsRetryableOutage(t *testing.T) {
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: errors.New("arango connection reset")}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "raw store fault")
}

func TestGetRecipeExecutionDeadlineExceededIsRetryableOutage(t *testing.T) {
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: context.DeadlineExceeded}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "deadline exceeded")
}

func TestGetRecipeExecutionWrappedBundleNotFoundStaysNotFound(t *testing.T) {
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: fmt.Errorf("arango lookup failed: %w", publication.ErrBundleNotFound)}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := response.(loomapi.GetRecipeExecution404JSONResponse)
	if !ok {
		t.Fatalf("wrapped sentinel response = %T, want the concealed 404 envelope", response)
	}
	payload, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "recipe execution not found") {
		t.Fatalf("wrapped sentinel body = %s, want the concealed not-found envelope", payload)
	}
}

func TestStableReadResponseFixtures(t *testing.T) {

	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionCatalog{execution: publication.BundleExecution{
		ID: "execution-a",
		BundleIdentity: publication.BundleIdentity{
			Project: "project-a", DatasetGeneration: "generation-a", RecipeDigest: "recipe-digest", SchemaDigest: "schema-digest",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: "patients", State: publication.BundlePublished, RowCount: 2,
			Columns: []publication.PhysicalColumn{{Name: "id", SemanticPath: "Patient.id", ClickHouse: "String"}},
		}},
	}}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := response.(loomapi.GetRecipeExecution200JSONResponse); !ok {
		t.Fatalf("recipe execution response = %T, want success", response)
	}
}

type recipeExecutionRouteConfig struct {
	releases publication.BundleCatalog
	scopes   *authscope.ScopeResolver
}

func recipeExecutionTestRoutes(config recipeExecutionRouteConfig) *routes {
	return &routes{reader: dataframeapi.NewHandlers(nil, nil, dataframeapi.NewRecipeExecutionReader(config.releases, config.scopes, func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, generation string, declared []string) (authscope.ReadScope, error) {
		return recipeExecutionReadScope(ctx, scopes, principal, project, generation, declared)
	}))}
}

var recipeExecutionReadScope = func(ctx context.Context, scopes *authscope.ScopeResolver, principal *authscope.Principal, project, generation string, declared []string) (authscope.ReadScope, error) {
	return scopes.ResolveReadScopeForGeneration(ctx, principal, project, generation, declared)
}

type routes struct {
	loomapi.StrictServerInterface
	reader *dataframeapi.Handlers
}

func (r *routes) GetRecipeExecution(ctx context.Context, request loomapi.GetRecipeExecutionRequestObject) (loomapi.GetRecipeExecutionResponseObject, error) {
	return r.reader.GetRecipeExecution(ctx, request)
}

func TestRecipeLookupRetainsTransportErrorContract(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		want404 bool
	}{

		{"notfound wins typed error", dataframeerrors.Wrap(publication.ErrBundleNotFound, dataframeerrors.CodeBackendUnavailable, ""), true},
		{"typed unauthenticated lookup remains outage", dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, ""), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: test.err}})
			response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
			if err != nil {
				t.Fatal(err)
			}
			if test.want404 {
				if _, ok := response.(loomapi.GetRecipeExecution404JSONResponse); !ok {
					t.Fatalf("response=%T, want404", response)
				}
			} else {
				if _, ok := response.(loomapi.GetRecipeExecution503JSONResponse); !ok {
					t.Fatalf("response=%T, want503", response)
				}
			}
		})
	}
}

func TestRecipeLookupRetainsExistingOutageDetails(t *testing.T) {
	fault := dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithDetails(map[string]any{"reason": "catalog unavailable"}), dataframeerrors.WithFieldPath("execution"), dataframeerrors.WithRetryable(false))
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: fault}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := response.(loomapi.GetRecipeExecution503JSONResponse)
	if !ok {
		t.Fatalf("response=%T", response)
	}
	if typed.Error.Details == nil || (*typed.Error.Details)["reason"] != "catalog unavailable" || typed.Error.FieldPath == nil || len(*typed.Error.FieldPath) != 1 || typed.Error.Retryable == nil || !*typed.Error.Retryable {
		t.Fatalf("lost existing error metadata: %#v", typed.Error)
	}
}

func TestRecipeScopeUnauthenticatedRetainsLegacyBody(t *testing.T) {
	old := recipeExecutionReadScope
	t.Cleanup(func() { recipeExecutionReadScope = old })
	recipeExecutionReadScope = func(context.Context, *authscope.ScopeResolver, *authscope.Principal, string, string, []string) (authscope.ReadScope, error) {
		return authscope.ReadScope{}, authscope.ErrUnauthenticated
	}
	routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionCatalog{execution: publication.BundleExecution{ID: "execution-a", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a"}}}, scopes: &authscope.ScopeResolver{}})
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	typed, ok := response.(loomapi.GetRecipeExecution401JSONResponse)
	if !ok {
		t.Fatalf("response=%T, want401", response)
	}
	got, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"error":"recipe execution not found"}` {
		t.Fatalf("401 body=%s, want legacy concealed body", got)
	}
}

func TestRecipeLookupRetainsLegacyAdapterRejections(t *testing.T) {
	for _, test := range []struct {
		status  int
		message string
	}{
		{403, "legacy error response is missing a string error"},
		{404, "legacy error response is missing a string error"},
		{400, "getRecipeExecution returned undocumented HTTP status 400"},
		{502, "getRecipeExecution returned undocumented HTTP status 502"},
	} {
		routes := recipeExecutionTestRoutes(recipeExecutionRouteConfig{releases: recipeExecutionFaultCatalog{err: fiber.NewError(test.status)}})
		response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
		if response != nil || err == nil || err.Error() != test.message {
			t.Fatalf("lookup status%d: response=%#v err=%v, want %q", test.status, response, err, test.message)
		}
	}
}
