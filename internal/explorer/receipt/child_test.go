package receipt

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/receipt/output"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/lineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
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
		step       ws.ConstructionStep
		outputIDs  []string
		inputNames []string
	}{
		{
			name: "Pivot",
			step: ws.ConstructionStep{
				ID: "pivot_family",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot_family", GroupKeyIDs: []string{"family_slot"},
					CategoryColumnID: childID, ValueColumnID: childID,
					Categories: []ws.ConstructionPivotCategory{{
						Key:            ws.TableScalar{Kind: ws.TableScalarString, String: sourceChildTestStringPointer("Smith")},
						OutputColumnID: "family_smith",
					}},
					DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
					MissingCellPolicy:      ws.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
				}},
				Outputs: []ws.StageColumn{
					{ID: "family_slot", Name: "family", Label: "Family"},
					{ID: "family_smith", Name: "family_smith", Label: "Family Smith"},
				},
			},
			outputIDs:  []string{"family_smith"},
			inputNames: []string{"family", "family_0"},
		},
		{
			name: "Unpivot",
			step: ws.ConstructionStep{
				ID: "unpivot_family",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot_family",
					Inputs: []ws.ConstructionUnpivotInput{{
						ColumnID: childID,
						Key:      ws.TableScalar{Kind: ws.TableScalarString, String: sourceChildTestStringPointer("name[].family [0]")},
					}},
					KeyOutputColumnID: "field_key", ValueOutputColumnID: "field_value",
					NullRowPolicy: ws.ConstructionUnpivotPreserve,
				}},
				Outputs: []ws.StageColumn{
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
			document := ws.Document{
				Output:       ws.Output{ID: "patients"},
				Columns:      []ws.Column{{ColumnID: "family_slot", Column: "family"}},
				Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{test.step}},
			}
			compiled := facade.CompiledRecipeOutput{Stages: []facade.CompiledStageDescriptor{{
				ID:      recipe.ConstructionSourceProjectionID,
				Columns: []facade.CompiledOutputColumn{{ID: childID, Name: "family_0", Label: "Family [0]", SourceChild: child}},
			}}}
			authored := make(map[string]output.AuthoredOutputColumn)
			if err := output.AuthoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil, capability.Snapshot{}); err != nil {
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
