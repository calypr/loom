package receipt

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
)

func cellTraceTestService(t *testing.T, executor CellTraceExecutor) (*CellTraceService, *receiptmodel.CompilationReceipt, context.Context) {
	t.Helper()
	ctx := context.Background()
	snapshot := testAuthoringV2CapabilitySnapshot()
	receipt := constructionfixtures.NativeReceipt(snapshot)
	store := explorerfixtures.NewStore()
	storedReceipt, err := store.InsertCompilationReceipt(ctx, *receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt = storedReceipt
	explorerService, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	core := authoringcore.New(explorerService, authoringcore.Config{Receipts: store})
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service := NewCellTraceService(CellTraceServiceConfig{
		Reader: core,
		ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
		Executor: executor,
	})
	return service, receipt, ctx
}

func TestCellTraceValidatesReceiptAndReturnsBoundEvidence(t *testing.T) {
	var receiptID string
	service, receipt, ctx := cellTraceTestService(t, func(_ context.Context, gotReceipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		if gotReceipt == nil || gotReceipt.ID != receiptID || bindings.DatasetGeneration != gotReceipt.SourceGeneration || request.Output != "patients" || request.Column != "patient_id" || request.RowID != "row-7" {
			t.Fatalf("unexpected trace invocation: receipt=%#v bindings=%#v request=%#v", gotReceipt, bindings, request)
		}
		return dataframeexecution.CellTraceResult{RowID: request.RowID, Column: request.Column, Value: "Patient/7", Status: dataframeexecution.CellTraceValue, Complete: true}, nil
	})
	receiptID = receipt.ID
	result, err := service.CellTrace(ctx, CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receiptID, OutputID: "patients", RowID: "row-7", Column: "patient_id", Limit: 25})
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
	service, receipt, ctx := cellTraceTestService(t, func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		return dataframeexecution.CellTraceResult{}, dataframeerrors.Wrap(
			dataframeexecution.ErrCellTraceRowNotFound, dataframeerrors.CodeInvalidRequest,
			fmt.Sprintf("published row %q was not found in output %q", request.RowID, request.Output),
			dataframeerrors.WithDetails(map[string]any{"rowId": request.RowID, "output": request.Output}),
		)
	})
	_, err := service.CellTrace(ctx, CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-9", Column: "patient_id"})
	var typed *ports.Error
	if !errors.As(err, &typed) || typed.Code != "PREVIEW_ROW_NOT_FOUND" || typed.Class != ports.ClassNotFound {
		t.Fatalf("a mistyped rowId must present as a non-server-fault not-found, got %v (%T)", err, err)
	}
}

func TestCellTraceRejectsUnknownColumnBeforeExecution(t *testing.T) {
	service, receipt, ctx := cellTraceTestService(t, func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		t.Fatal("trace executor must not run")
		return dataframeexecution.CellTraceResult{}, nil
	})
	_, err := service.CellTrace(ctx, CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7", Column: "not_a_column"})
	var typed *ports.Error
	if !errors.As(err, &typed) || typed.Code != "UNKNOWN_OUTPUT_COLUMN" {
		t.Fatalf("error = %v", err)
	}
}
