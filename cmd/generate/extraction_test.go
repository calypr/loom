package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateExtractUsesSchemaTargetTypeForResearchSubjectStudy(t *testing.T) {
	schema := loadCheckedInGraphFHIRSchema(t)
	generatedPath := filepath.Join(t.TempDir(), "extract.go")
	if err := generateExtract(schema, generatedPath); err != nil {
		t.Fatalf("generate FHIR edge extractor: %v", err)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated FHIR edge extractor: %v", err)
	}

	const signature = "func (x *ResearchSubject) ExtractEdges"
	start := strings.Index(string(generated), signature)
	if start < 0 {
		t.Fatalf("generated extractor is missing %s", signature)
	}
	section := string(generated[start:])
	if next := strings.Index(section[len(signature):], "// ExtractEdges extracts graph links from "); next >= 0 {
		section = section[:len(signature)+next]
	}
	if !strings.Contains(section, `collectionID("ResearchStudy", targetID)`) || !strings.Contains(section, `"ResearchStudy",`) {
		t.Fatalf("ResearchSubject.study forward edge did not use its schema target type:\n%s", section)
	}
	if strings.Contains(section, `collectionID("study", targetID)`) {
		t.Fatalf("ResearchSubject.study forward edge derived target collection from bare label:\n%s", section)
	}
}
