package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
)

// CompiledQueryFingerprint returns a deterministic, value-safe identifier for
// a compiled plan. Bind values are intentionally excluded; rendered query
// shape, bind-key names, and output metadata participate instead. The query
// text and bind-key names are derived through closed dispatch: only an
// AQL-tagged command contributes query shape and named bind keys, while a
// PostgreSQL-tagged or untagged command contributes the same empty shape the
// pre-boundary fingerprint computed for those plans, so the hash input is
// byte-identical.
func CompiledQueryFingerprint(compiled CompiledQuery) string {
	var queryText string
	var bindVars map[string]any
	if compiled.Command.Backend() == compiler.CompiledBackendAQL {
		text, binds, dispatchErr := compiled.Command.AQLParameters()
		if dispatchErr != nil {
			return ""
		}
		queryText, bindVars = text, binds
	}
	keys := make([]string, 0, len(bindVars))
	for key := range bindVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(queryText)
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
