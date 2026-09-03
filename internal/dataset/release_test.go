package dataset

import (
	"context"
	"errors"
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
	stageManifest(t, store, "project-a", "commit-a")
	selector := DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		selector.Key(): {Selector: selector, ExecutionID: "execution-a", Generation: "commit-a", State: "FAILED", Queryable: false},
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}, Now: func() time.Time { return now }}
	if _, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0}); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("failed requirement = %v", err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("failed activation moved pointer: %v", err)
	}
	verification := verifier.results[selector.Key()]
	verification.State, verification.Queryable, verification.VerifiedAt = "PUBLISHED", true, now
	verifier.results[selector.Key()] = verification
	if _, err := service.Create(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, ErrNoActiveRelease) {
		t.Fatalf("release creation moved pointer: %v", err)
	}
	active, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0})
	if err != nil || active.Revision != 1 || active.Release.Generation != "commit-a" || len(active.Release.Publications) != 1 || active.Release.Publications[0].Stale {
		t.Fatalf("activation = %#v, %v", active, err)
	}
}

func TestValidateGenerationRequiresExistingStagedManifest(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	service := ReleaseService{Manifests: store}

	if err := service.ValidateGeneration(ctx, "project-a", "missing"); !errors.Is(err, ErrManifestNotFound) {
		t.Fatalf("missing generation validation = %v", err)
	}

	ref, _ := NewRef("project-a", "loading")
	schema, _ := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	loading, _ := NewManifest(ref, schema)
	store.manifests[ref] = loading
	if err := service.ValidateGeneration(ctx, "project-a", "loading"); !errors.Is(err, ErrReleaseRequirementsUnmet) {
		t.Fatalf("loading generation validation = %v", err)
	}

	stageManifest(t, store, "project-a", "staged")
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
	service := ReleaseService{Manifests: manifestReaderFixture{manifest: manifest}, Releases: store, Verifier: verifier, Required: []DataframeSelector{selector}}
	if err := service.ValidateGeneration(ctx, ref.Project, ref.Generation); err != nil {
		t.Fatalf("legacy staged manifest validation = %v", err)
	}
	if release, err := service.Create(ctx, ActivationRequest{Project: ref.Project, Generation: ref.Generation}); err != nil || release.Generation != ref.Generation {
		t.Fatalf("legacy staged manifest release = %#v, %v", release, err)
	}
}

func TestValidateGenerationRejectsLegacyLoadingManifestWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
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
	service := ReleaseService{Manifests: manifestReaderFixture{manifest: manifest}}
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
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Required: []DataframeSelector{required}, Now: func() time.Time { return now }}

	stageManifest(t, store, "project-a", "commit-a")
	verifier.results[required.Key()] = published(required, "required-a", "commit-a", now)
	verifier.results[optional.Key()] = published(optional, "optional-a", "commit-a", now)
	first, err := service.Activate(ctx, ActivationRequest{Project: "project-a", Generation: "commit-a", ExpectedRevision: 0, OptionalSelectors: []DataframeSelector{optional}})
	if err != nil || len(first.Release.Publications) != 2 {
		t.Fatalf("first activation = %#v, %v", first, err)
	}

	stageManifest(t, store, "project-a", "commit-b")
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
	stageManifest(t, store, "project-a", "commit-a")
	firstSelector := DataframeSelector{Recipe: "explorer-one", TranslationVersion: "v1", Output: "Patient"}
	secondSelector := DataframeSelector{Recipe: "explorer-two", TranslationVersion: "v1", Output: "Observation"}
	verifier := &verificationFixture{results: map[string]PublicationVerification{
		firstSelector.Key():  published(firstSelector, "execution-a", "commit-a", now),
		secondSelector.Key(): published(secondSelector, "execution-b", "commit-a", now),
	}, errors: map[string]error{}}
	service := ReleaseService{Manifests: store, Releases: store, Verifier: verifier, Now: func() time.Time { return now }}

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

func stageManifest(t *testing.T, store *MemoryLifecycleStore, project, generation string) {
	t.Helper()
	ref, err := NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := NewSchemaSnapshot("schema-a", "R4", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := NewManifest(ref, schema)
	if err == nil {
		manifest, err = manifest.Transition(StateReady)
	}
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.manifests[ref] = manifest
	store.mu.Unlock()
}

func published(selector DataframeSelector, execution, generation string, now time.Time) PublicationVerification {
	return PublicationVerification{Selector: selector, ExecutionID: execution, Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: now}
}
