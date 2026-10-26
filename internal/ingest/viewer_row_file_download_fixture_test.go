package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewerRowFileDownloadObservationFixtureLoadsGenerated(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "testdata", "viewer-row-file-download", "Observation.ndjson")
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read FileCell Observation fixture: %v", err)
	}
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(rows) != 1 || rows[0] == "" {
		t.Fatalf("FileCell Observation fixture must contain exactly one row, got %d", len(rows))
	}

	var source struct {
		ResourceType string `json:"resourceType"`
		ID           string `json:"id"`
		Code         struct {
			Coding []struct {
				System string `json:"system"`
				Code   string `json:"code"`
			} `json:"coding"`
		} `json:"code"`
	}
	if err := json.Unmarshal([]byte(rows[0]), &source); err != nil {
		t.Fatalf("decode FileCell Observation fixture: %v", err)
	}
	if source.ResourceType != "Observation" || source.ID != "filecell-observation-001" {
		t.Fatalf("FileCell Observation identity = %s/%s, want Observation/filecell-observation-001", source.ResourceType, source.ID)
	}

	vertex, _, kind, err := loadRowGenerated("Observation", []byte(rows[0]), "filecell-project", map[string]float64{})
	if err != nil {
		t.Fatalf("loadRowGenerated(FileCell Observation): kind=%q err=%v", kind, err)
	}
	if len(source.Code.Coding) != 1 || source.Code.Coding[0].System != "urn:loom:fixture:filecell" || source.Code.Coding[0].Code != "report" {
		t.Fatalf("FileCell Observation code = %#v, want one report coding", source.Code)
	}
	if vertex.ResourceType != "Observation" || vertex.ID != source.ID || vertex.Project != "filecell-project" {
		t.Fatalf("generated FileCell Observation = %#v, want identity Observation/%s in filecell-project", vertex, source.ID)
	}
}
