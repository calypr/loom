package resolver

import (
	"context"

	materializationapi "github.com/calypr/loom/internal/api/graphql/graph/materialization"
	"github.com/calypr/loom/generated/graphql/graph/model"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/materialization"
)

// MaterializationService is the read contract used by the flat GraphQL API.
// Keeping this boundary narrow makes the generated resolver easy to exercise
// without constructing ClickHouse, catalog, and authorization dependencies.
type MaterializationService interface {
	Datasets(context.Context) ([]dfmaterialization.Materialization, error)
	Dataset(context.Context, model.DataframeDatasetInput) (*dfmaterialization.Materialization, error)
	Rows(context.Context, model.DataframeRowsInput) (dfmaterialization.Page, error)
	AggregateInput(context.Context, model.DataframeAggregateInput) (dfmaterialization.AggregateResult, error)
	AggregationsInput(context.Context, model.DataframeAggregationsInput) (dfmaterialization.AggregationsResult, error)
}

type Resolver struct {
	service MaterializationService
}

func NewResolver(service MaterializationService) *Resolver {
	return &Resolver{service: service}
}

var _ MaterializationService = (*materializationapi.Service)(nil)
