package semantic

import (
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestDiscoverCatalogEmptyIsValidAndDistinctFromPartial(t *testing.T) {
	empty := DiscoverCatalog(nil, CatalogOptions{Project: "P1", SourceGeneration: "g1"})
	if empty.Completeness.State != "empty" || len(empty.Resources) != 0 || len(empty.Diagnostics) != 0 {
		t.Fatalf("empty catalog = %#v", empty)
	}
	partial := DiscoverCatalog([]catalog.PopulatedField{{ResourceType: "Observation", Path: "code", DocCount: 2, SemanticObservations: []catalog.SemanticObservation{{Source: catalog.SemanticObservationSource{Canonical: "Observation.code", Type: "Observation", Path: "code"}, Value: catalog.SemanticObservationValue{Selector: "valueString", Type: "string"}, Cardinality: "single", Population: 2, Examples: []string{"x"}, ExamplesTruncated: true, RuleHint: "future.rule.v9", RuleVersion: "9"}}}}, CatalogOptions{Project: "P1", SourceGeneration: "g1"})
	if partial.Completeness.State != "complete" {
		t.Fatalf("example truncation should not imply resource scan partial: %#v", partial.Completeness)
	}
	if len(partial.Diagnostics) != 1 || partial.Diagnostics[0].Code != "unknown_rule" {
		t.Fatalf("unknown rule diagnostic = %#v", partial.Diagnostics)
	}
	concept := partial.Resources[0].Families[0].Concepts[0]
	if concept.RuleID != "future.rule.v9" || concept.Output.Generic != true || concept.Source.PopulationCount != 2 || !concept.Examples.Limited {
		t.Fatalf("catalog metadata not preserved: %#v", concept)
	}
}

func TestDiscoverCatalogMergesPartitionsAndPreservesStableIdentity(t *testing.T) {
	obs := catalog.SemanticObservation{SchemaVersion: 1, Source: catalog.SemanticObservationSource{Canonical: "Observation.code", Type: "Observation", Path: "code"}, Key: catalog.SemanticObservationKey{Selector: "code.coding[]", System: "s", Code: "c"}, Value: catalog.SemanticObservationValue{Selector: "valueQuantity.value", Type: "number"}, Cardinality: "repeated", Population: 3, Examples: []string{"2"}, RuleHint: "OBSERVATION_CODE_VALUE", RuleVersion: "1"}
	left := catalog.PopulatedField{ResourceType: "Observation", Path: "code", DocCount: 4, SemanticObservations: []catalog.SemanticObservation{obs}}
	right := obs
	right.Population = 5
	right.Examples = []string{"1"}
	first := DiscoverCatalog([]catalog.PopulatedField{left, {ResourceType: "Observation", Path: "code", DocCount: 5, SemanticObservations: []catalog.SemanticObservation{right}}}, CatalogOptions{Project: "P1"})
	second := DiscoverCatalog([]catalog.PopulatedField{{ResourceType: "Observation", Path: "code", DocCount: 5, SemanticObservations: []catalog.SemanticObservation{right}}, left}, CatalogOptions{Project: "P1"})
	if len(first.Resources) != 1 || len(first.Resources[0].Families) != 1 || len(first.Resources[0].Families[0].Concepts) != 1 {
		t.Fatalf("unexpected merged catalog: %#v", first)
	}
	a, b := first.Resources[0].Families[0].Concepts[0], second.Resources[0].Families[0].Concepts[0]
	if a.ID != b.ID || a.Source.PopulationCount != 8 || len(a.Examples.Values) != 2 || a.Output.Cardinality != CardinalityRepeated || !a.Source.Repeated {
		t.Fatalf("unstable or incomplete merge: %#v %#v", a, b)
	}
}
