package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
)

func TestRetiredStandaloneViewBuilderRoutesReturnNotFound(t *testing.T) {
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	routes := &HTTPRoutes{server: server, explorer: &explorerHTTPHandlers{authorizer: authscope.AllowAllAuthorizer{}}}
	loomapi.RegisterHandlersWithOptions(server.App(), loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
	tests := []struct{ method, path string }{
		{"GET", "/api/v1/projects/p/view-builder"},
		{"POST", "/api/v1/projects/p/view-builder/adopt"},
		{"PUT", "/api/v1/projects/p/view-builder/defaults"},
		{"PUT", "/api/v1/projects/p/view-builder/configs/e"},
		{"DELETE", "/api/v1/projects/p/view-builder/configs/e"},
	}
	for _, tc := range tests {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			response, err := server.App().Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, readErr := io.ReadAll(response.Body)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != 404 {
				t.Fatalf("retired route status%d body%s", response.StatusCode, raw)
			}
			t.Logf("%s %s status%d %s", tc.method, tc.path, response.StatusCode, raw)
		})
	}
	// A neighboring retained route must still mount and bind its JSON body.
	request := httptest.NewRequest("POST", "/api/v1/projects/p/generations/g/explorer-config", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	response, err := server.App().Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("retained explorer-config status%d want400", response.StatusCode)
	}
}
