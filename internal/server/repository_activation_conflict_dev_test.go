package server

import (
	"context"
	"errors"
	"flag"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	publicationcontract "github.com/calypr/loom/internal/dataset"
)

// Dev-only one-shot fault for the REPOSITORY publish activation leg, the
// parallel of LOOM_DEV_ACTIVATION_CONFLICT_ONCE (authoring/revision leg).
// These tests pin both the no-auth-only wiring of the knob and the consumption
// seam: the armed fault must make the real release CAS reject by corrupting
// exactly the expected-revision CAS input once, never by returning a synthetic
// error that hides a successful activation.

func TestDevRepositoryActivationConflictOnceIsNoAuthOnly(t *testing.T) {
	t.Setenv("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE", "true")

	options, err := parseServerOptions([]string{"--no-auth", "--dataframer-recipe", "recipe.json"}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseServerOptions() error = %v", err)
	}
	if !options.Server.DevRepositoryActivationConflictOnce {
		t.Fatalf("no-auth dev fault knob was not applied: %#v", options.Server)
	}

	secure := DefaultConfig()
	secure.Auth.Basic.Username = "loom"
	secure.Auth.Basic.Password = "secret"
	if err := applyDevFaultOverrides(&secure); err != nil {
		t.Fatalf("authenticated override check = %v", err)
	}
	if secure.Server.DevRepositoryActivationConflictOnce {
		t.Fatalf("authenticated config enabled the repository activation fault: %#v", secure.Server)
	}

	t.Setenv("LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE", "not-a-bool")
	faulty := DefaultConfig()
	faulty.Server.AllowUnauthenticated = true
	faulty.Auth.AllowUnauthenticated = true
	if err := applyDevFaultOverrides(&faulty); err == nil || !strings.Contains(err.Error(), "LOOM_DEV_REPOSITORY_ACTIVATION_CONFLICT_ONCE") {
		t.Fatalf("invalid repository activation fault error = %v", err)
	}
}

type devFaultReleaseRepository struct {
	releases         map[string]publicationcontract.ProjectRelease
	active           *publicationcontract.ActiveRelease
	casComparisons   int
	casExpectedSeen  []int64
	casCurrentSeen   []int64
	saveReleaseCalls int
}

func (r *devFaultReleaseRepository) SaveRelease(_ context.Context, release publicationcontract.ProjectRelease) (publicationcontract.ProjectRelease, error) {
	r.saveReleaseCalls++
	if r.releases == nil {
		r.releases = map[string]publicationcontract.ProjectRelease{}
	}
	r.releases[release.ID] = release
	return release, nil
}

func (r *devFaultReleaseRepository) ReadActiveRelease(_ context.Context, _ string) (publicationcontract.ActiveRelease, error) {
	if r.active == nil {
		return publicationcontract.ActiveRelease{}, publicationcontract.ErrNoActiveRelease
	}
	return *r.active, nil
}

// CompareAndSwapActivateRelease mirrors the shipped store guard in
// internal/backends/arango/internal/dataset/release_store.go: activateReleaseAQL compares the
// pointer revision with FILTER currentRevision == @expected_revision OR
// alreadyActive, and an emptied result becomes
// dataset.ErrReleaseActivationConflict (release_store.go:99-100). The
// comparison (and the pointer mutation on success) belongs to this fake
// store, not to the fault: the fault's only visible act is the CAS input it
// feeds in.
func (r *devFaultReleaseRepository) CompareAndSwapActivateRelease(_ context.Context, release publicationcontract.ProjectRelease, expectedRevision int64) (publicationcontract.ActiveRelease, error) {
	r.casComparisons++
	current := int64(0)
	if r.active != nil {
		current = r.active.Revision
	}
	r.casExpectedSeen = append(r.casExpectedSeen, expectedRevision)
	r.casCurrentSeen = append(r.casCurrentSeen, current)
	alreadyActive := r.active != nil && r.active.Release.ID == release.ID
	if !alreadyActive && expectedRevision != current {
		return publicationcontract.ActiveRelease{}, publicationcontract.ErrReleaseActivationConflict
	}
	if alreadyActive {
		return *r.active, nil
	}
	if _, ok := r.releases[release.ID]; !ok {
		return publicationcontract.ActiveRelease{}, publicationcontract.ErrReleaseActivationConflict
	}
	next := publicationcontract.ActiveRelease{Release: release, Revision: current + 1}
	r.active = &next
	return next, nil
}

type devFaultManifestReader struct{ state publicationcontract.State }

