package arango

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func auditFixture(t *testing.T) authoringv2.Workspace {
	t.Helper()
	return authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: "Audit"}, Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}}, Columns: []authoringv2.Column{}, Construction: &authoringv2.Construction{Version: 1}}}, Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0}}}
}

func containsAuditString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
