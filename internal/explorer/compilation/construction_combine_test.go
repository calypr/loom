package compilation

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestCompileMapsStandaloneCombineIntoRecipeWithoutSourceProjection(t *testing.T) {
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "training", Title: "Training table"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows: authoringv2.RowDefinition{
			Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{},
		},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "join_labs",
			Inputs: []authoringv2.ConstructionInputRef{
				{Kind: authoringv2.ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
				{Kind: authoringv2.ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
			},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
				Kind: authoringv2.ConstructionCombineKeyJoin,
				Keys: []authoringv2.ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
				Projections: []authoringv2.ConstructionCombineProjection{
					{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
					{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
				},
				JoinType: authoringv2.ConstructionCombineLeftJoin, RightMatchPolicy: authoringv2.ConstructionCombinePreserveAllMatches,
			}},
			Outputs: []authoringv2.StageColumn{
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
