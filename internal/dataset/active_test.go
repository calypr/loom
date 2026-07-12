package dataset

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestActiveGenerationResolutionAndActivationPlan(t *testing.T) {
	previous := readyManifest(t, "project-a", "generation-old")
	candidate := readyManifest(t, "project-a", "generation-new")
	active, err := ActiveGenerationFor(candidate)
	if err != nil {
		t.Fatalf("ActiveGenerationFor(candidate): %v", err)
	}
	resolved, err := ResolveActive(active, []Manifest{previous, candidate})
	if err != nil {
		t.Fatalf("ResolveActive: %v", err)
	}
	if !resolved.Dataset.Equal(candidate.Dataset) {
		t.Fatalf("ResolveActive dataset = %#v, want %#v", resolved.Dataset, candidate.Dataset)
	}

	plan, err := PlanActivation(&previous, candidate)
	if err != nil {
		t.Fatalf("PlanActivation: %v", err)
	}
	if !plan.Active.Dataset.Equal(candidate.Dataset) {
		t.Fatalf("activation active = %#v, want %#v", plan.Active.Dataset, candidate.Dataset)
	}
	if plan.Previous == nil || !plan.Previous.Equal(previous.Dataset) {
		t.Fatalf("activation previous = %#v, want %#v", plan.Previous, previous.Dataset)
	}
	if previous.State != ManifestStateReady {
		t.Fatalf("PlanActivation mutated previous manifest to %s", previous.State)
	}

	idempotent, err := PlanActivation(&candidate, candidate)
	if err != nil {
		t.Fatalf("PlanActivation(idempotent): %v", err)
	}
	if idempotent.Previous != nil {
		t.Fatalf("idempotent plan Previous = %#v, want nil", idempotent.Previous)
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal(ActivationPlan): %v", err)
	}
	var decoded ActivationPlan
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ActivationPlan): %v", err)
	}
	if !decoded.Active.Dataset.Equal(plan.Active.Dataset) || decoded.Previous == nil || !decoded.Previous.Equal(*plan.Previous) {
		t.Fatalf("activation plan did not round trip\ngot:  %#v\nwant: %#v", decoded, plan)
	}

	otherProject := readyManifest(t, "project-b", "generation-old")
	if _, err := PlanActivation(&otherProject, candidate); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("PlanActivation(cross-project) error = %v, want ErrInvalidActiveGeneration", err)
	}
	loading := transitionManifest(t, fixtureManifest(t, "project-a", "generation-loading"), ManifestStateLoading)
	if _, err := PlanActivation(&previous, loading); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("PlanActivation(LOADING candidate) error = %v, want ErrGenerationNotReady", err)
	}
	if _, err := PlanActivation(&previous, failedManifest(t, "project-a", "generation-failed")); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("PlanActivation(FAILED candidate) error = %v, want ErrGenerationNotReady", err)
	}
}

func TestResolveActiveRejectsMissingDuplicateAndNonReadyManifests(t *testing.T) {
	ready := readyManifest(t, "project-a", "generation-ready")
	active, err := ActiveGenerationFor(ready)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveActive(active, nil); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("ResolveActive(missing) error = %v, want ErrInvalidActiveGeneration", err)
	}
	if _, err := ResolveActive(active, []Manifest{ready, ready.Clone()}); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("ResolveActive(duplicate) error = %v, want ErrInvalidActiveGeneration", err)
	}

	failed := failedManifest(t, "project-a", "generation-ready")
	if _, err := ResolveActive(active, []Manifest{failed}); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("ResolveActive(FAILED matching generation) error = %v, want ErrGenerationNotReady", err)
	}
	if _, err := ActiveGenerationFor(failed); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("ActiveGenerationFor(FAILED) error = %v, want ErrGenerationNotReady", err)
	}
}

func TestReadBindingPinsReadyGenerationAndScope(t *testing.T) {
	ready := readyManifest(t, "project-a", "generation-ready")
	active, err := ActiveGenerationFor(ready)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := RestrictedAuthScopeFingerprint([]string{"project-a-scope"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := BindActive(active, ready, scope)
	if err != nil {
		t.Fatalf("BindActive: %v", err)
	}
	if !binding.Dataset().Equal(ready.Dataset) || !binding.AuthScopeFingerprint().Equal(scope) {
		t.Fatalf("binding = %#v, want generation/scope from ready manifest", binding)
	}

	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("json.Marshal(ReadBinding): %v", err)
	}
	if bytes.Contains(encoded, []byte("project-a-scope")) {
		t.Fatalf("ReadBinding exposed raw scope path: %s", encoded)
	}
	var decoded ReadBinding
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(ReadBinding): %v", err)
	}
	if !decoded.Dataset().Equal(binding.Dataset()) || decoded.AnalysisVersion() != binding.AnalysisVersion() || !decoded.AuthScopeFingerprint().Equal(binding.AuthScopeFingerprint()) {
		t.Fatalf("ReadBinding did not round trip\ngot:  %#v\nwant: %#v", decoded, binding)
	}

	loading := transitionManifest(t, fixtureManifest(t, "project-a", "generation-loading"), ManifestStateLoading)
	loadingActive := ActiveGeneration{Dataset: loading.Dataset}
	if _, err := BindActive(loadingActive, loading, scope); !errors.Is(err, ErrGenerationNotReady) {
		t.Fatalf("BindActive(LOADING) error = %v, want ErrGenerationNotReady", err)
	}
	other := readyManifest(t, "project-a", "generation-other")
	if _, err := BindActive(active, other, scope); !errors.Is(err, ErrInvalidActiveGeneration) {
		t.Fatalf("BindActive(mismatch) error = %v, want ErrInvalidActiveGeneration", err)
	}
	if _, err := BindActive(active, ready, AuthScopeFingerprint{}); !errors.Is(err, ErrInvalidAuthScopeFingerprint) {
		t.Fatalf("BindActive(invalid scope) error = %v, want ErrInvalidAuthScopeFingerprint", err)
	}
}
