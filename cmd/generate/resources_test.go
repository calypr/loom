package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFHIRResourceRegistryMatchesCheckedInFile(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	dir := t.TempDir()
	resourcesPath := filepath.Join(dir, "resources.go")
	if err := generateFHIRResources(schema, resourcesPath); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(resourcesPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "fhir", "model", "resources.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("checked-in fhir/model/resources.go is stale; run make generate-fhir")
	}
}

func TestFHIRResourceRegistryHasEveryConcreteRoot(t *testing.T) {
	schema := loadOfficialFHIRSchema(t)
	dir := t.TempDir()
	resourcesPath := filepath.Join(dir, "resources.go")
	if err := generateFHIRResources(schema, resourcesPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resourcesPath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, root := range schemaFHIRRootResourceTypes(schema) {
		if !strings.Contains(source, `case "`+root+`":`) {
			t.Errorf("missing constructor for %s", root)
		}
	}
}
