package load

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dataframepublication "github.com/calypr/loom/internal/dataframe/publication"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

type activationGenerationRunner struct{}

func (activationGenerationRunner) RunGeneration(context.Context, GenerationLoadRequest, ingest.EventSink) (ingest.LoadSummary, error) {
	return ingest.LoadSummary{}, nil
}

type activationManifestStore struct {
	manifest      publication.Manifest
	readErr       error
	activateErr   error
	readCalls     int
	activateCalls int
	activated     publication.Manifest
}

func (s *activationManifestStore) ReadManifest(context.Context, publication.Ref) (publication.Manifest, error) {
	s.readCalls++
	if s.readErr != nil {
		return publication.Manifest{}, s.readErr
	}
	return s.manifest, nil
}

func (s *activationManifestStore) Activate(_ context.Context, manifest publication.Manifest) error {
	s.activateCalls++
	s.activated = manifest
	return s.activateErr
}

type activationReleaseStore struct {
	execution  dataframepublication.BundleExecution
	pointer    dataframepublication.BundlePointer
	getErr     error
	pointerErr error
}

func (s *activationReleaseStore) GetExecution(context.Context, string) (dataframepublication.BundleExecution, error) {
	if s.getErr != nil {
		return dataframepublication.BundleExecution{}, s.getErr
	}
	return s.execution, nil
}

func (s *activationReleaseStore) GetPointer(context.Context, string) (dataframepublication.BundlePointer, error) {
	if s.pointerErr != nil {
		return dataframepublication.BundlePointer{}, s.pointerErr
	}
	return s.pointer, nil
}

func TestRunGenerationPropagatesDeferActivationAndResult(t *testing.T) {
	runner := &activationCapturingRunner{}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration})
	if err != nil {
		t.Fatal(err)
	}
	for _, deferred := range []bool{false, true} {
		result, err := svc.RunGeneration(context.Background(), GenerationLoadRequest{Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: deferred})
		if err != nil {
			t.Fatal(err)
		}
		if runner.request.DeferActivation != deferred {
			t.Fatalf("runner request DeferActivation = %v, want %v", runner.request.DeferActivation, deferred)
		}
		if result.Activated == deferred {
			t.Fatalf("result Activated = %v for DeferActivation=%v", result.Activated, deferred)
		}
	}
}

type activationCapturingRunner struct {
	request GenerationLoadRequest
	calls   int
}

func (r *activationCapturingRunner) RunGeneration(_ context.Context, request GenerationLoadRequest, _ ingest.EventSink) (ingest.LoadSummary, error) {
	r.calls++
	r.request = request
	return ingest.LoadSummary{}, nil
}

func TestRunGenerationReusesReadyManifest(t *testing.T) {
	runner := &activationCapturingRunner{}
	manifest := activationManifest(t, "project-a", "generation-a")
	store := &activationManifestStore{manifest: manifest}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.RunGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.Activated {
		t.Fatalf("result = %#v, want reused and inactive", result)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls = %d, want 0", runner.calls)
	}
}

func TestRunGenerationLoadsWhenManifestDoesNotExist(t *testing.T) {
	runner := &activationCapturingRunner{}
	store := &activationManifestStore{readErr: fmt.Errorf("lookup: %w", publication.ErrManifestNotFound)}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.RunGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || runner.calls != 1 {
		t.Fatalf("result reused = %v, runner calls = %d; want false, 1", result.Reused, runner.calls)
	}
}

func TestRunGenerationRejectsManifestLookupFailure(t *testing.T) {
	runner := &activationCapturingRunner{}
	store := &activationManifestStore{readErr: errors.New("database unavailable")}
	svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.RunGeneration(context.Background(), GenerationLoadRequest{
		Project: "project-a", Generation: "generation-a", StagedDir: t.TempDir(), DeferActivation: true,
	})
	if err == nil || runner.calls != 0 {
		t.Fatalf("error = %v, runner calls = %d; want lookup failure and no load", err, runner.calls)
	}
}

