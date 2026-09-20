package schema

import "testing"

func TestStructureKeepsTypedReferenceEdgesBoundedAndImmutable(t *testing.T) {
	index, err := NewIndex([]Definition{
		{
			Name:             "SignalPacket",
			Title:            "Signal packet",
			RequiredElements: []string{"id"},
			Elements: []Element{
				{Name: "id", JSONType: "string", ElementRequired: true},
				{
					Name:             "measurements",
					JSONType:         JSONTypeArray,
					ArrayElementType: "SignalReading",
					BindingURI:       "https://example.test/readings",
					RequiredChildren: []string{"concept"},
				},
				{
					Name:     "inline",
					JSONType: JSONTypeObject,
					Elements: []Element{{Name: "label", JSONType: "string"}},
				},
			},
		},
		{
			Name: "SignalReading",
			Elements: []Element{{
				Name:             "children",
				JSONType:         JSONTypeArray,
				ArrayElementType: "SignalReading",
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	structure, ok := index.Structure("SignalPacket")
	if !ok {
		t.Fatal("SignalPacket structure is unavailable")
	}
	if structure.Root != "SignalPacket" || structure.Title != "Signal packet" || len(structure.Members) != 3 {
		t.Fatalf("structure = %#v", structure)
	}
	measurement := structure.Members[1]
	if measurement.CanonicalPath != "measurements[]" || !measurement.Repeated || measurement.ReferencedType != "SignalReading" {
		t.Fatalf("measurement member = %#v", measurement)
	}
	if len(measurement.Members) != 0 {
		t.Fatalf("named reference was recursively expanded: %#v", measurement.Members)
	}
	if measurement.Element.BindingURI != "https://example.test/readings" || measurement.Element.RequiredChildren[0] != "concept" {
		t.Fatalf("measurement metadata = %#v", measurement.Element)
	}
	inline := structure.Members[2]
	if len(inline.Members) != 1 || inline.Members[0].CanonicalPath != "inline.label" {
		t.Fatalf("inline structure = %#v", inline)
	}

	structure.RequiredElements[0] = "changed"
	structure.Members[1].Element.RequiredChildren[0] = "changed"
	again, ok := index.Structure("SignalPacket")
	if !ok || again.RequiredElements[0] != "id" || again.Members[1].Element.RequiredChildren[0] != "concept" {
		t.Fatalf("Structure returned aliased metadata: %#v", again)
	}
	if _, ok := index.Structure("Missing"); ok {
		t.Fatal("unknown definition returned a structure")
	}
	var nilIndex *Index
	if _, ok := nilIndex.Structure("SignalPacket"); ok {
		t.Fatal("nil index returned a structure")
	}
}

func TestDefinitionViewRequiredElementsDoesNotAliasIndex(t *testing.T) {
	index, err := NewIndex([]Definition{{Name: "Root", RequiredElements: []string{"id"}}})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	view, ok := index.ReadDefinition("Root")
	if !ok {
		t.Fatal("Root definition view is unavailable")
	}
	required := view.RequiredElements()
	required[0] = "changed"
	if got := view.RequiredElements()[0]; got != "id" {
		t.Fatalf("required element = %q, want id", got)
	}
}
