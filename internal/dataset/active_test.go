package dataset

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestActiveGenerationResolutionAndActivationPlan(t *testing.T) {
	previous := readyManifest(t, "project-a", "generation-old")
	candidate := readyManifest(t, "project-a", "generation-new")
	if _, err := ActiveGenerationFor(candidate); err != nil {
		t.Fatalf("ActiveGenerationFor(candidate): %v", err)
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
