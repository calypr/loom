package main

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
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

func TestFHIRSchemaMetadataRetainsSemanticFields(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
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

func TestFHIRSchemaMetadataKeepsExternalRefsAsArrayShapeOnly(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	links := schema.Defs["Address"].Properties["links"]
	if links == nil || links.Items == nil || links.Items.Ref != "https://json-schema.org/draft/2020-12/links" {
		t.Fatalf("Address.links source schema changed unexpectedly: %#v", links)
	}
	if got := schemaDefinitionRefName(schema, links.Items.Ref); got != "" {
		t.Fatalf("external links schema reference became definition %q", got)
	}
	if got := schemaDefinitionRefName(schema, schema.ID+"/Patient"); got != "Patient" {
		t.Fatalf("FHIR schema-rooted reference became %q, want Patient", got)
	}

	generatedPath := filepath.Join(t.TempDir(), "generated.go")
	if err := generateFHIRSchema(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR schema metadata: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated FHIR metadata: %v", err)
	}
	if bytes.Contains(generated, []byte(`ItemRef: "links"`)) {
		t.Fatal("external JSON Schema link reference was emitted as a FHIR array element type")
	}
	arrayShape := regexp.MustCompile(`Name: "links",\n\s*Kind: "array",\n\s*ItemKind: "object",`)
	if !arrayShape.Match(generated) {
		t.Fatal("external links reference did not retain its array/object shape")
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
