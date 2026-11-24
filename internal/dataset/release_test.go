package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func legacyReleaseID(release ProjectRelease) string {
	digest := sha256.Sum256([]byte(historicalReleaseIDFrame(release)))
	return "release_" + hex.EncodeToString(digest[:])
}

func ridRelease(project, generation string, pubs ...ReleasePublication) ProjectRelease {
	return ProjectRelease{Project: project, Generation: generation, GitCommit: generation, Publications: pubs}
}

func ridProbeRelease(project, generation string, pubs []ReleasePublication, vers []ContractVerification) ProjectRelease {
	release := ridRelease(project, generation, pubs...)
	release.RequiredVerifications = vers
	return release
}

func ridPub(recipe, tv, output, exec string) ReleasePublication {
	return ReleasePublication{Selector: DataframeSelector{Recipe: recipe, TranslationVersion: tv, Output: output}, ExecutionID: exec}
}

func ridSelector(recipe, tv, output string) DataframeSelector {
	return DataframeSelector{Recipe: recipe, TranslationVersion: tv, Output: output}
}

var (
	ridInstant   = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	ridOtherZone = ridInstant.In(time.FixedZone("UTC+5:45", 5*3600+45*60))
	ridLater     = ridInstant.Add(time.Second)
)

var ridTimePool = []time.Time{{}, ridInstant, ridOtherZone, ridLater}

