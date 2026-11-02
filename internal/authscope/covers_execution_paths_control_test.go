package authscope

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

// Durable coverage control for CoversExecutionPaths (F-B20 review finding F-2).
// The subset-membership loop in scope_resolver.go is the only code that
// denies an execution declaring a path the restricted scope does not hold.
// The adversarial re-review (mutant C: membership loop neutered, guards kept)
// showed NO shipped repo test discriminated it. These controls replicate the
// reviewer probe semantics and pin every case where the subset loop is the
// sole discriminator.

const fb20CoversSentinelPath = "\x00all-generations"

func fb20CoversRestricted(paths ...string) ReadScope {
	return ReadScope{AuthResourcePaths: paths, Mode: ReadScopeRestricted}
}

// Subset-loop SOLE-DISCRIMINATOR cases: every one of these must flip when the
// membership loop is removed (mutant C). The mode and declared-empty guards
// alone answer every other cell of the truth table, so a test suite that
// passes here while missing these leaves the gate unguarded.
func TestCoversExecutionPathsSubsetLoopIsOnlyDiscriminatorControl(t *testing.T) {
	cases := []struct {
		name     string
		scope    ReadScope
		declared []string
		want     bool
	}{
		{"scope-missing-second-declared-denies", fb20CoversRestricted("p1"), []string{"p1", "p2"}, false},
		{"scope-superset-declared-subset-serves", fb20CoversRestricted("p1", "p2"), []string{"p1"}, true},
		{"cross-generation-style-disjoint-denies", fb20CoversRestricted("pA"), []string{"pB"}, false},
		{"prefix-trap-child-declared-denies", fb20CoversRestricted("p1"), []string{"p1/secret"}, false},
		{"prefix-trap-parent-declared-denies", fb20CoversRestricted("p1/secret"), []string{"p1"}, false},
		{"equal-count-disjoint-denies", fb20CoversRestricted("p1", "p2"), []string{"p3", "p4"}, false},
		{"mixed-declared-one-foreign-denies", fb20CoversRestricted("pA"), []string{"pA", "pB"}, false},
		{"sentinel-scope-denies-ordinary-declared", fb20CoversRestricted(fb20CoversSentinelPath), []string{"p1"}, false},
		{"restricted-empty-scope-denies-declaring", fb20CoversRestricted(), []string{"p1"}, false},
		{"restricted-nil-scope-denies-declaring", ReadScope{Mode: ReadScopeRestricted}, []string{"p1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.CoversExecutionPaths(tc.declared); got != tc.want {
				t.Fatalf("CoversExecutionPaths(%v) with scope mode=%q paths=%v = %v, want %v",
					tc.declared, tc.scope.Mode, tc.scope.AuthResourcePaths, got, tc.want)
			}
		})
	}
}

