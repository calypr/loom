package authoringv2

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func testObservationFrame() FrameDefinition {
	frame, err := NewFrameDefinition("Observation component values", "Codes and their paired values on Observation records.", capability.SemanticFrameFamily{
		BindingID: "binding-observation-component", ResourceType: "Observation", SourcePath: "component[]",
		OwningScope: "component[]", KeyPath: "component[].code.coding[]", ValuePath: "valueQuantity.value",
		LogicalType: "decimal", RuleVersion: "semantic-rule-v1", SchemaVersion: 1,
	}, nil, capability.ConstructionChoiceValue)
	if err != nil {
		panic(err)
	}
	return frame
}

func observationFrameWorkspace(frame FrameDefinition) Workspace {
	lookup := validCorrelatedLookup()
	tagged := Column{Column: "component_shared", Label: "Shared", OccurrenceID: RootOccurrenceID, FrameID: frame.ID, Source: ColumnSource{Kind: SourceCodedValue, Lookup: lookup}}
	otherLookup := validCorrelatedLookup()
	otherLookup.Key.Code = "unframed"
	ordinary := Column{Column: "component_other", Label: "Other", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceCodedValue, Lookup: otherLookup}}
	document := Document{
		Kind: Kind, Output: Output{ID: "out", Title: "Observations"}, RootResourceType: "Observation",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}, Rows: RecordsRowDefinition(),
		Frames: []FrameDefinition{frame}, Columns: []Column{tagged, ordinary},
	}
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Builder"},
		Documents: []Document{document}, Tabs: []Tab{{ID: "tab-out", Title: "Observations", OutputID: "out", Visible: true}},
	}
}

func observationFrameCatalog() CatalogSnapshot {
	catalog := testCatalog()
	catalog.Nodes = []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	catalog.Edges = nil
	catalog.Candidates = nil
	return catalog
}

func TestFrameSourcePersistsAndRoundTripsWithoutChangingRows(t *testing.T) {
	frame := testObservationFrame()
	workspace := emptyCommandWorkspace()
	workspace.Explorer.Title = "Builder"
	workspace.Documents = []Document{{
		Kind: Kind, Output: Output{ID: "out", Title: "Observations"}, RootResourceType: "Observation",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}, Rows: RecordsRowDefinition(), Columns: []Column{},
	}}
	workspace.Tabs = []Tab{{ID: "tab-out", Title: "Observations", OutputID: "out", Visible: true}}
	command := Command{Type: CommandSetFrameSource, OutputID: "out", FrameChoiceID: "signed-frame-choice", FrameForm: capability.ConstructionChoiceValue}
	if err := command.ResolveFrameSource(frame); err != nil {
		t.Fatal(err)
	}
	after, results, err := ApplyCommands(workspace, observationFrameCatalog(), "set-frame", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].FrameID != frame.ID || len(after.Documents[0].Frames) != 1 || !reflect.DeepEqual(after.Documents[0].Frames[0], frame) {
		t.Fatalf("frame source was not persisted: results=%#v frames=%#v", results, after.Documents[0].Frames)
	}
	if len(after.Documents[0].Columns) != 0 || !reflect.DeepEqual(after.Documents[0].Route, workspace.Documents[0].Route) {
		t.Fatalf("setting a frame source changed the output shape: document=%#v", after.Documents[0])
	}

	encoded, err := after.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Documents[0].Frames) != 1 || reloaded.Documents[0].Frames[0].ID != frame.ID || reloaded.Documents[0].Frames[0].Form != capability.ConstructionChoiceValue {
		t.Fatalf("saved frame source did not survive canonical reload: %#v", reloaded.Documents[0].Frames)
	}
}

func TestRemovingFrameSourceAtomicallyRemovesOnlyTaggedColumns(t *testing.T) {
	frame := testObservationFrame()
	workspace := observationFrameWorkspace(frame)
	if err := workspace.Validate(); err != nil {
		t.Fatalf("test workspace is invalid: %v", err)
	}
	after, results, err := ApplyCommands(workspace, observationFrameCatalog(), "remove-frame", []Command{{
		Type: CommandRemoveFrameSource, OutputID: "out", FrameID: frame.ID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].FrameID != frame.ID || len(results[0].RemovedColumns) != 1 || results[0].RemovedColumns[0] != "component_shared" {
		t.Fatalf("remove result did not enumerate atomically removed frame columns: %#v", results)
	}
	if len(after.Documents[0].Frames) != 0 || len(after.Documents[0].Columns) != 1 || after.Documents[0].Columns[0].Column != "component_other" {
		t.Fatalf("frame removal changed unrelated columns or retained frame columns: %#v", after.Documents[0])
	}
	if err := after.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFrameDefinitionRejectsUnsupportedZeroManyPolicy(t *testing.T) {
	frame := testObservationFrame()
	frame.ManyPolicy = FrameManyFirst
	if err := frame.Validate(); err == nil || !strings.Contains(err.Error(), "zero/many policy") {
		t.Fatalf("mismatched form policy error = %v", err)
	}
}
