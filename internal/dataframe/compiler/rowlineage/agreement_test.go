package rowlineage_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

func TestGroupRowLineagePartitionsForwardRowsAgainstArango(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database := arangotest.Owned(t, "grouplineage")
	client := database.Client
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_group_lineage_" + uuid.NewString()
	const generation = "group-lineage-generation"
	insertReshapeAgreementFixture(t, ctx, client, project, generation)

	output := currentOracleSource("group_lineage_agreement")
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group-lineage-agreement",
			Keys: []recipe.ConstructionGroupKey{
				{InputColumnID: "group_text", OutputColumnID: "group_text_id"},
				{InputColumnID: "group_number", OutputColumnID: "group_number_id"},
			},
			Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "group_text_id", Name: "group_text"}, {ID: "group_number_id", Name: "group_number"}, {ID: "rows_id", Name: "rows"},
		},
	}}
	compiled, preview, err := compileCurrentConstructionOracle(output, project, generation, 100)
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(rows) != 4 {
		t.Fatalf("forward Group rows = %d, want four group keys: %#v", len(rows), rows)
	}
	seen := make(map[string]string, len(reshapeOracleSourceRows))
	for _, row := range rows {
		rowID := row["__loom_row_id"].(string)
		query, err := rowlineage.CompileRowLineageOutput(compiled, rowID, 0, 50, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile Group lineage for %s: %v", rowID, err)
		}
		result := executeRowLineageOracleQuery(t, ctx, client, query)
		contributors := lineageContributorRows(t, result)
		if result["found"] != true || len(contributors) == 0 {
			t.Fatalf("Group row %s has no complete lineage: %#v", rowID, result)
		}
		for _, contributor := range contributors {
			sourceID, ok := contributor["resourceId"].(string)
			if !ok {
				t.Fatalf("Group contributor has no source ID: %#v", contributor)
			}
			source := reshapeSourceByID(t, sourceID)
			if *source.Group.Text.String != row["group_text"] || float64(*source.Group.Numeric.Integer) != row["group_number"] {
				t.Fatalf("source %s violates Group key for row %#v", sourceID, row)
			}
			if prior, duplicate := seen[sourceID]; duplicate {
				t.Fatalf("source %s belongs to both Group rows %s and %s", sourceID, prior, rowID)
			}
			seen[sourceID] = rowID
		}
	}
	if len(seen) != len(reshapeOracleSourceRows) {
		t.Fatalf("Group lineage covered %d of %d forward sources", len(seen), len(reshapeOracleSourceRows))
	}
}

func reshapeSourceByID(t *testing.T, id string) reshapeOracleSourceRow {
	t.Helper()
	for _, row := range reshapeOracleSourceRows {
		if row.SourceID == id {
			return row
		}
	}
	t.Fatalf("unknown reshape fixture source %q", id)
	return reshapeOracleSourceRow{}
}
