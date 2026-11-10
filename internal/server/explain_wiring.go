package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	dataframepostgres "github.com/calypr/loom/internal/dataframe/postgres"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// explainCompiledQuery is an AQL policy surface: the EXPLAIN view is derived
// from the command through closed dispatch, so a PostgreSQL-tagged, untagged,
// or unknown-tag command is refused here and never sent to the Arango EXPLAIN
// endpoint.
func explainCompiledQuery(ctx context.Context, client *arangostore.Client, compiled dataframeexecution.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
	query, bindVars, dispatchErr := compiled.Command.AQLParameters()
	if dispatchErr != nil {
		return dataframeexecution.ExplainAssessment{}, dispatchErr
	}
	result, err := client.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
	if err != nil {
		return dataframeexecution.ExplainAssessment{}, err
	}
	return dataframeexecution.AssessmentFromArango(arangostore.AssessExplainResult(result)), nil
}

// explainPostgreSQLCompiledQuery is the PostgreSQL policy surface of live
// recipe Explain. It refuses a nil executor fail-closed — the PostgreSQL
// source runtime is wired by a later slice, and until then a PostgreSQL-
// tagged command must report a typed backend failure rather than fall back
// to the Arango EXPLAIN endpoint. With an executor, the sanitized assessment
// comes from execution.PostgreSQLExplainPlan, which reaches PostgreSQL only
// through the parent-tagged EXPLAIN command.
func explainPostgreSQLCompiledQuery(ctx context.Context, executor dataframeexecution.PostgreSQLPlanJSONExecutor, compiled dataframeexecution.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
	if executor == nil {
		return dataframeexecution.PostgreSQLPlanAssessment{}, dataframeerrors.Wrap(
			errors.New("live PostgreSQL recipe Explain requires a PostgreSQL connection; the PostgreSQL source runtime is not wired"),
			dataframeerrors.CodeBackendUnavailable, "live PostgreSQL recipe Explain is unavailable",
			dataframeerrors.WithRetryable(false),
			dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "wiring"}),
		)
	}
	return dataframeexecution.PostgreSQLExplainPlan(ctx, executor, compiled)
}

// newPostgreSQLPlanJSONExecutor adapts the pool-shaped Querier (a
// *pgxpool.Pool or *pgxpool.Conn from the readiness-checked
// internal/store/postgres pool) to the execution EXPLAIN runner. The
// EXPLAIN statement and its ordered typed arguments arrive only inside the
// tagged command via the shared DataFrame PostgreSQL row executor; the raw
// single "QUERY PLAN" row is returned to the sanitizer and never surfaced.
func newPostgreSQLPlanJSONExecutor(querier dataframepostgres.Querier) (dataframeexecution.PostgreSQLPlanJSONExecutor, error) {
	executor, err := dataframepostgres.NewExecutor(querier)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, command compiler.CompiledCommand) (json.RawMessage, error) {
		var plans []string
		if err := executor.QueryRows(ctx, command, []string{"QUERY PLAN"}, func(row map[string]any) error {
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