func TestActivateGenerationRejectsInvalidDataframeRelease(t *testing.T) {
	readyManifest := activationManifest(t, "project-a", "generation-a")
	base := dataframepublication.BundleExecution{
		ID: "release-1", BundleIdentity: dataframepublication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a", Name: "all"},
		State: dataframepublication.BundlePublished, UpdatedAt: time.Now().UTC(),
		Outputs: []dataframepublication.BundleOutputRecord{
			{Name: "Patient", State: dataframepublication.BundlePublished},
			{Name: "Observation", State: dataframepublication.BundlePublished},
		},
	}
	tests := []struct {
		name   string
		mutate func(*dataframepublication.BundleExecution, *dataframepublication.BundlePointer)
	}{
		{name: "mismatched project", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.Project = "project-b"
		}},
		{name: "mismatched generation", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.DatasetGeneration = "generation-b"
		}},
		{name: "bundle not ready", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.State = dataframepublication.BundleLoading
		}},
		{name: "output not ready", mutate: func(execution *dataframepublication.BundleExecution, _ *dataframepublication.BundlePointer) {
			execution.Outputs[1].State = dataframepublication.BundleLoading
		}},
		{name: "pointer mismatch", mutate: func(_ *dataframepublication.BundleExecution, pointer *dataframepublication.BundlePointer) {
			pointer.ExecutionID = "another-release"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution := base
			execution.Outputs = append([]dataframepublication.BundleOutputRecord(nil), base.Outputs...)
			pointer := dataframepublication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}
			test.mutate(&execution, &pointer)
			activator := &activationManifestStore{manifest: readyManifest}
			releases := &activationReleaseStore{execution: execution, pointer: pointer}
			runner := activationGenerationRunner{}
			svc, err := NewService(ServiceConfig{LoadGeneration: runner.RunGeneration, GenerationActivator: activator, DataframeReleases: releases})
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.ActivateGeneration(context.Background(), "project-a", "generation-a", execution.ID); err == nil {
				t.Fatal("ActivateGeneration() unexpectedly succeeded")
			}
			if activator.readCalls != 0 || activator.activateCalls != 0 {
				t.Fatalf("manifest calls = read %d activate %d, want no activation", activator.readCalls, activator.activateCalls)
			}
		})
	}
}

func TestActivateGenerationActivatesPublishedAllOutputBundle(t *testing.T) {
	manifest := activationManifest(t, "project-a", "generation-a")
	execution := dataframepublication.BundleExecution{
		ID: "release-1", BundleIdentity: dataframepublication.BundleIdentity{Project: "project-a", DatasetGeneration: "generation-a", Name: "all"},
		State: dataframepublication.BundlePublished, UpdatedAt: time.Now().UTC(),
		Outputs: []dataframepublication.BundleOutputRecord{
			{Name: "Patient", State: dataframepublication.BundlePublished},
			{Name: "Observation", State: dataframepublication.BundlePublished},
		},
	}
	activator := &activationManifestStore{manifest: manifest}
	calledSuccess := false
	svc, err := NewService(ServiceConfig{
		LoadGeneration:      activationGenerationRunner{}.RunGeneration,
		GenerationActivator: activator,
		DataframeReleases:   &activationReleaseStore{execution: execution, pointer: dataframepublication.BundlePointer{Name: execution.PointerName(), ExecutionID: execution.ID}},
		OnSuccess:           func(string) { calledSuccess = true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ActivateGeneration(context.Background(), "project-a", "generation-a", execution.ID); err != nil {
		t.Fatal(err)
	}
	if activator.readCalls != 1 || activator.activateCalls != 1 || !calledSuccess {
		t.Fatalf("activation calls = read %d activate %d success %v", activator.readCalls, activator.activateCalls, calledSuccess)
	}
	if activator.activated.Dataset != manifest.Dataset || !activator.activated.IsStaged() {
		t.Fatalf("activated manifest = %#v", activator.activated)
	}
}

func activationManifest(t *testing.T, project, generation string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:test", "R5", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(publication.StateReady)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
