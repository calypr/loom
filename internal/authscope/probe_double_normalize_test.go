package authscope

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestProbeDeclaredPathNormalizationIsIdempotent(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
	}{
		{"r4-mixed-valid-and-nul", []string{"p1", "foreign\x00secret"}},
		{"nul-only", []string{"\x00"}},
		{"del-interior", []string{"for\x7feign"}},
		{"c0-sweep", func() []string {
			out := make([]string, 0, 0x20)
			for b := 0; b < 0x20; b++ {
				out = append(out, fmt.Sprintf("p1%csecret", b))
			}
			return out
		}()},
		{"tab-padded-valid", []string{"\tp1\t"}},
		{"whitespace-only", []string{"   "}},
		{"nil-list", nil},
		{"empty-list", []string{}},
		{"slash-only", []string{"/"}},
		{"multi-slash", []string{"///"}},
		{"trailing-slash", []string{"p1/"}},
		{"duplicates", []string{"p1", "p1", " p1 "}},
		{"program-project-path", []string{"/api/programs/prog1/projects/p1/datasets"}},
		{"program-project-bare", []string{"programs/prog1/projects/p1"}},
		{"dot-segments", []string{"./p1/../p2"}},
		{"sentinel", []string{fb20CoversSentinelPath}},
		{"unicode-whitespace-padded", []string{"\u00a0p1"}},
		{"valid-multi", []string{"p1", "p2"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			once, malformedOnce := normalizeDeclaredExecutionPaths(tc.paths)
			if _, again := normalizeDeclaredExecutionPaths(tc.paths); again != malformedOnce {
				t.Fatalf("declared-path refusal is not deterministic for %#v", tc.paths)
			}
			if !malformedOnce {
				twice, malformedTwice := normalizeDeclaredExecutionPaths(once)
				if malformedTwice {
					t.Fatalf("second declared-path pass refused an accepted declaration %q", once)
				}
				if len(once) != len(twice) {
					t.Fatalf("second declared-path pass dropped entries: first=%q second=%q", once, twice)
				}
				for i := range once {
					if once[i] != twice[i] {
						t.Fatalf("second declared-path pass mutated entries: first=%q second=%q", once, twice)
					}
				}
			}

			listOnce := normalizeAuthResourcePathList(tc.paths)
			listTwice := normalizeAuthResourcePathList(listOnce)
			if len(listOnce) != len(listTwice) {
				t.Fatalf("second list-normalizer pass dropped entries: first=%q second=%q", listOnce, listTwice)
			}
			for i := range listOnce {
				if listOnce[i] != listTwice[i] {
					t.Fatalf("second list-normalizer pass mutated entries: first=%q second=%q", listOnce, listTwice)
				}
			}

			for _, path := range once {
				if again := NormalizeAuthResourcePath(path); again != path {
					t.Fatalf("declared path %q is not a fixed point: re-normalized to %q", path, again)
				}
			}
		})
	}
}

func TestProbeIdempotencePreservesCoverageDecisions(t *testing.T) {
	cases := []struct {
		name       string
		scope      ReadScope
		declared   []string
		wantCovers bool
	}{
		{
			name:       "r4-mixed-valid-and-nul-denies",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{"p1"}},
			declared:   []string{"p1", "foreign\x00secret"},
			wantCovers: false,
		},
		{
			name:       "r4-reversed-order-denies",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{"p1"}},
			declared:   []string{"foreign\x7fsecret", "p1"},
			wantCovers: false,
		},
		{
			name:       "c0-in-second-entry-denies",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{"p1", "p2"}},
			declared:   []string{"p1", "p2\x00x"},
			wantCovers: false,
		},
		{
			name:       "valid-covered-serves",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{"p1", "p2"}},
			declared:   []string{"p1", "p2"},
			wantCovers: true,
		},
		{
			name:       "tab-padded-covered-serves",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{"p1", "p2"}},
			declared:   []string{" p1 ", "\tp2\t"},
			wantCovers: true,
		},
		{
			name:       "program-project-sentinel-scope-denies",
			scope:      ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: []string{fb20CoversSentinelPath}},
			declared:   []string{"p1"},
			wantCovers: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.CoversExecutionPaths(tc.declared); got != tc.wantCovers {
				t.Fatalf("CoversExecutionPaths(%q) = %v, want %v", tc.declared, got, tc.wantCovers)
			}
			reparsed, malformed := normalizeDeclaredExecutionPaths(tc.declared)
			if malformed {
				reparsed = append([]string(nil), tc.declared...)
			}
			if got := tc.scope.CoversExecutionPaths(reparsed); got != tc.wantCovers {
				t.Fatalf("CoversExecutionPaths on re-normalized declaration %q = %v, want %v", reparsed, got, tc.wantCovers)
			}
			listPass := normalizeAuthResourcePathList(tc.declared)
			if tc.wantCovers {
				if got := tc.scope.CoversExecutionPaths(listPass); !got {
					t.Fatalf("grant-side normalization flipped a coverage to denial: %q", listPass)
				}
			}
		})
	}
}

