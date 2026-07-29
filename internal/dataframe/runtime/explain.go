package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

// ExplainCompiledQuery returns Arango's optimizer plan for a compiled query
// without executing the dataframe. Callers can use ExtractPlanIndexes and the
// result's estimated costs to evaluate optimizer passes.
func ExplainCompiledQuery(ctx context.Context, opts arangostore.ConnectionOptions, compiled CompiledQuery) (arangostore.ExplainResult, error) {
	client, err := arangostore.Open(ctx, opts.URL, opts.Database)
	if err != nil {
		return arangostore.ExplainResult{}, err
	}
	defer client.Close(ctx)
	return client.Explain(ctx, arangostore.ExplainRequest{
		Query:    compiled.Query,
		BindVars: compiled.BindVars,
	})
}

// ExplainCompiledQueryAssessment is the shared live Explain entrypoint for
// every compiler frontend. It deliberately delegates parsing and assessment to
// the Arango store package so recipe queries cannot grow a second Explain
// parser or cost model.
func ExplainCompiledQueryAssessment(ctx context.Context, opts arangostore.ConnectionOptions, compiled CompiledQuery) (arangostore.ExplainAssessment, error) {
	result, err := ExplainCompiledQuery(ctx, opts, compiled)
	if err != nil {
		return arangostore.ExplainAssessment{}, err
	}
	return arangostore.AssessExplainResult(result), nil
}

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
