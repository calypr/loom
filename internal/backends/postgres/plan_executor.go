package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	rows "github.com/calypr/loom/internal/backends/postgres/internal/rows"
)

func newPostgreSQLPlanJSONExecutor(querier rows.Querier) (dataframeexecution.PostgreSQLPlanJSONExecutor, error) {
	executor, err := rows.NewExecutor(querier)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, command command.CompiledCommand) (json.RawMessage, error) {
		var plans []string
		if err := executor.SourceRows(ctx, dataframeexecution.RowsRequest{Command: command, DeclaredColumns: []string{"QUERY PLAN"}}, func(row map[string]any) error {
			switch plan := row["QUERY PLAN"].(type) {
			case string:
				plans = append(plans, plan)
			case []byte:
				plans = append(plans, string(plan))
			case json.RawMessage:
				plans = append(plans, string(plan))
			default:
				return fmt.Errorf("PostgreSQL EXPLAIN returned plan text as %T", row["QUERY PLAN"])
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if len(plans) != 1 {
			return nil, fmt.Errorf("PostgreSQL EXPLAIN must return exactly one plan row, got %d", len(plans))
		}
		return json.RawMessage(plans[0]), nil
	}, nil
}
