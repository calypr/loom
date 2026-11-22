package catalog

import (
	"sort"
	"testing"
)

func TestSemanticInventoryEntryGroupKeyUsesExactFields(t *testing.T) {
	first := SemanticInventoryContribution{
		AuthResourcePath: "a|b", ResourceType: "c", BindingID: "d\x00e", ConceptID: "f",
	}
	second := SemanticInventoryContribution{
		AuthResourcePath: "a", ResourceType: "b|c", BindingID: "d\x00e", ConceptID: "f",
	}
	if semanticInventoryEntryGroupKeyFor(first) == semanticInventoryEntryGroupKeyFor(second) {
		t.Fatal("distinct identity fields produced the same group key")
	}
	if got := semanticInventoryEntryGroupKeyFor(first); got != (semanticInventoryEntryGroupKey{
		AuthResourcePath: "a|b", ResourceType: "c", BindingID: "d\x00e", ConceptID: "f",
	}) {
		t.Fatalf("group key = %#v", got)
	}
}

func TestSemanticInventoryEntryAggregatorOrderMatchesEncodedKey(t *testing.T) {
	contributions := []SemanticInventoryContribution{
		{AuthResourcePath: "zz", ResourceType: "a", BindingID: "x", ConceptID: "2"},
		{AuthResourcePath: "a", ResourceType: "zz", BindingID: "x", ConceptID: "1"},
		{AuthResourcePath: "a", ResourceType: "b", BindingID: "x", ConceptID: "10"},
		{AuthResourcePath: "a", ResourceType: "b", BindingID: "x", ConceptID: "2"},
	}
	aggregator := &SemanticInventoryEntryAggregator{entries: make(map[semanticInventoryEntryGroupKey]SemanticInventoryEntryPartial)}
	for _, contribution := range contributions {
		aggregator.entries[semanticInventoryEntryGroupKeyFor(contribution)] = SemanticInventoryEntryPartial{Contribution: contribution}
	}

	want := append([]SemanticInventoryContribution(nil), contributions...)
	sort.Slice(want, func(i, j int) bool {
		return SemanticInventoryEntryGroupOrderKey(want[i]) < SemanticInventoryEntryGroupOrderKey(want[j])
	})
	got := aggregator.Values()
	if len(got) != len(want) {
		t.Fatalf("values count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if semanticInventoryEntryGroupKeyFor(got[i].Contribution) != semanticInventoryEntryGroupKeyFor(want[i]) {
			t.Fatalf("value %d has key %#v, want %#v", i, semanticInventoryEntryGroupKeyFor(got[i].Contribution), semanticInventoryEntryGroupKeyFor(want[i]))
		}
	}
}
