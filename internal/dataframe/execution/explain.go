package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// CompiledQueryFingerprint returns a deterministic, value-safe identifier for
// a compiled plan. Bind values are intentionally excluded; rendered query
// shape, bind-key names, and output metadata participate instead.
func CompiledQueryFingerprint(compiled CompiledQuery) string {
	keys := make([]string, 0, len(compiled.BindVars))
	for key := range compiled.BindVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(compiled.Query)
	builder.WriteByte('\x00')
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.Columns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.PublicColumns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, field := range compiled.PivotFields {
		builder.WriteString(field)
		builder.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}
