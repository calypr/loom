package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

func TestRowLineageBindsReceiptAndDefaultsToBoundedPage(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.RowLineage = func(_ context.Context, gotReceipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		if gotReceipt.ID != receipt.ID || bindings.DatasetGeneration != receipt.SourceGeneration || request.Output != "patients" || request.RowID != "row-7" || request.Offset != 0 || request.Limit != compiler.DefaultRowLineageLimit {
			t.Fatalf("unexpected lineage invocation: receipt=%#v bindings=%#v request=%#v", gotReceipt, bindings, request)
		}
		return dataframeexecution.RowLineageResult{Contributors: []dataframeexecution.RowLineageContributor{}, HasMore: true, NextOffset: request.Limit}, nil
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	result, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != receipt.ID || result.Status != "COMPLETE" || !result.HasMore || result.NextOffset == nil || *result.NextOffset != compiler.DefaultRowLineageLimit || result.Contributors == nil {
		t.Fatalf("unexpected row lineage page: %#v", result)
	}
}

func TestRowLineageMapsMissingFinalRowToNotFound(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.RowLineage = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		return dataframeexecution.RowLineageResult{}, dataframeexecution.ErrRowLineageRowNotFound
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "missing-row",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassNotFound || lifecycleErr.Code != "PREVIEW_ROW_NOT_FOUND" {
		t.Fatalf("missing output row error = %v", err)
	}
}

func TestRowLineageMapsUnsupportedConstructionCapability(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.RowLineage = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
		return dataframeexecution.RowLineageResult{}, &compiler.RowLineageUnsupportedError{Capability: compiler.RowLineageCapability{
			ReasonCode: "ROW_LINEAGE_OPERATION_UNSUPPORTED", Operation: "PIVOT",
		}}
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.RowLineage(context.Background(), RowLineageRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Code != "ROW_LINEAGE_OPERATION_UNSUPPORTED" {
		t.Fatalf("unsupported operation error = %v", err)
	}
}
