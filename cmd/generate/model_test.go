package main

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"testing"
)

func TestFHIRStructGenerationMatchesCheckedInArtifacts(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	for _, artifact := range []struct {
		name     string
		generate func(*Schema, string) error
	}{
		{name: "model.go", generate: generateModel},
		{name: "validate.go", generate: generateValidate},
		{name: "extract.go", generate: generateExtract},
		{name: "helpers.go", generate: func(_ *Schema, path string) error { return generateFHIRHelpers(path) }},
	} {
		t.Run(artifact.name, func(t *testing.T) {
			generatedPath := filepath.Join(t.TempDir(), artifact.name)
			if err := artifact.generate(schema, generatedPath); err != nil {
				t.Fatalf("generate %s: %v", artifact.name, err)
			}
			got, err := os.ReadFile(generatedPath)
			if err != nil {
				t.Fatalf("read generated %s: %v", artifact.name, err)
			}
			got, err = format.Source(got)
			if err != nil {
				t.Fatalf("format generated %s: %v", artifact.name, err)
			}
			want, err := os.ReadFile(filepath.Join("..", "..", "generated", "fhir", artifact.name))
			if err != nil {
				t.Fatalf("read checked-in %s: %v", artifact.name, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("checked-in generated/fhir/%s is stale; run make generate-fhir", artifact.name)
			}
		})
	}
}
