package authscope

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/projectid"
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
			ListExistingAuthResourcePaths: func(context.Context, AuthResourcePathOptions) ([]string, error) {
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

func fb27ctlSeed(r *ScopeResolver, project, datasetGeneration string, allGenerations bool, paths ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache[scopeCacheKey{project: project, datasetGeneration: datasetGeneration, allGenerations: allGenerations}] = cachedPaths{
		paths:     append([]string(nil), paths...),
		expiresAt: time.Now().Add(time.Minute),
	}
}

func fb27ctlForbiddingLister(t *testing.T) func(context.Context, AuthResourcePathOptions) ([]string, error) {
	t.Helper()
	return func(context.Context, AuthResourcePathOptions) ([]string, error) {
		t.Fatalf("VERDICT=UNEXPECTED cache-resident decision re-consulted the catalog")
		return nil, nil
	}
}

func TestFB27ScopeCacheNamespacesDoNotLeakAcrossDiscriminator(t *testing.T) {
	resolver := NewScopeResolver(ScopeResolverConfig{
		CacheTTL:                      time.Minute,
		ListExistingAuthResourcePaths: fb27ctlForbiddingLister(t),
	})
	fb27ctlSeed(resolver, "projA", "gen-1", false, "from-generation-key")
	fb27ctlSeed(resolver, "projA", "", true, "from-all-generations-key")
	ctx := context.Background()

	generationPaths, err := resolver.listExistingPaths(ctx, "projA", "gen-1")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation-scoped lookup = %v", err)
	}
	if !reflect.DeepEqual(generationPaths, []string{"from-generation-key"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=false lookup returned %q, want the generation entry only", generationPaths)
	}
	allGenerationsPaths, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations lookup = %v", err)
	}
	if !reflect.DeepEqual(allGenerationsPaths, []string{"from-all-generations-key"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=true lookup returned %q, want the all-generations entry only", allGenerationsPaths)
	}
	t.Logf("VERDICT=REPAIRED both namespaces served their own entry with no catalog call")
}

func TestFB27GenerationLookupNeverServedFromAllGenerationsEntry(t *testing.T) {
	l := &fb27ctlLister{behind: func(AuthResourcePathOptions) ([]string, error) {
		return []string{"fresh-generation-entry"}, nil
	}}
	resolver := fb27ctlResolver(l)
	fb27ctlSeed(resolver, "projA", "", true, "all-generations-only")
	ctx := context.Background()

	paths, err := resolver.listExistingPaths(ctx, "projA", "")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED empty-generation lookup = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the mode=false lookup was answered from the mode=true entry without consulting the catalog")
	}
	if !reflect.DeepEqual(paths, []string{"fresh-generation-entry"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=false lookup = %q, want the freshly listed generation entry", paths)
	}
}

func TestFB27AllGenerationsLookupNeverServedFromGenerationEntry(t *testing.T) {
	l := &fb27ctlLister{behind: func(AuthResourcePathOptions) ([]string, error) {
		return []string{"fresh-all-generations-entry"}, nil
	}}
	resolver := fb27ctlResolver(l)
	fb27ctlSeed(resolver, "projA", "", false, "generation-only")
	ctx := context.Background()

	paths, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations lookup = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the mode=true lookup was answered from the mode=false entry without consulting the catalog")
	}
	if !reflect.DeepEqual(paths, []string{"fresh-all-generations-entry"}) {
		t.Fatalf("VERDICT=UNEXPECTED mode=true lookup = %q, want the freshly listed all-generations entry", paths)
	}
}

