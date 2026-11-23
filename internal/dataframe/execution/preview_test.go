package execution

import (
	"context"
	"fmt"
	"strings"
	"testing"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
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

func TestPreviewFilterPreservesDynamicRuntimeKeysAndStillRejectsDrift(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "dynamic-filter", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields:         []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
			DynamicColumns: []recipe.DynamicColumn{{Name: "identifier", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Columns: []string{"x"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "identifier_x", Name: "identifier_x"}},
				Steps: []recipe.ConstructionStep{{
					ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "id", Operator: recipe.FilterExists}},
					Outputs:   []recipe.StageColumn{{ID: "id", Name: "id"}, {ID: "identifier_x", Name: "identifier_x"}},
				}},
			},
		}},
	}
	row := func(runtimeKeys []any) map[string]any {
		return map[string]any{"id": "patient-1", "identifier_x": "x", "__loom_row_id": "patient-1", "__loom_dynamic_runtime_keys": map[string]any{"identifier": runtimeKeys}}
	}
	run := func(keys []any) error {
		engine, err := New(Config{
			Registry:    invalidRecipeRegistry{},
			ScopeDigest: func(recipe.RuntimeBindings) string { return "scope" },
			QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
				return visit(row(keys))
			},
		})
		if err != nil {
			return err
		}
		resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "test-project"})
		if err != nil {
			return err
		}
		found := false
		for _, column := range resolved.Compiled.Outputs[0].OutputSchema {
			if column.Name == "__loom_dynamic_runtime_keys" && column.Internal && !column.Identity {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("compiled filter output dropped internal dynamic-key validation metadata")
		}
		_, err = engine.PreviewOutput(context.Background(), resolved, PreviewRequest{Output: "Patient", Limit: 10}, func(value map[string]any) error {
			if _, exposed := value["__loom_dynamic_runtime_keys"]; exposed {
				return fmt.Errorf("validation metadata was exposed as public data")
			}
			return nil
		})
		return err
	}
	if err := run([]any{"x"}); err != nil {
		t.Fatalf("preview with a known runtime key failed: %v", err)
	}
	if err := run([]any{"x", "unexpected"}); err == nil {
		t.Fatal("preview with unknown runtime key succeeded")
	} else if userErr, ok := dataframeerrors.AsUserError(err); !ok || userErr.Code() != string(dataframeerrors.CodeDynamicSchemaDrift) {
		t.Fatalf("preview with unknown runtime key error = %v, want dynamic schema drift", err)
	}
}
