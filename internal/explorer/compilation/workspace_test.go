package compilation

import (
	"context"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestCompileWorkspaceProducesOneArtifactWithFiveOutputs(t *testing.T) {
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("output_%d", i)
		workspace.Documents = append(workspace.Documents, authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: id, Title: id}, RootNodeID: "n_patient", RouteSteps: []authoringv2.RouteStep{}, Selections: []authoringv2.Selection{{CandidateID: "c_patient_id", OccurrenceID: "base", ProjectionMode: "VALUE"}}, Presentation: map[string]authoringv2.Presentation{}})
		workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: fmt.Sprintf("tab-%d", i), Title: id, OutputID: id, Order: i, Visible: true})
	}
	result, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, fixtureSnapshot())
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
		if contract.OutputID != emission.OutputID || len(contract.Columns) != 1 || contract.Columns[0].ProjectionMode != emission.ProjectionMode || contract.Columns[0].Label == "" {
			t.Fatalf("contract[%d]=%#v emission=%#v", i, contract, emission)
		}
	}
}
