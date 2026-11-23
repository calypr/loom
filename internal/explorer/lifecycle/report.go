package lifecycle

import "context"

func (s *Service) PopulationMapping(ctx context.Context, request PopulationMappingRequest) (PopulationMappingResult, error) {
	return s.populationService.PopulationMapping(ctx, request)
}
