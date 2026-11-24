package workspace

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func TestLegacyFrameWithNullRouteKeepsItsPersistedIdentity(t *testing.T) {
	source := capability.SemanticFrameFamily{
		BindingID: "observation-height", ResourceType: "Observation", SourcePath: "code",
		KeyPath: "code.coding[]", ValuePath: "valueQuantity.value", ChoiceArms: []string{"valueQuantity"},
		LogicalType: "decimal", RuleVersion: "4", SchemaVersion: 1,
	}
	legacyID := frameIdentityID(source, nil)
	if currentDirectID := frameIdentityID(source, []capability.ConstructionRouteStep{}); currentDirectID == legacyID {
		t.Fatal("legacy null route and new empty-array route unexpectedly share an identity")
	}
	workspace := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion, Explorer: ExplorerMetadata{Title: "Builder"}, Documents: []Document{}, Tabs: []Tab{}}
	workspace.Documents = []Document{{
		Kind: Kind, Output: Output{ID: "out", Title: "Observations"}, RootResourceType: "Observation",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}, Rows: RecordsRowDefinition(), Columns: []Column{},
		Frames: []FrameDefinition{{
			ID: legacyID, Title: "Observation height", Description: "Height observations.", Source: source,
			Route: nil, Form: capability.ConstructionChoiceValue,
			ZeroPolicy: FrameZeroNull, ManyPolicy: FrameManyInvalidMultipleValues,
		}},
	}}
	workspace.Tabs = []Tab{{ID: "tab-out", Title: "Observations", OutputID: "out", Visible: true}}

	legacyWire, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStoredWorkspaceForMigration(legacyWire)
	if err != nil {
		t.Fatal(err)
	}
	frame := decoded.Documents[0].Frames[0]
	if frame.ID != legacyID || frame.Route != nil {
		t.Fatalf("legacy frame changed during decode: id=%q route=%#v, want id=%q route:null", frame.ID, frame.Route, legacyID)
	}
	canonical, err := decoded.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Documents []struct {
			Frames []struct {
				ID    string          `json:"id"`
				Route json.RawMessage `json:"route"`
			} `json:"frames"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(canonical, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Documents) != 1 || len(wire.Documents[0].Frames) != 1 ||
		wire.Documents[0].Frames[0].ID != legacyID || string(wire.Documents[0].Frames[0].Route) != "null" {
		t.Fatalf("legacy frame was silently migrated or rekeyed: %s", canonical)
	}
	reloaded, err := DecodeStoredWorkspaceForMigration(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Documents[0].Frames[0]; got.ID != legacyID || got.Route != nil {
		t.Fatalf("legacy frame changed after canonical reload: id=%q route=%#v", got.ID, got.Route)
	}
}
