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

func TestRepeatedCodingPathsFindsDirectAndNestedFHIRCodings(t *testing.T) {
	index, err := GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		resource string
		path     string
	}{
		{resource: "Specimen", path: "type.coding[]"},
		{resource: "Observation", path: "component[].code.coding[]"},
	}
	for _, test := range tests {
		t.Run(test.resource+"/"+test.path, func(t *testing.T) {
			paths, err := index.RepeatedCodingPaths(DefinitionName(test.resource))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, path := range paths {
				found = found || path == test.path
			}
			if !found {
				t.Fatalf("RepeatedCodingPaths(%q) = %v, missing %q", test.resource, paths, test.path)
			}
			facts, err := index.ResolveRowPath(DefinitionName(test.resource), test.path)
			if err != nil {
				t.Fatal(err)
			}
			if facts.FHIRType != "Coding" || facts.Shape != RowPathArray || facts.Cardinality != RowCardinalityMany || facts.Reference {
				t.Fatalf("coding path facts = %#v", facts)
			}
		})
	}
}

func TestHasRepeatedCodingPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		elements  []Element
		want      bool
		wantError bool
	}{
		{"direct", []Element{{Name: "coding", JSONType: JSONTypeArray, ArrayElementType: "Coding"}}, true, false},
		{"scalar", []Element{{Name: "coding", ReferencedType: "Coding"}}, false, false},
		{"cycle", []Element{{Name: "self", ReferencedType: "Root"}}, false, false},
		{"nested", []Element{{Name: "child", ReferencedType: "Nested"}}, true, false},
		{"invalid after coding", []Element{{Name: "coding", JSONType: JSONTypeArray, ArrayElementType: "Coding"}, {Name: "missing", ReferencedType: "Missing"}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := &Index{definitions: map[DefinitionName]Definition{
				"Root":   {Name: "Root", Elements: tc.elements},
				"Coding": {Name: "Coding"},
				"Nested": {Name: "Nested", Elements: []Element{{Name: "parent", ReferencedType: "Root"}, {Name: "coding", JSONType: JSONTypeArray, ArrayElementType: "Coding"}}},
			}}
			got, err := index.HasRepeatedCodingPath("Root")
			if (err != nil) != tc.wantError || got != tc.want {
				t.Fatalf("got (%v, %v), want (%v, error=%v)", got, err, tc.want, tc.wantError)
			}
		})
	}
	var unavailable *Index
	if _, err := unavailable.HasRepeatedCodingPath("Root"); err == nil {
		t.Fatal("nil index must fail")
	}
	if _, err := (&Index{}).HasRepeatedCodingPath("Missing"); err == nil {
		t.Fatal("missing root must fail")
	}
}
