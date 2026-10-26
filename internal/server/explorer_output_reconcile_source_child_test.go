package server

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestAuthoredConstructionOutputsResolveCompilerSourceChildren(t *testing.T) {
	child := lineage.CloneSourceChild(&lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"family_slot"},
		OccurrenceID: "base", SourcePath: "name[].family",
		Coordinates: []lineage.Coordinate{{BoundaryPath: "name[]", Index: 0}},
	})
	childID, _, err := lineage.StableSourceChildID(*child)
	if err != nil {
		t.Fatalf("encode source child identity: %v", err)
	}

	tests := []struct {
		name       string
		step       authoringv2.ConstructionStep
		outputIDs  []string
		inputNames []string
	}{
		{
			name: "Pivot",
			step: authoringv2.ConstructionStep{
				ID: "pivot_family",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
					ConstructionID: "pivot_family", GroupKeyIDs: []string{"family_slot"},
					CategoryColumnID: childID, ValueColumnID: childID,
					Categories: []authoringv2.ConstructionPivotCategory{{
						Key:            authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: sourceChildTestStringPointer("Smith")},
						OutputColumnID: "family_smith",
					}},
					DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
					MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "family_slot", Name: "family", Label: "Family"},
					{ID: "family_smith", Name: "family_smith", Label: "Family Smith"},
				},
			},
			outputIDs:  []string{"family_smith"},
			inputNames: []string{"family", "family_0"},
		},
		{
			name: "Unpivot",
			step: authoringv2.ConstructionStep{
				ID: "unpivot_family",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot_family",
					Inputs: []authoringv2.ConstructionUnpivotInput{{
						ColumnID: childID,
						Key:      authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: sourceChildTestStringPointer("name[].family [0]")},
					}},
					KeyOutputColumnID: "field_key", ValueOutputColumnID: "field_value",
					NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "family_slot", Name: "family", Label: "Family"},
					{ID: "field_key", Name: "field_key", Label: "Field"},
					{ID: "field_value", Name: "field_value", Label: "Value"},
				},
			},
			outputIDs:  []string{"field_key", "field_value"},
			inputNames: []string{"family_0"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := authoringv2.Document{
				Output:       authoringv2.Output{ID: "patients"},
				Columns:      []authoringv2.Column{{ColumnID: "family_slot", Column: "family"}},
				Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{test.step}},
			}
			compiled := lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{{
				ID:      recipe.ConstructionSourceProjectionID,
				Columns: []lower.CompiledOutputColumn{{ID: childID, Name: "family_0", Label: "Family [0]", SourceChild: child}},
			}}}
			authored := make(map[string]authoredOutputColumn)
			if err := authoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil); err != nil {
				t.Fatalf("reconcile construction output: %v", err)
			}
			for _, outputID := range test.outputIDs {
				var outputName string
				for _, output := range test.step.Outputs {
					if output.ID == outputID {
						outputName = output.Name
						break
					}
				}
				if outputName == "" {
					t.Fatalf("test output %q has no declared name", outputID)
				}
				metadata, ok := authored[outputName]
				if !ok || !reflect.DeepEqual(metadata.InputColumns, test.inputNames) {
					t.Fatalf("output %q lineage = %#v, want public input names %#v", outputName, metadata.InputColumns, test.inputNames)
				}
			}
		})
	}
}

func sourceChildTestStringPointer(value string) *string {
	return &value
}
