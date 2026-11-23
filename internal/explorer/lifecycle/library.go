package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/lifecycle/support"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/interpretation"
)

func (s *Service) interpretationRepository() (interpretation.Repository, error) {
	if s == nil || s.config.InterpretationRepository == nil {
		return nil, support.Unavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "interpretation repository is not configured", nil)
	}
	return s.config.InterpretationRepository, nil
}

func (s *Service) CreateInterpretationRevision(ctx context.Context, request interpretation.CreateInterpretationRevisionRequest) (explorer.InterpretationRevision, error) {
	return s.interpretationService.CreateInterpretationRevision(ctx, request)
}
