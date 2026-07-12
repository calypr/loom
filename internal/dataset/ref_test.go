package dataset

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDatasetRefValidationAndJSON(t *testing.T) {
	ref, err := NewDatasetRef("demo-project", "load:2026-07-11/v1")
	if err != nil {
		t.Fatalf("NewDatasetRef: %v", err)
	}
	if got, want := ref.Project, "demo-project"; got != want {
		t.Fatalf("Project = %q, want %q", got, want)
	}
	if got, want := ref.Generation, "load:2026-07-11/v1"; got != want {
		t.Fatalf("Generation = %q, want %q", got, want)
	}

	encoded, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("json.Marshal(DatasetRef): %v", err)
	}
	var decoded DatasetRef
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(DatasetRef): %v", err)
	}
	if !decoded.Equal(ref) {
		t.Fatalf("round trip = %#v, want %#v", decoded, ref)
	}

	for _, candidate := range []DatasetRef{
		{},
		{Project: " project", Generation: "generation"},
		{Project: "project", Generation: "generation\nnext"},
		{Project: repeated("p", maxOpaqueIdentifierBytes+1), Generation: "generation"},
	} {
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidDatasetRef) {
			t.Errorf("Validate(%#v) error = %v, want ErrInvalidDatasetRef", candidate, err)
		}
		if _, err := json.Marshal(candidate); !errors.Is(err, ErrInvalidDatasetRef) {
			t.Errorf("json.Marshal(%#v) error = %v, want ErrInvalidDatasetRef", candidate, err)
		}
	}

	for _, raw := range []string{
		`{"project":"demo"}`,
		`{"project":"demo","generation":"g","unknown":true}`,
		`{"project":"demo","generation":" g"}`,
		`[]`,
	} {
		var value DatasetRef
		if err := json.Unmarshal([]byte(raw), &value); !errors.Is(err, ErrInvalidDatasetRef) {
			t.Errorf("json.Unmarshal(%s) error = %v, want ErrInvalidDatasetRef", raw, err)
		}
	}
}
