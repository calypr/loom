package dataset

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/graphschema"
)

func TestSnapshotSchemaIdentityPreservesSourceAndCopies(t *testing.T) {
	identity, err := graphschema.Load(filepath.Join("..", "..", "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("graphschema.Load: %v", err)
	}
	snapshot, err := SnapshotSchemaIdentity(identity)
	if err != nil {
		t.Fatalf("SnapshotSchemaIdentity: %v", err)
	}
	if got, want := snapshot.SchemaID(), identity.SchemaID(); got != want {
		t.Fatalf("SchemaID() = %q, want %q", got, want)
	}
	if got, want := snapshot.SchemaSHA256(), identity.SchemaSHA256(); got != want {
		t.Fatalf("SchemaSHA256() = %q, want %q", got, want)
	}
	if got := snapshot.FHIRVersion(); got != "" {
		t.Fatalf("FHIRVersion() = %q, want empty: graph-fhir.json has no explicit fhirVersion", got)
	}

	resourceTypes := snapshot.GeneratedResourceTypes()
	if len(resourceTypes) == 0 {
		t.Fatal("GeneratedResourceTypes() returned no roots")
	}
	resourceTypes[0] = "mutated"
	if snapshot.GeneratedResourceTypes()[0] == "mutated" {
		t.Fatal("GeneratedResourceTypes() exposed mutable backing storage")
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(SchemaIdentitySnapshot): %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode schema snapshot JSON: %v", err)
	}
	if _, ok := wire["fhirVersion"]; ok {
		t.Fatalf("serialized snapshot invented fhirVersion: %s", encoded)
	}

	var decoded SchemaIdentitySnapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(SchemaIdentitySnapshot): %v", err)
	}
	if !decoded.Equal(snapshot) {
		t.Fatalf("schema snapshot did not round trip\ngot:  %#v\nwant: %#v", decoded, snapshot)
	}
}

func TestSchemaIdentitySnapshotCanonicalizesAndValidates(t *testing.T) {
	source := []string{"Patient", "Observation"}
	snapshot, err := NewSchemaIdentitySnapshot("urn:example", "R5", fixtureSchemaSHA256, source)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot: %v", err)
	}
	if got, want := snapshot.GeneratedResourceTypes(), []string{"Observation", "Patient"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GeneratedResourceTypes() = %#v, want %#v", got, want)
	}
	source[0] = "mutated"
	if got, want := snapshot.GeneratedResourceTypes(), []string{"Observation", "Patient"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot changed after source mutation: %#v, want %#v", got, want)
	}

	clone := snapshot.Clone()
	cloneTypes := clone.GeneratedResourceTypes()
	cloneTypes[0] = "mutated"
	if got, want := snapshot.GeneratedResourceTypes(), []string{"Observation", "Patient"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot changed after clone accessor mutation: %#v, want %#v", got, want)
	}

	for _, test := range []struct {
		name          string
		digest        string
		resourceTypes []string
	}{
		{name: "nonhex digest", digest: repeated("z", 64), resourceTypes: []string{"Patient"}},
		{name: "uppercase digest", digest: repeated("A", 64), resourceTypes: []string{"Patient"}},
		{name: "empty roots", digest: fixtureSchemaSHA256, resourceTypes: nil},
		{name: "duplicate roots", digest: fixtureSchemaSHA256, resourceTypes: []string{"Patient", "Patient"}},
		{name: "blank root", digest: fixtureSchemaSHA256, resourceTypes: []string{" "}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSchemaIdentitySnapshot("urn:example", "", test.digest, test.resourceTypes)
			if !errors.Is(err, ErrInvalidSchemaIdentity) {
				t.Fatalf("NewSchemaIdentitySnapshot error = %v, want ErrInvalidSchemaIdentity", err)
			}
		})
	}

	var decoded SchemaIdentitySnapshot
	if err := json.Unmarshal([]byte(`{"schemaId":"urn:example","schemaSha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","generatedResourceTypes":["Patient","Observation"]}`), &decoded); err != nil {
		t.Fatalf("json.Unmarshal canonicalizable snapshot: %v", err)
	}
	if got, want := decoded.GeneratedResourceTypes(), []string{"Observation", "Patient"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded resources = %#v, want %#v", got, want)
	}
	if _, err := json.Marshal(decoded); err != nil {
		t.Fatalf("json.Marshal canonicalized snapshot: %v", err)
	}

	for _, raw := range []string{
		`{"schemaSha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","generatedResourceTypes":["Patient"],"unknown":true}`,
		`{"schemaSha256":"bad","generatedResourceTypes":["Patient"]}`,
		`{"fhirVersion":null,"schemaSha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","generatedResourceTypes":["Patient"]}`,
	} {
		var value SchemaIdentitySnapshot
		if err := json.Unmarshal([]byte(raw), &value); !errors.Is(err, ErrInvalidSchemaIdentity) {
			t.Errorf("json.Unmarshal(%s) error = %v, want ErrInvalidSchemaIdentity", raw, err)
		}
	}
}
