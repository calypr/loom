package semantic

import (
	"slices"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestSchemaPairingAcrossGeneratedDefinitions(t *testing.T) {
	registry, err := GeneratedDatatypeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for owner, key := range map[schema.DefinitionName]string{
		"Observation": "code", "ObservationComponent": "code", "TaskInput": "type",
		"TaskOutput": "type", "GroupCharacteristic": "code", "SubstanceDefinitionProperty": "type", "UsageContext": "code",
	} {
		t.Run(string(owner), func(t *testing.T) {
			pairs := registry.ScopePairings(owner)
			if len(pairs) != 1 || pairs[0].CategoricalPath != key || pairs[0].ValueChoiceGroup != "value" {
				t.Fatalf("pairings = %+v, want %s/value[x]", pairs, key)
			}
		})
	}
}

func TestStructuralPairingDependsOnAssociationNotResourceName(t *testing.T) {
	key := schema.Element{Name: "code", ReferencedType: "CodeableConcept"}
	value := schema.Element{Name: "valueString", JSONType: "string", ChoiceGroup: "value"}
	definition := schema.Definition{Name: "Unfamiliar", Elements: []schema.Element{key, value, {Name: "category", ReferencedType: "CodeableConcept"}}}
	for _, name := range []schema.DefinitionName{"Unfamiliar", "AnotherOwner"} {
		definition.Name = name
		slices.Reverse(definition.Elements)
		pair, ok := structuralScopePairing(definition)
		if !ok || pair.OwnerType != name || pair.CategoricalPath != "code" {
			t.Fatalf("reordered/renamed structure lost its association: %+v", pair)
		}
	}
	for _, elements := range [][]schema.Element{
		{key, {Name: "effectiveDateTime", ChoiceGroup: "effective"}},
		{value, {Name: "category", ReferencedType: "CodeableConcept"}},
		{key, value, {Name: "type", ReferencedType: "CodeableConcept"}},
	} {
		if pair, ok := structuralScopePairing(schema.Definition{Name: "Ambiguous", Elements: elements}); ok {
			t.Fatalf("inferred unproved association %+v from %+v", pair, elements)
		}
	}
}

func TestSchemaCoverageAccountsForEveryMemberWithoutRecursingReferences(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := GeneratedDatatypeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	rows := registry.Coverage(index)
	seen := map[string]MemberCoverage{}
	for _, row := range rows {
		key := string(row.Owner) + "." + row.Path
		if _, ok := seen[key]; ok || row.Treatment == "" {
			t.Fatalf("duplicate or unclassified coverage row %+v", row)
		}
		seen[key] = row
	}
	for _, definition := range index.Definitions() {
		for _, element := range definition.Elements {
			path := element.Name
			if element.JSONType == schema.JSONTypeArray {
				path += "[]"
			}
			if _, ok := seen[string(definition.Name)+"."+path]; !ok {
				t.Errorf("missing coverage %s.%s", definition.Name, path)
			}
		}
	}
	for _, path := range []string{"TaskInput.valueString", "GroupCharacteristic.valueBoolean", "Observation.valueString"} {
		if row := seen[path]; row.Treatment != "ASSOCIATED_VALUE" || row.KeyPath == "" {
			t.Errorf("lost binding for %s: %+v", path, row)
		}
	}
}
