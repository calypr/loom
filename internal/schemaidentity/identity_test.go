package schemaidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/fhirschema"
)

func TestLoadCheckedInGraphFHIRSchema(t *testing.T) {
	path := filepath.Join("..", "..", "schemas", "graph-fhir.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in graph schema: %v", err)
	}

	identity, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}

	wantDigest := sha256.Sum256(contents)
	if got, want := identity.SchemaSHA256(), hex.EncodeToString(wantDigest[:]); got != want {
		t.Fatalf("SchemaSHA256() = %q, want %q", got, want)
	}
	if got, want := identity.SchemaID(), "http://graph-fhir.io/schema/0.0.2"; got != want {
		t.Fatalf("SchemaID() = %q, want %q", got, want)
	}
	if got := identity.FHIRVersion(); got != "" {
		t.Fatalf("FHIRVersion() = %q, want empty because graph-fhir.json has no explicit top-level fhirVersion", got)
	}

	if got, want := identity.GeneratedResourceTypes(), fhirschema.ResourceTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("GeneratedResourceTypes() = %#v, want generated fhirschema roots %#v", got, want)
	}
	if !sort.StringsAreSorted(identity.GeneratedResourceTypes()) {
		t.Fatal("GeneratedResourceTypes() is not sorted")
	}
}

func TestLoadUsesExactBytesAndOnlyExplicitMetadata(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	contents := []byte("{\n  \"$id\": \"urn:example:graph\",\n  \"fhirVersion\": \"R5\",\n  \"$defs\": {}\n}\n")
	if err := os.WriteFile(first, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, append([]byte(" "), contents...), 0o600); err != nil {
		t.Fatal(err)
	}

	identity, err := Load(first)
	if err != nil {
		t.Fatalf("Load(first): %v", err)
	}
	if got, want := identity.SchemaID(), "urn:example:graph"; got != want {
		t.Fatalf("SchemaID() = %q, want %q", got, want)
	}
	if got, want := identity.FHIRVersion(), "R5"; got != want {
		t.Fatalf("FHIRVersion() = %q, want %q", got, want)
	}

	secondIdentity, err := Load(second)
	if err != nil {
		t.Fatalf("Load(second): %v", err)
	}
	if identity.SchemaSHA256() == secondIdentity.SchemaSHA256() {
		t.Fatal("SchemaSHA256() did not change after an exact-byte change")
	}
}

func TestLoadRootsComeFromCompiledFHIRSchemaNotAlternateInput(t *testing.T) {
	path := writeTempSchema(t, `{
  "$id": "urn:example:alternate-graph",
  "$defs": {
    "NotACompiledFHIRRoot": {
      "type": "object"
    }
  }
}`)

	identity, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if got, want := identity.GeneratedResourceTypes(), fhirschema.ResourceTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("GeneratedResourceTypes() = %#v, want compiled fhirschema roots %#v", got, want)
	}
	for _, resourceType := range identity.GeneratedResourceTypes() {
		if resourceType == "NotACompiledFHIRRoot" {
			t.Fatal("Load classified an alternate input definition instead of using compiled fhirschema metadata")
		}
	}
}

func TestLoadDoesNotInferFHIRVersion(t *testing.T) {
	path := writeTempSchema(t, `{
  "$id": "https://example.test/fhir/R5/schema",
  "version": "definitely-not-a-fhir-version"
}`)

	identity, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if got := identity.FHIRVersion(); got != "" {
		t.Fatalf("FHIRVersion() = %q, want empty without explicit top-level fhirVersion", got)
	}
}

func TestIdentityDefensivelyCopiesAndSerializes(t *testing.T) {
	identity, err := Load(writeTempSchema(t, `{"$id":"urn:example:immutable"}`))
	if err != nil {
		t.Fatal(err)
	}

	roots := identity.GeneratedResourceTypes()
	if len(roots) == 0 {
		t.Fatal("GeneratedResourceTypes() returned no roots")
	}
	roots[0] = "mutated"
	if identity.GeneratedResourceTypes()[0] == "mutated" {
		t.Fatal("GeneratedResourceTypes() exposed mutable backing storage")
	}

	encoded, err := json.Marshal(identity)
	if err != nil {
		t.Fatalf("json.Marshal(Identity): %v", err)
	}
	var serialized struct {
		SchemaID               string   `json:"schemaId"`
		SchemaSHA256           string   `json:"schemaSha256"`
		GeneratedResourceTypes []string `json:"generatedResourceTypes"`
	}
	if err := json.Unmarshal(encoded, &serialized); err != nil {
		t.Fatalf("json.Unmarshal serialized identity: %v", err)
	}
	if got, want := serialized.SchemaID, identity.SchemaID(); got != want {
		t.Fatalf("serialized schemaId = %q, want %q", got, want)
	}
	if got, want := serialized.SchemaSHA256, identity.SchemaSHA256(); got != want {
		t.Fatalf("serialized schemaSha256 = %q, want %q", got, want)
	}
	if got, want := serialized.GeneratedResourceTypes, identity.GeneratedResourceTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("serialized roots = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsMissingAndMalformedInputs(t *testing.T) {
	if _, err := Load("  "); !errors.Is(err, ErrGraphSchemaPathRequired) {
		t.Fatalf("Load(blank) error = %v, want ErrGraphSchemaPathRequired", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.json")
	if _, err := Load(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(missing) error = %v, want os.ErrNotExist", err)
	}

	for _, contents := range []string{
		`{`,
		`[]`,
		`null`,
		`{"$id": 7}`,
		`{"$id": null}`,
		`{"fhirVersion": false}`,
		`{"$id": "one", "$id": "two"}`,
		`{} {}`,
	} {
		path := writeTempSchema(t, contents)
		if _, err := Load(path); !errors.Is(err, ErrMalformedGraphSchema) {
			t.Errorf("Load(%q) error = %v, want ErrMalformedGraphSchema", contents, err)
		}
	}
}

func writeTempSchema(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
