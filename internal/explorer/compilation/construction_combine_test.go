package compilation

import (
	"context"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileMapsStandaloneCombineIntoRecipeWithoutSourceProjection(t *testing.T) {
	document := ws.Document{
		Kind:             ws.Kind,
		Output:           ws.Output{ID: "training", Title: "Training table"},
		RootResourceType: "Patient",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Rows: ws.RowDefinition{
			Kind: ws.RowDefinitionRecords, Records: &ws.RecordRows{},
		},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "join_labs",
			Inputs: []ws.ConstructionInputRef{
				{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
				{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
			},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
				Kind: ws.ConstructionCombineKeyJoin,
				Keys: []ws.ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
				Projections: []ws.ConstructionCombineProjection{
					{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
					{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
				},
				JoinType: ws.ConstructionCombineLeftJoin, RightMatchPolicy: ws.ConstructionCombinePreserveAllMatches,
			}},
			Outputs: []ws.StageColumn{
				{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
				{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
			},
		}}},
	}

	compiled, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatalf("compile terminal Combine: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Bundle.Outputs))
	}
	output := compiled.Bundle.Outputs[0]
	if len(output.Fields) != 0 {
		t.Fatalf("terminal Combine recipe declared source fields: %#v", output.Fields)
	}
	construction := output.Construction
	if construction == nil || len(construction.SourceColumns) != 0 || len(construction.Steps) != 1 {
		t.Fatalf("compiled construction = %#v, want one step without a source projection", construction)
	}
	if err := construction.Validate(output.Fields); err != nil {
		t.Fatalf("mapped construction failed recipe validation: %v", err)
	}
	step := construction.Steps[0]
	if step.Operation.Combine == nil || step.Operation.Combine.Kind != recipe.ConstructionCombineKeyJoin {
		t.Fatalf("compiled operation = %#v, want key join", step.Operation)
	}
	if len(step.Inputs) != 2 || step.Inputs[0].RevisionID != "patients-r4" || step.Inputs[1].RevisionID != "labs-r9" {
		t.Fatalf("compiled exact inputs = %#v", step.Inputs)
	}
	if !step.Outputs[1].Nullable {
		t.Fatalf("right-side left-join output lost nullability: %#v", step.Outputs[1])
	}
}
