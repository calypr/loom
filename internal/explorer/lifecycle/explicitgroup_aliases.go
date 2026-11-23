package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/explicitgroup"
)

type ExplicitGroupRevisionResolveRequest = explicitgroup.ExplicitGroupRevisionResolveRequest
type ExplicitGroupRevisionListRequest = explicitgroup.ExplicitGroupRevisionListRequest
type ExplicitGroupRevisionChoice = explicitgroup.ExplicitGroupRevisionChoice
type ExplicitGroupRevisionProof = explicitgroup.ExplicitGroupRevisionProof
type ExplicitGroupRevisionResolver = explicitgroup.ExplicitGroupRevisionResolver
type ExplicitGroupInput = explicitgroup.ExplicitGroupInput
type ExplicitGroupCreateRequest = explicitgroup.ExplicitGroupCreateRequest
type ExplicitGroupSummary = explicitgroup.ExplicitGroupSummary
type ExplicitGroupCreateResult = explicitgroup.ExplicitGroupCreateResult

var NewRepositoryExplicitGroupRevisionResolver = explicitgroup.NewRepositoryExplicitGroupRevisionResolver

func (s *Service) CreateExplicitGroupRevision(ctx context.Context, request ExplicitGroupCreateRequest) (ExplicitGroupCreateResult, error) {
	return s.explicitGroupService.CreateExplicitGroupRevision(ctx, request)
}