func ridTimeLabel(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func ridContentLabel(release ProjectRelease) string {
	pubLabels := make([]string, 0, len(release.Publications))
	for _, publication := range release.Publications {
		pubLabels = append(pubLabels, ridPublicationLabel(publication))
	}
	verLabels := make([]string, 0, len(release.RequiredVerifications))
	for _, verification := range release.RequiredVerifications {
		verLabels = append(verLabels, ridVerificationLabel(verification))
	}
	sort.Strings(pubLabels)
	sort.Strings(verLabels)
	return fmt.Sprintf("P=%q G=%q C=%q pubs=[%s] vers=[%s]",
		release.Project, release.Generation, release.GitCommit,
		strings.Join(pubLabels, ","), strings.Join(verLabels, ","))
}

func ridPublicationLabel(publication ReleasePublication) string {
	return fmt.Sprintf("{%q,%q,%q,%q,%q,%d,%d,%q}",
		publication.Selector.Recipe, publication.Selector.TranslationVersion, publication.Selector.Output,
		publication.ExecutionID, publication.Generation,
		boolDigit(publication.Required), boolDigit(publication.Stale), ridTimeLabel(publication.VerifiedAt))
}

func ridVerificationLabel(verification ContractVerification) string {
	return fmt.Sprintf("{%q,%q,%q,%q,%q,%q,%d,%q,%q}",
		verification.Selector.Recipe, verification.Selector.TranslationVersion, verification.Selector.Output,
		verification.ExecutionID, verification.Generation, verification.State,
		boolDigit(verification.Queryable), ridTimeLabel(verification.VerifiedAt), verification.ErrorCode)
}

func ridClone(release ProjectRelease) ProjectRelease {
	clone := release
	if release.Publications != nil {
		clone.Publications = append(make([]ReleasePublication, 0, len(release.Publications)), release.Publications...)
	}
	if release.RequiredVerifications != nil {
		clone.RequiredVerifications = append(make([]ContractVerification, 0, len(release.RequiredVerifications)), release.RequiredVerifications...)
	}
	return clone
}

func boolDigit(value bool) int {
	if value {
		return 1
	}
	return 0
}

type decodedRelease struct {
	project       string
	generation    string
	gitCommit     string
	publications  []decodedPublication
	verifications []decodedVerification
}

type decodedPublication struct {
	recipe, translationVersion, output string
	executionID, generation            string
	required, stale                    bool
	verifiedAt                         string
}

type decodedVerification struct {
	recipe, translationVersion, output string
	executionID, generation, state     string
	queryable                          bool
	verifiedAt, errorCode              string
}

func (d decodedRelease) label() string {
	pubLabels := make([]string, 0, len(d.publications))
	for _, publication := range d.publications {
		pubLabels = append(pubLabels, fmt.Sprintf("{%q,%q,%q,%q,%q,%d,%d,%q}",
			publication.recipe, publication.translationVersion, publication.output,
			publication.executionID, publication.generation,
			boolDigit(publication.required), boolDigit(publication.stale), publication.verifiedAt))
	}
	verLabels := make([]string, 0, len(d.verifications))
	for _, verification := range d.verifications {
		verLabels = append(verLabels, fmt.Sprintf("{%q,%q,%q,%q,%q,%q,%d,%q,%q}",
			verification.recipe, verification.translationVersion, verification.output,
			verification.executionID, verification.generation, verification.state,
			boolDigit(verification.queryable), verification.verifiedAt, verification.errorCode))
	}
	sort.Strings(pubLabels)
	sort.Strings(verLabels)
	return fmt.Sprintf("P=%q G=%q C=%q pubs=[%s] vers=[%s]",
		d.project, d.generation, d.gitCommit,
		strings.Join(pubLabels, ","), strings.Join(verLabels, ","))
}

func decodeReleaseIDFrame(frame []byte) (decodedRelease, bool) {
	rest := string(frame)
	if !strings.HasPrefix(rest, releaseIDDomain) {
		return decodedRelease{}, false
	}
	rest = strings.TrimPrefix(rest, releaseIDDomain)
	var decoded decodedRelease
	var ok bool
	if decoded.project, rest, ok = readLen(rest); !ok {
		return decodedRelease{}, false
	}
	if decoded.generation, rest, ok = readLen(rest); !ok {
		return decodedRelease{}, false
	}
	if decoded.gitCommit, rest, ok = readLen(rest); !ok {
		return decodedRelease{}, false
	}
	var section string
	if section, rest, ok = readLen(rest); !ok {
		return decodedRelease{}, false
	}
	publications, err := strconv.Atoi(section)
	if err != nil || publications < 0 {
		return decodedRelease{}, false
	}
	for i := 0; i < publications; i++ {
		var publication decodedPublication
		for _, target := range []*string{&publication.recipe, &publication.translationVersion, &publication.output, &publication.executionID, &publication.generation} {
			if *target, rest, ok = readLen(rest); !ok {
				return decodedRelease{}, false
			}
		}
		if publication.required, rest, ok = readBool(rest); !ok {
			return decodedRelease{}, false
		}
		if publication.stale, rest, ok = readBool(rest); !ok {
			return decodedRelease{}, false
		}
		if publication.verifiedAt, rest, ok = readLen(rest); !ok {
			return decodedRelease{}, false
		}
		decoded.publications = append(decoded.publications, publication)
	}
	if section, rest, ok = readLen(rest); !ok {
		return decodedRelease{}, false
	}
	verifications, err := strconv.Atoi(section)
	if err != nil || verifications < 0 {
		return decodedRelease{}, false
	}
	for i := 0; i < verifications; i++ {
		var verification decodedVerification
		for _, target := range []*string{&verification.recipe, &verification.translationVersion, &verification.output, &verification.executionID, &verification.generation, &verification.state} {
			if *target, rest, ok = readLen(rest); !ok {
				return decodedRelease{}, false
			}
		}
		if verification.queryable, rest, ok = readBool(rest); !ok {
			return decodedRelease{}, false
		}
		if verification.verifiedAt, rest, ok = readLen(rest); !ok {
			return decodedRelease{}, false
		}
		if verification.errorCode, rest, ok = readLen(rest); !ok {
			return decodedRelease{}, false
		}
		decoded.verifications = append(decoded.verifications, verification)
	}
	if rest != "" {
		return decodedRelease{}, false
	}
	return decoded, true
}

func readBool(s string) (bool, string, bool) {
	if len(s) == 0 {
		return false, "", false
	}
	switch s[0] {
	case '0':
		return false, s[1:], true
	case '1':
		return true, s[1:], true
	}
	return false, "", false
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

func TestReleaseIDHistoricalFramingFoldWitnessesPinnedByFix(t *testing.T) {

	classAA := ridRelease("p\x00q", "g")
	classAB := ridRelease("p", "q\x00g")
	if historicalReleaseIDFrame(classAA) != historicalReleaseIDFrame(classAB) {
		t.Fatalf("fault premise broken: historical Project/Generation join must fold %q and %q", historicalReleaseIDFrame(classAA), historicalReleaseIDFrame(classAB))
	}
	if string(releaseIDFraming(classAA)) == string(releaseIDFraming(classAB)) || releaseID(classAA) == releaseID(classAB) {
		t.Fatalf("REGRESSION: length-prefixed framing folds the Project/Generation NUL pair (frame %q)", string(releaseIDFraming(classAA)))
	}

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
	if got, ok := decodeReleaseIDFrame(releaseIDFraming(classBA)); !ok || got.label() != ridContentLabel(classBA) {
		t.Fatalf("decode round-trip failed for class B: ok=%v got=%s", ok, got.label())
	}

	selector := ridSelector("core", "v1", "Patient")
	classCA := ridRelease("p", "g", ReleasePublication{Selector: selector, ExecutionID: "execution-a", Generation: "g", Required: true})
	classCB := ridRelease("p", "g", ReleasePublication{Selector: selector, ExecutionID: "execution-a", Generation: "g", Required: false})
	if historicalReleaseIDFrame(classCA) != historicalReleaseIDFrame(classCB) {
		t.Fatalf("fault premise broken: the historical narrow framing must fold the required/optional pair:\n  A=%q\n  B=%q", historicalReleaseIDFrame(classCA), historicalReleaseIDFrame(classCB))
	}
	if releaseID(classCA) == releaseID(classCB) {
		t.Fatalf("REGRESSION: releaseID folds the required/optional policy pair that SaveRelease compares as different content")
	}

	classDA := ridProbeRelease("p", "g", nil, []ContractVerification{{Selector: selector, ExecutionID: "execution-a", Generation: "g", State: "PUBLISHED", Queryable: true}})
	classDB := ridProbeRelease("p", "g", nil, []ContractVerification{{Selector: selector, ExecutionID: "execution-a", Generation: "g", State: "STALE", Queryable: true}})
	if releaseID(classDA) == releaseID(classDB) {
		t.Fatalf("REGRESSION: releaseID folds a contract-verdict change (PUBLISHED vs STALE) for one selector/execution")
	}
	t.Logf("VERDICT=REPAIRED archived fold classes plus the R2 required/optional fold collapse under the historical framing and are separated by releaseID")
}

func TestReleaseIDIncludesEveryImmutableField(t *testing.T) {
	const project, generation = "project-a", "commit-a"
	base := func() ProjectRelease {
		return ridProbeRelease(project, generation,
			[]ReleasePublication{{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: generation, Required: true, VerifiedAt: ridInstant}},
			[]ContractVerification{{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant}},
		)
	}
	if got, ok := decodeReleaseIDFrame(releaseIDFraming(base())); !ok || got.label() != ridContentLabel(base()) {
		t.Fatalf("baseline decode failed: ok=%v got=%s", ok, got.label())
	}

	topLevel := []struct {
		name   string
		mutate func(*ProjectRelease)
	}{
		{"Project", func(r *ProjectRelease) { r.Project = "project-b" }},
		{"Generation", func(r *ProjectRelease) { r.Generation = "commit-b" }},
		{"GitCommit", func(r *ProjectRelease) { r.GitCommit = "commit-b" }},
	}
	for _, probe := range topLevel {
		mutated := base()
		probe.mutate(&mutated)
		assertDistinctID(t, base(), mutated, probe.name)
	}

	publications := []struct {
		name   string
		mutate func(*ReleasePublication)
	}{
		{"Publications[0].Selector", func(p *ReleasePublication) { p.Selector = ridSelector("core", "v1", "Observation") }},
		{"Publications[0].ExecutionID", func(p *ReleasePublication) { p.ExecutionID = "execution-b" }},
		{"Publications[0].Generation", func(p *ReleasePublication) { p.Generation = "commit-b" }},
		{"Publications[0].Required", func(p *ReleasePublication) { p.Required = !p.Required }},
		{"Publications[0].Stale", func(p *ReleasePublication) { p.Stale = !p.Stale }},
		{"Publications[0].VerifiedAt", func(p *ReleasePublication) { p.VerifiedAt = ridLater }},
	}
	for _, probe := range publications {
		mutated := base()
		probe.mutate(&mutated.Publications[0])
		assertDistinctID(t, base(), mutated, probe.name)
	}
	extended := base()
	extended.Publications = append(extended.Publications, ReleasePublication{Selector: ridSelector("core", "v1", "Condition"), ExecutionID: "execution-c", Generation: generation, VerifiedAt: ridInstant})
	assertDistinctID(t, base(), extended, "Publications appended")

	verifications := []struct {
		name   string
		mutate func(*ContractVerification)
	}{
		{"RequiredVerifications[0].Selector", func(v *ContractVerification) { v.Selector = ridSelector("core", "v1", "Observation") }},
		{"RequiredVerifications[0].ExecutionID", func(v *ContractVerification) { v.ExecutionID = "execution-b" }},
		{"RequiredVerifications[0].Generation", func(v *ContractVerification) { v.Generation = "commit-b" }},
		{"RequiredVerifications[0].State", func(v *ContractVerification) { v.State = "STALE" }},
		{"RequiredVerifications[0].Queryable", func(v *ContractVerification) { v.Queryable = !v.Queryable }},
		{"RequiredVerifications[0].VerifiedAt", func(v *ContractVerification) { v.VerifiedAt = ridLater }},
		{"RequiredVerifications[0].ErrorCode", func(v *ContractVerification) { v.ErrorCode = "PUBLICATION_FAILED" }},
	}
	for _, probe := range verifications {
		mutated := base()
		probe.mutate(&mutated.RequiredVerifications[0])
		assertDistinctID(t, base(), mutated, probe.name)
	}
	extendedVer := base()
	extendedVer.RequiredVerifications = append(extendedVer.RequiredVerifications, ContractVerification{Selector: ridSelector("core", "v1", "Condition"), ExecutionID: "execution-c", Generation: generation, State: "PUBLISHED", Queryable: true})
	assertDistinctID(t, base(), extendedVer, "RequiredVerifications appended")
	t.Logf("VERDICT=PASS 3 top-level, 6 publication and 7 verification field categories plus both section-length changes each move releaseID")
}

func assertDistinctID(t *testing.T, left, right ProjectRelease, field string) {
	t.Helper()
	if ridContentLabel(left) == ridContentLabel(right) {
		t.Fatalf("probe %s did not change the expected content", field)
	}
	if releaseID(left) == releaseID(right) {
		t.Fatalf("REGRESSION: mutating %s left releaseID unchanged (%s)", field, releaseID(left))
	}
	if got, ok := decodeReleaseIDFrame(releaseIDFraming(right)); !ok || got.label() != ridContentLabel(right) {
		t.Fatalf("probe %s failed the decode round-trip: ok=%v got=%s", field, ok, got.label())
	}
}

func TestReleaseIDIgnoresCreatedAt(t *testing.T) {
	base := ridProbeRelease("project-a", "commit-a",
		[]ReleasePublication{{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: "commit-a", Required: true, VerifiedAt: ridInstant}},
		[]ContractVerification{{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: "commit-a", State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant}},
	)
	first := base
	first.CreatedAt = ridInstant
	second := base
	second.CreatedAt = ridLater
	third := base
	third.CreatedAt = time.Now()
	if releaseID(first) != releaseID(second) || releaseID(second) != releaseID(third) {
		t.Fatalf("REGRESSION: CreatedAt leaked into releaseID: %s vs %s vs %s", releaseID(first), releaseID(second), releaseID(third))
	}
	if first.CreatedAt.Equal(second.CreatedAt) || first.CreatedAt.Equal(third.CreatedAt) {
		t.Fatalf("witness precondition broken: the CreatedAt probes must be different instants")
	}

	moved := ridClone(first)
	moved.Publications[0].VerifiedAt = ridLater
	if releaseID(moved) == releaseID(first) {
		t.Fatalf("REGRESSION: publication VerifiedAt is persisted content and must move releaseID")
	}
	movedVer := ridClone(first)
	movedVer.RequiredVerifications[0].VerifiedAt = ridLater
	if releaseID(movedVer) == releaseID(first) {
		t.Fatalf("REGRESSION: verification VerifiedAt is persisted content and must move releaseID")
	}
	t.Logf("VERDICT=PASS CreatedAt alone (three distinct instants) keeps one id; per-binding VerifiedAt still distinguishes")
}

func TestReleaseIDCanonicalEquivalence(t *testing.T) {
	first := ReleasePublication{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: "commit-a", Required: true, VerifiedAt: ridInstant}
	second := ReleasePublication{Selector: ridSelector("core", "v1", "Observation"), ExecutionID: "execution-b", Generation: "commit-a", Required: false, Stale: true, VerifiedAt: ridLater}
	firstVer := ContractVerification{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "execution-a", Generation: "commit-a", State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant}
	secondVer := ContractVerification{Selector: ridSelector("core", "v1", "Observation"), ExecutionID: "execution-b", Generation: "commit-a", State: "STALE", Queryable: false, VerifiedAt: ridLater, ErrorCode: "PUBLICATION_FAILED"}

	ordered := ridProbeRelease("project-a", "commit-a", []ReleasePublication{first, second}, []ContractVerification{firstVer, secondVer})
	reversed := ridProbeRelease("project-a", "commit-a", []ReleasePublication{second, first}, []ContractVerification{secondVer, firstVer})
	if releaseID(ordered) != releaseID(reversed) {
		t.Fatalf("REGRESSION: submission order leaked into releaseID: %s vs %s", releaseID(ordered), releaseID(reversed))
	}

	elsewhere := ridProbeRelease("project-a", "commit-a",
		[]ReleasePublication{{Selector: first.Selector, ExecutionID: first.ExecutionID, Generation: first.Generation, Required: true, VerifiedAt: ridOtherZone}},
		[]ContractVerification{{Selector: firstVer.Selector, ExecutionID: firstVer.ExecutionID, Generation: firstVer.Generation, State: "PUBLISHED", Queryable: true, VerifiedAt: ridOtherZone}},
	)
	utcForm := ridProbeRelease("project-a", "commit-a",
		[]ReleasePublication{{Selector: first.Selector, ExecutionID: first.ExecutionID, Generation: first.Generation, Required: true, VerifiedAt: ridInstant}},
		[]ContractVerification{{Selector: firstVer.Selector, ExecutionID: firstVer.ExecutionID, Generation: firstVer.Generation, State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant}},
	)
	if !ridOtherZone.Equal(ridInstant) {
		t.Fatalf("witness precondition broken: %s must be the same instant as %s", ridOtherZone, ridInstant)
	}
	if releaseID(elsewhere) != releaseID(utcForm) {
		t.Fatalf("REGRESSION: zone representation leaked into releaseID: %s vs %s", releaseID(elsewhere), releaseID(utcForm))
	}
	monotonic := ridProbeRelease("project-a", "commit-a",
		[]ReleasePublication{{Selector: first.Selector, ExecutionID: first.ExecutionID, Generation: first.Generation, Required: true, VerifiedAt: ridInstant}},
		[]ContractVerification{{Selector: firstVer.Selector, ExecutionID: firstVer.ExecutionID, Generation: firstVer.Generation, State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant}},
	)
	carrier := time.Now()
	monotonic.Publications[0].VerifiedAt = carrier
	monotonic.RequiredVerifications[0].VerifiedAt = carrier
	stripped := ridClone(monotonic)
	stripped.Publications[0].VerifiedAt = carrier.Round(0)
	stripped.RequiredVerifications[0].VerifiedAt = carrier.Round(0)
	if releaseID(monotonic) != releaseID(stripped) {
		t.Fatalf("REGRESSION: a monotonic reading leaked into releaseID: %s vs %s", releaseID(monotonic), releaseID(stripped))
	}

	later := ridClone(utcForm)
	later.Publications[0].VerifiedAt = ridLater
	if releaseID(later) == releaseID(utcForm) {
		t.Fatalf("REGRESSION: a different VerifiedAt instant failed to move releaseID")
	}

	withEmpties := ridProbeRelease("project-a", "commit-a", []ReleasePublication{first}, nil)
	withEmpties.RequiredVerifications = []ContractVerification{}
	if releaseID(withEmpties) != releaseID(ridProbeRelease("project-a", "commit-a", []ReleasePublication{first}, nil)) {
		t.Fatalf("REGRESSION: nil vs empty slices are one service form and must share an id")
	}
	if releaseID(utcForm) == releaseID(ridProbeRelease("project-a", "commit-a", []ReleasePublication{first}, nil)) {
		t.Fatalf("REGRESSION: dropping the whole verification section left releaseID unchanged")
	}
	t.Logf("VERDICT=PASS order, zone, monotonic and nil/empty canonicalization keep releaseID deterministic; distinct instants still separate")
}

func TestReleaseIDFramingIsInjectiveSmallAlphabet(t *testing.T) {
	components := ridBuildComponents([]string{"a", ":", "\x00", "1"}, 1)

	pgSet := ridBuildComponents([]string{"a", ":", "\x00", "0", "あ", "🎯", "é"}, 2)

	type probePub struct {
		recipe, tv, output, exec, generation string
		required, stale                      bool
		at                                   time.Time
	}

	var pubSpace []probePub
	for index, r := range components {
		for _, tv := range components {
			for _, o := range components {
				for _, e := range []string{"", "a", ":", "\x00"} {
					pubSpace = append(pubSpace, probePub{
						recipe: r, tv: tv, output: o, exec: e,
						generation: []string{"", "g", "commit-a"}[index%3],
						required:   index%2 == 0,
						stale:      index%3 == 0,
						at:         ridTimePool[index%len(ridTimePool)],
					})
					index++
				}
			}
		}
	}

	frames := map[string]string{}
	ids := map[string]string{}
	checked := 0

	add := func(project, generation string, list []probePub) {
		made := make([]ReleasePublication, 0, len(list))
		for _, p := range list {
			binding := ridPub(p.recipe, p.tv, p.output, p.exec)
			binding.Generation = p.generation
			binding.Required = p.required
			binding.Stale = p.stale
			binding.VerifiedAt = p.at
			made = append(made, binding)
		}
		release := ridRelease(project, generation, made...)
		label := ridContentLabel(release)
		frame := string(releaseIDFraming(release))
		id := releaseID(release)
		decoded, ok := decodeReleaseIDFrame([]byte(frame))
		if !ok || decoded.label() != label {
			t.Fatalf("decoder/round-trip failed for %s: ok=%v decoded=%s", label, ok, decoded.label())
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

	for _, project := range pgSet {
		for _, generation := range pgSet {
			add(project, generation, nil)
			for _, one := range pubSpace[:128] {
				add(project, generation, []probePub{one})
			}
		}
	}

	for _, one := range pubSpace {
		for _, two := range pubSpace {
			add("p\x00q", "g", []probePub{one, two})
		}
	}

	pairSet := pubSpace[:min(len(pubSpace), 20)]
	for _, project := range []string{"p", "\x00", "a:b"} {
		for _, generation := range []string{"g", "\x00", ":"} {
			for _, one := range pairSet {
				for _, two := range pairSet {
					add(project, generation, []probePub{one, two})
				}
			}
		}
	}
	t.Logf("VERDICT=HARDENED | exhaustive small-alphabet injectivity: %d distinct-content releases, zero framing/digest folds, all decoded", checked)
}

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

func TestReleaseIDFramingInjectiveKeySpace(t *testing.T) {
	components := ridBuildComponents([]string{"a", "1", ":", "\x00", "-"}, 3)
	count := 0
	for _, r := range components {
		for _, tv := range components {
			for _, o := range components {
				release := ridProbeRelease("proj", "gen",
					[]ReleasePublication{{Selector: ridSelector(r, tv, o), ExecutionID: "exec", Generation: "gen", Required: true, VerifiedAt: ridInstant}},
					[]ContractVerification{{Selector: ridSelector(o, r, tv), ExecutionID: "exec", Generation: "gen", State: "PUBLISHED", Queryable: true, VerifiedAt: ridOtherZone, ErrorCode: "E"}},
				)
				label := ridContentLabel(release)
				decoded, ok := decodeReleaseIDFrame(releaseIDFraming(release))
				if !ok || decoded.label() != label {
					t.Fatalf("key-space decode failed: selector={%q,%q,%q} ok=%v got=%s", r, tv, o, ok, decoded.label())
				}
				count++
			}
		}
	}
	t.Logf("VERDICT=HARDENED | exhaustive Key-space probe: %d selector triples over a delimiter-heavy alphabet (incl NUL/':'), decode-inverse injected", count)
}

func TestReleaseIDFramingInjectiveVerificationSpace(t *testing.T) {
	base := func() ProjectRelease {
		return ridProbeRelease("proj", "gen", []ReleasePublication{ridPub("core", "v1", "Patient", "exec-a")}, []ContractVerification{
			{Selector: ridSelector("core", "v1", "Patient"), ExecutionID: "exec-a", Generation: "gen", State: "PUBLISHED", Queryable: true, VerifiedAt: ridInstant},
			{Selector: ridSelector("core", "v1", "Observation"), ExecutionID: "exec-b", Generation: "gen", State: "STALE", Queryable: false, ErrorCode: "PUBLICATION_FAILED"},
		})
	}
	nilVer, emptyVer := base(), base()
	nilVer.RequiredVerifications = nil
	emptyVer.RequiredVerifications = []ContractVerification{}
	if releaseID(nilVer) != releaseID(emptyVer) {
		t.Fatalf("REGRESSION: nil and empty RequiredVerifications are one service form and must share an id")
	}
	probes := []struct {
		name   string
		mutate func(*ContractVerification)
	}{
		{"executionId", func(v *ContractVerification) { v.ExecutionID = "exec-z" }},
		{"generation", func(v *ContractVerification) { v.Generation = "other-gen" }},
		{"state", func(v *ContractVerification) { v.State = "FAILED" }},
		{"queryable", func(v *ContractVerification) { v.Queryable = !v.Queryable }},
		{"verifiedAt", func(v *ContractVerification) { v.VerifiedAt = ridLater }},
		{"errorCode", func(v *ContractVerification) { v.ErrorCode = v.ErrorCode + "X" }},
	}
	for _, probe := range probes {
		mutated := base()
		probe.mutate(&mutated.RequiredVerifications[0])
		assertDistinctID(t, base(), mutated, "RequiredVerifications[0]."+probe.name)
	}
	reversed := base()
	reversed.RequiredVerifications[0], reversed.RequiredVerifications[1] = reversed.RequiredVerifications[1], reversed.RequiredVerifications[0]
	if releaseID(reversed) != releaseID(base()) {
		t.Fatalf("REGRESSION: verification submission order leaked into releaseID")
	}
	extra := base()
	extra.RequiredVerifications = append(extra.RequiredVerifications, ContractVerification{Selector: ridSelector("core", "v1", "Condition"), ExecutionID: "exec-c", Generation: "gen", State: "PUBLISHED", Queryable: true})
	assertDistinctID(t, base(), extra, "RequiredVerifications appended")
	t.Logf("VERDICT=HARDENED | verification-section sweep: 6 field probes, nil/empty and order equivalence, all decoded")
}

func TestReleaseIDFramingInjectiveRandomProbe(t *testing.T) {
	const samples = 300_000

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
			made = append(made, ReleasePublication{
				Selector:    ridSelector(randomString(rng), randomString(rng), randomString(rng)),
				ExecutionID: randomString(rng),
				Generation:  randomString(rng),
				Required:    rng.Intn(2) == 0,
				Stale:       rng.Intn(2) == 0,
				VerifiedAt:  ridTimePool[rng.Intn(len(ridTimePool))],
			})
		}
		verCount := rng.Intn(3)
		vers := make([]ContractVerification, 0, verCount)
		for j := 0; j < verCount; j++ {
			vers = append(vers, ContractVerification{
				Selector:    ridSelector(randomString(rng), randomString(rng), randomString(rng)),
				ExecutionID: randomString(rng),
				Generation:  randomString(rng),
				State:       randomString(rng),
				Queryable:   rng.Intn(2) == 0,
				VerifiedAt:  ridTimePool[rng.Intn(len(ridTimePool))],
				ErrorCode:   randomString(rng),
			})
		}
		release := ridProbeRelease(randomString(rng), randomString(rng), made, vers)
		release.GitCommit = randomString(rng)
		label := ridContentLabel(release)
		frame := string(releaseIDFraming(release))
		decoded, ok := decodeReleaseIDFrame([]byte(frame))
		if !ok || decoded.label() != label {
			t.Fatalf("random decode failed for %q: ok=%v decoded=%s", label, ok, decoded.label())
		}
		if prior, already := seen[frame]; already && prior != label {
			t.Fatalf("REGRESSION (random fold): frame %q shared by %q and %q", frame, prior, label)
		}
		seen[frame] = label
	}
	t.Logf("VERDICT=HARDENED | bounded random probe: %d delimiter-laden releases, %d distinct frames, zero folds, all decoded", samples, len(seen))
}

type verificationFixture struct {
	mu      sync.Mutex
	results map[string]PublicationVerification
	errors  map[string]error
}

type manifestReaderFixture struct {
	manifest Manifest
	err      error
}

func (f manifestReaderFixture) ReadManifest(context.Context, Ref) (Manifest, error) {
	return f.manifest, f.err
}

func (f *verificationFixture) VerifyPublication(_ context.Context, _, _ string, selector DataframeSelector) (PublicationVerification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.results[selector.Key()], f.errors[selector.Key()]
}

func TestReleaseActivationRequiresPublishedQueryableGenerationAndPreservesPointerOnFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	stageManifest(t, store, "project-a", "commit-a")
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): {Selector: selector, ExecutionID: "execution-a", Generation: "commit-a", State: "FAILED", Queryable: false},
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	if _, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0}); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("failed requirement = %v", err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("failed activation moved pointer: %v", err)
	}
	verification := verifier.results[selector.Key()]
	verification.State, verification.Queryable, verification.VerifiedAt = "PUBLISHED", true, now
	verifier.results[selector.Key()] = verification
	if _, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("release creation moved pointer: %v", err)
	}
	active, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0})
	if err != nil || active.Revision != 1 || active.Release.Generation != "commit-a" || len(active.Release.Publications) != 1 || active.Release.Publications[0].Stale {
		t.Fatalf("activation = %#v, %v", active, err)
	}
}

