package rowlineage_test

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	store "github.com/calypr/loom/internal/store/arango"
)

func executeRowLineageOracleQuery(t *testing.T, ctx context.Context, client *store.Client, query rowlineage.CompiledRowLineageQuery) map[string]any {
	t.Helper()
	rows := make([]map[string]any, 0, 1)
	if err := client.QueryRows(ctx, query.Query, 32, query.BindVars, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("execute compiler-generated row lineage query: %v\n%s", err, query.Query)
	}
	if len(rows) != 1 {
		t.Fatalf("row lineage returned %d rows, want one result: %#v", len(rows), rows)
	}
	return rows[0]
}
