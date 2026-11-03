package execution

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func assertCellTraceMissingRowUserFault(t *testing.T, err error, wantRowID, wantOutput string) {
	t.Helper()
	if err == nil {
		t.Fatal("missing published row must surface typed as a non-retryable user fault")
	}
	if !errors.Is(err, ErrCellTraceRowNotFound) {
		t.Fatalf("missing row must carry the classified sentinel: %v", err)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("missing row escaped as untyped (authoring REST presents raw leaves as INTERNAL_ERROR 500): %v", err)
	}
	if userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("code=%s want %s", userErr.Code(), dataframeerrors.CodeInvalidRequest)
	}
	if userErr.Retryable() {
		t.Fatal("missing published row must be non-retryable")
	}
	if got := userErr.Details()["rowId"]; got != wantRowID {
		t.Fatalf("details=%v want echoing requested rowId", userErr.Details())
	}
	if got := userErr.Details()["output"]; got != wantOutput {
		t.Fatalf("details=%v want echoing requested output", userErr.Details())
	}
}

func TestEngineCellTraceCompiledMissingRowIsUserFault(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
	_, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-does-not-exist", Column: "gender"})
	assertCellTraceMissingRowUserFault(t, err, "row-does-not-exist", "patients")
}

func TestEngineCellTraceMissingPublishedRowIsUserFault(t *testing.T) {
	output := compileComposedCohortIdentityOutput(t)
	engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
	resolved := Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{output}}}
	rowID := `{"group_id":"cohort-absent","group_revision_id":"grouprev_absent"}`
	_, err := engine.CellTrace(context.Background(), resolved, CellTraceRequest{Output: output.Name, RowID: rowID, Column: "group_label", Limit: 10})
	assertCellTraceMissingRowUserFault(t, err, rowID, output.Name)
}

func TestEngineCellTraceKnownRowNeverTakesTheNotFoundArm(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"identity": "row-7", "value": nil, "status": "NO_MATCH", "contributors": []any{}, "hasMore": false})
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-7", Column: "condition"})
	if err != nil || result.Status != CellTraceNoMatch {
		t.Fatalf("matched row must not take the missing-row arm: result=%#v err=%v", result, err)
	}
}
