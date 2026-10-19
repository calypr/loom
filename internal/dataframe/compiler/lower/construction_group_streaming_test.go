package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestMixedScalarAndCodingIdentityGroupStreamsExistingUnnestRows(t *testing.T) {
	output := recipe.Output{
		Name: "mixed_scalar_coding_group", RootResourceType: "Observation", RootOccurrenceID: "root-observation", RowGrain: "expanded",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "root-observation", From: recipe.Expression{Select: "root.valueCodeableConcept.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	compiledSource := compileDerivedTestOutput(t, output)
	descriptor, err := DescribeConstructionSourceStage(compiledSource.OutputSchema, output.RootResourceType, &compiledSource.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe exact expanded source: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{
		Version:       1,
		SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
		Steps: []recipe.ConstructionStep{{
			ID: "group_status_coding", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_status_coding", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				Keys: []recipe.ConstructionGroupKey{
					{InputColumnID: "status_id", OutputColumnID: "status_group_id"},
					{OutputColumnID: "coding_label_id", FieldSource: &recipe.ConstructionGroupFieldSource{
						Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID,
						SourceShapeID: context.SourceShapeID, SourceRole: "CURRENT_ITEM",
						OccurrenceID: context.SourceExpansion.OwnerOccurrenceID, OwnerResourceType: context.SourceExpansion.OwnerResourceType,
						ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
					}},
				},
				Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
			}},
			Outputs: []recipe.StageColumn{
				{ID: "status_group_id", Name: "status", Type: "string"},
				{ID: "coding_label_id", Name: "coding_label", Type: "string"},
				{ID: "rows_id", Name: "rows", Type: "integer"},
			},
		}},
	}
	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatalf("render full mixed Group plan: %v", err)
	}
	if strings.Contains(rendered.Query, "LET __loom_construction_source_projection = (") {
		t.Fatalf("mixed Group materialized all source rows instead of streaming into COLLECT:\n%s", rendered.Query)
	}
	if strings.Count(rendered.Query, "FOR root IN @@root_collection") != 1 || strings.Count(rendered.Query, "FOR __loom_physical_unnest_index_0") != 1 {
		t.Fatalf("mixed Group should reuse one scoped root and its one existing UNNEST:\n%s", rendered.Query)
	}
	for _, want := range []string{
		"LET __loom_construction_group_coding_input_tuple_1 = [",
		"COLLECT __loom_construction_group_key_1 = ",
		", __loom_construction_group_coding_tuple_1 = __loom_construction_group_coding_input_tuple_1",
		"SUM(1)",
		"coding_ordinal",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Errorf("mixed Group AQL missing %q:\n%s", want, rendered.Query)
		}
	}
}
