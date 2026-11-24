package dataset

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type devFaultReleaseRepository struct {
	releases         map[string]ProjectRelease
	active           *ActiveRelease
	casComparisons   int
	casExpectedSeen  []int64
	casCurrentSeen   []int64
	saveReleaseCalls int
}

func (r *devFaultReleaseRepository) SaveRelease(_ context.Context, release ProjectRelease) (ProjectRelease, error) {
	r.saveReleaseCalls++
	if r.releases == nil {
		r.releases = map[string]ProjectRelease{}
	}
	r.releases[release.ID] = release
	return release, nil
}

func (r *devFaultReleaseRepository) ReadActiveRelease(_ context.Context, _ string) (ActiveRelease, error) {
	if r.active == nil {
		return ActiveRelease{}, ErrNoActiveRelease
	}
	return *r.active, nil
}

func (r *devFaultReleaseRepository) CompareAndSwapActivateRelease(_ context.Context, release ProjectRelease, expectedRevision int64) (ActiveRelease, error) {
	r.casComparisons++
	current := int64(0)
	if r.active != nil {
		current = r.active.Revision
	}
	r.casExpectedSeen = append(r.casExpectedSeen, expectedRevision)
	r.casCurrentSeen = append(r.casCurrentSeen, current)
	alreadyActive := r.active != nil && r.active.Release.ID == release.ID
	if !alreadyActive && expectedRevision != current {
		return ActiveRelease{}, ErrReleaseActivationConflict
	}
	if alreadyActive {
		return *r.active, nil
	}
	if _, ok := r.releases[release.ID]; !ok {
		return ActiveRelease{}, ErrReleaseActivationConflict
	}
	next := ActiveRelease{Release: release, Revision: current + 1}
	r.active = &next
	return next, nil
}

type devFaultManifestReader struct{ state State }

