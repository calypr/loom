package server

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

func TestRowCoreCrossTagGuardsRefusePreDispatch(t *testing.T) {
	visit := func(map[string]any) error { return nil }

	pgCommand, err := command.NewPostgreSQLCommand(`SELECT 1`, nil)
	if err != nil {
		t.Fatal(err)
	}
	aqlDispatches := 0
	aqlLane := configuredAQLSourceRows(nil, "contract", func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		aqlDispatches++
		return nil
	})
	if err := aqlLane(context.Background(), dataframeexecution.RowsRequest{Command: pgCommand, BatchSize: 10}, visit); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("PostgreSQL-tagged command into the AQL row core: %v", err)
	}
	if aqlDispatches != 0 {
		t.Fatalf("AQL core dispatched %d times for a cross-tag command; the tag guard must reject before dispatch", aqlDispatches)
	}

	aqlCommand, err := command.NewAQLCommand("RETURN 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	pgDispatches := 0
	pgLane := configuredPostgreSQLSourceRows(nil, "contract", func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		pgDispatches++
		return nil
	})
	if err := pgLane(context.Background(), dataframeexecution.RowsRequest{Command: aqlCommand, BatchSize: 10}, visit); !errors.Is(err, command.ErrCompiledCommandBackendMismatch) {
		t.Fatalf("AQL-tagged command into the PostgreSQL row core: %v", err)
	}
	if pgDispatches != 0 {
		t.Fatalf("PostgreSQL core dispatched %d times for a cross-tag command; the tag guard must reject before dispatch", pgDispatches)
	}
}
