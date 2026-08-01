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
	path := filepath.Join("..", "..", "schemas", "graph-fhir.json")
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
