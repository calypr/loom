package ingest

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"
)

func TestBasicObservationFixturesPassGeneratedLoaderValidation(t *testing.T) {
	fixtures := []struct {
		name string
		rows int
	}{
		{name: "basic-nested-coding-expand", rows: 8},
		{name: "basic-pivot-unpivot", rows: 5},
		{name: "scope-001-repeated-pivot", rows: 3},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			path := filepath.Join("..", "..", "testdata", fixture.name, "Observation.ndjson")
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			rows := 0
			stageSeconds := make(map[string]float64)
			for scanner.Scan() {
				if len(scanner.Bytes()) == 0 {
					continue
				}
				rows++
				if _, _, category, err := loadRowGenerated("Observation", scanner.Bytes(), "fixture-validation", stageSeconds); err != nil {
					t.Errorf("Observation row %d fails generated loading (%s): %v", rows, category, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if rows != fixture.rows {
				t.Fatalf("loaded %d Observation rows; want %d", rows, fixture.rows)
			}
		})
	}
}
