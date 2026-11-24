package arango

import (
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func auditFixture(t *testing.T) ws.Workspace {
	t.Helper()
	return ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Audit"}, Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RowDefinition{Kind: ws.RowDefinitionRecords, Records: &ws.RecordRows{}}, Columns: []ws.Column{}, Construction: &ws.Construction{Version: 1}}}, Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0}}}
}

func containsAuditString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
