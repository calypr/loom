package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestCellTraceValidatesReceiptAndReturnsBoundEvidence(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.CellTrace = func(_ context.Context, gotReceipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		if gotReceipt.ID != receipt.ID || bindings.DatasetGeneration != receipt.SourceGeneration || request.Output != "patients" || request.Column != "patient_id" || request.RowID != "row-7" {
			t.Fatalf("unexpected trace invocation: receipt=%#v bindings=%#v request=%#v", gotReceipt, bindings, request)
		}
		return dataframeexecution.CellTraceResult{RowID: request.RowID, Column: request.Column, Value: "Patient/7", Status: dataframeexecution.CellTraceValue, Complete: true}, nil
	}
	service := newTestService(t, store, config)
	result, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7", Column: "patient_id", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.ReceiptID != receipt.ID || result.Binding.ScopeDigest != receipt.AuthorizationScopeDigest || result.Trace.Value != "Patient/7" || !result.Trace.Complete {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Feature.OutputID != "patients" || result.Feature.Column != "patient_id" || result.Feature.AuthoredColumn != "patient_id" || result.Feature.OccurrenceID != "base" || result.Feature.Label != "Patient ID" {
		t.Fatalf("unexpected feature descriptor: %#v", result.Feature)
	}
}

func TestCellTraceMissingRowBecomesTypedNotFound(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.CellTrace = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		return dataframeexecution.CellTraceResult{}, dataframeerrors.Wrap(
			dataframeexecution.ErrCellTraceRowNotFound, dataframeerrors.CodeInvalidRequest,
			fmt.Sprintf("published row %q was not found in output %q", request.RowID, request.Output),
			dataframeerrors.WithDetails(map[string]any{"rowId": request.RowID, "output": request.Output}),
		)
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-9", Column: "patient_id"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "PREVIEW_ROW_NOT_FOUND" || lifecycleErr.Class != ClassNotFound {
		t.Fatalf("a mistyped rowId must present as a non-server-fault not-found, got %v (%T)", err, err)
	}
}

func TestCellTraceRejectsUnknownColumnBeforeExecution(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.CellTrace = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		t.Fatal("trace executor must not run")
		return dataframeexecution.CellTraceResult{}, nil
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7", Column: "not_a_column"})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "UNKNOWN_OUTPUT_COLUMN" {
		t.Fatalf("error = %v", err)
	}
}
