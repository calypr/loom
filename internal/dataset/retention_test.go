package dataset

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type retentionFixture struct {
	items   []RetentionGeneration
	deleted []Ref
}

func (f *retentionFixture) ListRetentionGenerations(context.Context) ([]RetentionGeneration, error) {
	return f.items, nil
}
func (f *retentionFixture) DeleteGeneration(_ context.Context, ref Ref) error {
	f.deleted = append(f.deleted, ref)
	return nil
}
func (f *retentionFixture) DeleteBlobs(_ context.Context, ref Ref) error { return nil }

type blobCleanupFixture struct{ deleted []Ref }

func (f *blobCleanupFixture) DeleteGeneration(_ context.Context, ref Ref) error {
	f.deleted = append(f.deleted, ref)
	return nil
}

func TestRetentionProtectsActiveLastGoodStagedAndInFlightData(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	old := now.Add(-30 * 24 * time.Hour)
	ref := func(generation string) Ref { return Ref{Project: "project-a", Generation: generation} }
	repository := &retentionFixture{items: []RetentionGeneration{
		{Dataset: ref("active"), State: StateFailed, UpdatedAt: old, Active: true},
		{Dataset: ref("last-good"), State: StateFailed, UpdatedAt: old, LastGood: true},
		{Dataset: ref("staged"), State: StateStaged, UpdatedAt: old},
		{Dataset: ref("loading"), State: StateLoading, UpdatedAt: old},
		{Dataset: ref("in-flight"), State: StateFailed, UpdatedAt: old, InFlight: true},
		{Dataset: ref("recoverable"), State: StateFailed, UpdatedAt: old, Recoverable: true},
		{Dataset: ref("recent"), State: StateFailed, UpdatedAt: now.Add(-time.Hour)},
		{Dataset: ref("expired"), State: StateFailed, UpdatedAt: old},
	}}
	blobs := &blobCleanupFixture{}
	deleted, err := (RetentionService{Repository: repository, Blobs: blobs, Retention: 7 * 24 * time.Hour, Now: func() time.Time { return now }}).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Ref{ref("expired")}
	if !reflect.DeepEqual(deleted, want) || !reflect.DeepEqual(repository.deleted, want) || !reflect.DeepEqual(blobs.deleted, want) {
		t.Fatalf("deleted = %v metadata=%v blobs=%v", deleted, repository.deleted, blobs.deleted)
	}
}
