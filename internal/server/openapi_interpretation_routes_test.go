package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func generatedInterpretationReadTestApp(readErr error) *fiber.App {
	app := fiber.New()
	routes := &HTTPRoutes{explorer: &explorerHTTPHandlers{
		authorizer:    authscope.AllowAllAuthorizer{},
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return readErr },
		application:   &lifecycle.Service{},
	}}
	registerGeneratedInterpretationRoutes(app, routes)
	return app
}

func registerGeneratedInterpretationRoutes(app *fiber.App, routes *HTTPRoutes) {
	// RegisterHandlers uses the same generated strict adapter as production. A
	// small helper keeps this test focused on auth/status behavior.
	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
}

func TestInterpretationReadRoutesEnforceProjectReadAuthorization(t *testing.T) {
	app := generatedInterpretationReadTestApp(errors.New("read denied"))
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "libraries", method: http.MethodGet, path: "/api/v1/projects/project/interpretation-libraries"},
		{name: "revision", method: http.MethodGet, path: "/api/v1/projects/project/interpretation-revisions/revision-1"},
		{name: "preview", method: http.MethodPost, path: "/api/v1/projects/project/explorers/explorer/authoring/v2/interpretation-preview", body: `{"snapshotToken":"token","expectedDraftVersion":1,"expectedDraftDigest":"digest","outputId":"patients","column":"code","revisionId":"revision-1"}`},
		{name: "configured column context", method: http.MethodPost, path: "/api/v1/projects/project/explorers/explorer/authoring/v2/configured-column-context", body: `{"snapshotToken":"token","expectedDraftVersion":1,"expectedDraftDigest":"digest"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusForbidden)
			}
		})
	}
}

func TestInterpretationPreviewRouteRejectsMissingBody(t *testing.T) {
	app := generatedInterpretationReadTestApp(nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project/explorers/explorer/authoring/v2/interpretation-preview", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestConfiguredColumnContextRouteRejectsMissingBody(t *testing.T) {
	app := generatedInterpretationReadTestApp(nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/project/explorers/explorer/authoring/v2/configured-column-context", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestConfiguredColumnContextResponseEncodesEmptyReadyListsAsArrays(t *testing.T) {
	response, err := configuredColumnContextResponse(lifecycle.ConfiguredColumnContextResult{
		SnapshotToken:   "snapshot",
		DraftVersion:    1,
		DraftDigest:     "digest",
		Libraries:       []lifecycle.InterpretationLibrarySummary{},
		PinnedRevisions: []lifecycle.InterpretationRevisionSummary{},
		Columns: []lifecycle.ConfiguredColumnContext{{
			OutputID:     "patients",
			Column:       "patient_id",
			OccurrenceID: "base",
			Resolution: lifecycle.ConfiguredColumnReady{
				CapabilityCandidateIDs: []string{"candidate-id"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"applicableRevisionIds":[]`) {
		t.Fatalf("configured column context JSON encoded an empty ready list as null: %s", payload)
	}
}
