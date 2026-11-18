package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFHIRModelGenerationUsesStructureDefinitionChoicesAndTypes(t *testing.T) {
	schema := loadStructureFixture(t)
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
		"XBirthDate *FHIRPrimitiveExtension `json:\"_birthDate,omitempty\"`",
		"ManagingOrganization *Reference `json:\"managingOrganization,omitempty\"`",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated model does not contain %q", expected)
		}
	}
}

func TestFHIRValidationGenerationPreservesPinnedCompatibilityRules(t *testing.T) {
	schema := loadStructureFixture(t)
	patient := schema.Defs["Patient"]
	patient.Required = []string{"id"}
	generatedPath := filepath.Join(t.TempDir(), "validate.go")
	if err := generateValidate(schema, generatedPath); err != nil {
		t.Fatalf("generate validation: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read validation: %v", err)
	}
	for _, expected := range []string{
		`Field: "id"`,
		"rx_Patient_ID.MatchString(*x.ID)",
		"ValidateFhirDate(*x.BirthDate)",
	} {
		if !strings.Contains(string(generated), expected) {
			t.Errorf("generated validation does not contain %q", expected)
		}
	}
}

func loadStructureFixture(t *testing.T) *Schema {
	t.Helper()
	packagePath := t.TempDir()
	writeFixture := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(packagePath, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeFixture("package.json", `{"name":"hl7.fhir.r5.core","version":"5.0.0"}`)
	writeFixture("StructureDefinition-Patient.json", `{
  "resourceType":"StructureDefinition",
  "url":"http://hl7.org/fhir/StructureDefinition/Patient",
  "version":"5.0.0",
  "name":"Patient",
  "description":"A person receiving care.",
  "type":"Patient",
  "kind":"resource",
  "derivation":"specialization",
  "snapshot":{"element":[
    {"path":"Patient","min":0,"max":"*"},
    {"path":"Patient.id","short":"Logical id","definition":"The resource identifier.","min":0,"max":"1","type":[{"code":"id"}]},
    {"path":"Patient.meta","min":0,"max":"1","type":[{"code":"Meta"}]},
    {"path":"Patient.active","short":"Whether active","min":0,"max":"1","type":[{"code":"boolean"}]},
    {"path":"Patient.birthDate","short":"Birth date","min":0,"max":"1","type":[{"code":"date"}]},
    {"path":"Patient.managingOrganization","min":0,"max":"1","type":[{"code":"Reference","targetProfile":["http://hl7.org/fhir/StructureDefinition/Organization"]}]}
  ]}
}`)
	writeFixture("StructureDefinition-Meta.json", `{
  "resourceType":"StructureDefinition",
  "url":"http://hl7.org/fhir/StructureDefinition/Meta",
  "version":"5.0.0",
  "name":"Meta",
  "type":"Meta",
  "kind":"complex-type",
  "derivation":"specialization",
  "snapshot":{"element":[
    {"path":"Meta","min":0,"max":"1"},
    {"path":"Meta.versionId","min":0,"max":"1","type":[{"code":"id"}]}
  ]}
}`)
	writeFixture("StructureDefinition-Reference.json", `{
  "resourceType":"StructureDefinition",
  "url":"http://hl7.org/fhir/StructureDefinition/Reference",
  "version":"5.0.0",
  "name":"Reference",
  "type":"Reference",
  "kind":"complex-type",
  "derivation":"specialization",
  "snapshot":{"element":[
    {"path":"Reference","min":0,"max":"1"},
    {"path":"Reference.reference","min":0,"max":"1","type":[{"code":"string"}]}
  ]}
}`)
	definitions, _, err := readStructureDefinitions(packagePath)
	if err != nil {
		t.Fatalf("read fixture StructureDefinitions: %v", err)
	}
	schema := &Schema{ID: fhirDefinitionBase, Defs: make(map[string]*Definition)}
	for _, definition := range definitions {
		if definition.Kind == "resource" || definition.Kind == "complex-type" {
			if err := addStructureDefinition(schema, definition); err != nil {
				t.Fatalf("convert %s: %v", definition.Type, err)
			}
		}
	}
	return schema
}
