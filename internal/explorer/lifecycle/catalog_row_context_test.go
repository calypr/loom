package lifecycle

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestSavedCatalogRowContextPinsTheSavedOutputRows(t *testing.T) {
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{{
		Output:           authoringv2.Output{ID: "observations", Title: "Observations"},
		RootResourceType: "Observation",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Rows:             authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionRecords, Records: &authoringv2.RecordRows{}},
	}}}
	initial, err := savedCatalogRowContext(workspace, "observations")
	if err != nil {
		t.Fatal(err)
	}
	if initial.RootResource != "Observation" || initial.Digest == "" {
		t.Fatalf("saved row context = %#v", initial)
	}
	workspace.Documents[0].Output.Title = "Renamed table"
	renamed, err := savedCatalogRowContext(workspace, "observations")
	if err != nil || renamed.Digest != initial.Digest {
		t.Fatalf("presentation changed row identity: %#v, %v", renamed, err)
	}
	workspace.Documents[0].Population = &authoringv2.Population{SelectionRevisionID: "selected-patients"}
	selected, err := savedCatalogRowContext(workspace, "observations")
	if err != nil || selected.Digest == initial.Digest {
		t.Fatalf("selection did not change row identity: %#v, %v", selected, err)
	}
	workspace.Documents[0].FixedFilters = []authoringv2.FixedFilter{{Column: "status", Values: []string{"final"}}}
	filtered, err := savedCatalogRowContext(workspace, "observations")
	if err != nil || filtered.Digest == selected.Digest {
		t.Fatalf("filter did not change row identity: %#v, %v", filtered, err)
	}
	if _, err := savedCatalogRowContext(workspace, "missing"); err == nil {
		t.Fatal("unknown output accepted")
	}
}
