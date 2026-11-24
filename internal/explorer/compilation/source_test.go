package compilation

import (
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

func addressableDocument() ws.Document {
	return ws.Document{Rows: ws.RecordsRowDefinition(),
		Kind: ws.Kind, Output: ws.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route:   ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []ws.Column{{Column: "subject_id", Label: "Renamed label", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
	}
}

func TestResolveAddressableSourceRowUsesValidatedPhysicalProvenance(t *testing.T) {
	document := addressableDocument()
	contract := receiptmodel.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}
	emitted := []viewer.EmittedColumn{{OutputID: "out", OccurrenceID: ws.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "id", PublicColumn: "subject_id"}}
	descriptor, err := resolveAddressableDocumentSourceRow(document, contract, emitted)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.ResourceType != "Observation" || descriptor.PhysicalColumn != "subject_id" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Explorer"}, Documents: []ws.Document{document}, Tabs: []ws.Tab{{ID: "tab", Title: "Tab", OutputID: "out", Order: 0}}, SemanticsVersion: ws.CurrentSemanticsVersion}
	if got, err := ResolveAddressableSourceRow(workspace, contract, emitted); err != nil || got != descriptor {
		t.Fatalf("workspace source row = %#v, err=%v", got, err)
	}

	bad := []viewer.EmittedColumn{{OutputID: "out", OccurrenceID: ws.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "label", PublicColumn: "subject_id"}}
	if _, err := resolveAddressableDocumentSourceRow(document, receiptmodel.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}, bad); err == nil || !strings.Contains(err.Error(), "do not preserve root id") {
		t.Fatalf("bad provenance error = %v", err)
	}
	rowID := []viewer.EmittedColumn{{OutputID: "out", OccurrenceID: ws.RootOccurrenceID, ProjectionMode: "VALUE", SourceResourceType: "Observation", SourcePath: "id", PublicColumn: "__loom_row_id"}}
	if _, err := resolveAddressableDocumentSourceRow(document, receiptmodel.PublicOutputContract{OutputID: "out", RootResourceType: "Observation", RowGrain: "resource"}, rowID); err == nil {
		t.Fatal("internal row id was accepted as source identity")
	}
}

func TestAddressableSourceRowAcceptsNamedResourceGrains(t *testing.T) {
	for _, fixture := range []struct{ resource, grain string }{
		{"DocumentReference", "file"}, {"Patient", "patient"}, {"Specimen", "specimen"}, {"Observation", "observation"},
	} {
		t.Run(fixture.grain, func(t *testing.T) {
			document := addressableDocument()
			document.RootResourceType, document.Route.ResourceType = fixture.resource, fixture.resource
			contract := receiptmodel.PublicOutputContract{OutputID: "out", RootResourceType: fixture.resource, RowGrain: fixture.grain}
			emitted := []viewer.EmittedColumn{{OutputID: "out", OccurrenceID: "base", ProjectionMode: "VALUE", SourceResourceType: fixture.resource, SourcePath: "id", PublicColumn: "subject_id"}}
			if got, err := resolveAddressableDocumentSourceRow(document, contract, emitted); err != nil || got.ResourceType != fixture.resource || got.PhysicalColumn != "subject_id" {
				t.Fatalf("source row = %#v, err=%v", got, err)
			}
			for _, invalid := range []string{"expanded", "groups", "invalid", "diagnosis"} {
				contract.RowGrain = invalid
				if _, err := resolveAddressableDocumentSourceRow(document, contract, emitted); err == nil {
					t.Fatalf("accepted incompatible grain %q", invalid)
				}
			}
		})
	}
}
