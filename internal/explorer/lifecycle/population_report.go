package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/population"
)

const (
	DefaultPopulationMappingLimit = population.DefaultPopulationMappingLimit
	MaxPopulationMappingLimit     = population.MaxPopulationMappingLimit
)

func (s *Service) PopulationMapping(ctx context.Context, request PopulationMappingRequest) (PopulationMappingResult, error) {
	return s.populationService.PopulationMapping(ctx, request)
}
