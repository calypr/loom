package publication

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const fixtureSchemaSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestSchemaSnapshotCanonicalizesCopiesAndValidates(t *testing.T) {
	input := []string{"Patient", "Observation"}
	snapshot, err := NewSchemaSnapshot("urn:test", "R5", fixtureSchemaSHA256, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = "mutated"
	if !reflect.DeepEqual(snapshot.GeneratedResourceTypes, []string{"Observation", "Patient"}) {
		t.Fatalf("resource types = %#v", snapshot.GeneratedResourceTypes)
	}
	for _, candidate := range []SchemaSnapshot{
		{SchemaSHA256: "bad", GeneratedResourceTypes: []string{"Patient"}},
		{SchemaSHA256: strings.Repeat("A", 64), GeneratedResourceTypes: []string{"Patient"}},
		{SchemaSHA256: fixtureSchemaSHA256},
		{SchemaSHA256: fixtureSchemaSHA256, GeneratedResourceTypes: []string{"Patient", "Patient"}},
	} {
		if !errors.Is(candidate.Validate(), ErrInvalidSchemaIdentity) {
			t.Errorf("Validate(%#v) should fail", candidate)
		}
	}
}
