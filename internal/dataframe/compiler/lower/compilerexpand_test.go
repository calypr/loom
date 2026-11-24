package lower_test

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestRelatedSourceProjectionLivenessForKeylessCount(t *testing.T) {
	for _, test := range []struct {
		name        string
		groupByFlag bool
		wantMarker  bool
	}{
		{name: "unused before related expansion", wantMarker: false},
		{name: "retained when grouped", groupByFlag: true, wantMarker: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := compileRelatedExpandCountQuery(t, relatedExpandCountOutput(test.groupByFlag))
			for _, marker := range []string{"unused_count_column_marker", "unused_list_column_marker", "unused_presence_column_marker"} {
				gotMarker := strings.Contains(query.Query, marker)
				if gotMarker != test.wantMarker {
					t.Errorf("related source projection %q included = %t, want %t:\n%s", marker, gotMarker, test.wantMarker, query.Query)
				}
			}
			if !strings.Contains(query.Query, "related_expand_records") || !strings.Contains(query.Query, "WITH COUNT INTO") {
				t.Fatalf("query must retain related expansion and row counting:\n%s", query.Query)
			}
		})
	}
}

func relatedExpandCountOutput(groupByPresence bool) recipe.Output {
	sourceField := recipe.Field{Name: "patient_id", ColumnID: "patient-id", Expr: recipe.Expression{Select: "root.id"}}
	sourceColumn := recipe.StageColumn{ID: sourceField.ColumnID, Name: sourceField.Name}
	countColumn := recipe.StageColumn{ID: "count-id", Name: "unused_count_column_marker", Type: "integer"}
	listColumn := recipe.StageColumn{ID: "list-id", Name: "unused_list_column_marker", Type: "string"}
	presenceColumn := recipe.StageColumn{ID: "presence-id", Name: "unused_presence_column_marker", Type: "boolean"}
	countColumns := []recipe.StageColumn{sourceColumn, countColumn}
	listColumns := append(append([]recipe.StageColumn(nil), countColumns...), listColumn)
	relatedSourceColumns := append(append([]recipe.StageColumn(nil), listColumns...), presenceColumn)
	expandedColumns := append(append([]recipe.StageColumn(nil), relatedSourceColumns...), recipe.StageColumn{
		ID: "observation-id", Name: "observation_id", Type: "string",
	})
	group := &recipe.ConstructionGroup{
		ConstructionID: "count_expanded_rows",
		Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row-count-id"}},
	}
	groupOutputs := []recipe.StageColumn{{ID: "row-count-id", Name: "row_count", Type: "integer"}}
	if groupByPresence {
		group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: presenceColumn.ID, OutputColumnID: "presence-group-id"}}
		groupOutputs = append([]recipe.StageColumn{{ID: "presence-group-id", Name: "presence_group", Type: "boolean"}}, groupOutputs...)
	}
	statusSource := recipe.ConstructionRelatedFieldSource{
		CandidateID: "observation-status", NodeID: "observation-node", ResourceType: "Observation",
		Path: "Observation.status", Cardinality: "optional_one", LogicalType: "string",
	}
	observationRoute := []recipe.ConstructionRelatedRouteStep{{
		EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	relatedSourceStep := func(id, inputStep, form string, output recipe.StageColumn, outputs []recipe.StageColumn) recipe.ConstructionStep {
		input := recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
		if inputStep != "" {
			input = recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: inputStep}
		}
		return recipe.ConstructionStep{
			ID: id, Inputs: []recipe.ConstructionInputRef{input},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedSourceOp, RelatedSource: &recipe.ConstructionRelatedSource{
				AnchorColumnID: "_key", ChoiceID: id + "-route", SourceOccurrenceID: "observation-node",
				Source: statusSource, Route: observationRoute, ContributorPolicy: "ALL_MATCHES",
				Form: form, OutputColumnID: output.ID,
			}},
			Outputs: outputs,
		}
	}
	return recipe.Output{
		Name: "related_expand_count", RootResourceType: "Patient", RowGrain: "patient",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields:           []recipe.Field{sourceField},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{sourceColumn},
			Steps: []recipe.ConstructionStep{
				relatedSourceStep("count", "", "COUNT", countColumn, countColumns),
				relatedSourceStep("list", "count", "ALL", listColumn, listColumns),
				relatedSourceStep("presence", "list", "PRESENCE", presenceColumn, relatedSourceColumns),
				{
					ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "presence"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
						AnchorColumnID: "_key", ChoiceID: "observation-route", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}},
						ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "observation-id",
					}},
					Outputs: expandedColumns,
				},
				{
					ID: "count_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: group}, Outputs: groupOutputs,
				},
			},
		},
	}
}

func compileRelatedExpandCountQuery(t *testing.T, output recipe.Output) model.CompiledQuery {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: "related-expand-count-project", DatasetGeneration: "related-expand-count-generation",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build related expansion count recipe: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("resolve related expansion count recipe: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related expansion count recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("render related expansion count recipe: %v", err)
	}
	return query
}
