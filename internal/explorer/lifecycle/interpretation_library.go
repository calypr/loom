package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Service) interpretationRepository() (explorer.InterpretationRepository, error) {
	if s == nil || s.config.InterpretationRepository == nil {
		return nil, unavailable("interpretations", "INTERPRETATION_UNAVAILABLE", "interpretation repository is not configured", nil)
	}
	return s.config.InterpretationRepository, nil
}

func (s *Service) CreateInterpretationRevision(ctx context.Context, request CreateInterpretationRevisionRequest) (explorer.InterpretationRevision, error) {
	return s.interpretationService.CreateInterpretationRevision(ctx, request)
}
