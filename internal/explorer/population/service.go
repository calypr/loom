package population

import (
	"context"
	"github.com/calypr/loom/internal/explorer/authoringcore"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/selection"
)

type PopulationMappingExecutor func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (execution.PopulationMappingResult, error)

type Config struct {
	Core                       *authoringcore.Core
	Capability                 ports.CapabilityResolver
	Store                      selection.Store
	SelectionMembersCollection string

	CreateSelection func(context.Context, selection.SelectionIntentCreateRequest) (selection.SelectionCreateResult, error)

	ClassifyMaterializationError func(string, error) error
	PopulationMapping            PopulationMappingExecutor
	PopulationMappingCursorCodec *HMACPopulationMappingCursorCodec
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	return &Service{config: config}, nil
}
