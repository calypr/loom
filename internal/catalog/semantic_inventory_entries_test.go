package catalog

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/semantic"
)

func TestSemanticInventoryEntryAggregatorBuildsMaterializedEntries(t *testing.T) {
	buildID := semantic.BuildID("project", "generation")
	aggregator := semantic.NewEntryAggregator("project", "generation", "scope", "Observation", semantic.SourceFile)
	for _, contribution := range []semantic.Contribution{
		{
			Project: "project", DatasetGeneration: "generation", AuthResourcePath: "scope", ResourceType: "Observation",
			BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "concept", Example: "Zebra",
			Observation: semantic.Observation{
				SchemaVersion: semantic.ObservationSchemaVersion,
				Source:        semantic.Source{Canonical: "canonical", Type: "Observation", Path: "value"},
				Key:           semantic.Key{Selector: "code.coding[]", System: "urn:system", Code: "c", Display: "Zulu"},
				Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
				ObservedUnits: []string{"mg", "mmol"}, Completeness: semantic.CompletenessComplete, Population: 1,
			},
		},
		{
			Project: "project", DatasetGeneration: "generation", AuthResourcePath: "scope", ResourceType: "Observation",
			BuildID: buildID, SourceKind: semantic.SourceFile, BindingID: "binding", ConceptID: "concept", Example: "Alpha",
			Observation: semantic.Observation{
				SchemaVersion:    semantic.ObservationSchemaVersion,
				Source:           semantic.Source{Canonical: "canonical", Type: "Observation", Path: "value"},
				Key:              semantic.Key{Selector: "code.coding[]", System: "urn:system", Code: "c", Display: "Alpha"},
				Value:            semantic.Value{Selector: "valueQuantity.value", Type: "decimal", FallbackSelectors: []string{"valueQuantity.unit"}, FallbackOnBlankString: true},
				ExtensionURLPath: []string{"http://example.org/extension"}, ObservedUnits: []string{"g"},
				Completeness: semantic.CompletenessComplete, Population: 1,
			},
		},
	} {
		if err := aggregator.Add(contribution); err != nil {
			t.Fatal(err)
		}
	}
	values := aggregator.Values()
	if len(values) != 1 {
		t.Fatalf("entry aggregate groups = %d, want 1", len(values))
	}
	document := semantic.EntryDocument(values[0], "project", "generation", buildID, semantic.SourceFile)
	if document["example"] != "Alpha" || document["search_text"] != "observation value valuequantity.value urn:system c alpha" {
		t.Fatalf("entry example/search = %v/%v", document["example"], document["search_text"])
	}
	observation := document["observation"].(map[string]any)
	if observation["population"] != int64(2) || observation["examples_truncated"] != true {
		t.Fatalf("entry population/truncation = %v/%v", observation["population"], observation["examples_truncated"])
	}
	if observation["observed_units_truncated"] != true {
		t.Fatalf("observed units truncation = %v, want true for differing MIN and MAX", observation["observed_units_truncated"])
	}
	units, ok := observation["observed_units"].([]string)
	if !ok || len(units) != 1 || units[0] != "g" {
		t.Fatalf("observed units = %#v, want byte-minimum present array", observation["observed_units"])
	}
	if observation["value"].(map[string]any)["fallback_on_blank_string"] != true || observation["value"].(map[string]any)["fallback_selectors"].([]string)[0] != "valueQuantity.unit" || observation["extension_url_path"].([]string)[0] != "http://example.org/extension" || observation["source"].(map[string]any)["path"] != "value" {
		t.Fatalf("missing-value MIN behavior = %#v", observation)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded["_key"] != document["_key"] {
		t.Fatalf("entry serialization key = %v, err %v", decoded["_key"], err)
	}
}
