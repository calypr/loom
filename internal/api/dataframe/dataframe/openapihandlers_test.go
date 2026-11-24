package dataframe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestRecipeExecutionHTTPStatePreservesExplorerReadyContract(t *testing.T) {
	if got := recipeExecutionHTTPState(publication.BundlePublished); got != "READY" {
		t.Fatalf("published HTTP state = %q, want READY", got)
	}
	if got := recipeExecutionHTTPState(publication.BundleRunning); got != "RUNNING" {
		t.Fatalf("running HTTP state = %q, want RUNNING", got)
	}
}

func TestRecipeExecutionAuthorizationStatusDistinguishesDenialFromOutage(t *testing.T) {

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

func TestRecipeExecutionHTTPStatePreservesOtherPersistedStates(t *testing.T) {
	for _, state := range []publication.BundleState{"PENDING", "PREFLIGHT", publication.BundleValidating, "LOADING", publication.BundleQueued, publication.BundleRunning, "READY", publication.BundleFailed, "FUTURE_STATE"} {
		if got := recipeExecutionHTTPState(state); got != string(state) {
			t.Errorf("HTTP state for %q = %q, want unchanged persisted state", state, got)
		}
	}
}

func TestRecipeExecutionAuthorizationResponseConcealsDenialAndStructuresOutage(t *testing.T) {
	denial, err := recipeExecutionAuthorizationResponse(context.Background(), authscope.ErrForbidden)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := denial.(loomapi.GetRecipeExecution404JSONResponse); !ok {
		t.Fatalf("denial=%T, want404", denial)
	}
	body, err := json.Marshal(denial)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"error":"recipe execution not found"}` {
		t.Fatalf("denial body=%s", body)
	}
	outage, err := recipeExecutionAuthorizationResponse(context.Background(), authscope.ErrAuthorizationBackendUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionMappedOutage(t, outage, "authorization backend unavailable")
}

func TestRecipeExecutionAuthorizationResponseStructuresArbitraryFaultLikeTheTwin(t *testing.T) {
	outage, err := recipeExecutionAuthorizationResponse(context.Background(), errors.New("catalog auth path listing exploded"))
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionMappedOutage(t, outage, "arbitrary authorization fault")
}

func TestRecipeExecutionResponsePreservesExplorerWireContract(t *testing.T) {
	execution := publication.BundleExecution{
		ID: "execution-a",
		BundleIdentity: publication.BundleIdentity{
			Project: "project-a", DatasetGeneration: "generation-a", RecipeDigest: "recipe-digest", SchemaDigest: "schema-digest",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: "patients", State: publication.BundlePublished, RowCount: 2,
			Columns: []publication.PhysicalColumn{{Name: "id", SemanticPath: "Patient.id", ClickHouse: "String"}},
		}},
	}
	want := `{"datasetGeneration":"generation-a","id":"execution-a","outputs":[{"columns":[{"aggregatable":true,"clickhouseType":"String","filterable":true,"logicalType":"string","name":"id","nullable":false,"repeated":false,"semanticPath":"Patient.id","sortable":true}],"name":"patients","rowCount":2,"state":"READY"}],"projectId":"project-a","recipeDigest":"recipe-digest","resolvedSchemaDigest":"schema-digest","schemaDigest":"schema-digest","state":"READY"}`
	payload, err := json.Marshal(recipeExecutionResponse(execution))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != want {
		t.Fatalf("recipe execution response = %s, want %s", payload, want)
	}
}

func assertRecipeExecutionMappedOutage(t *testing.T, response loomapi.GetRecipeExecutionResponseObject, cause string) {
	t.Helper()
	typed, ok := response.(loomapi.GetRecipeExecution503JSONResponse)
	if !ok || typed.Error.Code != "BACKEND_UNAVAILABLE" || typed.Error.Retryable == nil || !*typed.Error.Retryable {
		t.Fatalf("%s response = %#v, want retryable BACKEND_UNAVAILABLE", cause, response)
	}
}

func TestRecipeExecutionLookupErrorResponseKeepsConcealmentAndOutageMapping(t *testing.T) {
	notFound, err := recipeExecutionErrorResponse(context.Background(), errors.Join(errors.New("store lookup"), publication.ErrBundleNotFound))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := notFound.(loomapi.GetRecipeExecution404JSONResponse); !ok {
		t.Fatalf("not found = %T", notFound)
	}

	outage, err := recipeExecutionErrorResponse(context.Background(), errors.New("backend fault"))
	if err != nil {
		t.Fatal(err)
	}
	assertRecipeExecutionMappedOutage(t, outage, "lookup fault")
}
