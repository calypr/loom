package compilation

import (
	"context"
	"fmt"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestCompileWorkspaceProducesOneArtifactWithFiveOutputs(t *testing.T) {
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Five outputs"}, Documents: []ws.Document{}, Tabs: []ws.Tab{}, SemanticsVersion: ws.CurrentSemanticsVersion}
	visible := true
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("output_%d", i)
		workspace.Documents = append(workspace.Documents, ws.Document{Rows: ws.RecordsRowDefinition(), Kind: ws.Kind, Output: ws.Output{ID: id, Title: id}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &ws.TablePresentation{Visible: &visible}}}})
		workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: fmt.Sprintf("tab-%d", i), Title: id, OutputID: id, Order: i, Visible: true})
	}
	result, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, fixtureSnapshot(), ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bundle.Outputs) != 5 || len(result.EmittedColumns) != 5 || len(result.OutputContracts) != 5 {
		t.Fatalf("outputs=%d emissions=%d contracts=%d", len(result.Bundle.Outputs), len(result.EmittedColumns), len(result.OutputContracts))
	}
	for i, emission := range result.EmittedColumns {
		if emission.OutputID != fmt.Sprintf("output_%d", i) || emission.ProjectionMode != "VALUE" {
			t.Fatalf("emission[%d]=%#v", i, emission)
		}
		contract := result.OutputContracts[i]
		if contract.OutputID != emission.OutputID || len(contract.Columns) != 1 || contract.Columns[0].Column != emission.PublicColumn || contract.Columns[0].Label == "" {
			t.Fatalf("contract[%d]=%#v emission=%#v", i, contract, emission)
		}
	}
}

