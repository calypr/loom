package authscope

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

// Durable control for the persisted-declaration fails-closed rule (R4).
//
// Reproduction. A stored execution declares ["p1","foreign\x00secret"] — a
// persisted auth_resource_paths list whose second entry is malformed.
// normalizeAuthResourcePathList drops that entry, so the declaration collapsed
// onto the single grantable path "p1" and a member holding only p1 was served
// execution metadata. The malformed entry asserts a path the system can never
// name, list, or grant (NormalizeAuthResourcePath refuses forbidden C0/DEL
// bytes precisely because the \x00-joined scope digests would fold distinct
// grant lists), so a declaration containing one is not a resolvable
// authorization at all and must be refused rather than silently truncated.
//
// Scope of the rule. Only the DECLARATION a read is authorized against gets
// the stricter treatment. Incoming caller/grant normalization is deliberately
// unchanged (see TestGrantNormalizationRemainsUnchanged), and whitespace-only
// entries keep their long-standing "absent" meaning.

const (
	msGrantedPath  = "p1"
	msMalformedNUL = "foreign\x00secret"
)

func msRestricted(paths ...string) ReadScope {
	return ReadScope{AuthResourcePaths: paths, Mode: ReadScopeRestricted}
}

// TestCoversExecutionPathsRejectsInvalidNonblankDeclaration is the required
// regression: a mixed valid-plus-invalid declaration is refused outright, every
// forbidden C0/DEL control byte is refused, and both the valid covered policy
// and the legitimate blank policy keep their pre-existing answers.
func TestCoversExecutionPathsRejectsInvalidNonblankDeclaration(t *testing.T) {
	cases := []struct {
		name     string
		scope    ReadScope
		declared []string
		want     bool
	}{
		// The confirmed R4 reproduction: the surviving valid path alone must not
		// cover a declaration that also asserts a malformed path.
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

		// Valid covered declarations keep serving exactly as before.
		{"valid-single-still-serves", msRestricted(msGrantedPath), []string{msGrantedPath}, true},
		{"valid-multi-covered-still-serves", msRestricted(msGrantedPath, "p2"), []string{msGrantedPath, "p2"}, true},
		{"valid-multi-covered-duplicates-and-slashes-still-serves", msRestricted(msGrantedPath, "p2"), []string{msGrantedPath, "p2", "p2", " /p2/ "}, true},
		{"valid-entry-outside-declaration-still-serves", msRestricted(msGrantedPath, "p9"), []string{msGrantedPath}, true},
		{"trailing-tab-is-trimmed-whitespace-serves", msRestricted(msGrantedPath), []string{msGrantedPath, msGrantedPath + "\t"}, true},

		// The legitimate blank policy is untouched: whitespace-only entries
		// (including the C0 whitespace bytes TrimSpace recognizes) mean absent.
		{"blank-only-declaration-still-denies", msRestricted(msGrantedPath), []string{"", "  "}, false},
		{"valid-plus-blank-still-serves", msRestricted(msGrantedPath), []string{msGrantedPath, "", " ", "\t", "\n", "\r", "\v", "\f"}, true},
		{"nil-declaration-still-denies", msRestricted(msGrantedPath), nil, false},
		{"empty-declaration-still-denies", msRestricted(msGrantedPath), []string{}, false},
		{"foreign-valid-path-still-denies", msRestricted(msGrantedPath), []string{"p2"}, false},

		// Malformed declarations fail closed under every non-restricted mode too.
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

	// Exhaustive forbidden-control sweep: for every C0 byte and DEL, an interior
	// occurrence must refuse the whole mixed declaration, and a bare
	// non-whitespace control entry must refuse it too. Whitespace-only entries
	// stay legitimate blanks, so they are asserted not to flip the decision.
	// The swept set is exactly what containsAuthPathControlChar refuses:
	// 0x00-0x1f plus 0x7f. Printable bytes must not be asserted to refuse, and
	// 0x20 is excluded because a bare space entry is a legitimate blank (pinned
	// by the table cases above) whose padded forms are ordinary foreign paths —
	// asserting refusal there would pass through the subset-membership loop
	// rather than the malformed-entry rule this sweep exists to pin.
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

// TestCoversExecutionPathsMalformedRefusalIsSoleDiscriminator pins that the
// malformed-entry refusal is what denies the R4 read: the identical scope covers
// the declaration with the malformed entry removed and with blank padding
// instead, so the denial cannot come from the mode guard, the blank guard, or
// the subset-membership loop.
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

// TestCoversExecutionPathsResolverBackedMalformedDeclarationDrivesTheGate runs
// the decision through the production resolver rather than a hand-built scope:
// a restricted caller granted p1, a catalog that records p1, and an execution
// declaring the mixed valid-plus-NUL list. The resolver echoes back the clean
// subset, so only the declaration-side refusal can deny the read.
func TestCoversExecutionPathsResolverBackedMalformedDeclarationDrivesTheGate(t *testing.T) {
	lister := func(_ context.Context, opts catalog.AuthResourcePathOptions) ([]string, error) {
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

// TestGrantNormalizationRemainsUnchanged guards the stated non-goal: the
// stricter rule applies to persisted execution declarations only. Incoming
// caller/grant normalization keeps dropping what it always dropped instead of
// refusing the whole grant list.
func TestGrantNormalizationRemainsUnchanged(t *testing.T) {
	if got := NormalizeAuthResourcePaths([]string{msGrantedPath, msMalformedNUL, "", " /p2/ "}); strings.Join(got, "|") != "p1|p2" {
		t.Fatalf("grant normalization changed: got %q, want p1|p2 (malformed entries dropped, not fatal)", strings.Join(got, "|"))
	}
	if got := NormalizeAuthResourcePath(msMalformedNUL); got != "" {
		t.Fatalf("NormalizeAuthResourcePath(%q) = %q, want the empty refusal", msMalformedNUL, got)
	}
	principal := &Principal{Subject: "member", Projects: []string{"P"}, AuthResourcePaths: []string{msGrantedPath, msMalformedNUL}}
	lister := func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
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
