package source

import (
	"reflect"
	"sort"
	"testing"

	generated "github.com/calypr/loom/fhir/schema"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestLoadSchemaSnapshotUsesOfficialGeneratedIdentity(t *testing.T) {
	snapshot, err := LoadSchemaSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaID != generated.SchemaID || snapshot.FHIRVersion != generated.FHIRVersion || snapshot.SchemaSHA256 != generated.SourceSHA256 {
		t.Fatalf("snapshot = %#v, want generated schema identity", snapshot)
	}
	if len(snapshot.SchemaSHA256) != 64 {
		t.Fatalf("schema SHA-256 = %q", snapshot.SchemaSHA256)
	}
	resourceTypes := append([]string(nil), generated.ResourceTypes...)
	sort.Strings(resourceTypes)
	if !reflect.DeepEqual(snapshot.GeneratedResourceTypes, resourceTypes) {
		t.Fatalf("resource types = %#v, want %#v", snapshot.GeneratedResourceTypes, resourceTypes)
	}
}

func TestRuntimeGraphSchemaUsesGeneratedResourceRegistry(t *testing.T) {
	schema := LoadRuntimeFHIRSchema()
	for _, resourceType := range generated.ResourceTypes {
		if !schema.HasClass(resourceType) {
			t.Errorf("generated resource %q is missing from runtime registry", resourceType)
		}
	}
	for _, resourceType := range []string{"Directory", "Unknown"} {
		if schema.HasClass(resourceType) {
			t.Errorf("unregistered resource %q is accepted", resourceType)
		}
	}
}

func TestPreviousSchemaSnapshotsRemainReadable(t *testing.T) {
	previous, err := publication.NewSchemaSnapshot(
		"http://graph-fhir.io/schema/0.0.2",
		"",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		[]string{"Observation", "Patient"},
	)
	if err != nil {
		t.Fatalf("validate prior published identity: %v", err)
	}
	if err := previous.Validate(); err != nil {
		t.Fatalf("read prior published identity: %v", err)
	}
}
