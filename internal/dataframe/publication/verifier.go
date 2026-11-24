package publication

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataset"
)

type publicationVerificationStore struct {
	executions ExactExecutionCatalog
}

func (s publicationVerificationStore) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if s.executions == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication verification store is unavailable")
	}
	execution, output, err := s.executions.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		if errors.Is(err, ErrBundleNotFound) {

			return dataset.PublicationVerification{}, fmt.Errorf("%w: %w", dataset.ErrPublicationRecordMissing, err)
		}
		return dataset.PublicationVerification{}, err
	}
	verifiedAt := execution.UpdatedAt
	if output.VerifiedAt != nil {
		verifiedAt = *output.VerifiedAt
	}
	return dataset.PublicationVerification{
		Selector: selector, ExecutionID: execution.ID, Generation: execution.DatasetGeneration,
		State: string(DecodeBundleState(execution.State)), Queryable: execution.State.Successful() && output.Queryable(),
		VerifiedAt: verifiedAt, PhysicalTable: output.PhysicalTable,
	}, nil
}

var _ dataset.PublicationVerifier = publicationVerificationStore{}

func NewPublicationVerifier(executions ExactExecutionCatalog) dataset.PublicationVerifier {
	return publicationVerificationStore{executions: executions}
}
