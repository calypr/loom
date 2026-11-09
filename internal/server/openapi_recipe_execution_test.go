package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	dataset "github.com/calypr/loom/internal/dataset"
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

func TestRecipeExecutionHTTPStatePreservesExplorerReadyContract(t *testing.T) {
	if got := recipeExecutionHTTPState(publication.BundlePublished); got != "READY" {
		t.Fatalf("published HTTP state = %q, want READY", got)
	}
	if got := recipeExecutionHTTPState(publication.BundleRunning); got != "RUNNING" {
		t.Fatalf("running HTTP state = %q, want RUNNING", got)
	}
}

func TestRecipeExecutionAuthorizationStatusDistinguishesDenialFromOutage(t *testing.T) {
	// F-B32: concealed 404 — unified with GraphQL twin (reader:94 + authpolicy concealed-lookup) and CoversExecutionPaths sibling
	if got := recipeExecutionAuthorizationStatus(authscope.ErrForbidden); got != http.StatusNotFound {
		t.Fatalf("forbidden status = %d, want %d", got, http.StatusNotFound)
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
	// F-B32: concealed 404 — unified with GraphQL twin (reader:94 + authpolicy concealed-lookup) and CoversExecutionPaths sibling
	if status != http.StatusNotFound || !ok || denialBody["error"] != "recipe execution not found" {
		t.Fatalf("denial response = %#v, %d; want concealed 404", denial, status)
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

// F-4 (reviewer ses_e66757becffe8uCNimGXbEeIfc): the default arm's own
// promise. recipeExecutionAuthorizationResponse's docstring keeps a "retryable
// shared service envelope" for an authorization outage, and the GraphQL twin
// (recipe_execution_reader.go:94-98 -> recipeExecutionLookupError) emits
// retryable BACKEND_UNAVAILABLE for an arbitrary catalog/authorization fault.
// A non-sentinel fault leaving the resolver seam therefore has to keep that
// envelope here too; the shipped arm instead emitted a non-retryable
// INTERNAL_ERROR under the 503.
func TestRecipeExecutionAuthorizationResponseStructuresArbitraryFaultLikeTheTwin(t *testing.T) {
	outage, status := recipeExecutionAuthorizationResponse(context.Background(), errors.New("catalog auth path listing exploded"))
	if status != http.StatusServiceUnavailable {
		t.Fatalf("arbitrary authorization fault status = %d, want 503", status)
	}
	response, ok := outage.(loomapi.ServiceErrorResponse)
	if !ok {
		t.Fatalf("arbitrary authorization fault response = %T, want structured service error", outage)
	}
	if response.Error.Code != "BACKEND_UNAVAILABLE" || response.Error.Retryable == nil || !*response.Error.Retryable {
		t.Fatalf("arbitrary authorization fault = %#v, want retryable BACKEND_UNAVAILABLE", response.Error)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire loomapi.ServiceErrorResponse
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Error.Code != "BACKEND_UNAVAILABLE" || wire.Error.Retryable == nil || !*wire.Error.Retryable {
		t.Fatalf("arbitrary authorization fault marshaled body = %s, want explicit retryable BACKEND_UNAVAILABLE", payload)
	}
}

// Same promise at the client-visible wire: an arbitrary (non-sentinel) fault
// surfacing from the per-generation resolver must reach the caller as the
// generated retryable 503 outage envelope, exactly like the sentinel outage
// case below.
func TestGetRecipeExecutionAuthorizationArbitraryFaultIsRetryableOutage(t *testing.T) {
	routes := &HTTPRoutes{
		releases: recipeExecutionCatalog{execution: publication.BundleExecution{
			ID: "execution-a", BundleIdentity: publication.BundleIdentity{
				Project: "project-a", DatasetGeneration: "generation-a", AuthResourcePaths: []string{"path-a"},
			},
		}},
		scopes: authscope.NewScopeResolver(authscope.ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
				return nil, errors.New("catalog auth path listing exploded")
			},
		}),
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"path-a"}})
	response, err := routes.GetRecipeExecution(ctx, loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "arbitrary authorization fault")
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

// assertRecipeExecutionRetryableOutage proves the client-visible wire shape is
// the generated typed retryable outage envelope: the concrete response type is
// the retryable 503 discriminator, and the marshaled body carries an explicit
// retryable=true flag with the stable BACKEND_UNAVAILABLE code. Derived from
// docs/loom-reliability-contract.md ("Retryability is explicit; clients do not
// infer it from an HTTP status or message") and openapi/openapi.yaml
// getRecipeExecution's declared 503, not from the handler's internals.
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

// A raw Arango fault must stay an explicit retryable outage instead of folding
// into a permanent non-retryable not-found. Mirror of the GraphQL twin
// recipe_execution_reader.go:81-99 (faults -> retryable BACKEND_UNAVAILABLE).
func TestGetRecipeExecutionStoreFaultIsRetryableOutage(t *testing.T) {
	routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: errors.New("arango connection reset")}}
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "raw store fault")
}

// A deadline overrun is a transient fault, not evidence the execution is gone.
func TestGetRecipeExecutionDeadlineExceededIsRetryableOutage(t *testing.T) {
	routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: context.DeadlineExceeded}}
	response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionRetryableOutage(t, response, "deadline exceeded")
}

// The only permanent-missing signal is the durable not-found sentinel; a
// wrapped sentinel keeps the existing concealed not-found body.
func TestGetRecipeExecutionWrappedBundleNotFoundStaysNotFound(t *testing.T) {
	routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: fmt.Errorf("arango lookup failed: %w", publication.ErrBundleNotFound)}}
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
	status := generationStatusResponse(&loadapi.GenerationStatusResult{
		Project: "project-a", Generation: "generation-a", State: dataset.StateStaged, Reusable: true,
	})
	assertJSONFixture(t, status, `{"generation":"generation-a","project":"project-a","reusable":true,"state":"STAGED"}`)

	activation := generationActivationResponse(&loadapi.GenerationActivationResult{
		Project: "project-a", Generation: "generation-a", DataframeExecutionID: "execution-a", Activated: true,
	})
	assertJSONFixture(t, activation, `{"activated":true,"dataframeExecutionId":"execution-a","generation":"generation-a","project":"project-a"}`)

	routes := &HTTPRoutes{releases: recipeExecutionCatalog{execution: publication.BundleExecution{
		ID: "execution-a",
		BundleIdentity: publication.BundleIdentity{
			Project: "project-a", DatasetGeneration: "generation-a", RecipeDigest: "recipe-digest", SchemaDigest: "schema-digest",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: "patients", State: publication.BundlePublished, RowCount: 2,
			Columns: []publication.PhysicalColumn{{Name: "id", SemanticPath: "Patient.id", ClickHouse: "String"}},
		}},
	}}}
	body, statusCode := routes.recipeExecution(context.Background(), "execution-a")
	if statusCode != http.StatusOK {
		t.Fatalf("recipe execution status = %d, want 200", statusCode)
	}
	assertJSONFixture(t, body, `{"datasetGeneration":"generation-a","id":"execution-a","outputs":[{"columns":[{"aggregatable":true,"clickhouseType":"String","filterable":true,"logicalType":"string","name":"id","nullable":false,"repeated":false,"semanticPath":"Patient.id","sortable":true}],"name":"patients","rowCount":2,"state":"READY"}],"projectId":"project-a","recipeDigest":"recipe-digest","resolvedSchemaDigest":"schema-digest","schemaDigest":"schema-digest","state":"READY"}`)
}

func assertJSONFixture(t *testing.T, value any, want string) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != want {
		t.Fatalf("response fixture = %s, want %s", payload, want)
	}
}
