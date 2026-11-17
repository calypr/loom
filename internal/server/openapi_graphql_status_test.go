package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/gofiber/fiber/v3"
)

func TestGraphQLRoutesServeDocumentedStatusesAsGraphQLEnvelopesOnTheWire(t *testing.T) {
	cases := []struct {
		code      string
		status    int
		graphType any
		frameType any
	}{
		{"INVALID_REQUEST", 400, loomapi.ExecuteGraphQL400JSONResponse{}, loomapi.ExecuteDataframeGraphQL400JSONResponse{}},
		{"UNAUTHORIZED_PROJECT", 403, loomapi.ExecuteGraphQL403JSONResponse{}, loomapi.ExecuteDataframeGraphQL403JSONResponse{}},
		{"DATASET_NOT_FOUND", 404, loomapi.ExecuteGraphQL404JSONResponse{}, loomapi.ExecuteDataframeGraphQL404JSONResponse{}},
		{"RECIPE_NOT_FOUND", 404, loomapi.ExecuteGraphQL404JSONResponse{}, loomapi.ExecuteDataframeGraphQL404JSONResponse{}},
		{"NO_ACTIVE_GENERATION", 404, loomapi.ExecuteGraphQL404JSONResponse{}, loomapi.ExecuteDataframeGraphQL404JSONResponse{}},
		{"EXPORT_LIMIT_EXCEEDED", 413, loomapi.ExecuteGraphQL413JSONResponse{}, loomapi.ExecuteDataframeGraphQL413JSONResponse{}},
		{"STALE_CURSOR", 409, loomapi.ExecuteGraphQL409JSONResponse{}, loomapi.ExecuteDataframeGraphQL409JSONResponse{}},
		{"RECIPE_RESOLUTION_FAILED", 422, loomapi.ExecuteGraphQL422JSONResponse{}, loomapi.ExecuteDataframeGraphQL422JSONResponse{}},
		{"PLAN_TOO_EXPENSIVE", 429, loomapi.ExecuteGraphQL429JSONResponse{}, loomapi.ExecuteDataframeGraphQL429JSONResponse{}},
		{"CLIENT_CANCELED", 499, loomapi.ExecuteGraphQL499JSONResponse{}, loomapi.ExecuteDataframeGraphQL499JSONResponse{}},
		{"INTERNAL_ERROR", 500, loomapi.ExecuteGraphQL500JSONResponse{}, loomapi.ExecuteDataframeGraphQL500JSONResponse{}},
		{"BACKEND_UNAVAILABLE", 503, loomapi.ExecuteGraphQL503JSONResponse{}, loomapi.ExecuteDataframeGraphQL503JSONResponse{}},
		{"PREVIEW_TIMEOUT", 504, loomapi.ExecuteGraphQL504JSONResponse{}, loomapi.ExecuteDataframeGraphQL504JSONResponse{}},
	}
	for _, testCase := range cases {
		envelope := `{"data":null,"errors":[{"message":"typed","extensions":{"code":"` + testCase.code + `","retryable":false}}]}`
		routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(testCase.status, envelope)}}
		graphResponse, err := routes.ExecuteGraphQL(context.Background(), loomapi.ExecuteGraphQLRequestObject{Body: &loomapi.RawJSON{}})
		if err != nil {
			t.Fatalf("%s/%d: ExecuteGraphQL error: %v", testCase.code, testCase.status, err)
		}
		if got := reflect.TypeOf(graphResponse); got != reflect.TypeOf(testCase.graphType) {
			t.Fatalf("%s/%d: graph response = %v, want %v", testCase.code, testCase.status, got, reflect.TypeOf(testCase.graphType))
		}
		frameResponse, err := routes.ExecuteDataframeGraphQL(context.Background(), loomapi.ExecuteDataframeGraphQLRequestObject{Body: &loomapi.RawJSON{}})
		if err != nil {
			t.Fatalf("%s/%d: ExecuteDataframeGraphQL error: %v", testCase.code, testCase.status, err)
		}
		if got := reflect.TypeOf(frameResponse); got != reflect.TypeOf(testCase.frameType) {
			t.Fatalf("%s/%d: dataframe response = %v, want %v", testCase.code, testCase.status, got, reflect.TypeOf(testCase.frameType))
		}
		for _, target := range []struct {
			path   string
			routes *HTTPRoutes
		}{
			{"/graphql/graph", routes},
			{"/graphql/dataframe", routes},
		} {
			status, body := postGraphQLWire(t, target.routes, target.path)
			if status != testCase.status {
				t.Fatalf("%s/%d: wire status = %d, want %d (body %s)", testCase.code, testCase.status, status, testCase.status, body)
			}
			assertGraphQLEnvelopeWire(t, testCase.code+"/"+target.path, body, testCase.code)
		}
	}
}

func TestGraphQLRoutesServeSuccessEnvelopeOnTheWire(t *testing.T) {
	body := `{"data":{"__typename":"Query"},"errors":[]}`
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(http.StatusOK, body)}}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		if status != http.StatusOK {
			t.Fatalf("success wire status = %d (body %s)", status, wire)
		}
		assertGraphQLEnvelopeWire(t, "success/"+path, wire, "")
	}
}

