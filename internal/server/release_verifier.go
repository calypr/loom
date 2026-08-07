package server

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/store/arango"
)

// publicationVerificationStore is a narrow integration adapter over durable
// execution metadata. Publication owns the record shape; release activation
// consumes only its frozen exact-selector contract.
type releaseQueryClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arango.RowVisitor) error
}

type publicationVerificationStore struct {
	executions publication.ExactExecutionCatalog
	query      releaseQueryClient
}

func (s publicationVerificationStore) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if s.executions == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication verification store is unavailable")
	}
	execution, output, err := s.executions.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		return dataset.PublicationVerification{}, err
	}
	verifiedAt := execution.UpdatedAt
	if output.VerifiedAt != nil {
		verifiedAt = *output.VerifiedAt
	}
	return dataset.PublicationVerification{
		Selector: selector, ExecutionID: execution.ID, Generation: execution.DatasetGeneration,
		State: string(execution.State.Canonical()), Queryable: execution.State.Successful() && output.Queryable(),
		VerifiedAt: verifiedAt, PhysicalTable: output.PhysicalTable,
	}, nil
}

func (s publicationVerificationStore) ListExecutionProjects(ctx context.Context) ([]string, error) {
	if s.query == nil {
		return nil, fmt.Errorf("publication project inventory is unavailable")
	}
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
