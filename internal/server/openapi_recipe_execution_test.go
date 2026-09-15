package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
