package lifecycle

import "github.com/calypr/loom/internal/explorer/population"

var ErrPopulationMappingCursorUnavailable = population.ErrPopulationMappingCursorUnavailable

type PopulationMappingCursor = population.PopulationMappingCursor
type PopulationMappingCursorCodec = population.PopulationMappingCursorCodec
type HMACPopulationMappingCursorCodec = population.HMACPopulationMappingCursorCodec

func NewHMACPopulationMappingCursorCodec(secret string) (*HMACPopulationMappingCursorCodec, error) {
	return population.NewHMACPopulationMappingCursorCodec(secret)
}
