package runtime

import (
	"context"

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
