package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/arango"
)

// publicationVerificationStore is a narrow integration adapter over durable
// execution metadata. Publication owns the record shape; release activation
// consumes only its frozen exact-selector contract.
type releaseQueryClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arango.RowVisitor) error
}

type publicationVerificationStore struct{ query releaseQueryClient }

func (s publicationVerificationStore) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if s.query == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication verification store is unavailable")
	}
	binds := map[string]any{
		"@collection": "loom_dataframe_bundle_executions", "project": project, "generation": generation,
		"recipe": selector.Recipe, "translation_version": selector.TranslationVersion, "output": selector.Output,
	}
	var result *dataset.PublicationVerification
	err := s.query.QueryRows(ctx, findPublicationBySelectorAQL, 2, binds, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var decoded struct {
			ExecutionID    string    `json:"executionId"`
			Generation     string    `json:"generation"`
			ExecutionState string    `json:"executionState"`
			OutputState    string    `json:"outputState"`
			VerifiedAt     time.Time `json:"verifiedAt"`
			PhysicalTable  string    `json:"physicalTable"`
		}
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return err
		}
		executionState, outputState := decoded.ExecutionState, decoded.OutputState
		if executionState == "READY" {
			executionState = "PUBLISHED"
		}
		if outputState == "READY" {
			outputState = "PUBLISHED"
		}
		verification := dataset.PublicationVerification{Selector: selector, ExecutionID: decoded.ExecutionID, Generation: decoded.Generation, State: executionState, VerifiedAt: decoded.VerifiedAt, PhysicalTable: decoded.PhysicalTable}
		verification.Queryable = executionState == "PUBLISHED" && outputState == "PUBLISHED" && !decoded.VerifiedAt.IsZero() && decoded.PhysicalTable != ""
		result = &verification
		return nil
	})
	if err != nil {
		return dataset.PublicationVerification{}, err
	}
	if result == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication execution not found for selector %s", selector.Key())
	}
	return *result, nil
}

func (s publicationVerificationStore) ListExecutionProjects(ctx context.Context) ([]string, error) {
	projects := make([]string, 0)
	err := s.query.QueryRows(ctx, listExecutionProjectsAQL, 32, map[string]any{"@collection": "loom_dataframe_bundle_executions"}, func(row map[string]any) error {
		project, _ := row["project"].(string)
		if project != "" {
			projects = append(projects, project)
		}
		return nil
	})
	return projects, err
}

const findPublicationBySelectorAQL = `
FOR execution IN @@collection
  LET project = execution.project != null ? execution.project : execution.Project
  LET generation = execution.datasetGeneration != null ? execution.datasetGeneration : execution.DatasetGeneration
  FILTER project == @project AND generation == @generation
  FOR output IN execution.outputs
    LET selector = output.selector
    FILTER selector.recipe == @recipe
    FILTER selector.translationVersion == @translation_version
    FILTER selector.output == @output
    SORT execution.updatedAt DESC
    LIMIT 1
    RETURN {
      executionId: execution.id,
      generation,
      executionState: execution.state,
      outputState: output.state,
      verifiedAt: output.verifiedAt,
      physicalTable: output.physicalTable
    }
`

const listExecutionProjectsAQL = `
FOR execution IN @@collection
  LET project = execution.project != null ? execution.project : execution.Project
  FILTER project != null
  COLLECT value = project
  SORT value
  RETURN {project: value}
`

var _ dataset.PublicationVerifier = publicationVerificationStore{}
var _ dataset.ExecutionProjectSource = publicationVerificationStore{}
