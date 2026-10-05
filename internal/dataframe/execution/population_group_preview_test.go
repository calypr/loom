package execution

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestPreviewOutputMaterializesPopulationSourceForKeylessGroupContributors(t *testing.T) {
	populationOutput := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "observation_id", ColumnID: "source_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "source_id", Name: "observation_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID:     "group_empty",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_empty", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:       []recipe.ConstructionGroupKey{},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "row_count_id", Name: "row_count", Type: "integer"}},
			}},
		},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "selection_fixture", MembershipDigest: "sha256:membership_fixture",
			MemberCount: 1, ResourceType: "Observation",
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: "loom_dev_verify_muunxcwk-da90bce", SelectionProject: "loom_dev_verify_muunxcwk-da90bce",
		DatasetGeneration: "devloop-v1", SelectionMembersCollection: "loom_explorer_selection_members",
	}
	for _, test := range []struct {
		name            string
		population      bool
		wantSourceArray bool
		wantRootLineage bool
	}{
		{name: "selected population", population: true, wantSourceArray: true, wantRootLineage: true},
		{name: "ordinary streaming group", wantRootLineage: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := populationOutput
			if !test.population {
				output.Population = nil
			}
			var executedQuery string
			var executedBindVars map[string]any
			engine, err := New(Config{
				Registry: invalidRecipeRegistry{},
				QueryRows: func(_ context.Context, query string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
					executedQuery = query
					executedBindVars = bindVars
					return visit(map[string]any{"row_count": int64(1)})
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := engine.CompileResolvedBundle(context.Background(), recipe.Bundle{
				RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "population_empty_group", TranslationVersion: "test",
				Outputs: []recipe.Output{output},
			}, bindings)
			if err != nil {
				t.Fatalf("compile resolved bundle: %v", err)
			}
			var rows []map[string]any
			summary, err := engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: output.Name, Limit: 5}, func(row map[string]any) error {
				rows = append(rows, row)
				return nil
			})
			if err != nil {
				t.Fatalf("preview empty-key COUNT_ROWS Group: %v", err)
			}
			if summary.RowCount != 1 || len(rows) != 1 || rows[0]["row_count"] != int64(1) {
				t.Fatalf("preview rows = %#v, summary row count = %d; want one row_count=1", rows, summary.RowCount)
			}
			if got := strings.Contains(executedQuery, "LET __loom_construction_source_projection = ("); got != test.wantSourceArray {
				t.Fatalf("source array present = %t, want %t:\n%s", got, test.wantSourceArray, executedQuery)
			}
			if test.population {
				for name, want := range map[string]any{
					"population_selection_id":        "selection_fixture",
					"population_project":             "loom_dev_verify_muunxcwk-da90bce",
					"dataset_generation":             "devloop-v1",
					"population_resource_type":       "Observation",
					"@population_members_collection": "loom_explorer_selection_members",
				} {
					if got := executedBindVars[name]; got != want {
						t.Fatalf("population query bind %q = %#v, want %#v", name, got, want)
					}
				}
				for _, filter := range []string{
					"FILTER population_member.selectionId == @population_selection_id",
					"FILTER population_member.project == @population_project",
					"FILTER population_member.generation == @dataset_generation",
					"FILTER population_member.resourceType == @population_resource_type",
				} {
					if !strings.Contains(executedQuery, filter) {
						t.Fatalf("selected source scan omitted %q:\n%s", filter, executedQuery)
					}
				}
				if !strings.Contains(executedQuery, "COLLECT __loom_physical_construction_construction_group_all_rows = null INTO __loom_construction_group_rows_1") {
					t.Fatalf("population Group did not collect all selected source rows:\n%s", executedQuery)
				}
			}
			if got := strings.Contains(executedQuery, "SORTED_UNIQUE(FLATTEN((FOR __loom_physical_construction_root_contributor_row"); got != test.wantRootLineage {
				t.Fatalf("root contributor collection present = %t, want %t:\n%s", got, test.wantRootLineage, executedQuery)
			}
		})
	}
}
