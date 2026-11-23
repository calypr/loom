package rowsource

import (
	"context"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/semantic"
)

type Config struct {
	Capability        ports.CapabilityResolver
	SemanticInventory func(context.Context, semantic.PageOptions) (semantic.Page, error)
	RowChoiceResolver rowdef.RowChoiceResolver
	RowChoicePlanner  rowdef.RowChoicePlanner
}

type Service struct {
	config Config
}

func NewService(config Config) *Service {
	return &Service{config: config}
}

type RowChoiceResolver = rowdef.RowChoiceResolver
type RowChoicePlanner = rowdef.RowChoicePlanner
type RowChoiceResolveRequest = rowdef.RowChoiceResolveRequest

const RowChoiceFieldGroup = rowdef.RowChoiceFieldGroup

const RowChoiceExpanded = rowdef.RowChoiceExpanded

func (s *Service) ConstructionHasFilterSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasFilterSourceInput(construction)
}

func (s *Service) ConstructionHasDeriveSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasDeriveSourceInput(construction)
}

func (s *Service) ConstructionHasExpandSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasExpandSourceInput(construction)
}

func (s *Service) ConstructionHasUnpivotSourceInput(construction authoringv2.Construction) bool {
	return ConstructionHasUnpivotSourceInput(construction)
}

func DeriveSourceLogicalTypeIsNumeric(logicalType string) bool {
	return deriveSourceLogicalTypeIsNumeric(logicalType)
}

func UnpivotSourceLogicalTypeIsTableScalar(logicalType string) bool {
	return unpivotSourceLogicalTypeIsTableScalar(logicalType)
}

func UnpivotSourceLogicalTypeIsMeltEligible(logicalType string) bool {
	return unpivotSourceLogicalTypeIsMeltEligible(logicalType)
}

func UnpivotMeltKindsSatisfiable(kinds []string) bool {
	return unpivotMeltKindsSatisfiable(kinds)
}
