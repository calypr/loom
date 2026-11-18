package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	schemaPath := fs.String("json-schema", "fhir/r5/fhir.schema.json", "Path to the official HL7 FHIR R5 JSON Schema")
	definitionsPath := fs.String("structure-definitions", "fhir/r5/definitions", "Directory of official HL7 FHIR R5 StructureDefinitions")
	resourceTypesPath := fs.String("resource-types", "fhir/r5/resources.txt", "Path to Loom's supported FHIR resource type list")
	structsDir := fs.String("structs-out", "fhir/model", "Directory for generated FHIR Go structs, validation, and edge extraction")
	metadataOut := fs.String("metadata-out", "fhir/schema/generated.go", "Path for generated compiler FHIR schema metadata")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	schema, err := loadOfficialFHIRJSONSchema(*schemaPath, *resourceTypesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading FHIR JSON Schema: %v\n", err)
		os.Exit(1)
	}
	if err := augmentOfficialFHIRSchema(schema, *definitionsPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error applying FHIR StructureDefinitions: %v\n", err)
		os.Exit(1)
	}
	if err := addOfficialEdges(schema, *definitionsPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving FHIR edges: %v\n", err)
		os.Exit(1)
	}
	schema.SourceSHA256, err = sourceSchemaDigest(*schemaPath, *resourceTypesPath, *definitionsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error hashing FHIR inputs: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*structsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*metadataOut), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating compiler metadata directory: %v\n", err)
		os.Exit(1)
	}

	if err := generateFHIRHelpers(filepath.Join(*structsDir, "helpers.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating helpers.go: %v\n", err)
		os.Exit(1)
	}

	if err := generateModel(schema, filepath.Join(*structsDir, "model.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating model.go: %v\n", err)
		os.Exit(1)
	}

	if err := generateValidate(schema, filepath.Join(*structsDir, "validate.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating validate.go: %v\n", err)
		os.Exit(1)
	}

	if err := generateExtract(schema, filepath.Join(*structsDir, "extract.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating extract.go: %v\n", err)
		os.Exit(1)
	}

	if err := generateFHIRResources(schema, filepath.Join(*structsDir, "resources.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating resource registry: %v\n", err)
		os.Exit(1)
	}

	if err := generateFHIRSchema(schema, *metadataOut); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating fhir/schema/generated.go: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Code generation completed successfully.")
}
