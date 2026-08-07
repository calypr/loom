package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func loadCheckedInGraphFHIRSchema(t *testing.T) *Schema {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatalf("read graph schema: %v", err)
	}
	var schema Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("decode graph schema: %v", err)
	}
	return &schema
}
