package recipe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const archivedRecipeGolden = `{"recipeSchemaVersion":1,"name":"archived-table","translationVersion":"v1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","tableReshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"shape","inputs":[{"column":"value","key":{"kind":"STRING","string":"value"}}],"keyOutput":"name","keyLabel":"Name","valueOutput":"result","valueLabel":"Result","nullRowPolicy":"DROP"}}}]}`
const archivedRecipeGoldenDigest = "2ffdf4f14e5f6a339de8fcb2dcd1c8c0ac0bd6877be5d59b4a7f462d0eba4f6b"

func decodeTestArchive(t *testing.T, raw []byte) Bundle {
	t.Helper()
	b, err := decodeBundleJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestArchiveCanonicalGoldenSurvivesMapRoundTrip(t *testing.T) {
	raw := []byte(archivedRecipeGolden)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(reordered, raw) {
		t.Fatal("fixture did not reorder JSON")
	}
	b := decodeTestArchive(t, reordered)
	canonical, err := b.ArchiveCanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, raw) {
		t.Fatalf("historical typed wire changed:\n%s\n%s", canonical, raw)
	}
	digest, err := b.ArchiveDigest()
	if err != nil || digest != archivedRecipeGoldenDigest {
		t.Fatalf("digest=%s error=%v", digest, err)
	}
	metadata, err := ParseArchivedMetadata(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "archived-table" || metadata.TranslationVersion != "v1" || len(metadata.Outputs) != 1 || metadata.Outputs[0].Name != "patients" || metadata.Outputs[0].RootResourceType != "Patient" || metadata.Outputs[0].RowGrain != "patient" {
		t.Fatalf("metadata=%+v", metadata)
	}
	if !b.HasArchivedReshape() {
		t.Fatal("retired payload not recognized")
	}
}
func TestCurrentRecipeEntrypointsRejectRetiredPayload(t *testing.T) {
	raw := []byte(archivedRecipeGolden)
	b := decodeTestArchive(t, raw)
	for name, check := range map[string]func() error{
		"Parse":                        func() error { _, err := Parse(raw); return err },
		"Validate":                     b.Validate,
		"ValidateWithWorkspaceOutputs": func() error { return b.ValidateWithWorkspaceOutputs([]string{"patients"}) },
		"CanonicalJSON":                func() error { _, err := b.CanonicalJSON(); return err },
		"CanonicalBundle":              func() error { _, err := b.CanonicalBundle(); return err },
		"Digest":                       func() error { _, err := b.Digest(); return err },
		"CurrentBundle":                func() error { _, err := b.CurrentBundle(); return err },
	} {
		t.Run(name, func(t *testing.T) {
			err := check()
			if err == nil || !strings.Contains(err.Error(), "retired_recipe_shape") {
				t.Fatalf("retired shape accepted or misclassified: %v", err)
			}
		})
	}
}
func TestArchiveScalarCanonicalBytesRetainNumericZerosAndLargeIntegers(t *testing.T) {
	for _, key := range []string{`{"kind":"INTEGER","integer":0}`, `{"kind":"INTEGER","integer":1152921504606846976}`, `{"kind":"DECIMAL","decimal":0}`, `{"kind":"DECIMAL","decimal":-0}`, `{"kind":"BOOLEAN","boolean":false}`, `{"kind":"STRING","string":""}`} {
		t.Run(key, func(t *testing.T) {
			raw := []byte(strings.Replace(archivedRecipeGolden, `{"kind":"STRING","string":"value"}`, key, 1))
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			var record map[string]any
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			reordered, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			b := decodeTestArchive(t, reordered)
			got, err := b.ArchiveCanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, raw) {
				t.Fatalf("typed scalar changed: %s want %s", got, raw)
			}
			sum := sha256.Sum256(raw)
			digest, err := b.ArchiveDigest()
			if err != nil || digest != hex.EncodeToString(sum[:]) {
				t.Fatalf("scalar digest mismatch: %s %v", digest, err)
			}
		})
	}
}
func TestArchiveDecoderRejectsMalformedKnownUnionAndUnknownFields(t *testing.T) {
	tests := []string{
		strings.Replace(archivedRecipeGolden, `"kind":"UNPIVOT"`, `"kind":"GROUPED_PIVOT"`, 1),
		strings.Replace(archivedRecipeGolden, `"kind":"UNPIVOT"`, `"kind":"UNKNOWN"`, 1),
		strings.Replace(archivedRecipeGolden, `"kind":"UNPIVOT",`, `"kind":"UNPIVOT","groupedPivot":{},`, 1),
		strings.Replace(archivedRecipeGolden, `"unpivot":{`, `"unknown":true,"unpivot":{`, 1),
		strings.Replace(archivedRecipeGolden, `"string":"value"`, `"string":"value","integer":0`, 1),
		strings.Replace(archivedRecipeGolden, `"kind":"STRING","string":"value"`, `"kind":"NULL","string":"value"`, 1),
		strings.Replace(archivedRecipeGolden, `"nullRowPolicy":"DROP"`, `"nullRowPolicy":"UNKNOWN"`, 1),
		strings.Replace(archivedRecipeGolden, `"recipeSchemaVersion":1`, `"recipeSchemaVersion":1,"recipeSchemaVersion":1`, 1),
		archivedRecipeGolden + ` {}`,
	}
	for i, raw := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if _, err := ParseArchivedMetadata([]byte(raw)); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	b := decodeTestArchive(t, []byte(archivedRecipeGolden))
	b.Outputs[0].Construction = &Construction{Version: 1}
	if err := b.ValidateArchive(); err == nil {
		t.Fatal("archive validation accepted retired and current construction together")
	}
}
func TestArchiveEncodingDoesNotExpandFragments(t *testing.T) {
	b := decodeTestArchive(t, []byte(archivedRecipeGolden))
	b.Fragments = &FragmentLibrary{Fragments: map[string]Fragment{"unused": {Name: "unused", Version: "1", Expr: Expression{Literal: json.RawMessage(`"x"`)}}}}

	canonical, err := b.ArchiveCanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"fragments"`)) {
		t.Fatal("archive encoder expanded or discarded fragments")
	}
}
func TestCurrentBundleDetachedAndArchiveMetadataNeverExecutable(t *testing.T) {
	raw := []byte(`{"recipeSchemaVersion":1,"name":"current","translationVersion":"v1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]}]}`)
	b, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	current, err := b.CurrentBundle()
	if err != nil {
		t.Fatal(err)
	}
	current.Outputs[0].Fields[0].Name = "changed"
	if b.Outputs[0].Fields[0].Name != "id" {
		t.Fatal("current bundle aliases input")
	}
	if b.HasArchivedReshape() {
		t.Fatal("current bundle marked archived")
	}
	if _, err := ParseArchivedMetadata(raw); err != nil {
		t.Fatal(err)
	}
}
