package dataset

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type verificationFixture struct {
	mu      sync.Mutex
	results map[string]PublicationVerification
	errors  map[string]error
}

type manifestReaderFixture struct {
	manifest Manifest
	err      error
}

func (f manifestReaderFixture) ReadManifest(context.Context, Ref) (Manifest, error) {
	return f.manifest, f.err
}

func (f *verificationFixture) VerifyPublication(_ context.Context, _, _ string, selector DataframeSelector) (PublicationVerification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.results[selector.Key()], f.errors[selector.Key()]
}

func TestReleaseActivationRequiresPublishedQueryableGenerationAndPreservesPointerOnFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	stageSnapshot(t, store, "project-a", "commit-a", now)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): {Selector: selector, ExecutionID: "execution-a", Generation: "commit-a", State: "FAILED", Queryable: false},
	}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	if _, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0}); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("failed requirement = %v", err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("failed activation moved pointer: %v", err)
	}
	verification := verifier.results[selector.Key()]
	verification.State, verification.Queryable, verification.VerifiedAt = "PUBLISHED", true, now
	verifier.results[selector.Key()] = verification
	release, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("release creation moved pointer: %v", err)
	}
	active, err := service.ActivateExisting(ctx, "project-a", release.ID, 0)
	if err != nil || active.Revision != 1 || active.Release.Generation != "commit-a" || len(active.Release.Publications) != 1 || active.Release.Publications[0].Stale {
		t.Fatalf("activation = %#v, %v", active, err)
	}
}

func TestValidateGenerationRequiresExistingStagedSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	service := ReleaseService{Snapshots: store}

	if err := service.ValidateGeneration(ctx, "project-a", "missing"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("missing generation validation = %v", err)
	}

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	snapshot, err := NewSnapshotGeneration("project-a", "loading", "", []string{"Patient"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOrResumeSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateGeneration(ctx, "project-a", "loading"); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("loading generation validation = %v", err)
	}

	stageSnapshot(t, store, "project-a", "staged", now)
	if err := service.ValidateGeneration(ctx, "project-a", "staged"); err != nil {
		t.Fatalf("staged generation validation = %v", err)
	}
}

func TestValidateGenerationAcceptsLegacyStagedManifestWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	ref, err := NewRef("project-a", "legacy-generation")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(StateReady)
	if err != nil {
		t.Fatal(err)
	}
	selector := DataframeSelector{Recipe: "explorer", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", ref.Generation, time.Now().UTC()),
	}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Manifests: manifestReaderFixture{manifest: manifest}, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}}
	if err := service.ValidateGeneration(ctx, ref.Project, ref.Generation); err != nil {
		t.Fatalf("legacy staged manifest validation = %v", err)
	}
	if release, err := service.Create(ctx, ActivationRequest{Project: ref.Project, Generation: ref.Generation}); err != nil || release.Generation != ref.Generation {
		t.Fatalf("legacy staged manifest release = %#v, %v", release, err)
	}
}

func TestValidateGenerationRejectsLegacyLoadingManifestWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	ref, err := NewRef("project-a", "legacy-loading")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	service := ReleaseService{Snapshots: store, Manifests: manifestReaderFixture{manifest: manifest}}
	if err := service.ValidateGeneration(ctx, ref.Project, ref.Generation); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("legacy loading manifest validation = %v", err)
	}
}

func TestReleaseActivationCarriesOptionalPublicationAsStale(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	required := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	optional := DataframeSelector{Recipe: "optional", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{required}, Now: func() time.Time { return now }}

	stageSnapshot(t, store, "project-a", "commit-a", now)
	verifier.results[required.Key()] = published(required, "required-a", "commit-a", now)
	verifier.results[optional.Key()] = published(optional, "optional-a", "commit-a", now)
	first, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0, OptionalSelectors: []DataframeSelector{optional}})
	if err != nil || len(first.Release.Publications) != 2 {
		t.Fatalf("first activation = %#v, %v", first, err)
	}

	stageSnapshot(t, store, "project-a", "commit-b", now.Add(time.Hour))
	verifier.results[required.Key()] = published(required, "required-b", "commit-b", now.Add(time.Hour))
	second, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-b", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	bySelector := map[string]ReleasePublication{}
	for _, publication := range second.Release.Publications {
		bySelector[publication.Selector.Key()] = publication
	}
	if !bySelector[optional.Key()].Stale || bySelector[optional.Key()].ExecutionID != "optional-a" || bySelector[required.Key()].Stale {
		t.Fatalf("carried publications = %#v", second.Release.Publications)
	}
}

