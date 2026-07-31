package publication

import (
	"errors"
	"testing"
)

func testManifest(t *testing.T) Manifest {
	t.Helper()
	ref, err := NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("urn:test", "", fixtureSchemaSHA256, []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestManifestTransitions(t *testing.T) {
	loading := testManifest(t)
	if loading.State != StateLoading {
		t.Fatalf("new state = %s", loading.State)
	}
	for _, next := range []State{StateReady, StateFailed} {
		got, err := loading.Transition(next)
		if err != nil || got.State != next {
			t.Errorf("Transition(%s) = %#v, %v", next, got, err)
		}
		if _, err := got.Transition(StateLoading); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("terminal transition from %s = %v", next, err)
		}
	}
	if _, err := loading.Transition(StateLoading); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("LOADING -> LOADING = %v", err)
	}
}
