package main

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSchemaFHIRRootResourceTypesUsesCheckedInRootShape(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)

	roots := schemaFHIRRootResourceTypes(schema)
	if !slices.IsSorted(roots) {
		t.Fatalf("root resource types are not sorted: %v", roots)
	}
	for _, name := range []string{
		"DiagnosticReport",
		"MedicationRequest",
		"MedicationStatement",
		"Procedure",
		"Task",
	} {
		if !slices.Contains(roots, name) {
			t.Errorf("schema-derived roots do not include %q: %v", name, roots)
		}
	}
	for _, name := range []string{"Address", "PatientContact", "Resource"} {
		if slices.Contains(roots, name) {
			t.Errorf("schema-derived roots unexpectedly include non-root %q: %v", name, roots)
		}
	}
}

func TestFHIRSchemaMetadataGenerationMatchesCheckedInArtifact(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "generated.go")
	if err := generateFHIRSchema(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR schema metadata: %v", err)
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated metadata: %v", err)
	}
	got, err = format.Source(got)
	if err != nil {
		t.Fatalf("format generated metadata: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "generated", "fhirschema", "generated.go"))
	if err != nil {
		t.Fatalf("read checked-in metadata: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("checked-in generated/fhirschema/generated.go is stale; run make generate-fhir")
	}
}

func TestFHIRSchemaMetadataOmitsNonResourceTraversals(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "generated.go")
	if err := generateFHIRSchema(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR schema metadata: %v", err)
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated metadata: %v", err)
	}
	for _, key := range []string{
		`"PractitionerQualification|issuer|Organization"`,
		`"Organization|issuer|PractitionerQualification"`,
		`"OrganizationQualification|issuer|Organization"`,
	} {
		if bytes.Contains(got, []byte(key)) {
			t.Fatalf("generated traversal metadata contains non-resource key %s", key)
		}
	}
	if !bytes.Contains(got, []byte(`"Practitioner|qualification_issuer|Organization"`)) {
		t.Fatalf("generated traversal metadata omitted valid Practitioner relationship")
	}
}
