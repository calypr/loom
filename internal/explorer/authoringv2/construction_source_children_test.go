package authoringv2

import (
	"encoding/json"
	"testing"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

func TestConstructionCanReferenceIndexedSourceChildrenAcrossReload(t *testing.T) {
	childID, _, err := lineage.StableSourceChildID(lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"given_slot"}, OccurrenceID: RootOccurrenceID,
		SourcePath: "name[].given[]", Coordinates: []lineage.Coordinate{
			{BoundaryPath: "name[]", Index: 0}, {BoundaryPath: "name[].given[]", Index: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	columns := []Column{{
		ColumnID: "given_slot", Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: RootOccurrenceID,
		Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}},
	}}
	document := Document{
		Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"}, Rows: RecordsRowDefinition(),
		Columns: columns, Construction: &Construction{
			Version: ConstructionVersion,
			Steps: []ConstructionStep{{
				ID: "filter_indexed", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
				Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: childID, Operator: ConstructionFilterExists}},
				Outputs: []StageColumn{
					{ID: childID, Name: "given_at_0_1", Label: "Given [0] [1]", Type: "string", Nullable: true},
				},
			}},
		},
	}
	if err := document.Construction.Validate(document.Columns); err != nil {
		t.Fatalf("valid indexed child reference: %v", err)
	}

	normalizeConstructionOutputOrder(&document)
	if got := document.Construction.Steps[0].Outputs[0].Name; got != "given_at_0_1" {
		t.Fatalf("normalized child name = %q, want persisted public name", got)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded Document
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Construction.Validate(reloaded.Columns); err != nil {
		t.Fatalf("reloaded indexed child reference: %v", err)
	}
	if got := reloaded.Construction.Steps[0].Operation.Filter.ColumnID; got != childID {
		t.Fatalf("reloaded filter column ID = %q, want %q", got, childID)
	}
	replacement := reloaded.Construction.Steps[0]
	replacement.Operation.Filter.Operator = ConstructionFilterMissing
	edited, _, err := reloaded.AnalyzeStepEdit(replacement)
	if err != nil {
		t.Fatalf("edit persisted child-consuming step: %v", err)
	}
	if got := edited.Construction.Steps[0].Outputs[0].ID; got != childID {
		t.Fatalf("edited step child ID = %q, want %q", got, childID)
	}

	stale := reloaded
	stale.Columns = append([]Column(nil), reloaded.Columns...)
	stale.Columns[0].Source.Field = &FieldSource{Path: "telecom[].value", ProjectionMode: "INDEXED"}
	if err := stale.Construction.Validate(stale.Columns); err == nil {
		t.Fatal("source edit retained a child reference from the old repeated path")
	}
	stale = reloaded
	stale.Columns = append([]Column(nil), reloaded.Columns...)
	stale.Columns[0].Source.Field = &FieldSource{Path: "name[].given[]", ProjectionMode: "VALUE"}
	if err := stale.Construction.Validate(stale.Columns); err == nil {
		t.Fatal("non-indexed source retained an indexed child reference")
	}
}
