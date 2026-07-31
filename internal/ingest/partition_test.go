package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPartitionNDJSONGroupsMixedFHIRResources(t *testing.T) {
	dir := t.TempDir()
	rows, err := PartitionNDJSON(strings.NewReader(
		`{"resourceType":"Patient","id":"p1"}`+"\n"+
			`{"resourceType":"Specimen","id":"s1"}`+"\n"+
			`{"resourceType":"Patient","id":"p2"}`+"\n",
	), dir)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 {
		t.Fatalf("rows = %d, want 3", rows)
	}
	patient, err := os.ReadFile(filepath.Join(dir, "Patient.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(patient), "\n") != 2 {
		t.Fatalf("Patient rows = %q", patient)
	}
}

func TestPartitionNDJSONRejectsMissingResourceType(t *testing.T) {
	_, err := PartitionNDJSON(strings.NewReader(`{"id":"p1"}`+"\n"), t.TempDir())
	if err == nil {
		t.Fatal("expected missing resourceType error")
	}
}
