package ingest

import "testing"

func TestNormalizeLoadOptions(t *testing.T) {
	got := normalizeLoadOptions(LoadOptions{})
	if got.BatchSize != 5000 || got.ProgressEvery != 50000 || got.WriterCount != 8 || got.WriteAPI != "import" {
		t.Fatalf("normalizeLoadOptions defaults = %+v", got)
	}

	got = normalizeLoadOptions(LoadOptions{BatchSize: 13, ProgressEvery: 17, WriterCount: 3, WriteAPI: "documents"})
	if got.BatchSize != 13 || got.ProgressEvery != 17 || got.WriterCount != 3 || got.WriteAPI != "documents" {
		t.Fatalf("normalizeLoadOptions overwrote explicit values: %+v", got)
	}
}

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
