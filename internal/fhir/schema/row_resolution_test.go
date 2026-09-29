package schema

import "testing"

func TestResolveRowPathUsesGeneratedShapeRatherThanMemberNames(t *testing.T) {
	index, err := NewIndex([]Definition{
		{Name: "UnfamiliarPacket", Elements: []Element{
			{Name: "x_317", JSONType: "string", Format: "date-time"},
			{Name: "q9", JSONType: "integer"},
			{Name: "a42", JSONType: JSONTypeArray, ArrayElementType: "OpaqueReading"},
		}},
		{Name: "OpaqueReading", Elements: []Element{
			{Name: "mystery_1", JSONType: "boolean"},
			{Name: "payload_8", JSONType: JSONTypeObject, ReferencedType: "OpaqueCode"},
		}},
		{Name: "OpaqueCode", Elements: []Element{{Name: "v_4", JSONType: "string"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	date, err := index.ResolveRowPath("UnfamiliarPacket", "x_317")
	if err != nil || date.FHIRType != "dateTime" || date.Cardinality != RowCardinalityOne || date.Shape != RowPathScalar {
		t.Fatalf("scalar metadata = %#v, %v", date, err)
	}
	count, err := index.ResolveRowPath("UnfamiliarPacket", "q9")
	if err != nil || count.FHIRType != "integer" || count.Shape != RowPathScalar || count.Cardinality != RowCardinalityOne {
		t.Fatalf("integer metadata = %#v, %v", count, err)
	}
	scope, err := index.ResolveRowPath("UnfamiliarPacket", "a42[]")
	if err != nil || scope.FHIRType != "OpaqueReading" || scope.Shape != RowPathArray || scope.Cardinality != RowCardinalityMany {
		t.Fatalf("repeated scope metadata = %#v, %v", scope, err)
	}
	member, err := index.ResolveRowPath("UnfamiliarPacket", "a42[].mystery_1")
	if err != nil || member.FHIRType != "boolean" || member.Shape != RowPathScalar || member.Cardinality != RowCardinalityMany {
		t.Fatalf("repeated scalar metadata = %#v, %v", member, err)
	}
	object, err := index.ResolveRowPath("UnfamiliarPacket", "a42[].payload_8")
	if err != nil || object.FHIRType != "OpaqueCode" || object.Shape != RowPathObject || object.Cardinality != RowCardinalityMany {
		t.Fatalf("object metadata = %#v, %v", object, err)
	}
}

func TestResolveRowPathRejectsUnknownAndNoncanonicalPaths(t *testing.T) {
	index, err := NewIndex([]Definition{{Name: "SignalPacket", Elements: []Element{
		{Name: "readings", JSONType: JSONTypeArray, ItemJSONType: "number"},
		{Name: "status", JSONType: "string"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"absent", "readings", "status[]", "status[0]", "status..text", " status"} {
		if _, err := index.ResolveRowPath("SignalPacket", path); err == nil {
			t.Errorf("ResolveRowPath(%q) unexpectedly succeeded", path)
		}
	}
	if _, err := index.ResolveRowPath("MissingPacket", "status"); err == nil {
		t.Fatal("unknown root definition unexpectedly resolved")
	}
}

func TestResolveGeneratedRowPathClassifiesFHIRShape(t *testing.T) {
	index, err := GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path        string
		fhirType    string
		cardinality RowCardinality
		shape       RowPathShape
		reference   bool
	}{
		{path: "birthDate", fhirType: "date", cardinality: RowCardinalityOne, shape: RowPathScalar},
		{path: "name[]", fhirType: "HumanName", cardinality: RowCardinalityMany, shape: RowPathArray},
		{path: "name[].family", fhirType: "string", cardinality: RowCardinalityMany, shape: RowPathScalar},
		{path: "managingOrganization", fhirType: "Reference", cardinality: RowCardinalityOne, shape: RowPathObject, reference: true},
		{path: "managingOrganization.reference", fhirType: "string", cardinality: RowCardinalityOne, shape: RowPathScalar},
	}
	for _, test := range tests {
		got, resolveErr := index.ResolveRowPath("Patient", test.path)
		if resolveErr != nil || got.FHIRType != test.fhirType || got.Cardinality != test.cardinality || got.Shape != test.shape || got.Reference != test.reference {
			t.Errorf("generated Patient path %q = %#v, %v", test.path, got, resolveErr)
		}
	}
	observationReference, err := index.ResolveRowPath("Observation", "subject.reference")
	if err != nil || observationReference.FHIRType != "string" || observationReference.Cardinality != RowCardinalityOne ||
		observationReference.Shape != RowPathScalar || observationReference.Reference {
		t.Fatalf("generated Observation path subject.reference = %#v, %v", observationReference, err)
	}
}
