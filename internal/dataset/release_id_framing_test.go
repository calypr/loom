package dataset

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Defense-in-depth controls for the releaseID content-addressed framing
// (release.go). This is a HARDENING suite, NOT a defect repair: the fold
// classes below are proven-unreachable through the public surface today.
// Project and Generation reject control bytes at ref.go:26-30
// (validateOpaqueIdentifier), selector components are confined to the
// injective DataframeSelector.Key() encoding (dataframe_selector.go:34), and
// ExecutionID arrives from the publication store as an opaque id a caller of
// ReleaseService.Create cannot forge on this path. The controls therefore
// replay the archived fold classes against the historical framing to keep the
// fault premise honest (they still fold there) while pinning that the shipped
// length-prefixed framing separates them, and prove the new framing is
// injective by construction — so the property does not depend on those upstream
// validators staying in place. Patterns reuse /tmp/opencode/relid-repro
// (foldOptions / foldID and the exhaustive Key()-injective probe).

// historicalReleaseIDFrame replays the pre-hardening framing EXACTLY as
// shipped: bare "\x00" separators and an un-length-delimited ExecutionID. It
// pins the fault premise (the archived fold pairs collapse HERE) while
// production releaseIDFraming must separate them.
func historicalReleaseIDFrame(release ProjectRelease) string {
	var b strings.Builder
	b.WriteString(release.Project)
	b.WriteByte(0)
	b.WriteString(release.Generation)
	for _, publication := range release.Publications {
		b.WriteByte(0)
		b.WriteString(publication.Selector.Key())
		b.WriteByte(0)
		b.WriteString(publication.ExecutionID)
	}
	return b.String()
}

// ridSortedPubs mirrors the canonical ordering ReleaseService.Create applies
// (sort by Selector.Key(), dedup by Key at release.go:224-228) so the tests
// reason about the publication SET, which is what releaseID content-addresses.
func ridSortedPubs(pubs []ReleasePublication) []ReleasePublication {
	byKey := map[string]ReleasePublication{}
	for _, p := range pubs {
		byKey[p.Selector.Key()] = p
	}
	out := make([]ReleasePublication, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Selector.Key() < out[j].Selector.Key() })
	return out
}

func ridRelease(project, generation string, pubs ...ReleasePublication) ProjectRelease {
	return ProjectRelease{Project: project, Generation: generation, GitCommit: generation, Publications: ridSortedPubs(pubs)}
}

func ridPub(recipe, tv, output, exec string) ReleasePublication {
	return ReleasePublication{Selector: DataframeSelector{Recipe: recipe, TranslationVersion: tv, Output: output}, ExecutionID: exec}
}

// ridContentLabel is an unambiguous canonicalization of the content that feeds
// releaseID; a collision between two DIFFERENT labels under one id is the
// injectivity failure we hunt. %q keeps the label itself separator-safe.
func ridContentLabel(release ProjectRelease) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q|%q|", release.Project, release.Generation)
	for _, p := range release.Publications {
		fmt.Fprintf(&b, "{%q,%q,%q,%q};", p.Selector.Recipe, p.Selector.TranslationVersion, p.Selector.Output, p.ExecutionID)
	}
	return b.String()
}

// decodeReleaseIDFrame reconstructs (Project, Generation, Publications) from
// the exact bytes releaseIDFraming emits. Its existence is the constructive
// injectivity proof: a total left-inverse means two distinct contents can
// never share a framing string. readLen mirrors the repro's decodeKey
// canonical-digit rules so a spoofed length prefix is rejected, not silently
// accepted.
func decodeReleaseIDFrame(frame []byte) (ProjectRelease, bool) {
	rest := string(frame)
	var release ProjectRelease
	var ok bool
	if release.Project, rest, ok = readLen(rest); !ok {
		return ProjectRelease{}, false
	}
	if release.Generation, rest, ok = readLen(rest); !ok {
		return ProjectRelease{}, false
	}
	for len(rest) > 0 {
		var selector DataframeSelector
		if selector.Recipe, rest, ok = readLen(rest); !ok {
			return ProjectRelease{}, false
		}
		if selector.TranslationVersion, rest, ok = readLen(rest); !ok {
			return ProjectRelease{}, false
		}
		if selector.Output, rest, ok = readLen(rest); !ok {
			return ProjectRelease{}, false
		}
		var exec string
		if exec, rest, ok = readLen(rest); !ok {
			return ProjectRelease{}, false
		}
		release.Publications = append(release.Publications, ReleasePublication{Selector: selector, ExecutionID: exec})
	}
	if rest != "" {
		return ProjectRelease{}, false
	}
	return release, true
}