func TestValidateGenerationRequiresExistingStagedManifest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	service := ReleaseService{Manifests: store}

	if err := service.ValidateGeneration(ctx, "project-a", "missing"); !errors.Is(err, ErrManifestNotFound) {
		t.Fatalf("missing generation validation = %v", err)
	}

	ref, _ := NewRef("project-a", "loading")
	schema, _ := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	loading, _ := NewManifest(ref, schema)
	store.manifests[ref] = loading
	if err := service.ValidateGeneration(ctx, "project-a", "loading"); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("loading generation validation = %v", err)
	}

	stageManifest(t, store, "project-a", "staged")
	if err := service.ValidateGeneration(ctx, "project-a", "staged"); err != nil {
		t.Fatalf("staged generation validation = %v", err)
	}
}

func TestValidateGenerationAcceptsLegacyStagedManifestWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	ref, err := NewRef("project-a", "legacy-generation")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(StateReady)
	if err != nil {
		t.Fatal(err)
	}
	selector := DataframeSelector{Recipe: "explorer", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", ref.Generation, time.Now().UTC()),
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: manifestReaderFixture{manifest: manifest}, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}}
	if err := service.ValidateGeneration(ctx, ref.Project, ref.Generation); err != nil {
		t.Fatalf("legacy staged manifest validation = %v", err)
	}
	if release, err := service.Create(ctx, ActivationRequest{Project: ref.Project, Generation: ref.Generation}); err != nil || release.Generation != ref.Generation {
		t.Fatalf("legacy staged manifest release = %#v, %v", release, err)
	}
}

