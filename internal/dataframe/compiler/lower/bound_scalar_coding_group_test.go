package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestBoundScalarAndCodingKeysCompileFromZeroPublicSourceColumns(t *testing.T) {
	output := recipe.Output{
		Name: "bound_scalar_coding_group", RootResourceType: "Observation", RootOccurrenceID: "root-observation", RowGrain: "expanded",
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "root-observation", From: recipe.Expression{Select: "root.valueCodeableConcept.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	source := compileDerivedTestOutput(t, output)
	descriptor, err := DescribeConstructionSourceStage(source.OutputSchema, output.RootResourceType, &source.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe exact expanded source: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "group_status_coding", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_status_coding", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
			Keys: []recipe.ConstructionGroupKey{
				{OutputColumnID: "status_group_id", FieldSource: &recipe.ConstructionGroupFieldSource{
					Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: context.SourceStageID,
					SourceShapeID: context.SourceShapeID, SourceRole: "ROOT", OccurrenceID: "root-observation",
					OwnerResourceType: "Observation", ResourceType: "Observation", CandidateID: "observation-status", CanonicalPath: "status",
				}},
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
			{ID: "status_group_id", Name: "status", Type: "string", Nullable: true},
			{ID: "coding_label_id", Name: "coding_label", Type: "string"},
			{ID: "rows_id", Name: "rows", Type: "integer"},
		},
	}}}
	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[0]
	if stage.Group == nil || len(stage.Group.Keys) != 1 || !stage.Group.Keys[0].InputInternal || stage.Group.Keys[0].FieldSource == nil {
		t.Fatalf("scalar Group key did not retain its exact hidden field-source authority: %#v", stage.Group)
	}
	if stage.Group.Keys[0].Kind != "STRING" {
		t.Fatalf("typed scalar Group key kind = %q, want physical STRING", stage.Group.Keys[0].Kind)
	}
	if stage.Group.Keys[0].FieldSource.SourceStageID != stage.InputStageID ||
		stage.Group.Keys[0].FieldSource.SourceShapeID != context.SourceShapeID ||
		stage.Group.Keys[0].FieldSource.OwnerResourceType != "Observation" {
		t.Fatalf("scalar Group key binding differs from its exact input context: %#v", stage.Group.Keys[0].FieldSource)
	}
	var publicStatus *CompiledOutputColumn
	for index := range compiled.OutputSchema {
		if compiled.OutputSchema[index].Name == "status" {
			publicStatus = &compiled.OutputSchema[index]
			break
		}
	}
	if publicStatus == nil || publicStatus.Kind != "string" {
		t.Fatalf("compiled public Group key schema = %#v, want canonical logical string", publicStatus)
	}
	for _, column := range compiled.Stages[0].Columns {
		if !column.Internal {
			t.Fatalf("zero-public source acquired a public column: %#v", column)
		}
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatalf("render mixed field-source Group: %v", err)
	}
	if strings.Contains(rendered.Query, "LET __loom_construction_source_projection = (") ||
		strings.Count(rendered.Query, "FOR root IN @@root_collection") != 1 ||
		strings.Count(rendered.Query, "FOR __loom_physical_unnest_index_0") != 1 {
		t.Fatalf("field-source Group must stream the exact single root/UNNEST input:\n%s", rendered.Query)
	}
	badPlan := ir.ClonePhysicalPlan(compiled.Plan)
	badPlan.StageSequence.Stages[0].Group.Keys[0].FieldSource.SourceStageID = "later-stage"
	if err := badPlan.Validate(); err == nil || !strings.Contains(err.Error(), "exact source-stage authority") {
		t.Fatalf("hidden scalar Group input with a changed source stage must fail typed validation: %v", err)
	}
}
