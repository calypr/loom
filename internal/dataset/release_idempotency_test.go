package dataset

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func storedReleaseCandidates(store *MemoryLifecycleStore, project string) map[string]ProjectRelease {
	store.mu.RLock()
	defer store.mu.RUnlock()
	prefix := project + "\x00"
	candidates := map[string]ProjectRelease{}
	for key, record := range store.releaseRecords {
		if strings.HasPrefix(key, prefix) {
			candidates[strings.TrimPrefix(key, prefix)] = record
		}
	}
	return candidates
}

func candidateIDs(candidates map[string]ProjectRelease) []string {
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	return ids
}

func r2FixtureService(store *MemoryLifecycleStore, now time.Time, required ...DataframeSelector) ReleaseService {
	return ReleaseService{Manifests: store, Releases: store, Verifier: nil, Required: required, Now: func() time.Time { return now }}
}

func TestIdenticalReleaseContentRemainsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	stageManifest(t, store, "project-a", "commit-a")
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	request := ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0}

	first, err := service.Activate(ctx, request)
	if err != nil || first.Revision != 1 {
		t.Fatalf("first activation = %#v, %v", first, err)
	}

	for attempt := 2; attempt <= 4; attempt++ {
		repeat, err := service.Activate(ctx, request)
		if err != nil {
			t.Fatalf("identical activation replay #%d = %v", attempt, err)
		}
		if !reflect.DeepEqual(repeat, first) {
			t.Fatalf("REGRESSION: identical replay #%d moved the active release: got %#v want %#v", attempt, repeat, first)
		}
	}

	for attempt := 1; attempt <= 3; attempt++ {
		created, err := service.Create(ctx, ActivationRequest{Project: request.Project, Generation: request.Generation})
		if err != nil {
			t.Fatalf("identical Create #%d = %v", attempt, err)
		}
		if created.ID != first.Release.ID {
			t.Fatalf("REGRESSION: identical Create #%d minted a new identity %s, want %s", attempt, created.ID, first.Release.ID)
		}
	}

	candidates := storedReleaseCandidates(store, "project-a")
	if len(candidates) != 1 {
		t.Fatalf("REGRESSION: identical snapshots retained %d immutable candidates: %v", len(candidates), candidateIDs(candidates))
	}
	retained, ok := candidates[first.Release.ID]
	if !ok {
		t.Fatalf("retained candidate for the active id %s is missing, have %v", first.Release.ID, candidateIDs(candidates))
	}
	if !reflect.DeepEqual(retained, first.Release) {
		t.Fatalf("REGRESSION: the retained candidate drifted from the immutable first save: got %#v want %#v", retained, first.Release)
	}
	t.Logf("VERDICT=PASS 4 identical activations and 3 identical saves converge: revision 1, id %s, exactly one immutable candidate", first.Release.ID)
}

func TestLegacyReleaseIDReactivationConvergesOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", "commit-a", now),
	}, errors: map[string]error{}}

	probe := NewMemoryLifecycleStore()
	stageManifest(t, probe, "project-a", "commit-a")
	probeService := r2FixtureService(probe, now, selector)
	probeService.Verifier = verifier
	derived, err := probeService.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"})
	if err != nil {
		t.Fatalf("derive current release: %v", err)
	}
	if derived.ID != releaseID(derived) {
		t.Fatalf("witness precondition broken: saved id %s is not the derived id %s", derived.ID, releaseID(derived))
	}
	legacy := derived
	legacy.ID = legacyReleaseID(legacy)
	if legacy.ID == derived.ID {
		t.Fatalf("witness precondition broken: the historical framing must derive a different id for this content")
	}

	store := NewMemoryLifecycleStore()
	stageManifest(t, store, "project-a", "commit-a")
	service := r2FixtureService(store, now, selector)
	service.Verifier = verifier
	if _, err := store.SaveRelease(ctx, legacy); err != nil {
		t.Fatalf("seed legacy candidate: %v", err)
	}
	if _, err := store.CompareAndSwapActivateRelease(ctx, legacy, 0); err != nil {
		t.Fatalf("activate legacy candidate: %v", err)
	}
	active, err := store.ReadActiveRelease(ctx, "project-a")
	if err != nil || active.Revision != 1 || active.Release.ID != legacy.ID {
		t.Fatalf("legacy seeding = %#v, %v (want revision 1, id %s)", active, err, legacy.ID)
	}

	reactivation := ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 1}
	transitioned, err := service.Activate(ctx, reactivation)
	if err != nil {
		t.Fatalf("first reactivation of the stored legacy id: %v", err)
	}
	if transitioned.Release.ID == legacy.ID {
		t.Fatalf("REGRESSION: reactivating the stored legacy id %s never transitioned to the widened id", legacy.ID)
	}
	if transitioned.Revision != 2 || transitioned.Release.ID != derived.ID {
		t.Fatalf("REGRESSION: first reactivation must converge on %s at revision 2, got %#v", derived.ID, transitioned)
	}

	for attempt := 3; attempt <= 5; attempt++ {
		repeat, err := service.Activate(ctx, reactivation)
		if err != nil {
			t.Fatalf("repeated reactivation #%d = %v", attempt, err)
		}
		if repeat.Revision != 2 || repeat.Release.ID != transitioned.Release.ID {
			t.Fatalf("REGRESSION: reactivation #%d made a second new-id transition: revision %d id %s (want revision 2 id %s)",
				attempt, repeat.Revision, repeat.Release.ID, transitioned.Release.ID)
		}
	}

	candidates := storedReleaseCandidates(store, "project-a")
	if len(candidates) != 2 {
		t.Fatalf("REGRESSION: at most one new-id transition must retain exactly 2 candidates, got %v", candidateIDs(candidates))
	}
	retainedLegacy, ok := candidates[legacy.ID]
	if !ok || !reflect.DeepEqual(retainedLegacy, legacy) {
		t.Fatalf("REGRESSION: the pre-widening candidate is no longer immutable: got %#v want %#v", retainedLegacy, legacy)
	}
	if _, ok := candidates[transitioned.Release.ID]; !ok {
		t.Fatalf("the transitioned candidate %s is missing, have %v", transitioned.Release.ID, candidateIDs(candidates))
	}
	t.Logf("VERDICT=PASS stored id %s transitioned once to %s; 3 further retries converged at revision 2 with 2 retained candidates", legacy.ID, transitioned.Release.ID)
}
