package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateExtractUsesRuntimeConcreteReferenceType(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "extract.go")
	if err := generateExtract(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR edge extractor: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated FHIR edge extractor: %v", err)
	}

	const signature = "func (x *ResearchSubject) ExtractEdges"
	start := strings.Index(string(generated), signature)
	if start < 0 {
		t.Fatalf("generated extractor is missing %s", signature)
	}
	section := string(generated[start:])
	if next := strings.Index(section[len(signature):], "// ExtractEdges extracts graph links from "); next >= 0 {
		section = section[:len(signature)+next]
	}
	if !strings.Contains(section, `refType, ok = fhirschema.ConcreteResourceType(refType)`) {
		t.Fatalf("generated extractor does not validate runtime reference types:\n%s", section)
	}
	if !strings.Contains(section, `collectionID(refType, targetID)`) {
		t.Fatalf("generated extractor does not use the concrete runtime target type:\n%s", section)
	}
	if !strings.Contains(section, `seen[backrefKey] = struct{}{}`) {
		t.Fatalf("generated extractor does not deduplicate repeated backreferences:\n%s", section)
	}
	if strings.Contains(section, `collectionID("study", targetID)`) || strings.Contains(section, `collectionID("ResearchStudy", targetID)`) {
		t.Fatalf("ResearchSubject.study forward edge still hard-codes a label-derived target collection:\n%s", section)
	}
}

func TestGenerateExtractEmitsOnlyConcreteRootLoaders(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "extract.go")
	if err := generateExtract(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR edge extractor: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated FHIR edge extractor: %v", err)
	}
	text := string(generated)
	for _, resourceType := range []string{"Resource", "PatientContact", "PractitionerQualification", "OrganizationQualification"} {
		if strings.Contains(text, "ExtractEdges extracts graph links from "+resourceType) {
			t.Fatalf("generated extractor emitted non-resource definition %s", resourceType)
		}
	}
	if !strings.Contains(text, "ExtractEdges extracts graph links from Practitioner") {
		t.Fatal("generated extractor omitted concrete Practitioner root")
	}
}
