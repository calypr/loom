package arango

import (
	"strings"
	"testing"
)

func TestDecodeSemanticRowPreservesDomainErrorContext(t *testing.T) {
	for _, domain := range []string{"semantic inventory", "semantic coverage"} {
		t.Run(domain+" encode", func(t *testing.T) {
			err := decodeSemanticRow(map[string]any{"value": func() {}}, &struct{}{}, domain)
			if err == nil || !strings.HasPrefix(err.Error(), "encode "+domain+" row:") {
				t.Fatalf("decode row error = %v", err)
			}
		})
		t.Run(domain+" decode", func(t *testing.T) {
			target := struct {
				Count int `json:"count"`
			}{}
			err := decodeSemanticRow(map[string]any{"count": "not an integer"}, &target, domain)
			if err == nil || !strings.HasPrefix(err.Error(), "decode "+domain+" row:") {
				t.Fatalf("decode row error = %v", err)
			}
		})
	}
}
