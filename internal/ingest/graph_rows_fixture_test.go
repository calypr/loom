package ingest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The graph execution fixtures also seed the native editor through production
// ingestion. Exact compiler rows alone cannot prove that these source records
// satisfy the generated R5 validation boundary.
func TestGraphRowsFixturePassesProductionIngestionValidation(t *testing.T) {
	directory := filepath.Join("..", "..", "testdata", "graph-rows")
	raw, err := os.ReadFile(filepath.Join(directory, "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		RecordCounts map[string]int `json:"recordCounts"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(oracle.RecordCounts) {
		t.Fatalf("seed resource files=%d, expected %d", len(paths), len(oracle.RecordCounts))
	}
	builder := NewGeneratedRowBuilder("loom_dev_graph_rows_validation", "")
	for _, path := range paths {
		resourceType := strings.TrimSuffix(filepath.Base(path), ".ndjson")
		t.Run(resourceType, func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			count := 0
			for scanner.Scan() {
				count++
				if _, category, err := builder.Build(resourceType, scanner.Bytes(), map[string]float64{}); err != nil {
					t.Errorf("row %d production %s validation: %v", count, category, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			expected, known := oracle.RecordCounts[resourceType]
			if !known || count != expected {
				t.Errorf("%s source rows=%d, expected %d (known=%v)", resourceType, count, expected, known)
			}
		})
	}
}
