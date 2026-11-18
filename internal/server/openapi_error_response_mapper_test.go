package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/gofiber/fiber/v3"
)

type wp9Visit func(fiber.Ctx) error

func TestGeneratedErrorMappersPreserveInputWireBodies(t *testing.T) {
	details := map[string]interface{}{"retryAfter": 7, "context": map[string]interface{}{"field": "name"}}
	path := []string{"workspace", "documents", "0"}
	diagnostic := loomapi.Diagnostic{
		Code: "BAD_FIELD", Details: &details, JsonPath: wp9String("$.documents[0].name"),
		Message: "field is invalid", RequestId: wp9String("diag-1"), Severity: "ERROR", Stage: "validation",
	}
	diagnostics := []loomapi.Diagnostic{diagnostic}
	authoring := loomapi.ErrorResponse{
		Diagnostics: &diagnostics,
		Error: loomapi.ErrorBody{
			Code: "INVALID", Diagnostic: &diagnostic, Message: "bad input", RequestId: wp9String("request-1"),
			AdditionalProperties: map[string]interface{}{"details": map[string]interface{}{"fieldPath": path, "retryable": false}},
		},
	}
	serviceDetails := map[string]interface{}{"reason": "auth backend", "retryable": true}
	servicePath := []string{"projects", "p1"}
	retryable := true
	service := loomapi.ServiceErrorResponse{Error: loomapi.ServiceErrorBody{
		Code: "AUTH_UNAVAILABLE", Details: &serviceDetails, FieldPath: &servicePath,
		Message: "authorization service unavailable", RequestId: wp9String("request-401"), Retryable: &retryable,
	}}

	testWire := func(t *testing.T, status int, expected any, visit wp9Visit) {
		t.Helper()
		want, err := json.Marshal(expected)
		if err != nil {
			t.Fatal(err)
		}
		app := fiber.New()
		app.Get("/", func(ctx fiber.Ctx) error { return visit(ctx) })
		response, err := app.Test(httptest.NewRequest("GET", "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("status = %d, want %d", response.StatusCode, status)
		}
		if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Fatalf("Content-Type = %q", got)
		}
		var got bytes.Buffer
		if _, err := got.ReadFrom(response.Body); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("wire body = %s, want source-schema body %s", got.Bytes(), want)
		}
	}

	t.Run("authoring error retains diagnostics details path and request ids", func(t *testing.T) {
		mapped, err := loomapi.NewProposeExplorerColumnPolicyErrorResponse(400, authoring)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 400, authoring, mapped.VisitProposeExplorerColumnPolicyResponse)
	})
	t.Run("401 Explorer envelope retains diagnostics details and request id", func(t *testing.T) {
		mapped, err := loomapi.NewProposeExplorerColumnPolicyErrorResponse(401, authoring)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 401, authoring, mapped.VisitProposeExplorerColumnPolicyResponse)
	})
	t.Run("Explorer 403 common error body", func(t *testing.T) {
		body := loomapi.ErrorResponse{Error: loomapi.ErrorBody{Code: "FORBIDDEN", Message: "forbidden"}}
		mapped, err := loomapi.NewListExplorersErrorResponse(403, body)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 403, body, mapped.VisitListExplorersResponse)
	})
	t.Run("Explorer 409 common body retains details", func(t *testing.T) {
		details := map[string]interface{}{"retryable": false}
		body := loomapi.ErrorResponse{Error: loomapi.ErrorBody{Code: "CONFLICT", Message: "already exists", Details: &details}}
		mapped, err := loomapi.NewCreateExplorerErrorResponse(409, body)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 409, body, mapped.VisitCreateExplorerResponse)
	})
	t.Run("dataframe rows 409 service body", func(t *testing.T) {
		mapped, err := loomapi.NewPostDataframeRowsErrorResponse(409, service)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 409, service, mapped.VisitPostDataframeRowsResponse)
	})
	t.Run("recipe execution 503 service body", func(t *testing.T) {
		mapped, err := loomapi.NewGetRecipeExecutionErrorResponse(503, service)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 503, service, mapped.VisitGetRecipeExecutionResponse)
	})
	t.Run("source upload 422 issues array", func(t *testing.T) {
		body := loomapi.SourceLoadValidationErrorResponse{}
		body.Error.Code = loomapi.SourceLoadValidationErrorResponseErrorCode("INVALID_DATA")
		body.Error.Message = "file validation failed"
		body.Error.RequestId = wp9String("upload-1")
		body.Error.Retryable = &retryable
		body.Error.Details = loomapi.SourceLoadValidationDetails{Issues: []loomapi.SourceLoadValidationIssue{
			{Category: "INVALID_ROW", Filename: "Patient.ndjson", Message: "missing id", ResourceType: "Patient", Row: 4},
			{Category: "INVALID_REFERENCE", Filename: "Observation.ndjson", Message: "unknown patient", ResourceType: "Observation", Row: 8},
		}, TotalErrors: 2, Truncated: false}
		mapped, err := loomapi.NewUploadDatasetSourceLoadFileErrorResponse(422, body)
		if err != nil {
			t.Fatal(err)
		}
		testWire(t, 422, body, mapped.VisitUploadDatasetSourceLoadFileResponse)
	})
	t.Run("wrong body type is rejected", func(t *testing.T) {
		if _, err := loomapi.NewProposeExplorerColumnPolicyErrorResponse(400, service); err == nil {
			t.Fatal("accepted a ServiceErrorResponse for an ErrorResponse status")
		}
	})
}

func TestViewBuilderErrorFactoriesKeepHistoricalFallbackText(t *testing.T) {
	cases := []struct {
		name string
		call func() error
	}{
		{"GetProjectViewBuilder", func() error { _, err := loomapi.NewGetProjectViewBuilderErrorResponse(502, nil); return err }},
		{"AdoptProjectViewBuilder", func() error { _, err := loomapi.NewAdoptProjectViewBuilderErrorResponse(502, nil); return err }},
		{"SaveProjectViewDefaults", func() error { _, err := loomapi.NewSaveProjectViewDefaultsErrorResponse(502, nil); return err }},
		{"SaveProjectViewConfig", func() error { _, err := loomapi.NewSaveProjectViewConfigErrorResponse(502, nil); return err }},
		{"DeleteProjectViewConfig", func() error { _, err := loomapi.NewDeleteProjectViewConfigErrorResponse(502, nil); return err }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil || err.Error() != test.name+" returned undocumented HTTP status 502" {
				t.Fatalf("fallback = %v", err)
			}
		})
	}
}

func wp9String(value string) *string { return &value }