func (m devFaultManifestReader) ReadManifest(_ context.Context, ref publicationcontract.Ref) (publicationcontract.Manifest, error) {
	schema, err := publicationcontract.NewSchemaSnapshot("loom-test-schema", "4.0.1", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		return publicationcontract.Manifest{}, err
	}
	return publicationcontract.Manifest{Dataset: ref, State: m.state, SchemaIdentity: schema}, nil
}

type devFaultVerifier struct{}

func (devFaultVerifier) VerifyPublication(_ context.Context, project, generation string, selector publicationcontract.DataframeSelector) (publicationcontract.PublicationVerification, error) {
	return publicationcontract.PublicationVerification{Selector: selector, ExecutionID: "execution-dev-fault", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: time.Now().UTC()}, nil
}

func newDevFaultReleaseService(repository publicationcontract.ReleaseRepository) *publicationcontract.ReleaseService {
	return &publicationcontract.ReleaseService{
		Manifests: devFaultManifestReader{state: publicationcontract.StateStaged},
		Releases:  repository,
		Verifier:  devFaultVerifier{},
	}
}

// seedActiveRelease establishes a prior active release so the CAS guard is
// the discriminator: with a pointer at revision 7, a correct expected value of
// 7 wins and a corrupted 8 must lose through the store's own comparison.
func seedActiveRelease(t *testing.T, repository *devFaultReleaseRepository, project string, revision int64) publicationcontract.ProjectRelease {
	t.Helper()
	release := publicationcontract.ProjectRelease{ID: "release_prior", Project: project, GitCommit: "generation-prior", Generation: "generation-prior", CreatedAt: time.Now().UTC()}
	if repository.releases == nil {
		repository.releases = map[string]publicationcontract.ProjectRelease{}
	}
	repository.releases[release.ID] = release
	repository.active = &publicationcontract.ActiveRelease{Release: release, Revision: revision}
	return release
}

func TestDevRepositoryActivationConflictOnceTripsTheRealStoreCASOnce(t *testing.T) {
	const project, generation = "project-a", "generation-a"
	repository := &devFaultReleaseRepository{}
	prior := seedActiveRelease(t, repository, project, 7)
	armed := &atomic.Bool{}
	armed.Store(true)

	activate := newExplorerReleaseActivator(newDevFaultReleaseService(repository), armed)

	err := activate(context.Background(), project, generation, nil)
	if !errors.Is(err, publicationcontract.ErrReleaseActivationConflict) {
		t.Fatalf("armed publish activation error = %v, want the store's own ErrReleaseActivationConflict", err)
	}
	if repository.casComparisons != 1 {
		t.Fatalf("armed activation performed %d CAS attempts, want exactly one", repository.casComparisons)
	}
	// The fault corrupted the CAS input: the store saw expected=8 while its
	// pointer still read 7, so the store's guard rejected.
	if len(repository.casExpectedSeen) != 1 || repository.casExpectedSeen[0] != 8 || repository.casCurrentSeen[0] != 7 {
		t.Fatalf("fault did not corrupt exactly the CAS input once: expected=%v current=%v", repository.casExpectedSeen, repository.casCurrentSeen)
	}
	// A rejected CAS moves nothing: the prior active release pointer stands.
	if repository.active == nil || repository.active.Release.ID != prior.ID || repository.active.Revision != 7 {
		t.Fatalf("lost CAS moved the prior active pointer: %#v", repository.active)
	}

	// Consumption is one-shot: the next activation feeds the true CAS input and
	// the store's own guard lets it win, converging the pointer.
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

	activate := newExplorerReleaseActivator(newDevFaultReleaseService(repository), unarmed)
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

// Reviewer finding C-F1 (adopted, root-caused): the one-shot arm was consumed
// only inside the successful-Active branch, so an armed request with no active
// pointer left the fault live to silently hijack a LATER publish (proven on a
// copy: request 1 unconsumed, request 2 corrupted from 7 to 8). Consumption
// must belong to the request that arrived, not to the branch that can corrupt.
func TestDevRepositoryActivationConflictOnceCannotJumpToLaterRequest(t *testing.T) {
	const project, generation = "project-a", "generation-a"
	repository := &devFaultReleaseRepository{}
	armed := &atomic.Bool{}
	armed.Store(true)
	activate := newExplorerReleaseActivator(newDevFaultReleaseService(repository), armed)

	if err := activate(context.Background(), project, generation, nil); err != nil {
		t.Fatalf("first activation without a pointer = %v, want the arm consumed by this request and activation to proceed", err)
	}
	if armed.Load() {
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
