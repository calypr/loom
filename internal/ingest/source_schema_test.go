package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestLoadSchemaSnapshot(t *testing.T) {
	path := filepath.Join("..", "..", "fhir", "graph-fhir.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadSchemaSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if snapshot.SchemaSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("digest = %q", snapshot.SchemaSHA256)
	}
	if snapshot.SchemaID != "http://graph-fhir.io/schema/0.0.2" || snapshot.FHIRVersion != "" {
		t.Fatalf("metadata = %#v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.GeneratedResourceTypes, fhirschema.ResourceTypes()) || !sort.StringsAreSorted(snapshot.GeneratedResourceTypes) {
		t.Fatalf("resource types = %#v", snapshot.GeneratedResourceTypes)
	}
}

func TestLoadSchemaSnapshotUsesExactBytesAndExplicitMetadata(t *testing.T) {
	contents := []byte("{\n  \"$id\": \"urn:example:graph\",\n  \"fhirVersion\": \"R5\"\n}\n")
	first := writeTempSchemaBytes(t, contents)
	second := writeTempSchemaBytes(t, append([]byte(" "), contents...))
	one, err := loadSchemaSnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := loadSchemaSnapshot(second)
	if err != nil {
		t.Fatal(err)
	}
	if one.SchemaID != "urn:example:graph" || one.FHIRVersion != "R5" || one.SchemaSHA256 == two.SchemaSHA256 {
		t.Fatalf("snapshots = %#v %#v", one, two)
	}
}

func TestRuntimeGraphSchemaMatchesLegacyRootAllowlist(t *testing.T) {
	schema, err := loadRuntimeGraphSchema(repoPath(t, "fhir", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	for resourceType, want := range map[string]bool{
		"Patient":       true,
		"BodyStructure": true,
		"Resource":      true,
		"Directory":     false,
	} {
		if got := schema.HasClass(resourceType); got != want {
			t.Errorf("HasClass(%q) = %t, want %t", resourceType, got, want)
		}
	}
}

func TestRuntimeGraphSchemaPreservesConfiguredAllowlist(t *testing.T) {
	path := writeTempSchemaBytes(t, []byte(`{
		"$id":"urn:example:graph",
		"$defs":{
			"Patient":{"$id":"urn:example:graph/Patient","title":"Patient","type":"object"},
			"Observation":{"$id":"urn:example:graph/Observation","title":"Observation","type":"object"}
		},
		"anyOf":[{"$ref":"urn:example:graph/Patient"}]
	}`))
	schema, err := loadRuntimeGraphSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if !schema.HasClass("Patient") || schema.HasClass("Observation") {
		t.Fatalf("configured class allowlist = %#v", schema.classes)
	}
	if !supportsGeneratedLoad("Observation") {
		t.Fatal("fixture requires Observation to remain available in generated code")
	}
}

func TestRuntimeGraphSchemaResolvesLocalReferencesAndNestedRoots(t *testing.T) {
	for name, roots := range map[string]string{
		"pointer reference": `[{"$ref":"#/$defs/Patient"}]`,
		"absolute pointer":  `[{"$ref":"urn:example:graph#/$defs/Patient"}]`,
		"nested anyOf":      `[{"anyOf":[{"$ref":"urn:example:graph/Patient"}]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := writeTempSchemaBytes(t, []byte(`{
				"$id":"urn:example:graph",
				"$defs":{"Patient":{"$id":"urn:example:graph/Patient","title":"Patient","type":"object"}},
				"anyOf":`+roots+`
			}`))
			schema, err := loadRuntimeGraphSchema(path)
			if err != nil {
				t.Fatal(err)
			}
			if !schema.HasClass("Patient") {
				t.Fatalf("configured Patient is missing from class allowlist: %#v", schema.classes)
			}
		})
	}
}

func TestLoadSchemaSnapshotRejectsMalformedInputs(t *testing.T) {
	if _, err := loadSchemaSnapshot("  "); !errors.Is(err, ErrGraphSchemaPathRequired) {
		t.Fatalf("blank path = %v", err)
	}
	for _, contents := range []string{"{", "[]", "null", `{"$id":7}`, `{"$id":null}`, `{"fhirVersion":false}`, `{"$id":"one","$id":"two"}`, `{} {}`} {
		if _, err := loadSchemaSnapshot(writeTempSchemaBytes(t, []byte(contents))); !errors.Is(err, ErrMalformedGraphSchema) {
			t.Errorf("load %q = %v", contents, err)
		}
	}
}

func writeTempSchemaBytes(t *testing.T, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
