package workspace

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNetworkLegacyRouteUsesExactCatalogIdentity(t *testing.T) {
	d := Document{Output: Output{ID: "subjects"}, Route: RouteNode{OccurrenceID: "base", ResourceType: "ResearchSubject", Children: []RouteNode{{OccurrenceID: "patient", ResourceType: "Patient", Relationship: "subject"}}}, Rows: RecordsRowDefinition()}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "subject", ResourceType: "ResearchSubject", RowRootEligible: true}, {ID: "patient", ResourceType: "Patient"}}, Edges: []CatalogEdge{{ID: "subject-out", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "OUTBOUND"}}}
	n, err := NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if n.Occurrences[1].CatalogEdgeID != "subject-out" || n.Occurrences[1].StorageDirection != "OUTBOUND" {
		t.Fatalf("legacy route lost exact catalog identity: %+v", n.Occurrences[1])
	}
	if d.Route.Children[0].CatalogEdgeID != "" {
		t.Fatal("read adapter mutated saved legacy route")
	}
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "subject-in", FromNodeID: "subject", ToNodeID: "patient", Label: "subject", StorageDirection: "INBOUND"})
	if _, err := NetworkForDocument(d, catalog); err == nil || !strings.Contains(err.Error(), "multiple catalog edges") {
		t.Fatalf("ambiguous legacy route was guessed: %v", err)
	}
	d.Route.Children[0].CatalogEdgeID = "subject-in"
	n, err = NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if n.Occurrences[1].CatalogEdgeID != "subject-in" || n.Occurrences[1].StorageDirection != "INBOUND" {
		t.Fatal("explicit parallel edge was retargeted")
	}
}

func TestNetworkLegacyColumnsRemainInDocumentWithoutInventedBindingIdentity(t *testing.T) {
	d := Document{Output: Output{ID: "patients"}, Route: RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Rows: RecordsRowDefinition(), Columns: []Column{{Column: "legacy_id", OccurrenceID: "base", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}}, {ColumnID: "saved-id", Column: "other_id", OccurrenceID: "base", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "FIRST"}}}}}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}}
	network, err := NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.Bindings) != 1 || network.Bindings[0].ColumnID != "saved-id" || network.Bindings[0].OccurrenceID != "base" || network.Bindings[0].ValueMode != "FIRST" {
		t.Fatalf("legacy ID was invented or modern binding changed: %#v", network.Bindings)
	}
	if len(d.Columns) != 2 || d.Columns[0].ColumnID != "" || d.Columns[0].Column != "legacy_id" || d.Columns[0].Source.Field.Path != "id" || d.Columns[0].Source.Field.ProjectionMode != "VALUE" {
		t.Fatal("read adapter rewrote or removed legacy source facts")
	}
}

func TestNetworkProjectsOrderedFieldsWithExactRepeatedOccurrencesAndLegacyVisibility(t *testing.T) {
	d := Document{
		Output: Output{ID: "observations"},
		Route:  RouteNode{OccurrenceID: "base", ResourceType: "Observation"},
		Rows:   RecordsRowDefinition(),
		Columns: []Column{
			{ColumnID: "subject-id", Column: "subject_id", Label: "Subject ID", OccurrenceID: "patient_subject", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{ColumnID: "performer-id", Column: "performer_id", Label: "Performer ID", OccurrenceID: "patient_performer", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
			{Column: "legacy", Label: "Legacy", OccurrenceID: "base", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "status", ProjectionMode: "VALUE"}}},
		},
	}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}}
	network, err := NetworkForDocument(d, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if len(network.ProjectedFields) != 3 {
		t.Fatalf("projected field count = %d, want all three saved columns: %#v", len(network.ProjectedFields), network.ProjectedFields)
	}
	first, second, legacy := network.ProjectedFields[0], network.ProjectedFields[1], network.ProjectedFields[2]
	if first.ColumnID != "subject-id" || first.Label != "Subject ID" || first.OccurrenceID != "patient_subject" || first.Owner != ProjectedFieldOwnerAuthored || first.Removal != ProjectedFieldRemovalColumn || first.Status != ProjectedFieldStatusExact {
		t.Fatalf("first projected field lost its exact authored identity: %+v", first)
	}
	if second.ColumnID != "performer-id" || second.OccurrenceID != "patient_performer" || second.ColumnID == first.ColumnID {
		t.Fatalf("repeated resource occurrences were conflated: first=%+v second=%+v", first, second)
	}
	if legacy.ColumnID != "" || legacy.Column != "legacy" || legacy.Owner != ProjectedFieldOwnerAuthored || legacy.Status != ProjectedFieldStatusUnknown || legacy.Removal != ProjectedFieldRemovalUnavailable {
		t.Fatalf("legacy field must remain visible without a fabricated identity: %+v", legacy)
	}
}

func TestImportedProjectedFieldOccurrenceRequiresCompilerBinding(t *testing.T) {
	d := Document{
		Output:         Output{ID: "patients"},
		ImportedRecipe: &ImportedRecipeRef{Name: "pin", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"},
		Columns:        []Column{{ColumnID: "imported-id", Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceImported, Imported: &ImportedSource{OutputColumn: "patient_id"}}}},
	}
	unknown := ProjectedFieldsForDocument(d, AuthoringNetwork{})[0]
	if unknown.Owner != ProjectedFieldOwnerPinned || unknown.Status != ProjectedFieldStatusUnknown || unknown.OccurrenceID != "" || unknown.Removal != ProjectedFieldRemovalUnavailable {
		t.Fatalf("imported base occurrence was treated as provenance: %+v", unknown)
	}
	exact := ProjectedFieldsForDocument(d, AuthoringNetwork{Bindings: []NetworkColumnBinding{{ColumnID: "imported-id", OccurrenceID: "patient_subject", SourceStepID: "traversals[0].fields", Status: "EXACT"}}})[0]
	if exact.Owner != ProjectedFieldOwnerPinned || exact.Status != ProjectedFieldStatusExact || exact.OccurrenceID != "patient_subject" || exact.SourceStepID != "traversals[0].fields" || exact.Removal != ProjectedFieldRemovalUnavailable {
		t.Fatalf("compiler binding was not reflected without inventing a field-level remove action: %+v", exact)
	}
}

func TestAuthoringNetworkEmitsEmptyProjectedFieldsAsArray(t *testing.T) {
	document := Document{Output: Output{ID: "patients"}, Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: RecordsRowDefinition()}
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}}
	network, err := NetworkForDocument(document, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if network.ProjectedFields == nil || len(network.ProjectedFields) != 0 {
		t.Fatalf("empty projected field inventory must be a non-nil array: %#v", network.ProjectedFields)
	}
	raw, err := json.Marshal(network)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"projectedFields":[]`) {
		t.Fatalf("empty projectedFields did not serialize as an array: %s", raw)
	}
}

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
	if fields[0].Removal != ProjectedFieldRemovalUnavailable {
		t.Fatalf("pinned removal without compiler proof was advertised: %+v", fields[0])
	}
	if fields[1].Removal != ProjectedFieldRemovalColumn {
		t.Fatalf("authored removal was not advertised: %+v", fields[1])
	}
	if fields[2].Removal != ProjectedFieldRemovalUnavailable {
		t.Fatalf("legacy removal without stable identity was advertised: %+v", fields[2])
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