func readLen(s string) (string, string, bool) {
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", "", false
	}
	digits := s[:colon]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return "", "", false
		}
	}
	if len(digits) > 1 && digits[0] == '0' {
		return "", "", false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || len(s) < colon+1+n {
		return "", "", false
	}
	return s[colon+1 : colon+1+n], s[colon+1+n:], true
}

// TestReleaseIDHistoricalFramingFoldWitnessesPinnedByFix replays the two
// archived fold classes. Both COLLAPSE under historicalReleaseIDFrame (the
// premise) and SEPARATE under releaseID + releaseIDFraming (the repair).
func TestReleaseIDHistoricalFramingFoldWitnessesPinnedByFix(t *testing.T) {
	// Class A: a NUL smuggled across the Project/Generation bare-join edge.
	// (Public Create rejects control bytes in project/generation, so this is
	// the defense-in-depth / upstream-weakening surface, kept honest.)
	classAA := ridRelease("p\x00q", "g")
	classAB := ridRelease("p", "q\x00g")
	if historicalReleaseIDFrame(classAA) != historicalReleaseIDFrame(classAB) {
		t.Fatalf("fault premise broken: historical Project/Generation join must fold %q and %q", historicalReleaseIDFrame(classAA), historicalReleaseIDFrame(classAB))
	}
	if string(releaseIDFraming(classAA)) == string(releaseIDFraming(classAB)) || releaseID(classAA) == releaseID(classAB) {
		t.Fatalf("REGRESSION: length-prefixed framing folds the Project/Generation NUL pair (frame %q)", string(releaseIDFraming(classAA)))
	}

	// Class B: the release-specific class — an un-delimited ExecutionID lets
	// one publication's bytes impersonate the next (Key(), ExecutionID) group
	// (forgeable on this layer, the closest thing to a live surface).
	leftover := "a\x00" + ridPub("r", "t", "o", "").Selector.Key() + "\x00"
	classBA := ridRelease("p", "g", ridPub("R", "T", "O", leftover))
	classBB := ridRelease("p", "g", ridPub("R", "T", "O", "a"), ridPub("r", "t", "o", ""))
	if ridPub("R", "T", "O", "").Selector.Key() >= ridPub("r", "t", "o", "").Selector.Key() {
		t.Fatalf("witness precondition broken: the R-… selector must sort before the r-… selector")
	}
	if historicalReleaseIDFrame(classBA) != historicalReleaseIDFrame(classBB) {
		t.Fatalf("fault premise broken: historical un-delimited ExecutionID must fold the release pair:\n  A=%q\n  B=%q", historicalReleaseIDFrame(classBA), historicalReleaseIDFrame(classBB))
	}
	if string(releaseIDFraming(classBA)) == string(releaseIDFraming(classBB)) || releaseID(classBA) == releaseID(classBB) {
		t.Fatalf("REGRESSION: length-prefixed framing folds the ExecutionID un-delimited pair (frame %q)", string(releaseIDFraming(classBA)))
	}
	if got, ok := decodeReleaseIDFrame(releaseIDFraming(classBA)); !ok || ridContentLabel(ridNormalize(got)) != ridContentLabel(classBA) {
		t.Fatalf("decode round-trip failed for class B: ok=%v got=%+v", ok, got)
	}
	t.Logf("VERDICT=REPAIRED both archived fold classes collapse under the historical bare join yet are separated by releaseID")
}

// ridNormalize re-canonicalizes a decoded release the way Create orders it, so
// decoded publications compare by the canonical sorted/deduped set.
func ridNormalize(r ProjectRelease) ProjectRelease {
	r.Publications = ridSortedPubs(r.Publications)
	return r
}

