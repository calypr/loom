package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	definitionsPath := fs.String("structure-definitions", "fhir/.cache/hl7.fhir.r5.core-5.0.0.tgz", "Path to the pinned HL7 FHIR R5 core package")
	edgePolicyPath := fs.String("edge-policy", "fhir/edge-policy.json", "Path to Loom's graph edge policy")
	structsDir := fs.String("structs-out", "fhir/model", "Directory for generated FHIR Go structs, validation, and edge extraction")
	metadataOut := fs.String("metadata-out", "fhir/schema/generated.go", "Path for generated compiler FHIR schema metadata")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Reading FHIR definitions from %s...\n", *definitionsPath)
	schema, err := loadStructureDefinitions(*definitionsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading FHIR StructureDefinitions: %v\n", err)
		os.Exit(1)
	}
	if err := applyEdgePolicy(schema, *edgePolicyPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading Loom edge policy: %v\n", err)
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
