package dataset

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSnapshotRepositoryIsIdempotentAndRejectsConflictingContent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	candidate, err := NewSnapshotGeneration("project-a", "commit-a", "/programs/a", []string{"Patient", "Observation"}, now)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateOrResumeSnapshot(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateOrResumeSnapshot(ctx, candidate)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("resume = %#v, %v", second, err)
	}
	conflict := candidate
	conflict.ExpectedResourceTypes = []string{"Patient"}
	if _, err := store.CreateOrResumeSnapshot(ctx, conflict); !errors.Is(err, ErrSnapshotConflict) {
		t.Fatalf("metadata conflict = %v", err)
	}

	upload, _ := NewResourceUpload("Patient", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 10, now.Add(time.Minute))
	if _, err := store.RecordSnapshotUpload(ctx, candidate.Dataset, upload); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordSnapshotUpload(ctx, candidate.Dataset, upload); err != nil {
		t.Fatalf("identical upload retry = %v", err)
	}
	changed := upload
	changed.SHA256 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := store.RecordSnapshotUpload(ctx, candidate.Dataset, changed); !errors.Is(err, ErrChecksumConflict) {
		t.Fatalf("checksum conflict = %v", err)
	}
	if _, err := store.TransitionSnapshot(ctx, candidate.Dataset, StateLoading, StateStaged, now); !errors.Is(err, ErrGenerationIncomplete) {
		t.Fatalf("incomplete finalize = %v", err)
	}
}

func TestSnapshotFinalizeAndAbortTransitionsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryLifecycleStore()
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	candidate, _ := NewSnapshotGeneration("project-a", "commit-a", "", []string{"Patient"}, now)
	_, _ = store.CreateOrResumeSnapshot(ctx, candidate)
	upload, _ := NewResourceUpload("Patient", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 10, now)
	_, _ = store.RecordSnapshotUpload(ctx, candidate.Dataset, upload)
	staged, err := store.TransitionSnapshot(ctx, candidate.Dataset, StateLoading, StateStaged, now)
	if err != nil || staged.State != StateStaged {
		t.Fatalf("stage = %#v, %v", staged, err)
	}
	stagedAgain, err := store.TransitionSnapshot(ctx, candidate.Dataset, StateLoading, StateStaged, now.Add(time.Hour))
	if err != nil || stagedAgain.UpdatedAt != staged.UpdatedAt {
		t.Fatalf("idempotent stage = %#v, %v", stagedAgain, err)
	}

	abortCandidate, _ := NewSnapshotGeneration("project-b", "commit-b", "", []string{"Patient"}, now)
	_, _ = store.CreateOrResumeSnapshot(ctx, abortCandidate)
	aborted, err := store.TransitionSnapshot(ctx, abortCandidate.Dataset, StateLoading, StateFailed, now)
	if err != nil || aborted.State != StateFailed || aborted.AbortedAt == nil {
		t.Fatalf("abort = %#v, %v", aborted, err)
	}
}
