package postgres

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/backends/postgres/control"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	rows "github.com/calypr/loom/internal/backends/postgres/internal/rows"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

type Runtime struct {
	*control.Runtime
	SourceRows dataframeexecution.SourceRows
	PlanJSON   dataframeexecution.PostgreSQLPlanJSONExecutor
}

type Querier = rows.Querier

func NewPlanJSONExecutor(querier Querier) (dataframeexecution.PostgreSQLPlanJSONExecutor, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: nil row querier", control.ErrInvalidConfig)
	}
	return newPostgreSQLPlanJSONExecutor(querier)
}

func NewSourceRows(querier Querier) (dataframeexecution.SourceRows, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: nil row querier", control.ErrInvalidConfig)
	}
	executor, err := rows.NewExecutor(querier)
	if err != nil {
		return nil, err
	}
	return executor.SourceRows, nil
}

var _ rows.Querier = (*pgstore.Client)(nil)

func Open(ctx context.Context, cfg control.Config) (*Runtime, error) {
	inner, err := control.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	querier := inner.PoolClient()
	if querier == nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: control runtime did not expose its pool as the row querier", control.ErrBootstrap)
	}
	executor, err := rows.NewExecutor(querier)
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: assemble row executor: %w", control.ErrBootstrap, err)
	}
	planJSON, err := newPostgreSQLPlanJSONExecutor(querier)
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: assemble plan executor: %w", control.ErrBootstrap, err)
	}
	return &Runtime{Runtime: inner, SourceRows: executor.SourceRows, PlanJSON: planJSON}, nil
}
