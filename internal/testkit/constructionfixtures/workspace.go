package constructionfixtures

import (
	"encoding/json"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func BaselineExplorerWorkspace() []byte {
	visible, order := true, 0
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Patients"}, Documents: []ws.Document{{Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "patients", Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Columns: []ws.Column{{Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "FIRST"}}, Table: &ws.TablePresentation{Visible: &visible, Order: &order}}}}}, Tabs: []ws.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}}
	raw, _ := json.Marshal(workspace)
	return raw
}
