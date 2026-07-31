package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFHIRResourceRegistryMatchesCheckedInFiles(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	dir := t.TempDir()
	resourcesPath := filepath.Join(dir, "resources.go")
	graphqlPath := filepath.Join(dir, "graphql.go")
	if err := generateFHIRResources(schema, resourcesPath, graphqlPath); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resources.go", "graphql.go"} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join("..", "..", "generated", "fhir", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("checked-in generated/fhir/%s is stale; run make generate-fhir", name)
		}
	}
}

func TestFHIRResourceRegistryHasEveryConcreteRoot(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	dir := t.TempDir()
	resourcesPath := filepath.Join(dir, "resources.go")
	if err := generateFHIRResources(schema, resourcesPath, filepath.Join(dir, "graphql.go")); err != nil {
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
