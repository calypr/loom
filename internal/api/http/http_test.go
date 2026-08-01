package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"github.com/calypr/loom/internal/authscope"
)

func TestHealthDoesNotRequireAuthentication(t *testing.T) {
	server, err := NewHTTPServer(HTTPConfig{Authenticator: authscope.BasicAuthenticator{Username: "u", Password: "p"}})
	if err != nil { t.Fatal(err) }
	resp, err := server.App().Test(httptest.NewRequest(http.MethodGet, "/health", nil))
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { t.Fatalf("health status = %d", resp.StatusCode) }
}
