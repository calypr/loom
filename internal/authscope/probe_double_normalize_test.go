package authscope

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

// probeIdempotentNormalizations enumerates every normalization applied to the
// persisted declared paths and proves each one is idempotent. CoversExecution-
// Paths feeds a declaration through normalizeDeclaredExecutionPaths and then
// normalizeAuthResourcePathList, and separately passes the caller grant list
// through normalizeAuthResourcePathList, so "a second pass drops an entry the
// first pass kept" would let a declaration silently lose a path and be covered
// by a narrower grant. Idempotence of every pass rules that class of bug out:
// normalizing an already-normalized value is a fixed point.
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
			// A refused declaration never re-enters the pipeline (the gate
			// denies on the spot), so re-feeding a malformed first-pass *output*
			// is not a system flow. The real invariants are: the refusal is
			// deterministic on the original declaration, and a kept output is a
			// fixed point.
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

			// The list normalizer is idempotent too (it is the second pass on
			// kept entries, and the sole pass on grants).
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

			// Each surviving declared path is itself a fixed point of the
			// single-value normalizer, i.e. the declaration-side gate and the
			// grant-side normalizer agree on identical spellings.
			for _, path := range once {
				if again := NormalizeAuthResourcePath(path); again != path {
					t.Fatalf("declared path %q is not a fixed point: re-normalized to %q", path, again)
				}
			}
		})
	}
}

// TestProbeIdempotencePreservesCoverageDecisions locks the security-relevant
// consequence: because normalization is idempotent, re-passing both sides of
// the coverage gate cannot turn a denial into a coverage (no entry can be
// "dropped by the second pass" to make a declaration look covered).
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
			// The decision must be stable when the declaration is re-rendered
			// through the same normalization the gate itself uses: a second
			// pass can neither smuggle a denial into coverage nor the reverse.
			reparsed, malformed := normalizeDeclaredExecutionPaths(tc.declared)
			if malformed {
				reparsed = append([]string(nil), tc.declared...)
			}
			if got := tc.scope.CoversExecutionPaths(reparsed); got != tc.wantCovers {
				t.Fatalf("CoversExecutionPaths on re-normalized declaration %q = %v, want %v", reparsed, got, tc.wantCovers)
			}
			// Same again through the grant-side normalizer, proving neither
			// normalization order can flip a coverage decision.
			listPass := normalizeAuthResourcePathList(tc.declared)
			if tc.wantCovers {
				if got := tc.scope.CoversExecutionPaths(listPass); !got {
					t.Fatalf("grant-side normalization flipped a coverage to denial: %q", listPass)
				}
			}
		})
	}
}

// TestProbeUnicodeWhitespaceAndC1GapCharacterization characterizes the two
// unicode corners of the fails-closed rule and proves neither is a disclosure
// path.
//
// Corner 1 (unicode whitespace): strings.TrimSpace implements unicode.IsSpace,
// so NBSP/NEL/U+2000-series/U+3000 padding is dropped as "absent" on BOTH the
// declaration side and the grant side (both route through the same helpers).
// Because the trimming is symmetric, a declaration padded with a unicode space
// and a grant padded with the same (or ASCII) space resolve to the identical
// key — coverage can neither leak nor over-deny from padding asymmetry.
//
// Corner 2 (C1 controls, e.g. U+0086): not unicode whitespace, and no byte is
// <0x20 or ==0x7f, so NormalizeAuthResourcePath keeps such an entry verbatim
// instead of refusing it — the "gap" versus the C0/DEL rule. The gap is safe
// precisely because the entry is KEPT, not dropped: covering it requires a
// grant holding the byte-identical path, so the default is denial. Disclosure
// would need the entry to be silently dropped, which only happens for blanks
// (legitimate) and C0/DEL-or-empty normalizations (now refused).
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
			// A padded padding-only declaration stays the blank policy: no
			// non-blank path -> not a resolved authorization.
			if restricted("p1").CoversExecutionPaths([]string{pad}) {
				t.Fatalf("padding %q alone must stay blank policy and deny", pad)
			}
		}
	})

	t.Run("c1-controls-are-kept-never-dropped", func(t *testing.T) {
		for _, marker := range []string{"\u0080", "\u0086", "\u009f", "\u200b", "\u2060"} {
			sneaky := "p1" + marker + "secret"
			// Kept verbatim: the normalizer preserves the C1 bytes rather than
			// dropping the entry.
			if got := NormalizeAuthResourcePath(sneaky); got != sneaky {
				t.Fatalf("entry %q must survive normalization verbatim, got %q", sneaky, got)
			}
			// Denial is the default: a grant of the clean prefix does not cover
			// the C1-bearing declaration.
			if restricted("p1").CoversExecutionPaths([]string{"p1", sneaky}) {
				t.Fatalf("DISCLOSURE: clean grants covered C1-bearing declaration %q", sneaky)
			}
			// And a mixed declaration stays denied even when every other entry
			// is granted.
			if restricted("p1", "p2").CoversExecutionPaths([]string{"p1", sneaky, "p2"}) {
				t.Fatalf("DISCLOSURE: grants covered mixed declaration carrying %q", sneaky)
			}
		}
	})
}

// TestProbeUnrestrictedCallerStillTraversesCoverageGate pins the mechanism
// behind the R4 reader verdict for callers that hold no scope grants at all
// (restricted == false in resolveCallerPaths). Such a caller is not bypassed:
// a non-empty declared list still yields a Restricted scope carrying the
// normalized declaration, so the reader's restricted-coverage gate runs and
// CoversExecutionPaths decides. A malformed declaration therefore stays
// unreadable for everyone, and the truncated normalized list can never be
// laundered into a row filter for it. A blank-only declaration keeps the
// legitimate Unrestricted mode.
func TestProbeUnrestrictedCallerStillTraversesCoverageGate(t *testing.T) {
	newResolver := func() *ScopeResolver {
		return NewScopeResolver(ScopeResolverConfig{
			ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
				return []string{"p1", "p2"}, nil
			},
		})
	}
	// No grants and no Fence client: resolveCallerPaths reports restricted=false.
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
