package catalog

import (
	"reflect"
	"testing"
)

func TestScalarPathsPresentPreservesFalseZeroAndEmptyString(t *testing.T) {
	paths := ScalarPathsPresent(map[string]any{
		"zero":  float64(0),
		"false": false,
		"empty": "",
		"null":  nil,
	})
	want := []string{"empty", "false", "zero"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("ScalarPathsPresent() = %v, want %v", paths, want)
	}
}

func TestFieldSourceMembershipDeduplicatesRepeatedArrayPathsAndReplays(t *testing.T) {
	payload := map[string]any{
		"component": []any{
			map[string]any{"valueString": "first"},
			map[string]any{"valueString": "second"},
			map[string]any{"valueString": nil},
		},
	}
	first, err := NewFieldSourceMembership("project", " generation-a ", "scope-a", "Observation", "Observation/generation-key", payload)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewFieldSourceMembership("project", "generation-a", "scope-a", "Observation", "Observation/generation-key", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, replay) {
		t.Fatalf("replayed membership differs:\nfirst=%+v\nreplay=%+v", first, replay)
	}
	wantPaths := []string{"component[].valueString"}
	if !reflect.DeepEqual(first.ScalarPaths, wantPaths) {
		t.Fatalf("membership paths = %v, want one deduplicated path %v", first.ScalarPaths, wantPaths)
	}
}

func TestFieldSourceMembershipIsolatedByGenerationAndAuthorization(t *testing.T) {
	payload := map[string]any{"valueString": "present"}
	base, err := NewFieldSourceMembership("project", "generation-a", "scope-a", "Observation", "Observation/key-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	otherGeneration, err := NewFieldSourceMembership("project", "generation-b", "scope-a", "Observation", "Observation/key-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	otherScope, err := NewFieldSourceMembership("project", "generation-a", "scope-b", "Observation", "Observation/key-a", payload)
	if err != nil {
		t.Fatal(err)
	}
	if base.Key == otherGeneration.Key || base.Key == otherScope.Key || otherGeneration.Key == otherScope.Key {
		t.Fatalf("membership keys are not scope-isolated: base=%q generation=%q auth=%q", base.Key, otherGeneration.Key, otherScope.Key)
	}
}

func TestScalarPathsPresentDoesNotUseProfilerRetentionLimits(t *testing.T) {
	limits := DefaultProfileLimits()
	limits.MaxFields = 1
	profiler := NewProfilerForGenerationWithLimits("project", "generation", "", "Observation", nil, limits)
	payload := map[string]any{"first": "a", "second": "b"}
	if err := profiler.ObservePayload(payload, map[string]float64{}); err != nil {
		t.Fatal(err)
	}
	if got := len(profiler.Documents()); got != 1 {
		t.Fatalf("profiler retained %d fields, want limit 1", got)
	}
	if paths := ScalarPathsPresent(payload); !reflect.DeepEqual(paths, []string{"first", "second"}) {
		t.Fatalf("membership paths = %v, want all present fields independent of profiler limit", paths)
	}
	membership, err := profiler.ObservePayloadWithFieldSourceMembership(payload, map[string]float64{}, "", nil, "Observation/physical-key")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(membership.ScalarPaths, []string{"first", "second"}) {
		t.Fatalf("profiled membership paths = %v, want every source path", membership.ScalarPaths)
	}
}

func TestFieldSourceMembershipKeepsDistinctSemanticFeatureIdentitiesOnce(t *testing.T) {
	profiler := NewProfilerForGenerationWithLimits("project", "generation", "", "Observation", nil, DefaultProfileLimits())
	payload := map[string]any{
		"resourceType": "Observation",
		"component": []any{
			inventoryTestComponent("height", "111", "Height"),
			inventoryTestComponent("height", "111", "Height"),
		},
	}
	var emitted []SemanticInventoryContribution
	membership, err := profiler.ObservePayloadWithFieldSourceMembership(payload, map[string]float64{}, "retained:Observation/o1", func(contribution SemanticInventoryContribution) {
		emitted = append(emitted, contribution)
	}, "Observation/o1")
	if err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 3 {
		t.Fatalf("semantic contributions = %d, want two values and one resource type", len(emitted))
	}
	for _, contribution := range emitted {
		if contribution.SourceKind != SemanticInventorySourceRetained || contribution.SourceID != "retained:Observation/o1" {
			t.Fatalf("contribution lost retained source identity: %+v", contribution)
		}
	}
	want := SemanticFeatureReferences(emitted)
	if !reflect.DeepEqual(membership.SemanticFeatures, want) || len(want) != 2 {
		t.Fatalf("membership semantic features = %+v, want two deduplicated identities %+v", membership.SemanticFeatures, want)
	}
}
