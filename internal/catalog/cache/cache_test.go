package cache

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestCacheDiscoverFieldsCachesAndInvalidatesByProject(t *testing.T) {
	cache := New()
	calls := 0
	discover := cache.DiscoverFields(func(ctx context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		calls++
		return []catalog.PopulatedField{{ResourceType: opts.ResourceType, Path: "gender"}}, nil
	})

	opts := catalog.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
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
	discover := cache.DiscoverReferences(func(ctx context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		calls++
		return []catalog.PopulatedReference{{FromType: opts.NodeType, Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	base := catalog.PopulatedReferenceOptions{Project: "P1", NodeType: "Patient", Mode: catalog.TraversalModeBuilder}
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
