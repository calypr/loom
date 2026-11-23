package receipt

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestAuthoredSemanticGroupOutputCanBeRemovedByDownstreamUnpivot(t *testing.T) {
	semanticPath := "construction_semantic_group:binding_specimen_type:family_specimen_type"
	measureName := "specimen_type"
	category := &ws.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: "binding_specimen_type", FamilyID: "family_specimen_type",
		ResourceType: "Specimen", CodingPath: "type.coding[]",
	}
	document := ws.Document{
		Output:  ws.Output{ID: "specimens"},
		Columns: []ws.Column{{Column: "subject_reference", ColumnID: "source_subject_reference"}},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
			{
				ID: "semantic_group",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
					ConstructionID: "semantic_group", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
					Keys:       []ws.ConstructionGroupKey{{OutputColumnID: "specimen_type_group", SemanticCategory: category}},
					Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
				}},
				Outputs: []ws.StageColumn{
					{ID: "specimen_type_group", Name: "specimen_type_group", Label: "Specimen type", Type: "string"},
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
				},
			},
			{
				ID: "unpivot_semantic_key", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID:    "unpivot_semantic_key",
					Inputs:            []ws.ConstructionUnpivotInput{{ColumnID: "specimen_type_group", Key: ws.TableScalar{Kind: ws.TableScalarString, String: &measureName}}},
					KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ws.ConstructionUnpivotPreserve,
				}},
				Outputs: []ws.StageColumn{
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
					{ID: "measure_key", Name: "measure", Label: "Measure", Type: "string"},
					{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "string"},
				},
			},
		}},
	}
	compiled := facade.CompiledRecipeOutput{
		OutputSchema: []facade.CompiledOutputColumn{
			{ID: "row_count", Name: "row_count", Kind: "integer", Cardinality: "required_one"},
			{ID: "measure_key", Name: "measure", Kind: "string", Cardinality: "required_one"},
			{ID: "measure_value", Name: "measure_value", Kind: "string", Cardinality: "required_one"},
		},
		Stages: []facade.CompiledStageDescriptor{
			{ID: "semantic_group", Operation: "GROUP", Columns: []facade.CompiledOutputColumn{{ID: "specimen_type_group", Name: "specimen_type_group", SemanticPath: semanticPath}}},
			{ID: "unpivot_semantic_key", Operation: "UNPIVOT"},
		},
	}
	if err := AuthoredConstructionOutputsWithCombineInputs(document, map[string]AuthoredOutputColumn{}, compiled, nil, capability.Snapshot{}); err != nil {
		t.Fatalf("reconcile semantic Group key removed by downstream UNPIVOT: %v", err)
	}
}