func TestValidateGenerationRejectsLegacyLoadingManifestWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	ref, err := NewRef("project-a", "legacy-loading")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	service := ReleaseService{Manifests: manifestReaderFixture{manifest: manifest}}
	if err := service.ValidateGeneration(ctx, ref.Project, ref.Generation); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("legacy loading manifest validation = %v", err)
	}
}

func TestReleaseActivationCarriesOptionalPublicationAsStale(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	required := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	optional := DataframeSelector{Recipe: "optional", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{required}, Now: func() time.Time { return now }}

	stageManifest(t, store, "project-a", "commit-a")
	verifier.results[required.Key()] = published(required, "required-a", "commit-a", now)
	verifier.results[optional.Key()] = published(optional, "optional-a", "commit-a", now)
	first, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0, OptionalSelectors: []DataframeSelector{optional}})
	if err != nil || len(first.Release.Publications) != 2 {
		t.Fatalf("first activation = %#v, %v", first, err)
	}

	stageManifest(t, store, "project-a", "commit-b")
	verifier.results[required.Key()] = published(required, "required-b", "commit-b", now.Add(time.Hour))
	second, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-b", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	bySelector := map[string]ReleasePublication{}
	for _, publication := range second.Release.Publications {
		bySelector[publication.Selector.Key()] = publication
	}
	if !bySelector[optional.Key()].Stale || bySelector[optional.Key()].ExecutionID != "optional-a" || bySelector[required.Key()].Stale {
		t.Fatalf("carried publications = %#v", second.Release.Publications)
	}
}

