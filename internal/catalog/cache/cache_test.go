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

func TestCacheSeparatesRestrictedEmptyScopeFromUnrestrictedScope(t *testing.T) {
	cache := New()
	calls := 0
	discover := cache.DiscoverFields(func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		calls++
		return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})

	unrestricted := catalog.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	restrictedEmpty := catalog.PopulatedFieldOptions{
		Project:                       "P1",
		ResourceType:                  "Patient",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
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
	cache := New()
	fieldCalls := 0
	discoverFields := cache.DiscoverFields(func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
		fieldCalls++
		return []catalog.PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})
	referenceCalls := 0
	discoverReferences := cache.DiscoverReferences(func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
		referenceCalls++
		return []catalog.PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	fieldBase := catalog.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	referenceBase := catalog.PopulatedReferenceOptions{Project: "P1", NodeType: "Patient", Mode: catalog.TraversalModeBuilder}
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
