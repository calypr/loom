package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/population"
)

func (s *Service) PopulationMapping(ctx context.Context, request population.PopulationMappingRequest) (population.PopulationMappingResult, error) {
	return s.populationService.PopulationMapping(ctx, request)
}