func TestReleaseActivationConflictRetainsImmutableCandidate(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	release := ProjectRelease{ID: "release-a", Project: "project-a", GitCommit: "generation-a", Generation: "generation-a", CreatedAt: time.Now().UTC()}
	if _, err := store.SaveRelease(ctx, release); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapActivateRelease(ctx, release, 1); !errors.Is(err, ErrReleaseActivationConflict) {
		t.Fatalf("activation conflict = %v, want %v", err, ErrReleaseActivationConflict)
	}
	if _, err := store.ReadActiveRelease(ctx, release.Project); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("conflicting activation moved pointer: %v", err)
	}
	store.mu.RLock()
	retained, ok := store.releaseRecords[release.Project+"\x00"+release.ID]
	store.mu.RUnlock()
	if !ok || !reflect.DeepEqual(retained, release) {
		t.Fatalf("retained candidate = %#v, present = %v, want immutable release", retained, ok)
	}
}

func TestReleaseActivationAddsPublicationWithoutDroppingSameGenerationPublications(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	stageManifest(t, store, "project-a", "commit-a")
	firstSelector := DataframeSelector{Recipe: "explorer-one", TranslationVersion: "v1", Output: "Patient"}
	secondSelector := DataframeSelector{Recipe: "explorer-two", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		firstSelector.Key():  published(firstSelector, "execution-a", "commit-a", now),
		secondSelector.Key(): published(secondSelector, "execution-b", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Now: func() time.Time { return now }}

	first, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0, OptionalSelectors: []DataframeSelector{firstSelector}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: first.Revision, OptionalSelectors: []DataframeSelector{secondSelector}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || len(second.Release.Publications) != 2 {
		t.Fatalf("second activation = %#v", second)
	}
	bySelector := map[string]ReleasePublication{}
	for _, publication := range second.Release.Publications {
		bySelector[publication.Selector.Key()] = publication
	}
	if bySelector[firstSelector.Key()].ExecutionID != "execution-a" || bySelector[firstSelector.Key()].Stale || bySelector[secondSelector.Key()].ExecutionID != "execution-b" || bySelector[secondSelector.Key()].Stale {
		t.Fatalf("same-generation publications = %#v", second.Release.Publications)
	}
}

func stageManifest(t *testing.T, store *MemoryLifecycleStore, project, generation string) {
	t.Helper()
	ref, err := NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err == nil {
		manifest, err = manifest.Transition(StateReady)
	}
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.manifests[ref] = manifest
	store.mu.Unlock()
}

func published(selector DataframeSelector, execution, generation string, now time.Time) PublicationVerification {
	return PublicationVerification{Selector: selector, ExecutionID: execution, Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: now}
}

func TestReleasePreservesSourceCommitDistinctFromCandidateGeneration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	stageManifest(t, store, "project-a", "load-operation-a")
	service := ReleaseService{Manifests: store, Releases: store, Verifier: &verificationFixture{results: map[string]PublicationVerification{}, errors: map[string]error{}}}
	active, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "load-operation-a", GitCommit: "source-commit", ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if active.Release.GitCommit != "source-commit" || active.Release.Generation != "load-operation-a" {
		t.Fatalf("release lost source identity: %#v", active.Release)
	}
}

