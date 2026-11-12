package postgres

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/backends/postgres/control"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"

	rows "github.com/calypr/loom/internal/backends/postgres/internal/rows"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
)

// Runtime is the full backend composition returned by Open: the control
// plane from the control package (typed domain ports, pool lifecycle,
// schema setup, catalog evidence, dataset manifest lifecycle, structural
// query surface) plus the two data-plane ports that bridge compiled
// PostgreSQL commands into the shared DataFrame execution machinery.
//
// The data plane lives only here because the rows adapter imports the
// execution package, whose dependency chain reaches the renderer; keeping
// it out of control is what lets witness suites inside the render tree
// exercise the backend through the control plane without an import cycle.
type Runtime struct {
	*control.Runtime
	// SourceRows is the single required row-execution port for compiled
	// PostgreSQL commands (execution.SourceRows contract).
	SourceRows dataframeexecution.SourceRows
	// PlanJSON runs EXPLAIN over the tagged-command boundary for the
	// sanitized explain control plane.
	PlanJSON dataframeexecution.PostgreSQLPlanJSONExecutor
}

// Querier is the row-querier contract accepted by the data-plane builders:
// one Query method returning pgx.Rows. It is the factory's public spelling
// of the private rows adapter's interface (root re-export of its own
// internal, the accepted adapter-ownership shape): callers hand over a
// pool, an acquired connection, a dedicated single connection, or a test
// fake — all satisfied structurally through pgx's public Rows surface —
// and never name or import the private adapter package.
type Querier = rows.Querier

// NewPlanJSONExecutor exposes the EXPLAIN runner builder over a value
// satisfying the Querier contract. The contract is enforced at compile
// time by the parameter type; the only runtime rejection is absence. The
// composition surface and live witnesses build executors over connections
// they own without importing the private adapter.
func NewPlanJSONExecutor(querier Querier) (dataframeexecution.PostgreSQLPlanJSONExecutor, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: nil row querier", ErrInvalidConfig)
	}
	return newPostgreSQLPlanJSONExecutor(querier)
}

// NewSourceRows exposes the row-execution assembly over a Querier, the
// same seam NewPlanJSONExecutor uses. Witness binaries outside the backend
// (the execution test harness's adapter factory) assemble a live
// execution.SourceRows through here instead of importing the private rows
// package directly, keeping the factory the single assembly point.
func NewSourceRows(querier Querier) (dataframeexecution.SourceRows, error) {
	if querier == nil {
		return nil, fmt.Errorf("%w: nil row querier", ErrInvalidConfig)
	}
	executor, err := rows.NewExecutor(querier)
	if err != nil {
		return nil, err
	}
	return executor.SourceRows, nil
}

// rows.Querier conformance: the pool client is the Querier behind the
// executor, and this compile-time assertion makes any signature drift
// between Client.Query and the adapter contract fail the build (V7).
var _ rows.Querier = (*pgstore.Client)(nil)

// Open validates the configuration, opens exactly one readiness-gated
// PostgreSQL 18 pool for the selected backend, applies the approved schema
// setup policy, assembles the control-plane adapters, and attaches the
// row-execution and EXPLAIN ports. Every failure path closes the resources
// created so far; no half-initialized runtime escapes. The error text never
// carries connection credentials while the cause chain stays intact for
// errors.Is/As classification.
func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	inner, err := control.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	querier := inner.PoolClient()
	if querier == nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: control runtime did not expose its pool as the row querier", ErrBootstrap)
	}
	executor, err := rows.NewExecutor(querier)
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: assemble row executor: %w", ErrBootstrap, err)
	}
	planJSON, err := newPostgreSQLPlanJSONExecutor(querier)
	if err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("%w: assemble plan executor: %w", ErrBootstrap, err)
	}
	return &Runtime{Runtime: inner, SourceRows: executor.SourceRows, PlanJSON: planJSON}, nil
}
