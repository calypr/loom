package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	"errors"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

func TestPreviewRejectsArchivedRecipeBeforeExecution(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	raw, err := json.Marshal(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	var packet map[string]json.RawMessage
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	var outputs []map[string]json.RawMessage
	if err := json.Unmarshal(packet["outputs"], &outputs); err != nil {
		t.Fatal(err)
	}
	outputs[0]["tableReshape"] = json.RawMessage(`{"kind":"UNPIVOT","unpivot":{"constructionId":"archived-shape","inputs":[{"column":"patient_id","key":{"kind":"STRING","string":"id"}}],"keyOutput":"key","keyLabel":"Key","valueOutput":"value","valueLabel":"Value","nullRowPolicy":"DROP"}}`)
	packet["outputs"], err = json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &receipt.Bundle); err != nil {
		t.Fatal(err)
	}
	receipt.RecipeDigest, err = receipt.Bundle.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.ResolvedRecipeDigest = receipt.RecipeDigest
	receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = receiptmodel.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	config := testConfig(snapshot)
	config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		t.Fatal("archived recipe must be rejected before execution")
		return dataframeexecution.PreviewSummary{}, nil
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err = service.Preview(context.Background(), PreviewRequest{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	var typed *ports.Error
	if !errors.As(err, &typed) || typed.Code != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("Preview() error = %v, want RECEIPT_RECOMPILE_REQUIRED", err)
	}
}
