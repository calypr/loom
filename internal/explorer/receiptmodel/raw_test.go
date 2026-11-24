package receiptmodel

import (
	"strings"
	"testing"

	explorerconfig "github.com/calypr/loom/internal/explorer/config"
)

func TestReceiptRawValidationMatchesCanonicalDecoder(t *testing.T) {
	values := [][]byte{nil, []byte(""), []byte(" "), []byte("null"), []byte(" null \n"), []byte("true"), []byte("1e4000"), []byte("9007199254740993"), []byte("-0.0"), []byte(`{"b":2,"a":1}`), []byte(`{"a":1,"a":2}`), []byte(`[1,null,{"x":"\uD800"}]`), []byte(`{"x":"<>&"}`), []byte("[1,]"), []byte("null null"), []byte("01"), []byte("NaN"), []byte("{broken"), []byte{'"', 0xff, '"'}, []byte(strings.Repeat("[", 101) + "0" + strings.Repeat("]", 101))}
	for _, raw := range values {
		_, want := explorerconfig.CanonicalJSONBytes(raw)
		got := validateReceiptRawJSON(raw)
		if (want == nil) != (got == nil) {
			t.Fatalf("raw %q acceptance changed: old=%v new=%v", raw, want, got)
		}
		if want != nil && got.Error() != want.Error() {
			t.Fatalf("raw %q error changed: old=%v new=%v", raw, want, got)
		}
	}
}