// Full truth table replicating the reviewer probe: passes within the subset
// rules (duplicates, whitespace/slash normalization, blank-only declared),
// mode matrix fail-closed, and control-character refusal upstream.
func TestCoversExecutionPathsSubsetTruthTableControl(t *testing.T) {
	cases := []struct {
		name     string
		scope    ReadScope
		declared []string
		want     bool
	}{
		{"restricted-exact-single", fb20CoversRestricted("p1"), []string{"p1"}, true},
		{"restricted-superset-scope", fb20CoversRestricted("p1", "p2"), []string{"p1"}, true},
		{"restricted-missing-path-denies", fb20CoversRestricted("p1"), []string{"p1", "p2"}, false},
		{"restricted-all-missing-denies", fb20CoversRestricted("p9"), []string{"p1"}, false},
		{"restricted-dup-declared-served", fb20CoversRestricted("p1"), []string{"p1", "p1"}, true},
		{"restricted-dup-scope-served", fb20CoversRestricted("p1", "p1"), []string{"p1"}, true},
		{"restricted-exec-nil-paths-denies", fb20CoversRestricted("p1"), nil, false},
		{"restricted-exec-empty-paths-denies", fb20CoversRestricted("p1"), []string{}, false},
		{"restricted-exec-blank-only-denies", fb20CoversRestricted("p1"), []string{"", "  "}, false},
		{"restricted-exec-mixed-partial-blank-served", fb20CoversRestricted("p1"), []string{"p1", "", " "}, true},
		{"restricted-scope-empty-denies-declaring", fb20CoversRestricted(), []string{"p1"}, false},
		{"restricted-scope-empty-denies-empty-exec", fb20CoversRestricted(), nil, false},
		{"prefix-not-conflated", fb20CoversRestricted("p1"), []string{"p1/secret"}, false},
		{"trailing-slash-normalizes", fb20CoversRestricted("/p1/"), []string{"p1"}, true},
		{"whitespace-normalizes", fb20CoversRestricted(" p1 "), []string{"p1 "}, true},
		{"whitespace-dup-scope-served", fb20CoversRestricted("p1", " p1 "), []string{" p1"}, true},
		{"unrestricted-direct-denies", ReadScope{AuthResourcePaths: []string{"p1"}, Mode: ReadScopeUnrestricted}, []string{"p1"}, false},
		{"unrestricted-superset-denies", ReadScope{AuthResourcePaths: []string{"p1", "p2"}, Mode: ReadScopeUnrestricted}, []string{"p1"}, false},
		{"unrestricted-nil-scope-denies", ReadScope{Mode: ReadScopeUnrestricted}, []string{"p1"}, false},
		{"empty-mode-direct-denies", ReadScope{AuthResourcePaths: []string{"p1"}}, []string{"p1"}, false},
		{"empty-mode-empty-declared-denies", ReadScope{}, nil, false},
		{"unknown-mode-direct-denies", ReadScope{AuthResourcePaths: []string{"p1"}, Mode: ReadScopeMode("garbage")}, []string{"p1"}, false},
		{"sentinel-declared-only-denies", fb20CoversRestricted("p1"), []string{fb20CoversSentinelPath}, false},
		{"sentinel-scope-does-not-cover", fb20CoversRestricted(fb20CoversSentinelPath), []string{"p1"}, false},
		{"nul-bearing-shared-string-still-denies", fb20CoversRestricted("p\x001"), []string{"p\x001"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.CoversExecutionPaths(tc.declared); got != tc.want {
				t.Fatalf("CoversExecutionPaths(%v) with scope mode=%q paths=%v = %v, want %v",
					tc.declared, tc.scope.Mode, tc.scope.AuthResourcePaths, got, tc.want)
			}
		})
	}
}

// Resolver-driven cross-generation control: a scope resolved for one dataset
// generation must never cover an execution declared in a sibling generation.
// Each subtest builds its own resolver and lister so no mutable state is
// shared across subtests (race-clean by construction).
func TestCoversExecutionPathsGenerationBoundControl(t *testing.T) {
	cases := []struct {
		name     string
		declared []string
		want     bool
	}{
		{"home-generation-path-covered", []string{"pA"}, true},
		{"foreign-generation-path-denied", []string{"pB"}, false},
		{"mixed-generations-denied", []string{"pA", "pB"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			byGen := map[[2]string][]string{
				{"P", "gA"}: {"pA"},
				{"P", "gB"}: {"pB"},
			}
			var calls []catalog.AuthResourcePathOptions
			lister := func(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
				calls = append(calls, opts)
				return append([]string(nil), byGen[[2]string{opts.Project, opts.DatasetGeneration}]...), nil
			}
			res := NewScopeResolver(ScopeResolverConfig{ListExistingAuthResourcePaths: lister})
			principal := &Principal{Subject: "s", Projects: []string{"P"}, AuthResourcePaths: []string{"pA", "pB"}}
			scopeA, err := res.ResolveReadScopeForGeneration(context.Background(), principal, "P", "gA", nil)
			if err != nil {
				t.Fatalf("resolve gA: %v", err)
			}
			if scopeA.Mode != ReadScopeRestricted {
				t.Fatalf("expected restricted scope, got %q", scopeA.Mode)
			}
			for i, c := range calls {
				if c.AllGenerations {
					t.Fatalf("call %d: read path issued AllGenerations=true (project %q gen %q)", i, c.Project, c.DatasetGeneration)
				}
			}
			if got := scopeA.CoversExecutionPaths(tc.declared); got != tc.want {
				t.Fatalf("gA scope %v CoversExecutionPaths(%v) = %v, want %v",
					scopeA.AuthResourcePaths, tc.declared, got, tc.want)
			}
		})
	}
}
