package ingest

import (
	"context"
	"fmt"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

func removeDanglingFHIRGraphEdges(ctx context.Context, client *arangostore.Client, project, datasetGeneration string, cursorBatch int) (int, float64, error) {
	if client == nil {
		return 0, 0, fmt.Errorf("FHIR edge target validation requires an Arango client")
	}
	if project == "" {
		return 0, 0, fmt.Errorf("FHIR edge target validation requires a project")
	}
	if cursorBatch <= 0 {
		cursorBatch = 1000
	}
	started := time.Now()
	var generationValue any
	if datasetGeneration != "" {
		generationValue = datasetGeneration
	}
	removed := 0
	err := client.QueryRows(ctx, danglingFHIRGraphEdgesAQL, cursorBatch, map[string]any{
		"project":            project,
		"dataset_generation": generationValue,
	}, func(map[string]any) error {
		removed++
		return nil
	})
	return removed, time.Since(started).Seconds(), err
}

const danglingFHIRGraphEdgesAQL = `
FOR edge IN fhir_edge
  FILTER edge.project == @project
  FILTER edge.dataset_generation == @dataset_generation
  LET source = DOCUMENT(edge._from)
  LET target = DOCUMENT(edge._to)
  FILTER source == null OR target == null
    OR source.project != @project OR target.project != @project
    OR source.dataset_generation != @dataset_generation
    OR target.dataset_generation != @dataset_generation
  REMOVE edge IN fhir_edge
  RETURN { removed: 1 }`
