package semantic

import (
	"sort"
	"testing"
)

func TestSemanticInventoryEntryGroupKeyUsesExactFields(t *testing.T) {
	first := Contribution{
		AuthResourcePath: "a|b", ResourceType: "c", BindingID: "d\x00e", ConceptID: "f",
	}
	second := Contribution{
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
	contributions := []Contribution{
		{AuthResourcePath: "zz", ResourceType: "a", BindingID: "x", ConceptID: "2"},
		{AuthResourcePath: "a", ResourceType: "zz", BindingID: "x", ConceptID: "1"},
		{AuthResourcePath: "a", ResourceType: "b", BindingID: "x", ConceptID: "10"},
		{AuthResourcePath: "a", ResourceType: "b", BindingID: "x", ConceptID: "2"},
	}
	aggregator := &EntryAggregator{entries: make(map[semanticInventoryEntryGroupKey]EntryPartial)}
	for _, contribution := range contributions {
		aggregator.entries[semanticInventoryEntryGroupKeyFor(contribution)] = EntryPartial{Contribution: contribution}
	}

	want := append([]Contribution(nil), contributions...)
	sort.Slice(want, func(i, j int) bool {
		return EntryGroupOrderKey(want[i]) < EntryGroupOrderKey(want[j])
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
