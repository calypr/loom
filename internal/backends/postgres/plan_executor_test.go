package postgres

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
	if _, err := newPostgreSQLPlanJSONExecutor(nil); err == nil {
		t.Fatal("nil querier must fail closed")
	}
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
	if _, err := NewPlanJSONExecutor(nil); err == nil {
		t.Fatal("nil querier must fail closed at the public seam")
	}
}
