package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	schemaPath := fs.String("schema", "schemas/graph-fhir.json", "Path to graph-fhir JSON schema")
	structsDir := fs.String("structs-out", "generated/fhir", "Directory for generated FHIR Go structs, validation, and edge extraction")
	metadataOut := fs.String("metadata-out", "generated/fhirschema/generated.go", "Path for generated compiler FHIR schema metadata")
	graphqlOut := fs.String("graphql-out", "generated/graphql/graph/schema/fhir_schema.graphqls", "Path for generated FHIR GraphQL schema")
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Reading schema from %s...\n", *schemaPath)
	data, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading schema: %v\n", err)
		os.Exit(1)
	}

	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing schema JSON: %v\n", err)
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

	// 1. Generate model.go
	if err := generateModel(&schema, filepath.Join(*structsDir, "model.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating model.go: %v\n", err)
		os.Exit(1)
	}

	// 2. Generate validate.go
	if err := generateValidate(&schema, filepath.Join(*structsDir, "validate.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating validate.go: %v\n", err)
		os.Exit(1)
	}

	// 3. Generate extract.go
	if err := generateExtract(&schema, filepath.Join(*structsDir, "extract.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating extract.go: %v\n", err)
		os.Exit(1)
	}

	// 4. Generate the concrete-resource registry and GraphQL markers.
	if err := generateFHIRResources(&schema, filepath.Join(*structsDir, "resources.go"), filepath.Join(*structsDir, "graphql.go")); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating resource registry: %v\n", err)
		os.Exit(1)
	}

	// 5. Generate internal compiler schema metadata
	if err := generateFHIRSchema(&schema, *metadataOut); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating generated/fhirschema/generated.go: %v\n", err)
		os.Exit(1)
	}

	// Keep the FHIR GraphQL surface generated from the same parsed schema as
	// the compiler metadata and Go models. The SDL is intentionally emitted as
	// a separate schema document so the handwritten dataframe API remains
	// readable and stable.
	if strings.TrimSpace(*graphqlOut) != "" {
		if err := os.MkdirAll(filepath.Dir(*graphqlOut), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating GraphQL schema directory: %v\n", err)
			os.Exit(1)
		}
		if err := generateFHIRGraphQL(&schema, *graphqlOut); err != nil {
			fmt.Fprintf(os.Stderr, "Error generating FHIR GraphQL schema: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Println("Code generation completed successfully.")
}
