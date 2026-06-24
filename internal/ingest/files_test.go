package ingest

import "testing"

func TestResourceTypeFromPath(t *testing.T) {
	cases := map[string]string{
		"/tmp/META/Patient.ndjson":     "Patient",
		"/tmp/META/Specimen.ndjson.gz": "Specimen",
		"DocumentReference.ndjson":     "DocumentReference",
		"Observation.ndjson.gz":        "Observation",
	}
	for path, want := range cases {
		if got := ResourceTypeFromPath(path); got != want {
			t.Fatalf("ResourceTypeFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}