func TestCompileWorkspacePreservesForwardWorkspaceOutputReference(t *testing.T) {
	visible := true
	combined := ws.Document{
		Kind: ws.Kind, Rows: ws.RecordsRowDefinition(),
		Output: ws.Output{ID: "combined", Title: "Combined"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "append", Inputs: []ws.ConstructionInputRef{
				{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "base"},
				{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:archive", RevisionID: "archive-r1", OutputID: "archive"},
			},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
				Kind: ws.ConstructionCombineAppend,
				Projections: []ws.ConstructionCombineProjection{
					{OutputColumnID: "person", InputIndex: 0, InputColumnID: "person_id"},
					{OutputColumnID: "person", InputIndex: 1, InputColumnID: "archive_person_id"},
				},
			}},
			Outputs: []ws.StageColumn{{ID: "person", Name: "person_id", Label: "Person", Type: "string"}},
		}}},
	}
	base := ws.Document{
		Kind: ws.Kind, Rows: ws.RecordsRowDefinition(),
		Output: ws.Output{ID: "base", Title: "Base"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
			Table:  &ws.TablePresentation{Visible: &visible}}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind,
		Explorer: ws.ExplorerMetadata{Title: "Sibling output"}, Documents: []ws.Document{combined, base},
		Tabs: []ws.Tab{{ID: "combined", Title: "Combined", OutputID: "combined", Order: 0, Visible: true}, {ID: "base", Title: "Base", OutputID: "base", Order: 1, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	result, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, fixtureSnapshot(), ResolvedInputs{})
	if err != nil {
		t.Fatalf("CompileWorkspace: %v", err)
	}
	if len(result.Bundle.Outputs) != 2 || result.Bundle.Outputs[0].Name != "combined" || result.Bundle.Outputs[1].Name != "base" {
		t.Fatalf("recipe output order changed: %#v", result.Bundle.Outputs)
	}
	inputs := result.Bundle.Outputs[0].Construction.Steps[0].Inputs
	if inputs[0].Kind != "WORKSPACE_OUTPUT" || inputs[0].OutputID != "base" || inputs[1].Kind != "TABLE_REVISION" {
		t.Fatalf("compiled recipe input refs = %#v", inputs)
	}
}

func TestCompileWorkspaceCanonicalizesEquivalentProjectIdentities(t *testing.T) {
	visible := true
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion,
		Kind:       ws.WorkspaceKind,
		Explorer:   ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{
			Rows:             ws.RecordsRowDefinition(),
			Kind:             ws.Kind,
			Output:           ws.Output{ID: "patient_output", Title: "Patients"},
			RootResourceType: "Patient",
			Route:            ws.RouteNode{OccurrenceID: "base", ResourceType: "Patient"},
			Columns: []ws.Column{{
				Column: "project_id", Label: "Project", LogicalType: "string", OccurrenceID: "base",
				Source: ws.ColumnSource{Kind: ws.SourceProjectID},
				Table:  &ws.TablePresentation{Visible: &visible},
			}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patient_output", Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	snapshot := fixtureSnapshotForProject("HTAN_INT/BForePC")

	canonical, err := CompileWorkspace(context.Background(), "HTAN_INT/BForePC", "default", workspace, snapshot, ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"HTAN_INT%2FBForePC", "HTAN_INT-BForePC"} {
		t.Run(project, func(t *testing.T) {
			result, compileErr := CompileWorkspace(context.Background(), project, "default", workspace, snapshot, ResolvedInputs{})
			if compileErr != nil {
				t.Fatalf("CompileWorkspace(%q): %v", project, compileErr)
			}
			if result.Bundle.Name != canonical.Bundle.Name || result.RecipeDigest != canonical.RecipeDigest {
				t.Fatalf("equivalent project changed bundle identity: name=%q digest=%q, want name=%q digest=%q", result.Bundle.Name, result.RecipeDigest, canonical.Bundle.Name, canonical.RecipeDigest)
			}
			if got := string(result.Bundle.Outputs[0].Fields[0].Expr.Literal); got != `"HTAN_INT/BForePC"` {
				t.Fatalf("project binding = %s, want canonical project identity", got)
			}
		})
	}
}

func TestCompileWorkspaceAllowsEmptyConstructionTargetBesideSiblingSources(t *testing.T) {
	visible := true
	emptyTarget := ws.Document{
		Kind: ws.Kind, Rows: ws.RecordsRowDefinition(),
		Output:           ws.Output{ID: "empty_target", Title: "Empty target"},
		RootResourceType: "Patient",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
		Construction:     &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind,
		Explorer:  ws.ExplorerMetadata{Title: "Sparse target with sibling inputs"},
		Documents: []ws.Document{emptyTarget},
		Tabs:      []ws.Tab{{ID: "tab_empty_target", Title: "Empty target", OutputID: "empty_target", Order: 0, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	for index := 1; index <= 3; index++ {
		outputID := fmt.Sprintf("source_%d", index)
		columnID := fmt.Sprintf("stable-source-column-%d", index)
		workspace.Documents = append(workspace.Documents, ws.Document{
			Kind: ws.Kind, Rows: ws.RecordsRowDefinition(),
			Output: ws.Output{ID: outputID, Title: outputID}, RootResourceType: "Patient",
			Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []ws.Column{{
				ColumnID: columnID, Column: fmt.Sprintf("patient_id_%d", index), Label: fmt.Sprintf("Patient ID %d", index),
				OccurrenceID: ws.RootOccurrenceID,
				Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Table:        &ws.TablePresentation{Visible: &visible},
			}},
		})
		workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "tab_" + outputID, Title: outputID, OutputID: outputID, Order: index, Visible: true})
	}

	compiled, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, fixtureSnapshot(), ResolvedInputs{})
	if err != nil {
		t.Fatalf("CompileWorkspace with an empty rooted target and three source siblings: %v", err)
	}
	if len(compiled.Bundle.Outputs) != 4 || len(compiled.OutputContracts) != 4 {
		t.Fatalf("compiled output/contracts count = %d/%d, want target plus all three source siblings", len(compiled.Bundle.Outputs), len(compiled.OutputContracts))
	}
	if got := compiled.Bundle.Outputs[0]; got.Name != "empty_target" || len(got.Construction.Steps) != 0 || len(got.Fields) != 0 {
		t.Fatalf("empty target gained authored source fields or construction steps: %#v", got)
	}
	for index := 1; index <= 3; index++ {
		output := compiled.Bundle.Outputs[index]
		contract := compiled.OutputContracts[index]
		wantOutput := fmt.Sprintf("source_%d", index)
		wantColumn := fmt.Sprintf("patient_id_%d", index)
		if output.Name != wantOutput || len(contract.Columns) != 1 || contract.Columns[0].Column != wantColumn {
			t.Fatalf("sibling %d output/contract = %#v / %#v", index, output, contract)
		}
		if len(compiled.EmittedColumns) < index || compiled.EmittedColumns[index-1].OutputID != wantOutput || compiled.EmittedColumns[index-1].PublicColumn != wantColumn {
			t.Fatalf("sibling %d emitted schema was not preserved: %#v", index, compiled.EmittedColumns)
		}
	}
}

func TestCompileWorkspaceRejectsNegativePresentationOrderBeforeNormalization(t *testing.T) {
	visible, negativeOrder := true, -1
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind,
		SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Negative order"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Rows: ws.RecordsRowDefinition(),
			Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []ws.Column{{
				Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Table:  &ws.TablePresentation{Visible: &visible, Order: &negativeOrder},
			}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	_, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, fixtureSnapshot(), ResolvedInputs{})
	compileErr, ok := err.(*Error)
	if !ok || compileErr.Code != "INVALID_AUTHORING_INTENT" {
		t.Fatalf("CompileWorkspace error = %v, want INVALID_AUTHORING_INTENT", err)
	}
}
