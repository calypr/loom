package main

import (
	"path/filepath"
	"testing"
)

func loadOfficialFHIRSchema(t *testing.T) *Schema {
	t.Helper()
	base := filepath.Join("..", "..", "fhir", "r5")
	schema, err := loadOfficialFHIRJSONSchema(filepath.Join(base, "fhir.schema.json"), filepath.Join(base, "resources.txt"))
	if err != nil {
		t.Fatalf("read official FHIR JSON Schema: %v", err)
	}
	if err := augmentOfficialFHIRSchema(schema, filepath.Join(base, "definitions")); err != nil {
		t.Fatalf("apply official FHIR StructureDefinitions: %v", err)
	}
	if err := addOfficialEdges(schema, filepath.Join(base, "definitions")); err != nil {
		t.Fatalf("derive official FHIR edges: %v", err)
	}
	schema.SourceSHA256, err = sourceSchemaDigest(filepath.Join(base, "fhir.schema.json"), filepath.Join(base, "resources.txt"), filepath.Join(base, "definitions"))
	if err != nil {
		t.Fatalf("hash official FHIR inputs: %v", err)
	}
	return schema
}