func (m devFaultManifestReader) ReadManifest(_ context.Context, ref Ref) (Manifest, error) {
	schema, err := NewSchemaSnapshot("loom-test-schema", "4.0.1", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{Dataset: ref, State: m.state, SchemaIdentity: schema}, nil
}

type devFaultVerifier struct{}

func (devFaultVerifier) VerifyPublication(_ context.Context, project, generation string, selector DataframeSelector) (PublicationVerification, error) {
	return PublicationVerification{Selector: selector, ExecutionID: "execution-dev-fault", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: time.Now().UTC()}, nil
}

func newDevFaultReleaseService(repository ReleaseRepository) *ReleaseService {
	return &ReleaseService{
		Manifests: devFaultManifestReader{state: StateStaged},
		Releases:  repository,
		Verifier:  devFaultVerifier{},
	}
}

func seedActiveRelease(t *testing.T, repository *devFaultReleaseRepository, project string, revision int64) ProjectRelease {
	t.Helper()
	release := ProjectRelease{ID: "release_prior", Project: project, GitCommit: "generation-prior", Generation: "generation-prior", CreatedAt: time.Now().UTC()}
	if repository.releases == nil {
		repository.releases = map[string]ProjectRelease{}
	}
	repository.releases[release.ID] = release
	repository.active = &ActiveRelease{Release: release, Revision: revision}
	return release
}

func TestDevRepositoryActivationConflictOnceTripsTheRealStoreCASOnce(t *testing.T) {
	const project, generation = "project-a", "generation-a"
	repository := &devFaultReleaseRepository{}
	prior := seedActiveRelease(t, repository, project, 7)
	armed := &atomic.Bool{}
	armed.Store(true)

	runtime := NewReleaseRuntime(newDevFaultReleaseService(repository), false, armed.Load())
	activate := repositoryActivateForTest(runtime)

	err := activate(context.Background(), project, generation, nil)
	if !errors.Is(err, ErrReleaseActivationConflict) {
		t.Fatalf("armed publish activation error = %v, want the store's own ErrReleaseActivationConflict", err)
	}
	if repository.casComparisons != 1 {
		t.Fatalf("armed activation performed %d CAS attempts, want exactly one", repository.casComparisons)
	}

	if len(repository.casExpectedSeen) != 1 || repository.casExpectedSeen[0] != 8 || repository.casCurrentSeen[0] != 7 {
		t.Fatalf("fault did not corrupt exactly the CAS input once: expected=%v current=%v", repository.casExpectedSeen, repository.casCurrentSeen)
	}

	if repository.active == nil || repository.active.Release.ID != prior.ID || repository.active.Revision != 7 {
		t.Fatalf("lost CAS moved the prior active pointer: %#v", repository.active)
	}

	if err := activate(context.Background(), project, generation, nil); err != nil {
		t.Fatalf("post-fault activation error = %v, want success after the one-shot was consumed", err)
	}
	if len(repository.casExpectedSeen) != 2 || repository.casExpectedSeen[1] != 7 {
		t.Fatalf("post-fault activation fed a corrupted CAS input: %v", repository.casExpectedSeen)
	}
	if repository.active == nil || repository.active.Revision != 8 {
		t.Fatalf("post-fault activation did not advance the prior pointer once: %#v", repository.active)
	}
	if repository.active.Release.ID == prior.ID {
		t.Fatalf("post-fault activation re-activated the prior release instead of the candidate")
	}
	if repository.active.Release.Generation != generation {
		t.Fatalf("post-fault activation pointer = %#v, want the published generation", repository.active)
	}
}

func TestDevRepositoryActivationConflictUnarmedNeverFaults(t *testing.T) {
	const project, generation = "project-a", "generation-a"
	repository := &devFaultReleaseRepository{}
	seedActiveRelease(t, repository, project, 3)
	unarmed := &atomic.Bool{}

	activate := repositoryActivateForTest(NewReleaseRuntime(newDevFaultReleaseService(repository), false, unarmed.Load()))
	if err := activate(context.Background(), project, generation, nil); err != nil {
		t.Fatalf("unarmed activation error = %v, want success", err)
	}
	if len(repository.casExpectedSeen) != 1 || repository.casExpectedSeen[0] != 3 {
		t.Fatalf("unarmed activation corrupted the CAS input: %v", repository.casExpectedSeen)
	}
	if repository.active == nil || repository.active.Revision != 4 {
		t.Fatalf("unarmed activation pointer = %#v, want the candidate at revision 4", repository.active)
	}
}

func TestDevRepositoryActivationConflictOnceCannotJumpToLaterRequest(t *testing.T) {
	const project, generation = "project-a", "generation-a"
	repository := &devFaultReleaseRepository{}
	armed := &atomic.Bool{}
	armed.Store(true)
	runtime := NewReleaseRuntime(newDevFaultReleaseService(repository), false, armed.Load())
	activate := repositoryActivateForTest(runtime)

	if err := activate(context.Background(), project, generation, nil); err != nil {
		t.Fatalf("first activation without a pointer = %v, want the arm consumed by this request and activation to proceed", err)
	}
	if runtime.repositoryActivationConflictOnce.Load() {
		t.Fatalf("armed fault stayed live after a request that could not consume it; it would hijack a later publish")
	}
	if len(repository.casExpectedSeen) != 1 || repository.casExpectedSeen[0] != repository.casCurrentSeen[0] {
		t.Fatalf("first activation corrupted a CAS input with no prior pointer: expected=%v current=%v", repository.casExpectedSeen, repository.casCurrentSeen)
	}

	seedActiveRelease(t, repository, project, 7)
	if err := activate(context.Background(), project, generation+"-next", nil); err != nil {
		t.Fatalf("second activation = %v, want success on the true CAS input", err)
	}
	if len(repository.casExpectedSeen) != 2 || repository.casExpectedSeen[1] != 7 {
		t.Fatalf("second activation fed a corrupted CAS input from a stale arm: %v", repository.casExpectedSeen)
	}
}

func TestReleaseRuntimePrepareConsumesConflictOnlyAfterSuccessfulCreate(t *testing.T) {
	repository := &devFaultReleaseRepository{}
	seedActiveRelease(t, repository, "project-a", 7)
	service := newDevFaultReleaseService(repository)
	service.Manifests = devFaultManifestReader{state: StateLoading}
	runtime := NewReleaseRuntime(service, true, false)
	if _, _, err := runtime.Prepare(context.Background(), "project-a", "generation-a", nil); err == nil {
		t.Fatal("unpublishable generation prepared successfully")
	}
	if !runtime.activationConflictOnce.Load() {
		t.Fatal("failed prepare consumed the one-shot conflict")
	}
	service.Manifests = devFaultManifestReader{state: StateStaged}
	release, expected, err := runtime.Prepare(context.Background(), "project-a", "generation-a", nil)
	if err != nil || expected != 8 || release.Generation != "generation-a" {
		t.Fatalf("armed prepare = %#v, %d, %v", release, expected, err)
	}
	if repository.active.Revision != 7 {
		t.Fatal("prepare changed active pointer")
	}
	_, expected, err = runtime.Prepare(context.Background(), "project-a", "generation-b", nil)
	if err != nil || expected != 7 {
		t.Fatalf("second prepare expected=%d err=%v", expected, err)
	}
}

func repositoryActivateForTest(runtime *ReleaseRuntime) func(context.Context, string, string, []DataframeSelector) error {
	return func(ctx context.Context, project, generation string, selectors []DataframeSelector) error {
		release, expected, err := runtime.PrepareRepository(ctx, project, generation, selectors)
		if err != nil {
			return err
		}
		_, err = runtime.service.Releases.CompareAndSwapActivateRelease(ctx, release, expected)
		return err
	}
}
