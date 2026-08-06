package load

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

type snapshotRunnerFixture struct {
	calls    int
	requests []GenerationLoadRequest
	err      error
}

type stagedManifestRepository struct {
	*dataset.MemoryLifecycleStore
	manifest dataset.Manifest
}

func (r *stagedManifestRepository) ReadManifest(context.Context, dataset.Ref) (dataset.Manifest, error) {
	return r.manifest, nil
}

func (f *snapshotRunnerFixture) RunGeneration(_ context.Context, request GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	f.calls++
	f.requests = append(f.requests, request)
	return ingest.LoadSummary{Files: 1}, f.err
}

func TestSnapshotServiceSafeRetryFinalizeAndNoImplicitActivation(t *testing.T) {
	store := dataset.NewMemoryLifecycleStore()
	runner := &snapshotRunnerFixture{}
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	blobs := LocalSnapshotBlobs{Root: t.TempDir()}
	service := &SnapshotService{Repository: store, Blobs: blobs, Runner: runner, Now: func() time.Time { return now }}
	ctx := context.Background()

	created, err := service.CreateOrResume(ctx, "project-a", "commit-a", "", []string{"Patient", "Observation"})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := service.CreateOrResume(ctx, "project-a", "commit-a", "", []string{"Observation", "Patient"})
	if err != nil || resumed.Dataset != created.Dataset {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
	undeclared := []byte("{}\n")
	if _, err := service.Upload(ctx, "project-a", "commit-a", "Encounter", checksumFor(undeclared), undeclared); !errors.Is(err, dataset.ErrSnapshotConflict) {
		t.Fatalf("undeclared resource upload = %v", err)
	}
	if _, err := os.Stat(filepath.Join(blobs.generationDirectory(created.Dataset), "Encounter.ndjson")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("undeclared resource left a blob: %v", err)
	}
	patient := []byte("{\"resourceType\":\"Patient\",\"id\":\"p1\"}\n")
	checksum := checksumFor(patient)
	if _, err := service.Upload(ctx, "project-a", "commit-a", "Patient", checksum, patient); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(ctx, "project-a", "commit-a", "Patient", checksum, patient); err != nil {
		t.Fatalf("upload retry = %v", err)
	}
	if _, _, err := service.Finalize(ctx, "project-a", "commit-a", "operator"); !errors.Is(err, dataset.ErrGenerationIncomplete) {
		t.Fatalf("partial finalize = %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("partial finalize invoked loader %d times", runner.calls)
	}
	observation := []byte("{\"resourceType\":\"Observation\",\"id\":\"o1\"}\n")
	if _, err := service.Upload(ctx, "project-a", "commit-a", "Observation", checksumFor(observation), observation); err != nil {
		t.Fatal(err)
	}
	staged, result, err := service.Finalize(ctx, "project-a", "commit-a", "operator")
	if err != nil || staged.State != dataset.StateStaged || result == nil {
		t.Fatalf("finalize = %#v, %#v, %v", staged, result, err)
	}
	if runner.calls != 1 || len(runner.requests) != 1 || !runner.requests[0].StageOnly {
		t.Fatalf("runner requests = %#v", runner.requests)
	}
	if _, err := service.Upload(ctx, "project-a", "commit-a", "Patient", checksum, patient); err != nil {
		t.Fatalf("idempotent upload after finalize = %v", err)
	}
	stagedAgain, resultAgain, err := service.Finalize(ctx, "project-a", "commit-a", "operator")
	if err != nil || stagedAgain.State != dataset.StateStaged || resultAgain != nil || runner.calls != 1 {
		t.Fatalf("idempotent finalize = %#v, %#v, calls=%d, err=%v", stagedAgain, resultAgain, runner.calls, err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, dataset.ErrNoActiveRelease) {
		t.Fatalf("finalize activated release: %v", err)
	}
}

func TestSnapshotServiceChecksumConflictAndFailurePreservePriorRelease(t *testing.T) {
	ctx := context.Background()
	store := dataset.NewMemoryLifecycleStore()
	now := time.Now().UTC()
	runner := &snapshotRunnerFixture{err: errors.New("invalid rows")}
	service := &SnapshotService{Repository: store, Blobs: LocalSnapshotBlobs{Root: t.TempDir()}, Runner: runner, Now: func() time.Time { return now }}
	_, _ = service.CreateOrResume(ctx, "project-a", "commit-b", "", []string{"Patient"})
	body := []byte("{}\n")
	if _, err := service.Upload(ctx, "project-a", "commit-b", "Patient", checksumFor([]byte("different")), body); !errors.Is(err, dataset.ErrChecksumConflict) {
		t.Fatalf("declared checksum mismatch = %v", err)
	}
	if _, err := service.Upload(ctx, "project-a", "commit-b", "Patient", checksumFor(body), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Finalize(ctx, "project-a", "commit-b", "operator"); err == nil || !errors.Is(err, runner.err) {
		t.Fatalf("loader failure = %v", err)
	}
	status, err := service.Status(ctx, "project-a", "commit-b")
	if err != nil || status.State != dataset.StateFailed {
		t.Fatalf("failed status = %#v, %v", status, err)
	}
	if _, err := store.ReadActiveRelease(ctx, "project-a"); !errors.Is(err, dataset.ErrNoActiveRelease) {
		t.Fatalf("failure activated release: %v", err)
	}
}

func TestSnapshotFinalizeRecoversWhenLoadCommittedBeforeResponseWasLost(t *testing.T) {
	ctx := context.Background()
	memory := dataset.NewMemoryLifecycleStore()
	now := time.Now().UTC()
	ref, _ := dataset.NewRef("project-a", "commit-a")
	schema, _ := dataset.NewSchemaSnapshot("urn:test", "R5", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	manifest, _ := dataset.NewManifest(ref, schema)
	manifest, _ = manifest.Transition(dataset.StateStaged)
	repository := &stagedManifestRepository{MemoryLifecycleStore: memory, manifest: manifest}
	runner := &snapshotRunnerFixture{err: errors.New("connection lost after commit")}
	service := &SnapshotService{Repository: repository, Blobs: LocalSnapshotBlobs{Root: t.TempDir()}, Runner: runner, Now: func() time.Time { return now }}
	_, _ = service.CreateOrResume(ctx, "project-a", "commit-a", "", []string{"Patient"})
	body := []byte("{}\n")
	_, _ = service.Upload(ctx, "project-a", "commit-a", "Patient", checksumFor(body), body)
	staged, result, err := service.Finalize(ctx, "project-a", "commit-a", "operator")
	if err != nil || staged.State != dataset.StateStaged || result != nil {
		t.Fatalf("recovered finalize = %#v, %#v, %v", staged, result, err)
	}
}

func checksumFor(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
