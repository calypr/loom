package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFHIRGraphQLGenerationMatchesCheckedInSDL(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	path := filepath.Join(t.TempDir(), "fhir_schema.graphqls")
	if err := generateFHIRGraphQL(schema, path); err != nil {
		t.Fatalf("generate FHIR GraphQL SDL: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated SDL: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "generated", "graphql", "graph", "schema", "fhir_schema.graphqls"))
	if err != nil {
		t.Fatalf("read checked-in SDL: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("checked-in generated/graphql/graph/schema/fhir_schema.graphqls is stale; run make generate-fhir")
	}
}

func TestFHIRGraphQLGenerationHasCompleteTypedSurface(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	path := filepath.Join(t.TempDir(), "fhir_schema.graphqls")
	if err := generateFHIRGraphQL(schema, path); err != nil {
		t.Fatalf("generate FHIR GraphQL SDL: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated SDL: %v", err)
	}
	sdl := string(data)

	if got := strings.Count(sdl, "\ntype "); got != len(schema.Defs) {
		t.Fatalf("generated %d output objects, want %d", got, len(schema.Defs))
	}
	if got := strings.Count(sdl, "(project: String!, filters:"); got != 23 {
		t.Fatalf("generated %d root fields, want 23", got)
	}
	if strings.Contains(sdl, "first: Int") {
		t.Fatal("typed FHIR roots expose first instead of limit")
	}
	if strings.Contains(sdl, "links:") {
		t.Fatal("graph-internal links property leaked into generated SDL")
	}
	for _, root := range schemaFHIRRootResourceTypes(schema) {
		if !strings.Contains(sdl, "  "+root+"(project: String!, filters:") {
			t.Errorf("missing exact-capitalization root field %q", root)
		}
		if !strings.Contains(sdl, "type "+root+" implements FHIRResource {") {
			t.Errorf("root type %q does not implement FHIRResource", root)
		}
	}
	if !strings.Contains(sdl, "resource(type: FHIRResourceType, optional: Boolean! = false): FHIRResource") {
		t.Fatal("Reference.resource field is missing")
	}
	if !strings.Contains(sdl, "contained: [Resource!]") {
		t.Fatal("contained does not use generated Resource shape")
	}
	if !strings.Contains(sdl, `_birthDate: FHIRPrimitiveExtension @goField(name: "XBirthDate")`) {
		t.Fatal("primitive-extension fields do not carry a distinct gqlgen Go field name")
	}
}

func TestFHIRGraphQLGenerationRejectsUnresolvedReferences(t *testing.T) {
	schema := &Schema{Defs: map[string]*Definition{
		"Patient": {Properties: map[string]*Property{
			"resourceType": {Type: "string", Const: "Patient"},
			"id":           {Type: "string"},
			"meta":         {Ref: "Meta"},
		}},
	}}
	path := filepath.Join(t.TempDir(), "fhir_schema.graphqls")
	if err := generateFHIRGraphQL(schema, path); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("expected unresolved $ref error, got %v", err)
	}
}