func TestFB27LiteralSentinelGenerationIsAnOrdinaryKey(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{"projA/data"}, nil
		}
		return []string{"gen-only-path"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("projA/data")
	ctx := context.Background()

	first, err := resolver.listExistingPaths(ctx, "projA", fb27ctlSentinelGeneration)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED sentinel-literal generation listing = %v", err)
	}
	if !reflect.DeepEqual(first, []string{"gen-only-path"}) {
		t.Fatalf("VERDICT=UNEXPECTED sentinel-literal generation listing = %q", first)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED the first sentinel-literal generation listing should hit the catalog once, calls=%d", l.calls)
	}
	second, err := resolver.listExistingPaths(ctx, "projA", fb27ctlSentinelGeneration)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED cached sentinel-literal generation listing = %v", err)
	}
	if !reflect.DeepEqual(second, first) || l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED the literal generation must cache like any ordinary generation: calls=%d paths=%q", l.calls, second)
	}
	if got := l.opts[0]; got.Project != "projA" || got.DatasetGeneration != fb27ctlSentinelGeneration || got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED the literal must reach the catalog as a plain generation, got %+v", got)
	}

	allGenerations, err := resolver.listExistingPathsAllGenerations(ctx, "projA")
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED all-generations listing = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the all-generations lookup reused the literal-keyed entry (calls=%d)", l.calls)
	}
	if !reflect.DeepEqual(allGenerations, []string{"projA/data"}) {
		t.Fatalf("VERDICT=UNEXPECTED all-generations listing = %q", allGenerations)
	}
	if err := resolver.AuthorizeWrite(ctx, principal, "projA", "projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project write after the literal-keyed read = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=UNEXPECTED the write decision must reuse the mode=true entry, calls=%d", l.calls)
	}
	t.Logf("VERDICT=REPAIRED %q behaved as a plain generation key and never substituted for the project-binding entry", fb27ctlSentinelGeneration)
}

func TestFB27AuthoritativeProjectBindingProducerUsesModeTrueOnly(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {
			return []string{"projA/data"}, nil
		}
		return []string{"gen-only-path"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := resolver.AuthorizeWrite(ctx, principal, "HTAN_INT/cohort", " /projA/data "); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project member write = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED write decision issued %d listings, want 1", l.calls)
	}
	if got := l.opts[0]; got.Project != "HTAN_INT-cohort" || got.DatasetGeneration != "" || !got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED the sole mode=true writer must send an empty generation with AllGenerations, got %+v", got)
	}
	if err := resolver.AuthorizeWrite(ctx, principal, "HTAN_INT/cohort", "projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED cached same-project member write = %v", err)
	}
	if l.calls != 1 {
		t.Fatalf("VERDICT=UNEXPECTED warm write must reuse the mode=true entry, calls=%d", l.calls)
	}

	scope, err := resolver.ResolveReadScopeForGeneration(ctx, principal, "HTAN_INT/cohort", "", nil)
	if err != nil {
		t.Fatalf("VERDICT=UNEXPECTED empty-generation read scope = %v", err)
	}
	if l.calls != 2 {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the (mode,false) lookup reused the project-binding entry: calls=%d", l.calls)
	}
	if got := l.opts[1]; got.Project != "HTAN_INT-cohort" || got.DatasetGeneration != "" || got.AllGenerations {
		t.Fatalf("VERDICT=UNEXPECTED generation-scoped read lookup options = %+v", got)
	}
	if scope.Mode != ReadScopeRestricted || len(scope.AuthResourcePaths) != 0 {
		t.Fatalf("VERDICT=UNEXPECTED read scope must intersect the generation listing (gen-only-path), got %+v", scope)
	}
	t.Logf("VERDICT=REPAIRED the all-generations producer is the only mode=true writer and read lookups stay on the false side")
}

const (
	msGrantedPath  = "p1"
	msMalformedNUL = "foreign\x00secret"
)

func msRestricted(paths ...string) ReadScope {
	return ReadScope{AuthResourcePaths: paths, Mode: ReadScopeRestricted}
}

