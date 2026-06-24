package catalogcache

import (
	"context"
	"testing"

	"arangodb-proto/internal/proto"
)

func TestCacheDiscoverFieldsCachesAndInvalidatesByProject(t *testing.T) {
	cache := New()
	calls := 0
	discover := cache.DiscoverFields(func(ctx context.Context, opts proto.PopulatedFieldOptions) ([]proto.PopulatedField, error) {
		calls++
		return []proto.PopulatedField{{ResourceType: opts.ResourceType, Path: "gender"}}, nil
	})

	opts := proto.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	cache.InvalidateProject("P2")
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls after unrelated invalidate = %d, want 1", calls)
	}

	cache.InvalidateProject("P1")
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls after project invalidate = %d, want 2", calls)
	}
}

func TestCacheDiscoverReferencesSeparatesAuthScopes(t *testing.T) {
	cache := New()
	calls := 0
	discover := cache.DiscoverReferences(func(ctx context.Context, opts proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error) {
		calls++
		return []proto.PopulatedReference{{FromType: opts.NodeType, Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	base := proto.PopulatedReferenceOptions{Project: "P1", NodeType: "Patient", Mode: proto.TraversalModeBuilder}
	withA := base
	withA.AuthResourcePaths = []string{"a"}
	withB := base
	withB.AuthResourcePaths = []string{"b"}

	if _, err := discover(context.Background(), withA); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(context.Background(), withA); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(context.Background(), withB); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}
