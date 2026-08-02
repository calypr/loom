package server

import (
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestWireAuthNoAuthOverridesConfiguredMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.Mode = "calypr"
	components, err := wireAuth(cfg, true, arangostore.ConnectionOptions{})
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
	if _, err := wireAuth(cfg, false, arangostore.ConnectionOptions{}); err == nil {
		t.Fatal("unknown auth mode unexpectedly accepted")
	}
}

func TestRecipeScopeDigestIsOrderIndependent(t *testing.T) {
	a := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthResourcePaths: []string{"b", "a"}}
	b := a
	b.AuthResourcePaths = []string{"a", "b"}
	if got, want := recipeScopeDigest(a), recipeScopeDigest(b); got != want {
		t.Fatalf("scope digest changed with path order: %q != %q", got, want)
	}
}