func TestReleaseActivationAddsPublicationWithoutDroppingSameGenerationPublications(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	stageSnapshot(t, store, "project-a", "commit-a", now)
	firstSelector := DataframeSelector{Recipe: "explorer-one", TranslationVersion: "v1", Output: "Patient"}
	secondSelector := DataframeSelector{Recipe: "explorer-two", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		firstSelector.Key():  published(firstSelector, "execution-a", "commit-a", now),
		secondSelector.Key(): published(secondSelector, "execution-b", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Releases: store, Verifier: verifier, Now: func() time.Time { return now }}

	first, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0, OptionalSelectors: []DataframeSelector{firstSelector}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: first.Revision, OptionalSelectors: []DataframeSelector{secondSelector}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || len(second.Release.Publications) != 2 {
		t.Fatalf("second activation = %#v", second)
	}
	bySelector := map[string]ReleasePublication{}
	for _, publication := range second.Release.Publications {
		bySelector[publication.Selector.Key()] = publication
	}
	if bySelector[firstSelector.Key()].ExecutionID != "execution-a" || bySelector[firstSelector.Key()].Stale || bySelector[secondSelector.Key()].ExecutionID != "execution-b" || bySelector[secondSelector.Key()].Stale {
		t.Fatalf("same-generation publications = %#v", second.Release.Publications)
	}
}

func TestConcurrentReleaseActivationUsesCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Now().UTC()
	stageSnapshot(t, store, "project-a", "commit-a", now)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	optional := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): published(selector, "execution-a", "commit-a", now),
		optional.Key(): published(optional, "execution-b", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	first, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", OptionalSelectors: []DataframeSelector{optional}})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("distinct release contents produced the same ID")
	}

	start := make(chan struct{})
	errorsSeen := make(chan error, 2)
	for _, releaseID := range []string{first.ID, second.ID} {
		go func(releaseID string) {
			<-start
			_, err := service.ActivateExisting(ctx, "project-a", releaseID, 0)
			errorsSeen <- err
		}(releaseID)
	}
	close(start)
	results := []error{<-errorsSeen, <-errorsSeen}
	var success, conflict int
	for _, err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrReleaseActivationConflict) {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("activation results = %#v", results)
	}
}

func TestActivationRetryOfSameReleaseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Now().UTC()
	stageSnapshot(t, store, "project-a", "commit-a", now)
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{selector.Key(): published(selector, "execution-a", "commit-a", now)}, errors: map[string]error{}}
	service := ReleaseService{Snapshots: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	release, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ActivateExisting(ctx, "project-a", release.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := service.ActivateExisting(ctx, "project-a", release.ID, 0)
	if err != nil || !reflect.DeepEqual(retry, first) {
		t.Fatalf("activation retry = %#v, %v; want %#v", retry, err, first)
	}
}

func stageSnapshot(t *testing.T, store *MemoryLifecycleStore, project, generation string, now time.Time) {
	t.Helper()
	snapshot, _ := NewSnapshotGeneration(project, generation, "", []string{"Patient"}, now)
	_, _ = store.CreateOrResumeSnapshot(context.Background(), snapshot)
	upload, _ := NewResourceUpload("Patient", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1, now)
	_, _ = store.RecordSnapshotUpload(context.Background(), snapshot.Dataset, upload)
	if _, err := store.TransitionSnapshot(context.Background(), snapshot.Dataset, StateLoading, StateStaged, now); err != nil {
		t.Fatal(err)
	}
}

func published(selector DataframeSelector, execution, generation string, now time.Time) PublicationVerification {
	return PublicationVerification{Selector: selector, ExecutionID: execution, Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: now}
}
