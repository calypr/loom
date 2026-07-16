package clickhouse

import (
	"context"
	"encoding/json"

	materializationapi "github.com/calypr/loom/graphqlapi/materialization"
	"github.com/calypr/loom/graphqlapi/model"
	"github.com/calypr/loom/internal/dataframe/materialization"
)

type Resolver struct {
	service *materializationapi.Service
}

func NewResolver(service *materializationapi.Service) *Resolver {
	return &Resolver{service: service}
}

func (r *Resolver) DataframeDatasets(ctx context.Context) ([]*model.DataframeMaterialization, error) {
	values, err := r.service.Datasets(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*model.DataframeMaterialization, 0, len(values))
	for _, value := range values {
		result = append(result, materializationapi.Model(value))
	}
	return result, nil
}

func (r *Resolver) DataframeDataset(ctx context.Context, input model.DataframeDatasetInput) (*model.DataframeMaterialization, error) {
	value, err := r.service.Dataset(ctx, input)
	if err != nil {
		return nil, err
	}
	return materializationapi.Model(*value), nil
}

func (r *Resolver) DataframeRows(ctx context.Context, input model.DataframeRowsInput) (*model.DataframeRowConnection, error) {
	page, err := r.service.Rows(ctx, input)
	if err != nil {
		return nil, err
	}
	rows, err := json.Marshal(page.Rows)
	if err != nil {
		return nil, err
	}
	var cursor *string
	if page.NextCursor != "" {
		cursor = &page.NextCursor
	}
	total := int(page.TotalCount)
	return &model.DataframeRowConnection{
		Materialization: materializationapi.Model(page.Materialization),
		Columns:         page.Columns,
		Rows:            rows,
		TotalCount:      &total,
		PageInfo:        &model.DataframePageInfo{HasNextPage: page.HasNext, EndCursor: cursor},
	}, nil
}

func (r *Resolver) DataframeAggregate(ctx context.Context, input model.DataframeAggregateInput) (*model.DataframeAggregateResult, error) {
	result, err := r.service.AggregateInput(ctx, input)
	if err != nil {
		return nil, err
	}
	return &model.DataframeAggregateResult{
		Materialization: materializationapi.Model(result.Materialization),
		Columns:         result.Columns,
		Rows:            materializationapi.AggregateRows(result.Rows),
	}, nil
}

var _ = materialization.Page{}
