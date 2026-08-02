package publication

import (
	"reflect"
	"testing"
)

func TestFlatColumnNamePrefixesRootOnly(t *testing.T) {
	for _, test := range []struct {
		resourceType string
		name         string
		want         string
	}{
		{resourceType: "DocumentReference", name: "size", want: "document_reference_size"},
		{resourceType: "MedicationAdministration", name: "status", want: "medication_administration_status"},
		{resourceType: "DocumentReference", name: "specimen__id", want: "specimen__id"},
		{resourceType: "DocumentReference", name: "document_reference_title", want: "document_reference_title"},
		{resourceType: "DocumentReference", name: "auth_resource_path", want: "auth_resource_path"},
	} {
		if got := FlatColumnName(test.resourceType, test.name); got != test.want {
			t.Fatalf("FlatColumnName(%q, %q) = %q, want %q", test.resourceType, test.name, got, test.want)
		}
	}
}

func TestQualifyFlatRowMatchesPublishedColumns(t *testing.T) {
	got, err := QualifyFlatRow("DocumentReference", map[string]any{
		"id":                 "doc-1",
		"size":               int64(42),
		"specimen__id":       "spec-1",
		"auth_resource_path": "/programs/example",
		"__loom_row_id":      uint64(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"document_reference_id":   "doc-1",
		"document_reference_size": int64(42),
		"specimen__id":            "spec-1",
		"auth_resource_path":      "/programs/example",
		"__loom_row_id":           uint64(1),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qualified row = %#v, want %#v", got, want)
	}
}

func TestQualifyFlatRowRejectsCollisions(t *testing.T) {
	_, err := QualifyFlatRow("DocumentReference", map[string]any{
		"id":                    "doc-1",
		"document_reference_id": "doc-2",
	})
	if err == nil {
		t.Fatal("expected collision error")
	}
}
