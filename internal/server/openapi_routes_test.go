package server

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
)

func TestServiceErrorResponsePreservesOrderedFieldPath(t *testing.T) {
	want := []string{"input", "outputs", "0", "columns", "2"}
	response := serviceErrorResponse(httpapi.ErrorResponse{Error: httpapi.HTTPErrorBody{
		Code: "INVALID_REQUEST", Message: "the request is invalid", FieldPath: want,
	}})
	if response.Error.FieldPath == nil || !reflect.DeepEqual(*response.Error.FieldPath, want) {
		t.Fatalf("generated service error field path = %#v, want %#v", response.Error.FieldPath, want)
	}
	want[0] = "changed"
	if (*response.Error.FieldPath)[0] != "input" {
		t.Fatalf("generated service error retained caller slice: %#v", *response.Error.FieldPath)
	}
}

func TestGraphQLAdaptersPreserveConflictResponses(t *testing.T) {
	handler := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusConflict)
		_, _ = response.Write([]byte(`{"errors":[{"message":"the preview cursor is no longer valid","extensions":{"code":"STALE_CURSOR"}}]}`))
	})
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: handler}}

	graphResponse, err := routes.ExecuteGraphQL(context.Background(), loomapi.ExecuteGraphQLRequestObject{Body: &loomapi.RawJSON{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := graphResponse.(loomapi.ExecuteGraphQL409JSONResponse); !ok {
		t.Fatalf("graph response = %T, want generated 409 response", graphResponse)
	}

	dataframeResponse, err := routes.ExecuteDataframeGraphQL(context.Background(), loomapi.ExecuteDataframeGraphQLRequestObject{Body: &loomapi.RawJSON{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := dataframeResponse.(loomapi.ExecuteDataframeGraphQL409JSONResponse); !ok {
		t.Fatalf("dataframe response = %T, want generated 409 response", dataframeResponse)
	}
}
