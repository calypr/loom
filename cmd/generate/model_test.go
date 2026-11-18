package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFHIRModelGenerationUsesOfficialSchemaShapes(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "model.go")
	if err := generateModel(schema, generatedPath); err != nil {
		t.Fatalf("generate model: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read model: %v", err)
	}
	for _, expected := range []string{
		"Active *bool `json:\"active,omitempty\"`",
		"BirthDate *string `json:\"birthDate,omitempty\"`",
		"XBirthDate *Element `json:\"_birthDate,omitempty\"`",
		"ManagingOrganization *Reference `json:\"managingOrganization,omitempty\"`",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated model does not contain %q", expected)
		}
	}
}

func TestFHIRValidationGenerationUsesDirectOfficialPatternChecks(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "validate.go")
	if err := generateValidate(schema, generatedPath); err != nil {
		t.Fatalf("generate validation: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read validation: %v", err)
	}
	for _, expected := range []string{
		`Field: "resourceType"`,
		"validFHIRID(*x.ID)",
		"validFHIRDate(*x.BirthDate)",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated validation does not contain %q", expected)
		}
	}
}
