package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

func TestWireAuthNoAuthOverridesConfiguredMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Mode = "calypr"
	components, err := wireAuth(cfg, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := components.authorizer.(authscope.AllowAllAuthorizer); !ok || components.scopeResolver != nil {
		t.Fatalf("no-auth wiring = %#v", components)
	}
}

func TestWireAuthRejectsUnknownMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Mode = "unknown"
	if _, err := wireAuth(cfg, false, nil); err == nil {
		t.Fatal("unknown auth mode unexpectedly accepted")
	}
}

func TestRegisteredRecipeExecutionUsesAuthorizedReader(t *testing.T) {
	authorizer := authscope.AllowAllAuthorizer{}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{
		Authenticator: authscope.StaticAuthenticator{Principal: authscope.Principal{Subject: "user", Projects: []string{"P1"}}},
		Authorizer:    authorizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	load, err := loadapi.NewService(loadapi.ServiceConfig{LoadGeneration: routeRunner{}.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	catalog := &dataframeRouteCatalog{execution: publication.BundleExecution{
		ID: "execution", State: publication.BundlePublished, BundleIdentity: publication.BundleIdentity{Project: "P1"},
	}}
	if err := registerRoutes(server, load, authorizer, &explorerHandlers{}, routeDependencies{releases: catalog}); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		project    string
		wantStatus int
	}{{"P1", http.StatusOK}, {"P2", http.StatusNotFound}} {
		catalog.execution.Project = test.project
		response, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/api/v1/dataframe/recipe-executions/execution", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != test.wantStatus {
			t.Fatalf("project %s: status = %d, want %d", test.project, response.StatusCode, test.wantStatus)
		}
		if test.wantStatus == http.StatusOK {
			var body struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.ID != "execution" {
				t.Fatalf("execution response = %#v, err = %v", body, err)
			}
		}
	}
}
