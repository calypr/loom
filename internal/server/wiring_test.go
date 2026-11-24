package server

import (
	"os"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
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
	source, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "dataframeapi.NewRecipeExecutionReader(releases, scopes, nil)") {
		t.Fatal("HTTP registration no longer constructs the recipe execution reader")
	}
}
