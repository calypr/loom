package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoadOfficialFHIRJSONSchemaUsesSourceDefinitions(t *testing.T) {
	schema, err := loadOfficialFHIRJSONSchema(filepath.Join("..", "..", "fhir", "r5", "fhir.schema.json"), filepath.Join("..", "..", "fhir", "r5", "resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if schema.ID != "http://hl7.org/fhir/json-schema/5.0" || schema.FHIRVersion != "5.0.0" {
		t.Fatalf("schema identity = (%q, %q)", schema.ID, schema.FHIRVersion)
	}
	if len(schema.RootResourceTypes) != 23 {
		t.Fatalf("supported resources = %d, want 23", len(schema.RootResourceTypes))
	}
	if schema.Defs["Patient"] == nil || schema.Defs["Account"] != nil {
		t.Fatalf("resource scope includes Patient=%t Account=%t", schema.Defs["Patient"] != nil, schema.Defs["Account"] != nil)
	}
	if union := schema.Defs["ResourceList"]; union == nil || !union.Abstract {
		t.Fatal("ResourceList should remain an abstract union in schema metadata")
	}
	patient := schema.Defs["Patient"]
	if !isRequired(patient, "resourceType") {
		t.Fatal("Patient.resourceType is not required by the official schema")
	}
	name := patient.Properties["name"]
	if name == nil || name.Items == nil || refName(name.Items.Ref) != "HumanName" {
		t.Fatalf("Patient.name = %#v, want an array of HumanName", name)
	}
	contained := patient.Properties["contained"]
	if contained == nil || contained.Items == nil || refName(contained.Items.Ref) != "ResourceList" {
		t.Fatalf("Patient.contained = %#v, want raw object entries", contained)
	}
	address := schema.Defs["Address"]
	if address == nil || len(address.Properties["type"].EnumValues) == 0 {
		t.Fatal("official Address.type enumeration was not retained")
	}
	if schema.Defs["ObservationComponent"] == nil || schema.Defs["Observation_Component"] != nil {
		t.Fatal("official backbone definition was not normalized to Loom's exported Go type name")
	}
	quantityValue := schema.Defs["Quantity"].Properties["value"]
	if quantityValue == nil || quantityValue.Type != "number" || quantityValue.Pattern != "" {
		t.Fatalf("JSON Schema pattern on numeric Quantity.value should not become a string pattern: %#v", quantityValue)
	}
}

func TestOfficialFHIRSchemaGeneratedValidationUsesRequiredPatternsAndEnums(t *testing.T) {
	schema, err := loadOfficialFHIRJSONSchema(filepath.Join("..", "..", "fhir", "r5", "fhir.schema.json"), filepath.Join("..", "..", "fhir", "r5", "resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "validate.go")
	if err := generateValidate(schema, path); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`Field: "code"`,
		"field 'url' does not match its schema pattern",
		"field 'type' has an unsupported value",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated validator does not contain %q", expected)
		}
	}
}

func TestOfficialFHIRSchemaGeneratesTypedModels(t *testing.T) {
	schema, err := loadOfficialFHIRJSONSchema(filepath.Join("..", "..", "fhir", "r5", "fhir.schema.json"), filepath.Join("..", "..", "fhir", "r5", "resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.go")
	if err := generateModel(schema, path); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"type Patient struct {",
		"BirthDate *string `json:\"birthDate,omitempty\"`",
		"Name []*HumanName `json:\"name,omitempty\"`",
		"Contained []map[string]any `json:\"contained,omitempty\"`",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated official model does not contain %q", expected)
		}
	}
	if strings.Contains(string(generated), "type Account struct") {
		t.Fatal("model generator included a resource outside Loom's supported resource list")
	}
	if !strings.Contains(string(generated), "Contained []map[string]any `json:\"contained,omitempty\"`") {
		t.Fatal("contained resources should preserve their JSON shape as raw objects")
	}
}

func TestOfficialFHIRJSONSchemaPatternsCompileAsGoRegularExpressions(t *testing.T) {
	schema, err := loadOfficialFHIRJSONSchema(filepath.Join("..", "..", "fhir", "r5", "fhir.schema.json"), filepath.Join("..", "..", "fhir", "r5", "resources.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for definitionName, definition := range schema.Defs {
		for propertyName, property := range definition.Properties {
			check := func(path string, property *Property) {
				if property.Pattern == "" {
					return
				}
				if _, err := regexp.Compile(property.Pattern); err != nil {
					t.Errorf("FHIR schema pattern %s.%s: %v", definitionName, path, err)
				}
			}
			check(propertyName, property)
			if property.Items != nil {
				check(propertyName+"[]", property.Items)
			}
		}
	}
}

func TestLoadOfficialFHIRJSONSchemaRejectsUnknownResourceTypes(t *testing.T) {
	resourceTypesPath := filepath.Join(t.TempDir(), "resources.txt")
	if err := os.WriteFile(resourceTypesPath, []byte("NoSuchResource\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOfficialFHIRJSONSchema(filepath.Join("..", "..", "fhir", "r5", "fhir.schema.json"), resourceTypesPath); err == nil || !strings.Contains(err.Error(), "NoSuchResource") {
		t.Fatalf("loading unknown resource allowlist = %v", err)
	}
}
