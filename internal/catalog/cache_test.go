package catalog

import (
	"context"
	"testing"
)

func TestCacheDiscoverFieldsCachesAndInvalidatesByProject(t *testing.T) {
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverFields(func(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
		calls++
		return []PopulatedField{{ResourceType: opts.ResourceType, Path: "gender"}}, nil
	})

	opts := PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
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
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverReferences(func(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
		calls++
		return []PopulatedReference{{FromType: opts.NodeType, Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	base := PopulatedReferenceOptions{Project: "P1", NodeType: "Patient", Mode: TraversalModeBuilder}
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

func TestCacheSeparatesRestrictedEmptyScopeFromUnrestrictedScope(t *testing.T) {
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverFields(func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error) {
		calls++
		return []PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})

	unrestricted := PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	restrictedEmpty := PopulatedFieldOptions{
		Project:                       "P1",
		ResourceType:                  "Patient",
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{},
	}

	if _, err := discover(context.Background(), unrestricted); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(context.Background(), restrictedEmpty); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want separate unrestricted and restricted-empty cache entries", calls)
	}
}

func TestCacheSeparatesDatasetGenerationNamespaces(t *testing.T) {
	cache := NewCache()
	fieldCalls := 0
	discoverFields := cache.DiscoverFields(func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error) {
		fieldCalls++
		return []PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})
	referenceCalls := 0
	discoverReferences := cache.DiscoverReferences(func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error) {
		referenceCalls++
		return []PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	fieldBase := PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	referenceBase := PopulatedReferenceOptions{Project: "P1", NodeType: "Patient", Mode: TraversalModeBuilder}
	for _, generation := range []string{"", "generation-a", " generation-a ", "generation-b"} {
		fields := fieldBase
		fields.DatasetGeneration = generation
		if _, err := discoverFields(context.Background(), fields); err != nil {
			t.Fatal(err)
		}
		references := referenceBase
		references.DatasetGeneration = generation
		if _, err := discoverReferences(context.Background(), references); err != nil {
			t.Fatal(err)
		}
	}
	if fieldCalls != 3 {
		t.Fatalf("field calls = %d, want legacy + two exact generation namespaces", fieldCalls)
	}
	if referenceCalls != 3 {
		t.Fatalf("reference calls = %d, want legacy + two exact generation namespaces", referenceCalls)
	}
}
