package compilation

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func addressableDocument() authoringv2.Document {
	return authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{{Column: "subject_id", Label: "Renamed label", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
	}
}

func TestResolveAddressableSourceRowUsesValidatedPhysicalProvenance(t *testing.T) {
	document := addressableDocument()
	contract := explorer.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}
	emitted := []explorer.EmittedColumn{{OutputID: "out", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "id", PublicColumn: "subject_id"}}
	descriptor, err := resolveAddressableDocumentSourceRow(document, contract, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ResourceType != "Observation" || descriptor.PhysicalColumn != "subject_id" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Explorer"}, Documents: []authoringv2.Document{document}, Tabs: []authoringv2.Tab{{ID: "tab", Title: "Tab", OutputID: "out", Order: 0}}}
	if got, err := ResolveAddressableSourceRow(workspace, contract, emitted); err != nil || got != descriptor {
		t.Fatalf("workspace source row = %#v, err=%v", got, err)
	}

	bad := []explorer.EmittedColumn{{OutputID: "out", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "label", PublicColumn: "subject_id"}}
	if _, err := resolveAddressableDocumentSourceRow(document, explorer.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}, bad); err == nil || !strings.Contains(err.Error(), "do not preserve root id") {
		t.Fatalf("bad provenance error = %v", err)
	}
	rowID := []explorer.EmittedColumn{{OutputID: "out", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "id", PublicColumn: "__loom_row_id"}}
	if _, err := resolveAddressableDocumentSourceRow(document, explorer.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}, rowID); err == nil {
		t.Fatal("internal row id was accepted as source identity")
	}
}