// TestReleaseIDFramingIsInjectiveSmallAlphabet EXHAUSTIVELY enumerates
// Project/Generation against 0/1 publication releases over the breach alphabet
// {a, ':', NUL, digit}, then exhaustively enumerates 2-publication releases
// (fixed Project/Generation) over that publication space, and 2-publication
// releases over a Project/Generation cross with a capped publication set. Every
// distinct content must map to a distinct framing string, a distinct releaseID,
// and round-trip through the decoder.
func TestReleaseIDFramingIsInjectiveSmallAlphabet(t *testing.T) {
	components := ridBuildComponents([]string{"a", ":", "\x00", "1"}, 1)
	// pgSet drives Project/Generation, both written by writeLengthPrefixed. The
	// multibyte tokens (CJK/emoji/accented — LEGAL under validateOpaqueIdentifier,
	// which only requires valid-UTF8 + no-control, ref.go:26-30 / validation.go:31-44)
	// kill the length-vs-rune-count mutant class: a RuneCountInString encoder
	// mis-sizes these prefixes and the decode-inverse check below rejects it.
	pgSet := ridBuildComponents([]string{"a", ":", "\x00", "0", "あ", "🎯", "é"}, 2)

	type pub struct{ recipe, tv, output, exec string }
	var pubSpace []pub
	for _, r := range components {
		for _, tv := range components {
			for _, o := range components {
				for _, e := range []string{"", "a", ":", "\x00"} {
					pubSpace = append(pubSpace, pub{r, tv, o, e})
				}
			}
		}
	}

	frames := map[string]string{}
	ids := map[string]string{}
	checked := 0

	add := func(project, generation string, list []pub) {
		made := make([]ReleasePublication, 0, len(list))
		for _, p := range list {
			made = append(made, ridPub(p.recipe, p.tv, p.output, p.exec))
		}
		release := ridRelease(project, generation, made...)
		label := ridContentLabel(release)
		frame := string(releaseIDFraming(release))
		id := releaseID(release)
		decoded, ok := decodeReleaseIDFrame([]byte(frame))
		if !ok || ridContentLabel(ridNormalize(decoded)) != label {
			t.Fatalf("decoder/round-trip failed for %s: ok=%v decoded=%s", label, ok, ridContentLabel(decoded))
		}
		if prior, seen := frames[frame]; seen && prior != label {
			t.Fatalf("REGRESSION (fold): frame %q shared by %q and %q", frame, prior, label)
		}
		frames[frame] = label
		if prior, seen := ids[id]; seen && prior != label {
			t.Fatalf("REGRESSION (digest fold): releaseID %s shared by %q and %q", id, prior, label)
		}
		ids[id] = label
		checked++
	}

	// P/G cross over 0/1 publication releases: exercises the Project/Generation
	// boundary and single-publication framing at every delimiter value.
	for _, project := range pgSet {
		for _, generation := range pgSet {
			add(project, generation, nil)
			for _, one := range pubSpace {
				add(project, generation, []pub{one})
			}
		}
	}
	// Exhaustive 2-publication fold space at a representative Project/Generation.
	for _, one := range pubSpace {
		for _, two := range pubSpace {
			add("p\x00q", "g", []pub{one, two})
		}
	}
	// 2-publication releases that also vary Project/Generation over a small set.
	pairSet := pubSpace[:min(len(pubSpace), 20)]
	for _, project := range []string{"p", "\x00", "a:b"} {
		for _, generation := range []string{"g", "\x00", ":"} {
			for _, one := range pairSet {
				for _, two := range pairSet {
					add(project, generation, []pub{one, two})
				}
			}
		}
	}
	t.Logf("VERDICT=HARDENED | exhaustive small-alphabet injectivity: %d distinct-content releases, zero framing/digest folds, all decoded", checked)
}

// ridBuildComponents returns all strings formed from the alphabet with length
// 0..maxLen, deterministic ordering.
func ridBuildComponents(alphabet []string, maxLen int) []string {
	out := []string{""}
	level := []string{""}
	for depth := 1; depth <= maxLen; depth++ {
		var next []string
		for _, prefix := range level {
			for _, char := range alphabet {
				next = append(next, prefix+char)
			}
		}
		out = append(out, next...)
		level = next
	}
	return out
}

