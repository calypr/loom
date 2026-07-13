package dataset

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestManifestLifecycleTransitionsAndTerminalStates(t *testing.T) {
	preflight := fixtureManifest(t, "project-a", "generation-a")
	if got, want := preflight.State, ManifestStatePreflight; got != want {
		t.Fatalf("initial state = %s, want %s", got, want)
	}
	if _, err := preflight.Transition(ManifestStateReady); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("PREFLIGHT -> READY error = %v, want ErrInvalidTransition", err)
	}

	loading := transitionManifest(t, preflight, ManifestStateLoading)
	if preflight.State != ManifestStatePreflight {
		t.Fatalf("Transition mutated source state to %s", preflight.State)
	}
	if _, err := loading.Transition(ManifestStateReady); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("LOADING -> READY error = %v, want ErrInvalidTransition", err)
	}

	analyzing := transitionManifest(t, loading, ManifestStateAnalyzing)
	version, err := NewAnalysisVersion("analysis-2026-07-11")
	if err != nil {
		t.Fatalf("NewAnalysisVersion: %v", err)
	}
	withAnalysis, err := analyzing.WithAnalysisVersion(version)
	if err != nil {
		t.Fatalf("WithAnalysisVersion: %v", err)
	}
	if analyzing.AnalysisVersion.IsSet() {
		t.Fatal("WithAnalysisVersion mutated source manifest")
	}
	if got, want := withAnalysis.AnalysisVersion, version; got != want {
		t.Fatalf("AnalysisVersion = %q, want %q", got, want)
	}

	ready := transitionManifest(t, withAnalysis, ManifestStateReady)
	if !ready.IsReady() {
		t.Fatal("READY manifest was not eligible for activation")
	}
	if _, err := ready.WithAnalysisVersion(version); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("READY WithAnalysisVersion error = %v, want ErrInvalidTransition", err)
	}
	if _, err := ready.Transition(ManifestStateFailed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("READY -> FAILED error = %v, want ErrInvalidTransition", err)
	}

	superseded := transitionManifest(t, ready, ManifestStateSuperseded)
	if _, err := superseded.Transition(ManifestStateReady); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SUPERSEDED -> READY error = %v, want ErrInvalidTransition", err)
	}
	if _, err := ActiveGenerationFor(superseded); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("ActiveGenerationFor(SUPERSEDED) error = %v, want ErrGenerationNotReady", err)
	}

	failed := failedManifest(t, "project-a", "generation-failed")
	if _, err := failed.Transition(ManifestStateLoading); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("FAILED -> LOADING error = %v, want ErrInvalidTransition", err)
	}
	if _, err := ActiveGenerationFor(failed); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("ActiveGenerationFor(FAILED) error = %v, want ErrGenerationNotReady", err)
	}
}

func TestManifestReadyAllowsAnalysisPlaceholderButNeverSynthesizesIt(t *testing.T) {
	ready := readyManifest(t, "project-a", "generation-no-analysis")
	if ready.AnalysisVersion.IsSet() {
		t.Fatalf("ready AnalysisVersion = %q, want empty C1 placeholder", ready.AnalysisVersion)
	}
	if err := ready.AnalysisVersion.Validate(); err != nil {
		t.Fatalf("empty analysis placeholder Validate: %v", err)
	}
	if _, err := NewAnalysisVersion(""); !errors.Is(err, ErrInvalidAnalysisVersion) {
		t.Fatalf("NewAnalysisVersion(empty) error = %v, want ErrInvalidAnalysisVersion", err)
	}
	var decoded AnalysisVersion
	if err := json.Unmarshal([]byte("null"), &decoded); !errors.Is(err, ErrInvalidAnalysisVersion) {
		t.Fatalf("json.Unmarshal(null AnalysisVersion) error = %v, want ErrInvalidAnalysisVersion", err)
	}
}

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
