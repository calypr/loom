package server

import (
	"context"
	"encoding/json"
	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/gofiber/fiber/v3"
	"testing"
)

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
			routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: test.err}}
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
	routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: fault}}
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
	routes := &HTTPRoutes{releases: recipeExecutionCatalog{execution: publication.BundleExecution{ID: "execution-a", BundleIdentity: publication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a"}}}, scopes: &authscope.ScopeResolver{}}
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
		routes := &HTTPRoutes{releases: recipeExecutionFaultCatalog{err: fiber.NewError(test.status)}}
		response, err := routes.GetRecipeExecution(context.Background(), loomapi.GetRecipeExecutionRequestObject{Id: "execution-a"})
		if response != nil || err == nil || err.Error() != test.message {
			t.Fatalf("lookup status%d: response=%#v err=%v, want %q", test.status, response, err, test.message)
		}
	}
}
