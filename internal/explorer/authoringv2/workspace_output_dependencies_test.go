package authoringv2

import (
	"strings"
	"testing"
)

func workspaceOutputCombineDocument(id string, workspaceOutputID string) Document {
	document := terminalCombineDocument()
	document.Output.ID = id
	document.Output.Title = id
	document.Construction.Steps[0].Inputs[0] = ConstructionInputRef{Kind: ConstructionInputWorkspaceOutput, OutputID: workspaceOutputID}
	return document
}

func dependencyWorkspace(documents ...Document) Workspace {
	w := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Dependencies"}, Documents: documents}
	for index, document := range documents {
		w.Tabs = append(w.Tabs, Tab{ID: "tab_" + document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: index, Visible: true})
	}
	return w
}

func TestWorkspaceOutputDependenciesResolveForwardReferencesInStableOrder(t *testing.T) {
	w := dependencyWorkspace(workspaceOutputCombineDocument("combined", "grouped"), workspaceDocument("grouped"))
	if err := w.Validate(); err != nil {
		t.Fatalf("workspace with exact sibling output rejected: %v", err)
	}
	order, err := w.OutputDependencyOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("dependency order = %v, want [1 0]", order)
	}
}

func TestWorkspaceOutputDependenciesRejectMissingSelfAndCycles(t *testing.T) {
	tests := []struct {
		name string
		w    Workspace
		want string
	}{
		{name: "missing", w: dependencyWorkspace(workspaceOutputCombineDocument("combined", "unknown"), workspaceDocument("base")), want: "WORKSPACE_OUTPUT_NOT_FOUND"},
		{name: "self", w: dependencyWorkspace(workspaceOutputCombineDocument("combined", "combined"), workspaceDocument("base")), want: "WORKSPACE_OUTPUT_SELF_REFERENCE"},
		{name: "cycle", w: dependencyWorkspace(workspaceOutputCombineDocument("first", "second"), workspaceOutputCombineDocument("second", "first")), want: "WORKSPACE_OUTPUT_CYCLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.w.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %s", err, test.want)
			}
		})
	}
}
