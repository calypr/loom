package dataset

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestManifestJSONValidationAndCopy(t *testing.T) {
	manifest := readyManifest(t, "project-a", "generation-a")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal(Manifest): %v", err)
	}
	var decoded Manifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(Manifest): %v", err)
	}
	if !decoded.Dataset.Equal(manifest.Dataset) || decoded.State != manifest.State || !decoded.SchemaIdentity.Equal(manifest.SchemaIdentity) || decoded.AnalysisVersion != manifest.AnalysisVersion {
		t.Fatalf("manifest did not round trip\ngot:  %#v\nwant: %#v", decoded, manifest)
	}

	clone := decoded.Clone()
	cloneTypes := clone.SchemaIdentity.GeneratedResourceTypes()
	cloneTypes[0] = "mutated"
	if decoded.SchemaIdentity.GeneratedResourceTypes()[0] == "mutated" {
		t.Fatal("manifest clone exposed shared schema slice")
	}

	if _, err := json.Marshal(Manifest{}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("json.Marshal(invalid Manifest) error = %v, want ErrInvalidManifest", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode manifest JSON: %v", err)
	}
	fields["unknown"] = json.RawMessage(`true`)
	unknown, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode unknown manifest JSON: %v", err)
	}
	if err := json.Unmarshal(unknown, &decoded); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("json.Unmarshal(unknown Manifest field) error = %v, want ErrInvalidManifest", err)
	}
}
