package explorer

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
)

func TestReceiptValidationReuseIsLocalAndPublicIdentityStaysFresh(t *testing.T) {
	receipt := testReceipt()
	key, err := receiptmodel.CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	id, err := receiptmodel.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey = key
	receipt.ID = id
	if err = receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*receiptmodel.CompilationReceipt){func(r *receiptmodel.CompilationReceipt) { r.NormalizedBundle = json.RawMessage(`{"changed":true}`) }, func(r *receiptmodel.CompilationReceipt) { r.CompiledConfig = json.RawMessage(`{"changed":true}`) }, func(r *receiptmodel.CompilationReceipt) { r.OutputFingerprints[r.Bundle.Outputs[0].Name] = "changed" }} {
		copy := receipt
		copy.OutputFingerprints = map[string]string{}
		for k, v := range receipt.OutputFingerprints {
			copy.OutputFingerprints[k] = v
		}
		mutate(&copy)
		if err = copy.Validate(); err == nil {
			t.Fatal("a mutation after previous successful validation reused stale authority")
		}
		changed, identityErr := receiptmodel.ReceiptID(copy)
		if identityErr == nil && changed == id {
			t.Fatal("public receiptmodel.ReceiptID reused previous validation")
		}
	}
	if err = receipt.Validate(); err != nil {
		t.Fatal("isolated mutations changed original receipt", err)
	}
}

func BenchmarkCompilationReceiptValidateWideRaw(b *testing.B) {
	receipt := testReceipt()
	fields := make([]map[string]any, 5000)
	for i := range fields {
		fields[i] = map[string]any{"columnId": i, "name": "history_value", "type": "integer", "nullable": false}
	}
	receipt.NormalizedBundle, _ = json.Marshal(map[string]any{"history": fields})
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(receipt)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := receipt.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
