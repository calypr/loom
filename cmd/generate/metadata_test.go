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
	schema := loadOfficialFHIRSchema(t)

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

func TestFHIRSchemaMetadataGenerationRetainsStructureDefinitionEdges(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
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
	for _, expected := range []string{
		`"Patient|managingOrganization|Organization"`,
		`"outbound",`,
		`"has_one",`,
	} {
		if !bytes.Contains(got, []byte(expected)) {
			t.Errorf("generated metadata omitted %q", expected)
		}
	}
}

func TestFHIRSchemaMetadataOmitsNonResourceTraversals(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
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

func TestFHIRSchemaMetadataRetainsSemanticFields(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "generated.go")
	if err := generateFHIRSchema(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR schema metadata: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated FHIR metadata: %v", err)
	}

	var (
		definitionRequired    bool
		definitionTitle       bool
		definitionDescription bool
		choiceGroup           bool
		choiceRequired        bool
		elementRequired       bool
		bindingStrength       bool
		bindingURI            bool
		bindingVersion        bool
		bindingDescription    bool
		referenceTargets      bool
		elementTitle          bool
		elementDescription    bool
	)
	for _, definition := range schema.Defs {
		definitionRequired = definitionRequired || len(definition.Required) > 0
		definitionTitle = definitionTitle || definition.Title != ""
		definitionDescription = definitionDescription || definition.Description != ""
		walkSchemaProperties(definition.Properties, func(property *Property) {
			choiceGroup = choiceGroup || property.ChoiceGroup != ""
			choiceRequired = choiceRequired || property.ChoiceGroupRequired
			elementRequired = elementRequired || property.ElementRequired
			bindingStrength = bindingStrength || property.BindingStrength != ""
			bindingURI = bindingURI || property.BindingURI != ""
			bindingVersion = bindingVersion || property.BindingVersion != ""
			bindingDescription = bindingDescription || property.BindingDescription != ""
			referenceTargets = referenceTargets || len(property.ReferenceTargetTypes) > 0
			elementTitle = elementTitle || property.Title != ""
			elementDescription = elementDescription || property.Description != ""
		})
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{name: "definition required elements", ok: definitionRequired},
		{name: "definition title", ok: definitionTitle},
		{name: "definition description", ok: definitionDescription},
		{name: "choice group", ok: choiceGroup},
		{name: "required choice group", ok: choiceRequired},
		{name: "required element", ok: elementRequired},
		{name: "binding strength", ok: bindingStrength},
		{name: "binding URI", ok: bindingURI},
		{name: "binding version", ok: bindingVersion},
		{name: "binding description", ok: bindingDescription},
		{name: "reference target types", ok: referenceTargets},
		{name: "element title", ok: elementTitle},
		{name: "element description", ok: elementDescription},
	}
	for _, check := range checks {
		if !check.ok {
			t.Errorf("source schema has no %s metadata", check.name)
		}
	}

	for _, field := range []string{
		"Required: []string{",
		"ChoiceGroup:",
		"ChoiceGroupRequired: true",
		"ElementRequired: true",
		"BindingStrength:",
		"BindingURI:",
		"BindingVersion:",
		"BindingDescription:",
		"ReferenceTargetTypes: []string{",
		"Title:",
		"Description:",
	} {
		if !bytes.Contains(generated, []byte(field)) {
			t.Errorf("generated FHIR metadata omitted %s", field)
		}
	}
}

func TestFHIRSchemaMetadataUsesOfficialContainedResourceUnion(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	contained := schema.Defs["Patient"].Properties["contained"]
	if contained == nil || contained.Items == nil || schemaDefinitionRefName(schema, contained.Items.Ref) != "ResourceList" {
		t.Fatalf("contained resources did not retain the official ResourceList reference: %#v", contained)
	}
	if schema.Defs["Address"].Properties["links"] != nil || schema.Defs["Task"].Properties["for_fhir"] != nil {
		t.Fatal("generated metadata contains fields from the legacy graph schema")
	}
}

func walkSchemaProperties(properties map[string]*Property, visit func(*Property)) {
	for _, property := range properties {
		visit(property)
		if property.Items != nil {
			visit(property.Items)
			walkSchemaProperties(property.Items.Properties, visit)
		}
		walkSchemaProperties(property.Properties, visit)
	}
}