func TestCoversExecutionPathsRejectsInvalidNonblankDeclaration(t *testing.T) {
	cases := []struct {
		name     string
		scope    ReadScope
		declared []string
		want     bool
	}{

		{"r4-mixed-valid-and-nul-denies", msRestricted(msGrantedPath), []string{msGrantedPath, msMalformedNUL}, false},
		{"r4-mixed-order-reversed-denies", msRestricted(msGrantedPath), []string{msMalformedNUL, msGrantedPath}, false},
		{"r4-mixed-with-malformed-entry-in-scope-too-denies", msRestricted(msGrantedPath, msMalformedNUL), []string{msGrantedPath, msMalformedNUL}, false},
		{"mixed-valid-blank-and-nul-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "", " ", msMalformedNUL}, false},
		{"malformed-entry-whitespace-padded-still-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "  " + msMalformedNUL + "  "}, false},
		{"nul-only-entry-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "\x00"}, false},
		{"nul-prefixed-valid-path-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "\x00" + msGrantedPath}, false},
		{"nul-suffixed-valid-path-denies", msRestricted(msGrantedPath), []string{msGrantedPath, msGrantedPath + "\x00"}, false},
		{"del-bearing-entry-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "foreign\x7fsecret"}, false},
		{"del-only-entry-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "\x7f"}, false},
		{"interior-tab-is-a-control-byte-denies", msRestricted(msGrantedPath), []string{msGrantedPath, msGrantedPath + "\tx"}, false},
		{"interior-newline-is-a-control-byte-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "p1\np2"}, false},
		{"interior-vertical-tab-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "p1\vp2"}, false},
		{"slash-only-entry-normalizes-empty-and-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "/"}, false},
		{"multi-slash-entry-normalizes-empty-and-denies", msRestricted(msGrantedPath), []string{msGrantedPath, "//"}, false},
		{"malformed-entry-besides-foreign-path-denies", msRestricted(msGrantedPath, "p2"), []string{msGrantedPath, "p2", msMalformedNUL}, false},

		{"valid-single-still-serves", msRestricted(msGrantedPath), []string{msGrantedPath}, true},
		{"valid-multi-covered-still-serves", msRestricted(msGrantedPath, "p2"), []string{msGrantedPath, "p2"}, true},
		{"valid-multi-covered-duplicates-and-slashes-still-serves", msRestricted(msGrantedPath, "p2"), []string{msGrantedPath, "p2", "p2", " /p2/ "}, true},
		{"valid-entry-outside-declaration-still-serves", msRestricted(msGrantedPath, "p9"), []string{msGrantedPath}, true},
		{"trailing-tab-is-trimmed-whitespace-serves", msRestricted(msGrantedPath), []string{msGrantedPath, msGrantedPath + "\t"}, true},

		{"blank-only-declaration-still-denies", msRestricted(msGrantedPath), []string{"", "  "}, false},
		{"valid-plus-blank-still-serves", msRestricted(msGrantedPath), []string{msGrantedPath, "", " ", "\t", "\n", "\r", "\v", "\f"}, true},
		{"nil-declaration-still-denies", msRestricted(msGrantedPath), nil, false},
		{"empty-declaration-still-denies", msRestricted(msGrantedPath), []string{}, false},
		{"foreign-valid-path-still-denies", msRestricted(msGrantedPath), []string{"p2"}, false},

		{"unrestricted-mode-malformed-denies", ReadScope{AuthResourcePaths: []string{msGrantedPath}, Mode: ReadScopeUnrestricted}, []string{msGrantedPath, msMalformedNUL}, false},
		{"empty-mode-malformed-denies", ReadScope{AuthResourcePaths: []string{msGrantedPath}}, []string{msGrantedPath, msMalformedNUL}, false},
		{"unknown-mode-malformed-denies", ReadScope{AuthResourcePaths: []string{msGrantedPath}, Mode: ReadScopeMode("garbage")}, []string{msGrantedPath, msMalformedNUL}, false},
		{"restricted-empty-scope-malformed-denies", msRestricted(), []string{msGrantedPath, msMalformedNUL}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.scope.CoversExecutionPaths(tc.declared); got != tc.want {
				t.Fatalf("CoversExecutionPaths(%s) with mode=%q scope=%s = %v, want %v",
					msJoinQuoted(tc.declared), tc.scope.Mode, msJoinQuoted(tc.scope.AuthResourcePaths), got, tc.want)
			}
		})
	}

	t.Run("every-forbidden-control-byte-refuses-declaration", func(t *testing.T) {
		forbidden := make([]byte, 0, 0x21)
		for b := 0x00; b <= 0x1f; b++ {
			forbidden = append(forbidden, byte(b))
		}
		forbidden = append(forbidden, 0x7f)
		for _, b := range forbidden {
			control := string(rune(b))
			whitespaceOnly := strings.TrimSpace(control) == ""
			forms := [][]string{
				{msGrantedPath, "foreign" + control + "secret"},
				{msGrantedPath, msGrantedPath + control + "x"},
			}
			if !whitespaceOnly {
				forms = append(forms, []string{msGrantedPath, control})
			}
			for _, declared := range forms {
				if msRestricted(msGrantedPath).CoversExecutionPaths(declared) {
					t.Fatalf("declaration %s (control byte %s) was covered by scope [p1]; a forbidden control byte must refuse the whole declaration",
						msJoinQuoted(declared), strconv.Quote(control))
				}
			}
			if whitespaceOnly {
				if !msRestricted(msGrantedPath).CoversExecutionPaths([]string{msGrantedPath, control}) {
					t.Fatalf("declaration [p1, %s] was refused although the entry is whitespace-only; the legitimate blank policy must be preserved",
						strconv.Quote(control))
				}
			}
		}
	})
}

func TestCoversExecutionPathsMalformedRefusalIsSoleDiscriminator(t *testing.T) {
	scope := msRestricted(msGrantedPath)
	if !scope.CoversExecutionPaths([]string{msGrantedPath}) {
		t.Fatal("precondition: scope must cover the clean single-path declaration")
	}
	if !scope.CoversExecutionPaths([]string{msGrantedPath, "", " ", "\t"}) {
		t.Fatal("precondition: blank padding must not change the decision")
	}
	if scope.CoversExecutionPaths([]string{msGrantedPath, msMalformedNUL}) {
		t.Fatal("R4 REGRESSION: adding the malformed non-blank entry left the declaration covered — the refusal is the sole discriminator")
	}
}

func TestCoversExecutionPathsResolverBackedMalformedDeclarationDrivesTheGate(t *testing.T) {
	lister := func(_ context.Context, opts AuthResourcePathOptions) ([]string, error) {
		if opts.Project != "legacy-project" || opts.DatasetGeneration != "g1" {
			t.Fatalf("unexpected catalog lookup %+v", opts)
		}
		return []string{msGrantedPath}, nil
	}
	resolver := NewScopeResolver(ScopeResolverConfig{ListExistingAuthResourcePaths: lister})
	principal := &Principal{Subject: "member", Projects: []string{"legacy-project"}, AuthResourcePaths: []string{msGrantedPath}}
	declared := []string{msGrantedPath, msMalformedNUL}

	scope, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, "legacy-project", "g1", declared)
	if err != nil {
		t.Fatalf("resolver must still resolve the caller's own grant without erroring on the persisted declaration, got %v", err)
	}
	if scope.Mode != ReadScopeRestricted {
		t.Fatalf("scope mode = %q, want restricted", scope.Mode)
	}
	if got := strings.Join(scope.AuthResourcePaths, ","); got != msGrantedPath {
		t.Fatalf("resolver echoed back %q; it returns the clean subset, proving CoversExecutionPaths is the disclosure gate", got)
	}
	if scope.CoversExecutionPaths(declared) {
		t.Fatal("R4 REGRESSION: a resolver-echoed restricted scope covered a persisted declaration holding a malformed non-blank path")
	}
	if !scope.CoversExecutionPaths([]string{msGrantedPath}) {
		t.Fatal("over-rejection: the same scope must still cover the clean declaration")
	}
}

func TestGrantNormalizationRemainsUnchanged(t *testing.T) {
	if got := NormalizeAuthResourcePaths([]string{msGrantedPath, msMalformedNUL, "", " /p2/ "}); strings.Join(got, "|") != "p1|p2" {
		t.Fatalf("grant normalization changed: got %q, want p1|p2 (malformed entries dropped, not fatal)", strings.Join(got, "|"))
	}
	if got := NormalizeAuthResourcePath(msMalformedNUL); got != "" {
		t.Fatalf("NormalizeAuthResourcePath(%q) = %q, want the empty refusal", msMalformedNUL, got)
	}
	principal := &Principal{Subject: "member", Projects: []string{"P"}, AuthResourcePaths: []string{msGrantedPath, msMalformedNUL}}
	lister := func(context.Context, AuthResourcePathOptions) ([]string, error) {
		return []string{msGrantedPath}, nil
	}
	resolver := NewScopeResolver(ScopeResolverConfig{ListExistingAuthResourcePaths: lister})
	scope, err := resolver.ResolveReadScopeForGeneration(context.Background(), principal, "P", "g1", nil)
	if err != nil {
		t.Fatalf("restricted resolution for a principal holding a malformed grant must still succeed, got %v", err)
	}
	if scope.Mode != ReadScopeRestricted || strings.Join(scope.AuthResourcePaths, ",") != msGrantedPath {
		t.Fatalf("grant-side posture changed: mode=%q paths=%q", scope.Mode, scope.AuthResourcePaths)
	}
}

func msJoinQuoted(values []string) string {
	if values == nil {
		return "<nil>"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Quote(value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

const fb27ctlSentinelGeneration = "\x00all-generations"

type fb27ctlLister struct {
	mu     sync.Mutex
	calls  int
	opts   []AuthResourcePathOptions
	behind func(AuthResourcePathOptions) ([]string, error)
}

func (l *fb27ctlLister) list(_ context.Context, opts AuthResourcePathOptions) ([]string, error) {
	result, err := l.behind(opts)
	l.mu.Lock()
	l.calls++
	l.opts = append(l.opts, opts)
	l.mu.Unlock()
	return result, err
}

func (l *fb27ctlLister) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func (l *fb27ctlLister) snapshotCalls() []AuthResourcePathOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AuthResourcePathOptions(nil), l.opts...)
}

func (l *fb27ctlLister) lastCall() (AuthResourcePathOptions, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.opts) == 0 {
		return AuthResourcePathOptions{}, false
	}
	return l.opts[len(l.opts)-1], true
}

func fb27ctlResolver(l *fb27ctlLister) *ScopeResolver {
	return NewScopeResolver(ScopeResolverConfig{
		CacheTTL:                      time.Minute,
		ListExistingAuthResourcePaths: l.list,
	})
}

func fb27ctlPrincipal(paths ...string) *Principal {
	return &Principal{Subject: "u", AuthResourcePaths: append([]string(nil), paths...)}
}

func TestFB27SentinelGenerationPoisonCannotReachAuthorizeWrite(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations {

			return []string{"projB/data"}, nil
		}

		return nil, nil
	}}
	r := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := r.AuthorizeWrite(ctx, principal, "projB", "/projA/data"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED unpoisoned cross-project write = %v, want ErrForbidden", err)
	}
	r.InvalidateProject("projB")

	if _, err := r.ResolveReadScopeForGeneration(ctx, principal, "projB", fb27ctlSentinelGeneration, nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED poisoning read (result irrelevant) = %v", err)
	}
	callsAfterPoison := l.count()

	err := r.AuthorizeWrite(ctx, principal, "projB", "/projA/data")
	if l.count() == callsAfterPoison {
		if err == nil {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): F-B27 BYPASS CONFIRMED: sentinel cache collision let a projA-scoped principal write /projA/data into projB whose recorded path is projB/data")
		}
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): the write decision was answered from the entry the poisoned read populated (no lister call) with err=%v", err)
	}
	all := l.snapshotCalls()
	if len(all) == 0 {
		t.Fatal("VERDICT=UNEXPECTED no catalog discovery call recorded")
	}
	last, ok := l.lastCall()
	if !ok {
		t.Fatal("VERDICT=UNEXPECTED no catalog discovery call recorded")
	}
	if !last.AllGenerations || last.DatasetGeneration != "" || last.Project != "projB" {
		t.Fatalf("VERDICT=UNEXPECTED project-binding lookup must be all-generations mode with an empty generation for projB, got %+v (all opts %+v)", last, all)
	}
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("VERDICT=UNEXPECTED warmed cross-project write = %v, want ErrForbidden", err)
	}
	t.Logf("VERDICT=REPAIRED sentinel-literal generation %q stayed an ordinary generation key; the write decision re-listed projB (%d fresh call) and denied /projA/data", fb27ctlSentinelGeneration, l.count()-callsAfterPoison)
}

func TestFB27ConcurrentDistinctKeyBurstKeepsSharedRecorderConsistent(t *testing.T) {
	const goroutines = 64
	l := &fb27ctlLister{behind: func(opts AuthResourcePathOptions) ([]string, error) {

		if opts.AllGenerations {
			return []string{opts.Project + "/data"}, nil
		}
		return nil, nil
	}}
	r := fb27ctlResolver(l)
	ctx := context.Background()

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			project := "proj" + strconv.Itoa(i)

			if _, err := r.listExistingPaths(ctx, project, "gen-"+strconv.Itoa(i)); err != nil {
				errCh <- err
				return
			}
			if _, err := r.listExistingPathsAllGenerations(ctx, project); err != nil {
				errCh <- err
				return
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("VERDICT=UNEXPECTED concurrent resolver error = %v", err)
	}

	if got := l.count(); got != 2*goroutines {
		t.Fatalf("VERDICT=UNEXPECTED lost/torn recorder increments: count=%d want=%d", got, 2*goroutines)
	}
	snapshot := l.snapshotCalls()
	if len(snapshot) != l.count() {
		t.Fatalf("VERDICT=UNEXPECTED recorder desync: snapshot=%d count=%d", len(snapshot), l.count())
	}
	seenProjects := map[string]int{}
	for _, opts := range snapshot {
		seenProjects[opts.Project]++
	}
	if len(seenProjects) != goroutines {
		t.Fatalf("VERDICT=UNEXPECTED expected %d distinct projects recorded, got %d", goroutines, len(seenProjects))
	}
	t.Logf("VERDICT=HARDENED | concurrent distinct-key burst drove %d goroutines through the shared recorder: %d invocations, %d keys, race-clean", goroutines, l.count(), len(seenProjects))
}

const fb20CoversSentinelPath = "\x00all-generations"

func fb20CoversRestricted(paths ...string) ReadScope {
	return ReadScope{AuthResourcePaths: paths, Mode: ReadScopeRestricted}
}

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
			var calls []AuthResourcePathOptions
			lister := func(_ context.Context, opts AuthResourcePathOptions) ([]string, error) {
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

func TestUnrestrictedRequiresExplicitUnrestrictedMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode ReadScopeMode
		want bool
	}{
		{"explicit-unrestricted", ReadScopeUnrestricted, true},
		{"restricted", ReadScopeRestricted, false},
		{"empty", ReadScopeMode(""), false},
		{"garbage", ReadScopeMode("garbage"), false},
		{"wrong-case-restricted", ReadScopeMode("RESTRICTED"), false},
		{"simulated-bypass-string", ReadScopeMode("unrestricted "), false},
	} {
		if got := (ReadScope{Mode: tc.mode}).Unrestricted(); got != tc.want {
			t.Fatalf("Unrestricted() for mode %q (%s) = %v, want %v", tc.mode, tc.name, got, tc.want)
		}
	}
}

func TestFB27ResolverNeverEmitsTheConflictingShape(t *testing.T) {
	l := &fb27ctlLister{behind: func(opts AuthResourcePathOptions) ([]string, error) {
		if opts.AllGenerations && projectid.NormalizeGeneration(opts.DatasetGeneration) != "" {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): resolver emitted the rejected option combination %+v", opts)
		}
		return []string{"projA/data"}, nil
	}}
	resolver := fb27ctlResolver(l)
	principal := fb27ctlPrincipal("/projA/data")
	ctx := context.Background()

	if err := resolver.AuthorizeWrite(ctx, principal, "projA", "/projA/data"); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED same-project member write = %v", err)
	}
	if _, err := resolver.ResolveReadScopeForGeneration(ctx, principal, "projA", " gen-1 ", nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation read scope = %v", err)
	}
	if _, err := resolver.ResolveWriteScopeForGeneration(ctx, principal, "projA", "gen-2", nil); err != nil {
		t.Fatalf("VERDICT=UNEXPECTED generation write scope = %v", err)
	}
	if l.calls != 3 {
		t.Fatalf("VERDICT=UNEXPECTED discovery calls = %d, want 3 ( %+v)", l.calls, l.opts)
	}
	if !l.opts[0].AllGenerations || l.opts[0].DatasetGeneration != "" {
		t.Fatalf("VERDICT=UNEXPECTED project-binding request = %+v", l.opts[0])
	}
	if l.opts[1].AllGenerations || l.opts[1].DatasetGeneration != "gen-1" {
		t.Fatalf("VERDICT=UNEXPECTED read request = %+v", l.opts[1])
	}
	if l.opts[2].AllGenerations || l.opts[2].DatasetGeneration != "gen-2" {
		t.Fatalf("VERDICT=UNEXPECTED write-scope request = %+v", l.opts[2])
	}
	t.Logf("VERDICT=REPAIRED resolver requests stay on one side of the F-2 boundary")
}