// TestReleaseIDFramingInjectiveKeySpace reuses the repro's exhaustive
// Key-space probe shape (TestProbeKeyEncoderInjective): enumerate selector
// triples over a delimiter-heavy alphabet down to depth 3, embed each in a
// release, and decode the framing back. Because decodeReleaseIDFrame is a total
// left-inverse, decode(encode(x))==x for the ENTIRE enumerated domain proves
// the framing is injective on it without materialising a 64M-entry map. The
// alphabet depth is bounded to protect the gate budget while still driving NUL,
// ':', and digit bytes through the self-delimiting Key().
func TestReleaseIDFramingInjectiveKeySpace(t *testing.T) {
	components := ridBuildComponents([]string{"a", "1", ":", "\x00", "-"}, 3)
	count := 0
	for _, r := range components {
		for _, tv := range components {
			for _, o := range components {
				release := ridRelease("proj", "gen", ridPub(r, tv, o, "exec"))
				label := ridContentLabel(release)
				decoded, ok := decodeReleaseIDFrame(releaseIDFraming(release))
				if !ok || ridContentLabel(ridNormalize(decoded)) != label {
					t.Fatalf("key-space decode failed: selector={%q,%q,%q} ok=%v got=%s", r, tv, o, ok, ridContentLabel(decoded))
				}
				count++
			}
		}
	}
	t.Logf("VERDICT=HARDENED | exhaustive Key-space probe: %d selector triples over a delimiter-heavy alphabet (incl NUL/':'), decode-inverse injected", count)
}

// TestReleaseIDFramingInjectiveRandomProbe is the bounded 64M-style random probe
// (pattern reuse from /tmp/opencode/relid-repro): randomized delimiter-laden
// releases must stay injective. The decode-inverse check is the correctness
// proof; the collision map is a cheap direct fold-hunt kept for the report
// count. Sample size is capped to protect the gate budget.
func TestReleaseIDFramingInjectiveRandomProbe(t *testing.T) {
	const samples = 300_000
	// Multibyte tokens (CJK/emoji/accented) plus digit-colon traps: the former
	// exercise the writeLengthPrefixed length-vs-rune-count class on Project,
	// Generation AND the opaque ExecutionID; the latter ('2:ab','4:a\x00b', the
	// '00:x' leading-zero decoy) stress the readLen ':'/canonical-digit parse at
	// every field position. All multibyte bytes are legal under
	// validateOpaqueIdentifier (valid-UTF8 + no-control only).
	pool := []string{"", "a", "b", ":", "\x00", "1", "2", "a\x00b", "\x00:", "::", "0", "-",
		"あ", "🎯", "é", "Ω", "2:ab", "4:a\x00b", "00:x"}
	randomString := func(r *rand.Rand) string {
		n := r.Intn(4)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(pool[r.Intn(len(pool))])
		}
		return b.String()
	}
	rng := rand.New(rand.NewSource(0xC0FFEE))
	seen := map[string]string{}
	for i := 0; i < samples; i++ {
		pubCount := rng.Intn(3)
		made := make([]ReleasePublication, 0, pubCount)
		for j := 0; j < pubCount; j++ {
			made = append(made, ridPub(randomString(rng), randomString(rng), randomString(rng), randomString(rng)))
		}
		release := ridRelease(randomString(rng), randomString(rng), made...)
		label := ridContentLabel(release)
		frame := string(releaseIDFraming(release))
		decoded, ok := decodeReleaseIDFrame([]byte(frame))
		if !ok || ridContentLabel(ridNormalize(decoded)) != label {
			t.Fatalf("random decode failed for %q: ok=%v", label, ok)
		}
		if prior, already := seen[frame]; already && prior != label {
			t.Fatalf("REGRESSION (random fold): frame %q shared by %q and %q", frame, prior, label)
		}
		seen[frame] = label
	}
	t.Logf("VERDICT=HARDENED | bounded random probe: %d delimiter-laden releases, %d distinct frames, zero folds, all decoded", samples, len(seen))
}
