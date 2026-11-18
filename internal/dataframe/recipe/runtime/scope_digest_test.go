package runtime

import (
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRecipeScopeDigestIsOrderIndependent(t *testing.T) {
	a := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthResourcePaths: []string{"b", "a"}}
	b := a
	b.AuthResourcePaths = []string{"a", "b"}
	if got, want := ScopeDigest(a), ScopeDigest(b); got != want {
		t.Fatalf("scope digest changed with path order: %q != %q", got, want)
	}
}

func TestRecipeScopeDigestDistinguishesRestrictedEmptyScope(t *testing.T) {
	unrestricted := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthScopeMode: authscope.ReadScopeUnrestricted}
	restricted := unrestricted
	restricted.AuthScopeMode = authscope.ReadScopeRestricted
	if ScopeDigest(unrestricted) == ScopeDigest(restricted) {
		t.Fatal("restricted-empty and unrestricted bindings shared a scope digest")
	}
}
