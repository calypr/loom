package schema

import "testing"

func TestResolveTerminalScalarMetadata(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		path         string
		want         TerminalScalarMetadata
	}{
		{
			name:         "string primitive",
			resourceType: "Patient",
			path:         "gender",
			want:         TerminalScalarMetadata{Primitive: PrimitiveString},
		},
		{
			name:         "integer primitive",
			resourceType: "Observation",
			path:         "valueInteger",
			want:         TerminalScalarMetadata{Primitive: PrimitiveInteger},
		},
		{
			name:         "decimal primitive",
			resourceType: "Observation",
			path:         "valueQuantity.value",
			want:         TerminalScalarMetadata{Primitive: PrimitiveDecimal},
		},
		{
			name:         "boolean primitive",
			resourceType: "Observation",
			path:         "valueBoolean",
			want:         TerminalScalarMetadata{Primitive: PrimitiveBoolean},
		},
		{
			name:         "repeated object terminal",
			resourceType: "Observation",
			path:         "component[]",
			want:         TerminalScalarMetadata{Primitive: PrimitiveUnknown, Repeated: true},
		},
		{
			name:         "scalar below repeated object",
			resourceType: "Observation",
			path:         "component[].valueInteger",
			want:         TerminalScalarMetadata{Primitive: PrimitiveInteger, Repeated: true},
		},
		{
			name:         "date time format is generated metadata",
			resourceType: "Observation",
			path:         "valueDateTime",
			want:         TerminalScalarMetadata{Primitive: PrimitiveDateTime},
		},
		{
			name:         "date format is generated metadata",
			resourceType: "Patient",
			path:         "birthDate",
			want:         TerminalScalarMetadata{Primitive: PrimitiveDate},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ResolveTerminalScalarMetadata(tt.resourceType, tt.path)
			if !ok {
				t.Fatalf("ResolveTerminalScalarMetadata(%q, %q) did not resolve", tt.resourceType, tt.path)
			}
			if got != tt.want {
				t.Fatalf("metadata = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveTerminalScalarMetadataRejectsUnknownPath(t *testing.T) {
	if got, ok := ResolveTerminalScalarMetadata("Patient", "notARealField"); ok {
		t.Fatalf("unexpected metadata for unknown path: %#v", got)
	}
	if got, ok := ResolveTerminalScalarMetadata("NotAResource", "id"); ok {
		t.Fatalf("unexpected metadata for unknown resource: %#v", got)
	}
	// The active generated Observation definition does not advertise
	// valueDecimal, so the semantic API must not infer it from another FHIR
	// version or an external model.
	if got, ok := ResolveTerminalScalarMetadata("Observation", "valueDecimal"); ok {
		t.Fatalf("unexpected metadata for unadvertised Observation.valueDecimal: %#v", got)
	}
}

func TestResolveTerminalScalarMetadataRepeatedPrimitive(t *testing.T) {
	got, ok := ResolveTerminalScalarMetadata("Patient", "name[]")
	if !ok {
		t.Fatal("Patient.name[] did not resolve")
	}
	if !got.Repeated || got.Primitive != PrimitiveUnknown {
		t.Fatalf("Patient.name[] metadata = %#v, want repeated object", got)
	}
}
