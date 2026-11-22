package receipt

import (
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestAuthoredSemanticGroupOutputCanBeRemovedByDownstreamUnpivot(t *testing.T) {
	semanticPath := "construction_semantic_group:binding_specimen_type:family_specimen_type"
	measureName := "specimen_type"
	category := &authoringv2.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: "binding_specimen_type", FamilyID: "family_specimen_type",
		ResourceType: "Specimen", CodingPath: "type.coding[]",
	}
	document := authoringv2.Document{
		Output:  authoringv2.Output{ID: "specimens"},
		Columns: []authoringv2.Column{{Column: "subject_reference", ColumnID: "source_subject_reference"}},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
			{
				ID: "semantic_group",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "semantic_group", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
					Keys:       []authoringv2.ConstructionGroupKey{{OutputColumnID: "specimen_type_group", SemanticCategory: category}},
					Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "specimen_type_group", Name: "specimen_type_group", Label: "Specimen type", Type: "string"},
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
				},
			},
			{
				ID: "unpivot_semantic_key", Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID:    "unpivot_semantic_key",
					Inputs:            []authoringv2.ConstructionUnpivotInput{{ColumnID: "specimen_type_group", Key: authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &measureName}}},
					KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{
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
