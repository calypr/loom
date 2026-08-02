package httpapi

import (
	"github.com/calypr/loom/internal/authscope"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{Authenticator: authscope.BasicAuthenticator{Username: "u", Password: "p"}, Authorizer: authscope.AllowAllAuthorizer{}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

func TestNewHTTPServerRequiresAuthorizer(t *testing.T) {
	if _, err := NewHTTPServer(HTTPConfig{}); err == nil {
		t.Fatal("expected missing authorizer error")
	}
}