func TestProbeUnicodeWhitespaceAndC1GapCharacterization(t *testing.T) {
	restricted := func(paths ...string) ReadScope {
		return ReadScope{Mode: ReadScopeRestricted, AuthResourcePaths: paths}
	}

	t.Run("unicode-space-padding-is-symmetric", func(t *testing.T) {
		pads := []string{"\u00a0", "\u0085", "\u2000", "\u205f", "\u3000", "\t", " "}
		for _, pad := range pads {
			declared := []string{pad + "p1" + pad}
			if !restricted("p1").CoversExecutionPaths(declared) {
				t.Fatalf("padding %q: declaration %q must cover the clean grant (symmetric trim)", pad, declared[0])
			}
			if !restricted(pad + "p1" + pad).CoversExecutionPaths([]string{"p1"}) {
				t.Fatalf("padding %q: padded grant must cover the clean declaration (symmetric trim)", pad)
			}
			if restricted("p1").CoversExecutionPaths([]string{pad}) {
				t.Fatalf("padding %q alone must stay blank policy and deny", pad)
			}
		}
	})

	t.Run("c1-controls-are-kept-never-dropped", func(t *testing.T) {
		for _, marker := range []string{"\u0080", "\u0086", "\u009f", "\u200b", "\u2060"} {
			sneaky := "p1" + marker + "secret"
			if got := NormalizeAuthResourcePath(sneaky); got != sneaky {
				t.Fatalf("entry %q must survive normalization verbatim, got %q", sneaky, got)
			}
			if restricted("p1").CoversExecutionPaths([]string{"p1", sneaky}) {
				t.Fatalf("DISCLOSURE: clean grants covered C1-bearing declaration %q", sneaky)
			}
			if restricted("p1", "p2").CoversExecutionPaths([]string{"p1", sneaky, "p2"}) {
				t.Fatalf("DISCLOSURE: grants covered mixed declaration carrying %q", sneaky)
			}
		}
	})
}

func TestProbeUnrestrictedCallerStillTraversesCoverageGate(t *testing.T) {
	newResolver := func() *ScopeResolver {
		return NewScopeResolver(ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
				return []string{"p1", "p2"}, nil
			},
		})
	}
	unrestricted := &Principal{Subject: "root", Projects: []string{"P"}}

	t.Run("malformed-declaration-routes-through-the-gate", func(t *testing.T) {
		declared := []string{"p1", "p2\x00secret"}
		scope, err := newResolver().ResolveReadScopeForGeneration(context.Background(), unrestricted, "P", "g1", declared)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if scope.Mode != ReadScopeRestricted {
			t.Fatalf("mode = %q, want Restricted so the reader gate decides", scope.Mode)
		}
		if !reflect.DeepEqual(scope.AuthResourcePaths, []string{"p1"}) {
			t.Fatalf("scope paths = %q — the truncated list must not serve as a row filter for a refused declaration", scope.AuthResourcePaths)
		}
		if scope.CoversExecutionPaths(declared) {
			t.Fatal("DISCLOSURE: the refused declaration is covered for an unrestricted caller too")
		}
	})

	t.Run("blank-declaration-keeps-unrestricted-mode", func(t *testing.T) {
		scope, err := newResolver().ResolveReadScopeForGeneration(context.Background(), unrestricted, "P", "g1", []string{"", " "})
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if scope.Mode != ReadScopeUnrestricted {
			t.Fatalf("mode = %q, want Unrestricted for a blank-only declaration", scope.Mode)
		}
	})
}
