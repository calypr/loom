package celltrace_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/celltrace"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func compileAndFindCellTrace(t *testing.T, ctx context.Context, client *store.Client, output lower.CompiledRecipeOutput, column, rowID string) map[string]any {
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

func traceContributions(t *testing.T, row map[string]any, column string) []map[string]any {
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

func TestFilteredSourceCellTraceMatchesForwardValueAgainstArango(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := arangotest.Owned(t, "filtertrace")
	client := database.Client
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_filter_trace_" + uuid.NewString()
	const generation = "filter-trace-generation"
	insertReshapeAgreementFixture(t, ctx, client, project, generation)

	output := currentOracleSource("filter_trace_agreement")
	status := "final"
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "group_text", Operator: recipe.FilterEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &status}},
		}},
		Outputs: append([]recipe.StageColumn(nil), output.Construction.SourceColumns...),
	}}
	compiled, preview, err := compileCurrentConstructionOracle(output, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	forwardRows := executeReshapeOracleQuery(t, ctx, client, preview)
	var forward map[string]any
	for _, row := range forwardRows {
		if row["source_id"] == "alpha-a" {
			forward = row
			break
		}
	}
	if forward == nil {
		t.Fatal("forward Filter omitted the matching source row")
	}
	identityParts := make([]any, 0, len(compiled.RowIdentity.Fields))
	for _, field := range compiled.RowIdentity.Fields {
		switch field {
		case "project":
			identityParts = append(identityParts, project)
		case "_key":
			identityParts = append(identityParts, project+"_alpha-a")
		default:
			identityParts = append(identityParts, forward[field])
		}
	}
	encodedIdentity, err := json.Marshal(identityParts)
	if err != nil {
		t.Fatal(err)
	}
	trace := compileAndFindCellTrace(t, ctx, client, compiled, "source_id", string(encodedIdentity))
	if trace[ir.PhysicalCellTraceValueField] != forward["source_id"] {
		t.Fatalf("filtered source trace = %#v, forward value = %#v", trace[ir.PhysicalCellTraceValueField], forward["source_id"])
	}
	contributors := traceContributions(t, trace, ir.PhysicalCellTraceContributionsField)
	if len(contributors) != 1 || contributors[0]["inputColumn"] != "source_id" ||
		contributors[0]["value"] != "alpha-a" || contributors[0]["operation"] != "SOURCE_PROJECTION" {
		t.Fatalf("filtered source trace contributors = %#v, want the source_id projection value", contributors)
	}
}
