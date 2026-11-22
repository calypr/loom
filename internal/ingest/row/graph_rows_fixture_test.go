package row

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/testkit"
)

func TestGraphRowsFixturePassesProductionIngestionValidation(t *testing.T) {
	directory := testkit.FixturePath(t, "graph-rows")
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
