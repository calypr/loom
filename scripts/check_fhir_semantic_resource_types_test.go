package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticResourceTypeGuardScansProductionFilesOnly(t *testing.T) {
	root := t.TempDir()
	semanticDir := filepath.Join(root, "internal", "fhir", "semantic")
	if err := os.MkdirAll(semanticDir, 0755); err != nil {
		t.Fatalf("create semantic source directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(semanticDir, "semantic_test.go"), []byte("package semantic\nconst resourceExample = \"Patient\"\n"), 0600); err != nil {
		t.Fatalf("write semantic test source: %v", err)
	}

	violations, err := semanticResourceTypeViolations(root)
	if err != nil {
		t.Fatalf("scan test source: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("test-only ResourceType literal was rejected: %v", violations)
	}

	productionPath := filepath.Join(semanticDir, "semantic.go")
	if err := os.WriteFile(productionPath, []byte("package semantic\nconst resourceType = \"Patient\"\n"), 0600); err != nil {
		t.Fatalf("write semantic production source: %v", err)
	}
	violations, err = semanticResourceTypeViolations(root)
	if err != nil {
		t.Fatalf("scan production source: %v", err)
	}
	if len(violations) != 1 || !strings.HasSuffix(violations[0], "semantic.go:2: concrete FHIR ResourceType literal \"Patient\"") {
		t.Fatalf("production ResourceType literal violations = %v", violations)
	}
}
