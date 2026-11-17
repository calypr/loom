package dataset

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

func TestManifestTerminalStatesRejectEveryOutgoingTransition(t *testing.T) {
	for _, terminal := range []State{StateFailed, StateReady} {
		source, err := testManifest(t).Transition(terminal)
		if err != nil {
			t.Fatalf("setup Transition(%s): %v", terminal, err)
		}
		for _, next := range []State{StateLoading, StateStaged, StateReady, StateFailed} {
			if _, err := source.Transition(next); !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Transition(%s -> %s) = %v, want ErrInvalidTransition", terminal, next, err)
			}
		}
	}
	staged, err := testManifest(t).Transition(StateStaged)
	if err != nil || staged.State != StateStaged {
		t.Fatalf("LOADING -> STAGED = %#v, %v (positive path for activation)", staged, err)
	}
}
