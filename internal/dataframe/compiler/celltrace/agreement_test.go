package celltrace_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/testkit/recipetest"

	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

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
	trace := recipetest.CompileAndFindCellTrace(t, ctx, client, compiled, "source_id", string(encodedIdentity))
	if trace[ir.PhysicalCellTraceValueField] != forward["source_id"] {
		t.Fatalf("filtered source trace = %#v, forward value = %#v", trace[ir.PhysicalCellTraceValueField], forward["source_id"])
	}
	contributors := recipetest.TraceContributions(t, trace, ir.PhysicalCellTraceContributionsField)
	if len(contributors) != 1 || contributors[0]["inputColumn"] != "source_id" ||
		contributors[0]["value"] != "alpha-a" || contributors[0]["operation"] != "SOURCE_PROJECTION" {
		t.Fatalf("filtered source trace contributors = %#v, want the source_id projection value", contributors)
	}
}