func storedReleaseCandidates(store *MemoryLifecycleStore, project string) map[string]ProjectRelease {
	store.mu.RLock()
	defer store.mu.RUnlock()
	prefix := project + "\x00"
	candidates := map[string]ProjectRelease{}
	for key, record := range store.releaseRecords {
		if strings.HasPrefix(key, prefix) {
			candidates[strings.TrimPrefix(key, prefix)] = record
		}
	}
	return candidates
}

func candidateIDs(candidates map[string]ProjectRelease) []string {
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	return ids
}

func r2FixtureService(store *MemoryLifecycleStore, now time.Time, required ...DataframeSelector) ReleaseService {
	return ReleaseService{Manifests: store, Releases: store, Verifier: nil, Required: required, Now: func() time.Time { return now }}
}

func TestIdenticalReleaseContentRemainsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	stageManifest(t, store, "project-a", "commit-a")
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	request := ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0}

	first, err := service.Activate(ctx, request)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first activation = %#v, %v", first, err)
	}

	for attempt := 2; attempt <= 4; attempt++ {
		repeat, err := service.Activate(ctx, request)
		if err != nil {
			t.Fatalf("identical activation replay #%d = %v", attempt, err)
		}
		if !reflect.DeepEqual(repeat, first) {
			t.Fatalf("REGRESSION: identical replay #%d moved the active release: got %#v want %#v", attempt, repeat, first)
		}
	}

	for attempt := 1; attempt <= 3; attempt++ {
		created, err := service.Create(ctx, ActivationRequest{Project: request.Project, Generation: request.Generation})
		if err != nil {
			t.Fatalf("identical Create #%d = %v", attempt, err)
		}
		if created.ID != first.Release.ID {
			t.Fatalf("REGRESSION: identical Create #%d minted a new identity %s, want %s", attempt, created.ID, first.Release.ID)
		}
	}

	candidates := storedReleaseCandidates(store, "project-a")
	if len(candidates) != 1 {
		t.Fatalf("REGRESSION: identical snapshots retained %d immutable candidates: %v", len(candidates), candidateIDs(candidates))
	}
	retained, ok := candidates[first.Release.ID]
	if !ok {
		t.Fatalf("retained candidate for the active id %s is missing, have %v", first.Release.ID, candidateIDs(candidates))
	}
	if !reflect.DeepEqual(retained, first.Release) {
		t.Fatalf("REGRESSION: the retained candidate drifted from the immutable first save: got %#v want %#v", retained, first.Release)
	}
	t.Logf("VERDICT=PASS 4 identical activations and 3 identical saves converge: revision 1, id %s, exactly one immutable candidate", first.Release.ID)
}

func TestLegacyReleaseIDReactivationConvergesOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", "commit-a", now),
	}, errors: map[string]error{}}

	probe := NewMemoryLifecycleStore()
	stageManifest(t, probe, "project-a", "commit-a")
	probeService := r2FixtureService(probe, now, selector)
	probeService.Verifier = verifier
	derived, err := probeService.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"})
	if err != nil {
		t.Fatalf("derive current release: %v", err)
	}
	if derived.ID != releaseID(derived) {
		t.Fatalf("witness precondition broken: saved id %s is not the derived id %s", derived.ID, releaseID(derived))
	}
	legacy := derived
	legacy.ID = legacyReleaseID(legacy)
	if legacy.ID == derived.ID {
		t.Fatalf("witness precondition broken: the historical framing must derive a different id for this content")
	}

	store := NewMemoryLifecycleStore()
	stageManifest(t, store, "project-a", "commit-a")
	service := r2FixtureService(store, now, selector)
	service.Verifier = verifier
	if _, err := store.SaveRelease(ctx, legacy); err != nil {
		t.Fatalf("seed legacy candidate: %v", err)
	}
	if _, err := store.CompareAndSwapActivateRelease(ctx, legacy, 0); err != nil {
		t.Fatalf("activate legacy candidate: %v", err)
	}
	active, err := store.ReadActiveRelease(ctx, "project-a")
	if err != nil || active.Revision != 1 || active.Release.ID != legacy.ID {
		t.Fatalf("legacy seeding = %#v, %v (want revision 1, id %s)", active, err, legacy.ID)
	}

	reactivation := ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 1}
	transitioned, err := service.Activate(ctx, reactivation)
	if err != nil {
		t.Fatalf("first reactivation of the stored legacy id: %v", err)
	}
	if transitioned.Release.ID == legacy.ID {
		t.Fatalf("REGRESSION: reactivating the stored legacy id %s never transitioned to the widened id", legacy.ID)
	}
	if transitioned.Revision != 2 || transitioned.Release.ID != derived.ID {
		t.Fatalf("REGRESSION: first reactivation must converge on %s at revision 2, got %#v", derived.ID, transitioned)
	}

	for attempt := 3; attempt <= 5; attempt++ {
		repeat, err := service.Activate(ctx, reactivation)
		if err != nil {
			t.Fatalf("repeated reactivation #%d = %v", attempt, err)
		}
		if repeat.Revision != 2 || repeat.Release.ID != transitioned.Release.ID {
			t.Fatalf("REGRESSION: reactivation #%d made a second new-id transition: revision %d id %s (want revision 2 id %s)",
				attempt, repeat.Revision, repeat.Release.ID, transitioned.Release.ID)
		}
	}

	candidates := storedReleaseCandidates(store, "project-a")
	if len(candidates) != 2 {
		t.Fatalf("REGRESSION: at most one new-id transition must retain exactly 2 candidates, got %v", candidateIDs(candidates))
	}
	retainedLegacy, ok := candidates[legacy.ID]
	if !ok || !reflect.DeepEqual(retainedLegacy, legacy) {
		t.Fatalf("REGRESSION: the pre-widening candidate is no longer immutable: got %#v want %#v", retainedLegacy, legacy)
	}
	if _, ok := candidates[transitioned.Release.ID]; !ok {
		t.Fatalf("the transitioned candidate %s is missing, have %v", transitioned.Release.ID, candidateIDs(candidates))
	}
	t.Logf("VERDICT=PASS stored id %s transitioned once to %s; 3 further retries converged at revision 2 with 2 retained candidates", legacy.ID, transitioned.Release.ID)
}

