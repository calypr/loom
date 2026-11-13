package explorer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReceiptRawSyntaxValidationMatchesCanonicalDecoder(t *testing.T) {
	values := [][]byte{nil, []byte(""), []byte(" "), []byte("null"), []byte(" null \n"), []byte("true"), []byte("1e4000"), []byte("9007199254740993"), []byte("-0.0"), []byte(`{"b":2,"a":1}`), []byte(`{"a":1,"a":2}`), []byte(`[1,null,{"x":"\uD800"}]`), []byte(`{"x":"<>&"}`), []byte("[1,]"), []byte("null null"), []byte("01"), []byte("NaN"), []byte("{broken"), []byte{'"', 0xff, '"'}, []byte(strings.Repeat("[", 101) + "0" + strings.Repeat("]", 101))}
	for _, raw := range values {
		_, want := canonicalJSONBytes(raw)
		got := validateReceiptRawJSON(raw)
		if (want == nil) != (got == nil) {
			t.Fatalf("raw %q acceptance changed: old=%v new=%v", raw, want, got)
		}
		if want != nil && got.Error() != want.Error() {
			t.Fatalf("raw %q error changed: old=%v new=%v", raw, want, got)
		}
	}
}

func TestReceiptValidationReuseIsLocalAndPublicIdentityStaysFresh(t *testing.T) {
	receipt := testReceipt()
	key, err := CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey = key
	receipt.ID = id
	if err = receipt.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*CompilationReceipt){func(r *CompilationReceipt) { r.NormalizedBundle = json.RawMessage(`{"changed":true}`) }, func(r *CompilationReceipt) { r.CompiledConfig = json.RawMessage(`{"changed":true}`) }, func(r *CompilationReceipt) { r.OutputFingerprints[r.Bundle.Outputs[0].Name] = "changed" }} {
		copy := receipt
		copy.OutputFingerprints = map[string]string{}
		for k, v := range receipt.OutputFingerprints {
			copy.OutputFingerprints[k] = v
		}
		mutate(&copy)
		if err = copy.Validate(); err == nil {
			t.Fatal("a mutation after previous successful validation reused stale authority")
		}
		changed, identityErr := ReceiptID(copy)
		if identityErr == nil && changed == id {
			t.Fatal("public ReceiptID reused previous validation")
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
	receipt.CompilationKey, _ = CompilationKey(receipt)
	receipt.ID, _ = ReceiptID(receipt)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := receipt.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
