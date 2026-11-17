package recipeapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/recipe/schema"
)

func capturedOptions(t *testing.T, authScopeMode string) catalog.PopulatedFieldOptions {
	t.Helper()
	var got catalog.PopulatedFieldOptions
	read := func(_ context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		got = opts
		return nil, nil
	}
	if _, err := NewFieldDiscovery(read).Fields(context.Background(), schema.Scope{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authScopeMode,
	}, "Observation"); err != nil {
		t.Fatalf("Fields(mode=%q): %v", authScopeMode, err)
	}
	return got
}

func TestFieldDiscoveryEveryAuthScopeModeArrivesExplicit(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		want bool
	}{
		{"unrestricted", string(authscope.ReadScopeUnrestricted), true},
		{"restricted", string(authscope.ReadScopeRestricted), false},
		{"empty", "", false},
		{"garbage", "garbage", false},
		{"wrong-case", "RESTRICTED", false},
	} {
		opts := capturedOptions(t, tc.mode)
		if opts.AuthResourcePathsUnrestricted == nil {
			t.Fatalf("mode %q reached the catalog with a nil explicit pointer; the legacy empty-means-unrestricted rule then escalates an empty path list", tc.name)
		}
		if *opts.AuthResourcePathsUnrestricted != tc.want {
			t.Fatalf("mode %q: unrestricted=%v want %v", tc.name, *opts.AuthResourcePathsUnrestricted, tc.want)
		}
		if effective := catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted); effective != tc.want {
			t.Fatalf("mode %q: effective=%v want %v", tc.name, effective, tc.want)
		}
	}
}

func TestFieldDiscoveryUnknownModeMatchesCoversDeny(t *testing.T) {
	unknown := authscope.ReadScope{AuthResourcePaths: nil, Mode: authscope.ReadScopeMode("garbage")}
	if unknown.Unrestricted() {
		t.Fatal("authscope.IsUnrestricted(garbage) must be false")
	}
	opts := capturedOptions(t, "garbage")
	if catalog.EffectiveAuthResourcePathsUnrestricted(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted) {
		t.Fatal("discovery escalated an unknown auth scope mode to unrestricted catalog reads")
	}
}