type faultClassVerifier struct {
	err      error
	missing  bool
	mismatch bool
}

func (v faultClassVerifier) VerifyPublication(_ context.Context, _, generation string, selector DataframeSelector) (PublicationVerification, error) {
	if v.missing {
		return PublicationVerification{}, ErrPublicationRecordMissing
	}
	if v.err != nil {
		return PublicationVerification{}, v.err
	}
	answerGeneration := generation
	if v.mismatch {
		answerGeneration = "stale-generation"
	}
	return PublicationVerification{Selector: selector, ExecutionID: "exec-fault-class", Generation: answerGeneration, State: "PUBLISHED", Queryable: true, VerifiedAt: time.Unix(1, 0).UTC()}, nil
}

func newFaultClassReleaseService(t *testing.T, verifier PublicationVerifier) ReleaseService {
	t.Helper()
	store := NewMemoryLifecycleStore()
	stageManifest(t, store, "project-fault-class", "commit-a")
	return ReleaseService{
		Manifests: store,
		Releases:  store,
		Verifier:  verifier,
		Required:  []DataframeSelector{{Recipe: "evidence_unit", TranslationVersion: "sha256:t", Output: "units.parquet"}},
		Now:       func() time.Time { return time.Unix(1000, 0).UTC() },
	}
}

func releaseFaultClassRequest() ActivationRequest {
	return ActivationRequest{Project: "project-fault-class", Generation: "commit-a", GitCommit: "commit-a"}
}

func TestReleaseCreateKeepsVerifierFaultOutOfRequirementsError(t *testing.T) {
	storeFault := errors.New("arangodb: connection reset by peer")
	svc := newFaultClassReleaseService(t, faultClassVerifier{err: storeFault})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	if err == nil {
		t.Fatalf("verifier fault accepted release")
	}
	var typed *ReleaseRequirementsError
	if errors.As(err, &typed) {
		t.Fatalf("verifier fault misclassified as contract miss: %#v", typed)
	}
	if !errors.Is(err, storeFault) {
		t.Fatalf("store fault must stay errors.Is-visible for transport classification: %v", err)
	}
	if errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("store fault must not satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
}

func TestReleaseCreateRecordMissStaysRequirementsError(t *testing.T) {
	svc := newFaultClassReleaseService(t, faultClassVerifier{missing: true})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	var typed *ReleaseRequirementsError
	if !errors.As(err, &typed) {
		t.Fatalf("deterministic record miss must be a typed requirements error: %v", err)
	}
	if !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("record miss must satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
	if len(typed.Verifications) != 1 || typed.Verifications[0].ErrorCode != "RELEASE_REQUIREMENTS_UNMET" {
		t.Fatalf("record miss verdict lost: %#v", typed.Verifications)
	}
}

func TestReleaseCreateShapeMismatchStaysRequirementsError(t *testing.T) {
	svc := newFaultClassReleaseService(t, faultClassVerifier{mismatch: true})
	_, err := svc.Create(context.Background(), releaseFaultClassRequest())
	var typed *ReleaseRequirementsError
	if !errors.As(err, &typed) {
		t.Fatalf("shape mismatch must remain a typed requirements error: %v", err)
	}
	if !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("shape mismatch must satisfy ErrReleaseRequirementsUnmet: %v", err)
	}
}
