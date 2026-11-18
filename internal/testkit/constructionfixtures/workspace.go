package constructionfixtures

import (
	"encoding/json"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func BaselineExplorerWorkspace() []byte {
	visible, order := true, 0
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"}, Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "patients", Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Columns: []authoringv2.Column{{Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "FIRST"}}, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order}}}}}, Tabs: []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}}
	raw, _ := json.Marshal(workspace)
	return raw
}
