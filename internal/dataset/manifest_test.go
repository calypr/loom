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
	if loading.IdentityFormat != IdentityFormatCompact {
		t.Fatalf("new identity format = %q, want %q", loading.IdentityFormat, IdentityFormatCompact)
	}
	legacyReady := loading
	legacyReady.State = StateReady
	if !legacyReady.IsStaged() {
		t.Fatal("legacy READY manifest is not activatable")
	}
	terminalStates := map[State]Manifest{StateReady: legacyReady}
	for _, next := range []State{StateStaged, StateFailed} {
		got, err := loading.Transition(next)
		if err != nil || got.State != next {
			t.Errorf("Transition(%s) = %#v, %v", next, got, err)
		}
		terminalStates[next] = got
	}
	for _, next := range []State{StateLoading, StateReady} {
		if _, err := loading.Transition(next); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("LOADING -> %s = %v, want ErrInvalidTransition", next, err)
		}
	}
	for _, state := range []State{StateStaged, StateFailed, StateReady} {
		source := terminalStates[state]
		for _, next := range []State{StateLoading, StateStaged, StateReady, StateFailed} {
			if _, err := source.Transition(next); !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Transition(%s -> %s) = %v, want ErrInvalidTransition", state, next, err)
			}
		}
	}
}

func TestManifestAcceptsLegacyIdentityFormatAbsence(t *testing.T) {
	legacy := testManifest(t)
	legacy.IdentityFormat = ""
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy manifest without identity format: %v", err)
	}
	legacy.IdentityFormat = "unknown"
	if err := legacy.Validate(); err == nil {
		t.Fatal("manifest with an unknown identity format was accepted")
	}
}
