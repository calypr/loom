package graphqlapi

import "testing"

func TestNormalizeFHIRReference(t *testing.T) {
	for _, test := range []struct {
		name, input, target, id string
	}{
		{name: "relative", input: "Patient/123", target: "Patient", id: "123"},
		{name: "absolute", input: "https://example.test/fhir/Patient/123", target: "Patient", id: "123"},
		{name: "versioned", input: "Patient/123/_history/4", target: "Patient", id: "123"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, id, ok := normalizeFHIRReference(test.input)
			if !ok || target != test.target || id != test.id {
				t.Fatalf("normalizeFHIRReference(%q) = %q, %q, %v", test.input, target, id, ok)
			}
		})
	}
	if _, _, ok := normalizeFHIRReference("not-a-reference"); ok {
		t.Fatal("malformed reference accepted")
	}
}

func TestFHIRReferenceBatchSize(t *testing.T) {
	if fhirReferenceBatchSize != 256 {
		t.Fatalf("batch size = %d, want 256", fhirReferenceBatchSize)
	}
}
