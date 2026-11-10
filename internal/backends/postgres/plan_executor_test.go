package postgres

// Offline construction contract for the EXPLAIN plan executor adapter
// (moved from internal/server/explain_wiring_pg18_test.go by
// arch-backend-06: the constructor lives in the assembly that owns the
// pool, so its fail-closed behavior is tested at home).

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/jackc/pgx/v5"
)

type countingQuerier struct{ called *bool }

func (q countingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	*q.called = true
	return nil, errors.New("unexpected querier call")
}

func TestPostgreSQLPlanJSONExecutorConstructionFailsClosed(t *testing.T) {
	// The EXPLAIN runner builder refuses a nil querier rather than
	// inventing a connection.
	if _, err := newPostgreSQLPlanJSONExecutor(nil); err == nil {
		t.Fatal("nil querier must fail closed")
	}
	// The execution boundary refuses an AQL-tagged command before the
	// runner is touched.
	aqlCommand, err := command.NewAQLCommand("FOR d IN c RETURN d", nil)
	if err != nil {
		t.Fatal(err)
	}
	querierCalled := false
	runner, err := newPostgreSQLPlanJSONExecutor(countingQuerier{called: &querierCalled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner(context.Background(), aqlCommand); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-tagged command into the PostgreSQL runner: %v", err)
	}
	if querierCalled {
		t.Fatal("cross-tag command reached the querier")
	}
	// The public seam applies the same Querier contract to an untyped
	// value and fails closed on anything that does not satisfy it.
	if _, err := NewPlanJSONExecutor(struct{}{}); err == nil {
		t.Fatal("non-querier value must fail closed at the public seam")
	}
}
