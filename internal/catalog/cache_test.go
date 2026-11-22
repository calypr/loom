package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
)

func TestCacheDiscoverFieldsCachesAndInvalidatesByProject(t *testing.T) {
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverFields(func(ctx context.Context, opts fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		calls++
		return []fieldprofile.PopulatedField{{ResourceType: opts.ResourceType, Path: "gender"}}, nil
	})

	opts := fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
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

func TestCacheClonesExtensionObservations(t *testing.T) {
	cache := NewCache()
	value := fieldprofile.PopulatedField{ResourceType: "DocumentReference", Path: "extension[].url", ExtensionValues: []fieldprofile.ExtensionValueObservation{{URL: "u", SourcePath: "extension[]", ValuePath: "valueString", ValueType: "string"}}}
	discover := cache.DiscoverFields(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		return []fieldprofile.PopulatedField{value}, nil
	})
	first, err := discover(context.Background(), fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "DocumentReference"})
	if err != nil {
		t.Fatal(err)
	}
	first[0].ExtensionValues[0].URL = "mutated"
	second, err := discover(context.Background(), fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "DocumentReference"})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].ExtensionValues[0].URL != "u" {
		t.Fatalf("cached extension observation mutated: %#v", second[0].ExtensionValues)
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

func TestCacheDiscoverReferencesSeparatesActiveFiltersByMode(t *testing.T) {
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverReferences(func(_ context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
		calls++
		return []PopulatedReference{{FromType: opts.FromType, ToType: opts.NodeType}}, nil
	})

	for _, opts := range []PopulatedReferenceOptions{
		{Project: "P1", Mode: TraversalModeStorage, FromType: "Patient"},
		{Project: "P1", Mode: TraversalModeStorage, FromType: "Observation"},
		{Project: "P1", Mode: TraversalModeBuilder, NodeType: "Patient"},
		{Project: "P1", Mode: TraversalModeBuilder, NodeType: "Observation"},
	} {
		if _, err := discover(context.Background(), opts); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatalf("calls = %d, want one cache entry per active filter", calls)
	}
}

func TestCacheSeparatesRestrictedEmptyScopeFromUnrestrictedScope(t *testing.T) {
	cache := NewCache()
	calls := 0
	discover := cache.DiscoverFields(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		calls++
		return []fieldprofile.PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})

	unrestricted := fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	restrictedEmpty := fieldprofile.PopulatedFieldOptions{
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
	discoverFields := cache.DiscoverFields(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		fieldCalls++
		return []fieldprofile.PopulatedField{{ResourceType: "Patient", Path: "gender"}}, nil
	})
	referenceCalls := 0
	discoverReferences := cache.DiscoverReferences(func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error) {
		referenceCalls++
		return []PopulatedReference{{FromType: "Patient", Label: "subject_Patient", ToType: "Specimen"}}, nil
	})

	fieldBase := fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
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

func TestCacheBoundsEntriesAcrossFieldsAndReferences(t *testing.T) {
	cache := NewCacheWithOptions(CacheOptions{MaxEntries: 2, MaxBytes: 1 << 20})
	fieldCalls, referenceCalls := 0, 0
	discoverFields := cache.DiscoverFields(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		fieldCalls++
		return []fieldprofile.PopulatedField{{Path: "field"}}, nil
	})
	discoverReferences := cache.DiscoverReferences(func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error) {
		referenceCalls++
		return []PopulatedReference{{FromType: "Patient", ToType: "Observation"}}, nil
	})

	if _, err := discoverFields(context.Background(), fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverReferences(context.Background(), PopulatedReferenceOptions{Project: "P1", FromType: "Patient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverFields(context.Background(), fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverFields(context.Background(), fieldprofile.PopulatedFieldOptions{Project: "P2", ResourceType: "Patient"}); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverReferences(context.Background(), PopulatedReferenceOptions{Project: "P1", FromType: "Patient"}); err != nil {
		t.Fatal(err)
	}

	if fieldCalls != 2 || referenceCalls != 2 {
		t.Fatalf("calls = fields %d, references %d, want one field eviction and one reference hit", fieldCalls, referenceCalls)
	}
	stats := cache.Stats()
	if stats.Entries != 2 || stats.Bytes <= 0 || stats.Evictions != 2 {
		t.Fatalf("cache stats = %#v, want two entries, positive bytes, and two evictions", stats)
	}
}

func TestCacheRejectsValueOverConfiguredByteWeight(t *testing.T) {
	cache := NewCacheWithOptions(CacheOptions{MaxEntries: 4, MaxBytes: 32})
	calls := 0
	discover := cache.DiscoverFields(func(context.Context, fieldprofile.PopulatedFieldOptions) ([]fieldprofile.PopulatedField, error) {
		calls++
		return []fieldprofile.PopulatedField{{Path: strings.Repeat("long", 16)}}, nil
	})
	opts := fieldprofile.PopulatedFieldOptions{Project: "P1", ResourceType: "Patient"}
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := discover(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want uncached oversized values to call the source twice", calls)
	}
	if stats := cache.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("oversized value cache stats = %#v, want empty cache", stats)
	}
}

type CacheStats struct {
	Entries   int
	Bytes     int
	Evictions uint64
}

func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{Entries: len(c.entries), Bytes: c.bytes, Evictions: c.evictions}
}