func TestGraphQLRoutesRender401AsServiceErrorResponseWithoutWrapperKey(t *testing.T) {
	payload := `{"error":{"code":"UNAUTHENTICATED","message":"missing principal","requestId":"req-401"}}`
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(http.StatusUnauthorized, payload)}}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		if status != http.StatusUnauthorized {
			t.Fatalf("401 wire status = %d (body %s)", status, wire)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
			t.Fatalf("401 body not JSON: %v (%s)", err, wire)
		}
		if _, keyed := decoded["ServiceUnauthorizedJSONResponse"]; keyed {
			t.Fatalf("401 body key-wrapped: %s", wire)
		}
		errorValue, ok := decoded["error"].(map[string]any)
		if !ok || errorValue["code"] != "UNAUTHENTICATED" {
			t.Fatalf("401 service error envelope lost: %s", wire)
		}
	}
}

func TestGraphQLRoutesNilHandlerServesDocumented404GraphQLEnvelope(t *testing.T) {
	routes := &HTTPRoutes{}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		if status != http.StatusNotFound {
			t.Fatalf("nil-handler wire status = %d (body %s)", status, wire)
		}
		assertGraphQLEnvelopeWire(t, "nilHandler/"+path, wire, "")
	}
}

func TestGraphQLRoutesUndocumented502NeverLeaksUpstreamBody(t *testing.T) {
	canary := `{"data":null,"errors":[{"message":"SECRETCANARY-77 goroutine 1 [running]: /srv/loom/stack.go:42","extensions":{"code":"SOMETHING_UNKNOWN"}}],"internal":{"dsn":"postgres://user:p` + "a" + `SSWORD@db"}}`
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(http.StatusBadGateway, canary)}}
	_, wrapperErr := routes.ExecuteGraphQL(context.Background(), loomapi.ExecuteGraphQLRequestObject{Body: &loomapi.RawJSON{}})
	if wrapperErr == nil {
		t.Fatalf("undocumented 502 must exit through the unexpectedResponseStatus default")
	}
	for _, probe := range []string{"SECRETCANARY", "goroutine", "paSSWORD"} {
		if strings.Contains(wrapperErr.Error(), probe) {
			t.Fatalf("upstream body leaked through wrapper error: %v", wrapperErr)
		}
	}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		for _, probe := range []string{"SECRETCANARY", "goroutine", "paSSWORD"} {
			if strings.Contains(wire, probe) {
				t.Fatalf("undocumented 502 leaked upstream body to client: %s", wire)
			}
		}
		if status < 500 {
			t.Fatalf("undocumented 502 must surface as server-side handling, got %d (body %s)", status, wire)
		}
	}
}

func TestGraphQLRoutes401WithUndecodableBodyNeverLeaksUpstream(t *testing.T) {
	routes := &HTTPRoutes{graphql: graphapi.RouteConfig{Handler: stubGraphQLHandler(http.StatusUnauthorized, `SECRETCANARY-401 not-json <html>`)}}
	for _, path := range []string{"/graphql/graph", "/graphql/dataframe"} {
		status, wire := postGraphQLWire(t, routes, path)
		if strings.Contains(wire, "SECRETCANARY") || strings.Contains(wire, "not-json") {
			t.Fatalf("undecodable 401 body leaked to client: %s", wire)
		}
		if status < 400 {
			t.Fatalf("undecodable 401 must not surface as success, got %d (body %s)", status, wire)
		}
	}
}

func stubGraphQLHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		_, _ = response.Write([]byte(body))
	})
}

func graphQLWireApp(routes *HTTPRoutes) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		mapped := httpapi.MapDataframeError(err, "")
		return c.Status(mapped.Status).JSON(mapped.Body)
	}})
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	return app
}

func postGraphQLWire(t *testing.T, routes *HTTPRoutes, path string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"query":"query { __typename }"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err := graphQLWireApp(routes).Test(request)
	if err != nil {
		t.Fatalf("app.Test(%s): %v", path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body(%s): %v", path, err)
	}
	return response.StatusCode, string(raw)
}

func assertGraphQLEnvelopeWire(t *testing.T, label, body, wantedCode string) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("%s: wire body is not JSON: %v (%s)", label, err, body)
	}
	for key := range decoded {
		if strings.HasSuffix(key, "JSONResponse") {
			t.Fatalf("%s: wire body key-wrapped by generated response struct: %s", label, body)
		}
	}
	if _, leaked := decoded["error"]; leaked {
		t.Fatalf("%s: opaque service envelope leaked onto GraphQL wire: %s", label, body)
	}
	if _, hasData := decoded["data"]; !hasData {
		t.Fatalf("%s: GraphQL envelope has no top-level data: %s", label, body)
	}
	if wantedCode == "" {
		return
	}
	errorsValue, ok := decoded["errors"].([]any)
	if !ok || len(errorsValue) != 1 {
		t.Fatalf("%s: GraphQL envelope has no single typed error: %s", label, body)
	}
	first, ok := errorsValue[0].(map[string]any)
	if !ok {
		t.Fatalf("%s: error entry not an object: %s", label, body)
	}
	extensions, ok := first["extensions"].(map[string]any)
	if !ok || extensions["code"] != wantedCode {
		t.Fatalf("%s: extension code lost in rendering: %s", label, body)
	}
}
