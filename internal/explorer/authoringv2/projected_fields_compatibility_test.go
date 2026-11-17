package authoringv2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestProjectedFieldsPreserveSavedInventoryAndExactBindings(t *testing.T) {
	document := Document{Columns: []Column{
		{ColumnID: "pinned-id", Column: "LICENSE", Label: "License", Source: ColumnSource{Kind: SourceImported}},
		{ColumnID: "authored-id", Column: "added", Label: "Added", OccurrenceID: "base", Source: ColumnSource{Kind: SourceField}},
		{Column: "legacy", Label: "Legacy", Source: ColumnSource{Kind: SourceImported}},
	}}
	before, _ := json.Marshal(document)
	network := AuthoringNetwork{Bindings: []NetworkColumnBinding{{ColumnID: "pinned-id", OccurrenceID: "base", SourceStepID: "source-step", Status: "EXACT"}}}
	fields := ProjectedFieldsForDocument(document, network)
	if len(fields) != 3 || fields[0].Column != "LICENSE" || fields[1].Column != "added" || fields[2].Column != "legacy" {
		t.Fatalf("saved inventory/order lost: %+v", fields)
	}
	if fields[0].Owner != ProjectedFieldOwnerPinned || fields[0].Status != ProjectedFieldStatusExact || fields[0].SourceStepID != "source-step" || fields[0].OccurrenceID != "base" {
		t.Fatalf("exact pinned binding lost: %+v", fields[0])
	}
	if fields[1].Owner != ProjectedFieldOwnerAuthored || fields[1].Status != ProjectedFieldStatusExact {
		t.Fatalf("authored ownership lost: %+v", fields[1])
	}
	if fields[2].ColumnID != "" || fields[2].Status != ProjectedFieldStatusUnknown {
		t.Fatalf("legacy identity invented: %+v", fields[2])
	}
	for _, field := range fields {
		if field.Removal != ProjectedFieldRemovalUnavailable {
			t.Fatalf("unsupported projected-field mutation advertised: %+v", field)
		}
	}
	after, _ := json.Marshal(document)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read model mutated saved document")
	}
}

func TestProjectedFieldsWireContainsTheDerivedInventory(t *testing.T) {
	document := Document{Output: Output{ID: "output-a"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Rows: RecordsRowDefinition(), Columns: []Column{{ColumnID: "license-id", Column: "LICENSE", Label: "License", Source: ColumnSource{Kind: SourceImported}}}}
	network := NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Bindings = []NetworkColumnBinding{{ColumnID: "license-id", OccurrenceID: "base", SourceStepID: "source-step", Status: "EXACT"}}
	network.ProjectedFields = ProjectedFieldsForDocument(document, network)
	wire, err := json.Marshal(map[string]any{"snapshotToken": "snapshot-proof", "draftVersion": 1, "draftDigest": "sha256:proof", "network": network, "rowIdentity": DescribeRowIdentity(document)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Network struct {
			ProjectedFields []ProjectedField `json:"projectedFields"`
		} `json:"network"`
	}
	if err = json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Network.ProjectedFields) != 1 || decoded.Network.ProjectedFields[0].Column != "LICENSE" {
		t.Fatalf("wire inventory lost: %s", wire)
	}
	t.Logf("PROJECTED_FIELDS_WIRE=%s", wire)
}
