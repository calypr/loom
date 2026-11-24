package recipetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	store "github.com/calypr/loom/internal/store/arango"
)

func CompileAndFindCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column, rowID string) map[string]any {
	t.Helper()
	trace, err := celltrace.CompileCellTraceOutputWithPolicy(output, column, 0, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile trace for %s: %v", column, err)
	}
	var match map[string]any
	var returned []map[string]any
	if err := client.QueryRows(ctx, trace.Query, 100, trace.BindVars, func(row map[string]any) error {
		var identity any
		returned = append(returned, row)
		if trace.ExplicitIdentityColumn != "" {
			identity = row[trace.ExplicitIdentityColumn]
		} else if trace.IdentityPartsColumn != "" {
			identity = row[trace.IdentityPartsColumn]
		}
		if trace.ExplicitIdentityColumn != "" {
			if identity == rowID {
				match = row
			}
			return nil
		}
		encoded, _ := json.Marshal(identity)
		if string(encoded) == rowID {
			match = row
		}
		return nil
	}); err != nil {
		t.Fatalf("execute %s trace query: %v\n%s", column, err, trace.Query)
	}
	if match == nil {
		t.Fatalf("trace for %s did not return forward row identity %s, identity columns=(%q,%q), rows=%#v", column, rowID, trace.ExplicitIdentityColumn, trace.IdentityPartsColumn, returned)
	}
	if match[trace.OmissionColumn] != nil && match[trace.OmissionColumn] != "" {
		t.Fatalf("trace for %s reports omission %v", column, match[trace.OmissionColumn])
	}
	return match
}

func TraceContributions(t *testing.T, row map[string]any, column string) []map[string]any {
	t.Helper()
	raw, ok := row[column].([]any)
	if !ok {
		t.Fatalf("trace %s = %#v, want array", column, row[column])
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("trace contributor = %#v, want object", item)
		}
		items = append(items, value)
	}
	return items
}
