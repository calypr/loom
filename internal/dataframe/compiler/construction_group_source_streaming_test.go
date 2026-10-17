package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRelatedUnnestCountRowsStreamsSourceProjection(t *testing.T) {
	output := recipe.Output{
		Name:                  "related_unnest_count_rows",
		RootResourceType:      "Patient",
		RootOccurrenceID:      "patient-root",
		RowGrain:              "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner",
			From:              recipe.Expression{Select: "observation.identifier[]"},
			As:                "identifier",
			EmptyPolicy:       recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "observation-owner", Alias: "observation",
			ToResourceType: "Observation",
			Fields: []recipe.Field{{
				Name: "subject_reference", ColumnID: "subject-reference", Label: "Subject reference",
				Expr: recipe.Expression{Select: "observation.subject.reference"},
			}},
		}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "subject-reference", Name: "subject_reference", Label: "Subject reference", Type: "string", Nullable: true}},
			Steps: []recipe.ConstructionStep{{
				ID:     "group_by_subject_reference",
				Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID:   "group_by_subject_reference",
					MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:             []recipe.ConstructionGroupKey{{InputColumnID: "subject-reference", OutputColumnID: "subject-reference"}},
					Aggregates:       []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "subject-reference", Name: "subject_reference", Label: "Subject reference", Type: "string", Nullable: true},
					{ID: "row-count", Name: "row_count", Label: "Rows", Type: "integer"},
				},
			}},
		},
	}
	bindings := recipe.RuntimeBindings{Project: "group-stream-project", DatasetGeneration: "group-stream-generation"}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if len(compiledOutput.Plan.StageSequence.Stages) != 1 || compiledOutput.Plan.StageSequence.Stages[0].Group == nil {
		t.Fatalf("compiled stages = %#v, want one ordinary Group", compiledOutput.Plan.StageSequence.Stages)
	}
	var unnest *ir.PhysicalUnnest
	for index := range compiledOutput.Plan.Operations {
		if compiledOutput.Plan.Operations[index].Kind == ir.PhysicalUnnestOp {
			unnest = compiledOutput.Plan.Operations[index].Unnest
		}
	}
	if unnest == nil || len(unnest.Owner.Route) != 1 {
		t.Fatalf("source unnest = %#v, want one relationship owner route", unnest)
	}

	query, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileRecipeOutputWithPolicy() error = %v", err)
	}
	for _, want := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths",
		"FILTER __loom_expansion_edge_1.project == @project",
		"FILTER __loom_expansion_edge_1.dataset_generation == @dataset_generation",
		"__loom_expansion_edge_1.auth_resource_path IN @auth_resource_paths",
		"FILTER __loom_expansion_node_1.project == @project",
		"FILTER __loom_expansion_node_1.dataset_generation == @dataset_generation",
		"__loom_expansion_node_1.auth_resource_path IN @auth_resource_paths",
		"FOR __loom_physical_unnest_index_0 IN",
		"LET __loom_construction_input_1 = {",
		"__loom_expansion_node_1.payload.subject.reference",
		"COLLECT __loom_construction_group_key_1 = __loom_construction_input_1[@",
		"WITH COUNT INTO",
		"FILTER ASSERT(__loom_construction_group_key_1 == null OR TYPENAME(__loom_construction_group_key_1) == @",
		"FILTER expansion_route_1_scope_allowed == @scope_allowed",
	} {
		if !strings.Contains(query.Query, want) {
			t.Errorf("streamed related COUNT_ROWS query missing %q:\n%s", want, query.Query)
		}
	}
	if got := query.BindVars["__loom_physical_construction_construction_group_key_type_0"]; got != "string" {
		t.Fatalf("group key type bind = %#v, want string", got)
	}
	if strings.Contains(query.Query, "LET __loom_construction_source_projection = (") {
		t.Fatalf("related COUNT_ROWS query retained the projected source array:\n%s", query.Query)
	}
	if strings.Contains(query.Query, "FOR __loom_construction_input_1 IN __loom_construction_source_projection") {
		t.Fatalf("GROUP re-read materialized source rows:\n%s", query.Query)
	}
	if strings.Contains(query.Query, "FILTER __loom_construction_input_1[@__loom_physical_construction_construction_group_key_column_0] != null") {
		t.Fatalf("GROUP excluded missing keys instead of grouping null values:\n%s", query.Query)
	}
	projectionAt := strings.Index(query.Query, "LET __loom_construction_input_1 = {")
	collectAt := strings.Index(query.Query, "COLLECT __loom_construction_group_key_1 = __loom_construction_input_1[@")
	if projectionAt < 0 || collectAt <= projectionAt || !strings.Contains(query.Query[projectionAt:collectAt], "__loom_expansion_node_1.payload.subject.reference") {
		t.Fatalf("projected source row does not feed GROUP directly:\n%s", query.Query)
	}
	projectionLineEnd := strings.Index(query.Query[projectionAt:], "\n") + projectionAt
	if strings.TrimSpace(query.Query[projectionLineEnd:collectAt]) != "" {
		t.Fatalf("GROUP is not the next operation after its scalar source projection LET:\n%s", query.Query)
	}
	unnestAt := strings.Index(query.Query, "FOR __loom_physical_unnest_index_0 IN")
	traversalAt := strings.Index(query.Query, "FOR __loom_expansion_edge_1 IN")
	if traversalAt < 0 || unnestAt <= traversalAt || collectAt <= unnestAt {
		t.Fatalf("relationship and UNNEST loops must remain before COUNT_ROWS COLLECT:\n%s", query.Query)
	}
}
