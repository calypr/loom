package main

import (
	"bytes"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	want, err := os.ReadFile(filepath.Join("..", "..", "internal", "fhirschema", "generated.go"))
	if err != nil {
		t.Fatalf("read checked-in metadata: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("checked-in internal/fhirschema/generated.go is stale; run make generate-fhir")
	}
}

func TestGenerateExtractUsesSchemaTargetTypeForResearchSubjectStudy(t *testing.T) {
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
	if !strings.Contains(section, `collectionID("ResearchStudy", targetID)`) || !strings.Contains(section, `"ResearchStudy",`) {
		t.Fatalf("ResearchSubject.study forward edge did not use its schema target type:\n%s", section)
	}
	if strings.Contains(section, `collectionID("study", targetID)`) {
		t.Fatalf("ResearchSubject.study forward edge derived target collection from bare label:\n%s", section)
	}
}

func TestFHIRRootResourceDefinitionRequiresConcreteResourceShape(t *testing.T) {
	stringType := any("string")
	root := &Definition{Properties: map[string]*Property{
		"resourceType": {Const: "Task"},
		"id":           {Type: stringType},
		"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
	}}
	if !isFHIRRootResourceDefinition("Task", root) {
		t.Fatal("concrete resource root shape was rejected")
	}

	for testName, testCase := range map[string]struct {
		definitionName string
		definition     *Definition
	}{
		"mismatched resource type": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Patient"},
			"id":           {Type: stringType},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
		"missing metadata root field": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Task"},
			"id":           {Type: stringType},
		}}},
		"non-string id": {definitionName: "Task", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Task"},
			"id":           {Type: any("integer")},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
		"abstract Resource placeholder": {definitionName: "Resource", definition: &Definition{Properties: map[string]*Property{
			"resourceType": {Const: "Resource"},
			"id":           {Type: stringType},
			"meta":         {Ref: "http://graph-fhir.io/schema/0.0.2/Meta"},
		}}},
	} {
		t.Run(testName, func(t *testing.T) {
			if isFHIRRootResourceDefinition(testCase.definitionName, testCase.definition) {
				t.Fatal("non-root resource shape was accepted")
			}
		})
	}
}

func loadCheckedInGraphFHIRSchema(t *testing.T) *Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("read graph schema: %v", err)
	}
	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode graph schema: %v", err)
	}
	return &schema
}
