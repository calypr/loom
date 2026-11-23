package compilerprobe

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func decodeCandidate(raw []byte) (recipe.Bundle, error) {
	var bundle recipe.Bundle
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return recipe.Bundle{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return recipe.Bundle{}, io.ErrUnexpectedEOF
	}
	return bundle, bundle.ValidateArchive()
}

func checkTranslation(t *testing.T, v1 recipe.Bundle) {
	t.Helper()
	archive, err := v1.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Translate(v1)
	if v1.HasArchivedReshape() {
		if err == nil {
			t.Fatal("expected an explicit archived reshape mapping gap")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := Translate(v1)
	if err != nil {
		t.Fatal(err)
	}
	encoder := func(v Bundle) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	first, second := encoder(a), encoder(b)
	if !bytes.Equal(first, second) {
		t.Fatal("translation is nondeterministic")
	}
	var roundTrip Bundle
	if err := json.Unmarshal(first, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, encoder(roundTrip)) {
		t.Fatal("v2 canonical JSON does not round-trip")
	}
	restored, err := RestoreTransport(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	restoredDigest, err := restored.ArchiveDigest()
	if err != nil || restoredDigest != archive {
		if scratch := os.Getenv("LOOM_COMPILER_RESEARCH_DIFF"); scratch != "" {
			for name, bundle := range map[string]recipe.Bundle{"original": v1, "restored": restored} {
				raw, _ := bundle.ArchiveCanonicalJSON()
				if writeErr := os.WriteFile(filepath.Join(scratch, name+".json"), raw, 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
		}
		t.Fatalf("transport restore changed v1 archive digest for %q: got %q want %q, error %v", v1.Name, restoredDigest, archive, err)
	}
	after, err := v1.ArchiveDigest()
	if err != nil || archive != after {
		t.Fatalf("archive digest changed: %v", err)
	}
}

func checkedInCandidates(t testing.TB) [][]byte {
	t.Helper()
	root := "../../.."
	var candidates [][]byte
	for _, pattern := range []string{"tests/conformance/fixtures/*.json", "tests/fixtures/*/recipe.json", "tests/fixtures/acceptance/*/recipe.json", "internal/explorer/recipeimport/testdata/default-dataframer.json", "docs/work-packages/record-network-and-rows/fixtures/default-dataframer.json"} {
		files, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			encoded, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct{ Recipe json.RawMessage }
			if err := json.Unmarshal(encoded, &envelope); err != nil {
				t.Fatal(err)
			}
			if len(envelope.Recipe) > 0 {
				encoded = envelope.Recipe
			}
			if bytes.Contains(encoded, []byte(`"recipeSchemaVersion"`)) {
				candidates = append(candidates, encoded)
			}
		}
	}
	return candidates
}

func researchCandidates(t testing.TB) [][]byte {
	candidates := checkedInCandidates(t)
	if dir := os.Getenv("LOOM_COMPILER_RESEARCH_CORPUS"); dir != "" {
		files, err := filepath.Glob(filepath.Join(dir, "attempted-*.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			encoded, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			candidates = append(candidates, encoded)
		}
	}
	return candidates
}

func TestCorpusTranslation(t *testing.T) {
	candidates := researchCandidates(t)
	localCount := len(checkedInCandidates(t))
	invalid, gaps, translated, canonicalDuplicates := 0, 0, 0, 0
	seen := map[string]bool{}
	for _, raw := range candidates {
		bundle, err := decodeCandidate(raw)
		if err != nil {
			invalid++
			continue
		}
		digest, err := bundle.ArchiveDigest()
		if err != nil {
			t.Fatal(err)
		}
		if seen[digest] {
			canonicalDuplicates++
			continue
		}
		seen[digest] = true
		checkTranslation(t, bundle)
		if bundle.HasArchivedReshape() {
			gaps++
			t.Logf("unmapped archived shape: %q (%s)", bundle.Name, digest)
		} else {
			translated++
		}
	}
	t.Logf("inputs=%d checked-in=%d invalid=%d canonical_duplicates=%d translated_unique=%d unmapped_unique=%d", len(candidates), localCount, invalid, canonicalDuplicates, translated, gaps)
	if translated == 0 {
		t.Fatal("empty corpus")
	}
}

func TestStoredReceiptArchiveDigest(t *testing.T) {
	path := os.Getenv("LOOM_COMPILER_RESEARCH_RECEIPTS")
	if path == "" {
		t.Skip("owned receipt export not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r struct {
			Bundle recipe.Bundle `json:"compiledRecipe"`
			Digest string        `json:"resolvedRecipeDigest"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if digest, err := r.Bundle.ArchiveDigest(); err != nil || digest != r.Digest {
			t.Fatalf("stored archive digest failed: got %q want %q, error %v", digest, r.Digest, err)
		}
		checkTranslation(t, r.Bundle)
		count++
	}
	t.Logf("rechecked %d stored receipt archive digests", count)
}

func TestStoredRegistryTranslation(t *testing.T) {
	path := os.Getenv("LOOM_COMPILER_RESEARCH_REGISTRY")
	if path == "" {
		t.Skip("owned registry export not supplied")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		var entry struct{ Bundle recipe.Bundle }
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		checkTranslation(t, entry.Bundle)
		count++
	}
	t.Logf("checked %d stored registry bundles", count)
}

func FuzzTranslate(f *testing.F) {
	for _, raw := range researchCandidates(f) {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		bundle, err := decodeCandidate(raw)
		if err != nil {
			return
		}
		checkTranslation(t, bundle)
	})
}
