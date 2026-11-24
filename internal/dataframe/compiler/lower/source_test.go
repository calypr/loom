package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestBoundFirstCodingGroupCompilesWithoutPublicSourceColumns(t *testing.T) {
	output := recipe.Output{
		Name: "empty_source_coding_group", RootResourceType: "Observation", RootOccurrenceID: "root-observation", RowGrain: "expanded",
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "root-observation", From: recipe.Expression{Select: "root.valueCodeableConcept.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	source := compileDerivedTestOutput(t, output)
	descriptor, err := DescribeConstructionSourceStage(source.OutputSchema, output.RootResourceType, &source.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe compiler-bound Coding source: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "group_codings", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "coding-group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
			Keys: []recipe.ConstructionGroupKey{{
				OutputColumnID: "coding_label_id",
				FieldSource: &recipe.ConstructionGroupFieldSource{
					Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID,
					SourceShapeID: context.SourceShapeID, SourceRole: "CURRENT_ITEM",
					OccurrenceID: context.SourceExpansion.OwnerOccurrenceID, OwnerResourceType: context.SourceExpansion.OwnerResourceType,
					ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
				},
			}},
			Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		Outputs: []recipe.StageColumn{{ID: "coding_label_id", Name: "coding_label", Type: "string"}, {ID: "rows_id", Name: "rows", Type: "integer"}},
	}}}
	compiled := compileDerivedTestOutput(t, output)
	publicSourceColumns := 0
	for _, column := range compiled.Stages[0].Columns {
		if !column.Internal {
			publicSourceColumns++
		}
	}
	if publicSourceColumns != 0 {
		t.Fatalf("bound empty source unexpectedly gained %d public projection columns: %#v", publicSourceColumns, compiled.Stages[0].Columns)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatalf("render bound empty-source Group: %v", err)
	}
	if strings.Contains(rendered.Query, "LET __loom_construction_source_projection = (") ||
		!strings.Contains(rendered.Query, "COLLECT __loom_construction_group_coding_tuple_1 = __loom_construction_group_coding_input_tuple_1") {
		t.Fatalf("bound empty-source Group did not stream its typed source into tuple COLLECT:\n%s", rendered.Query)
	}
}
