package compilation

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCompileCompactsIdentifierFamiliesOnOneOccurrence(t *testing.T) {
	document := authoringv2.Document{
		Rows:             authoringv2.RecordsRowDefinition(),
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "out", Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{
			identifierColumn("case_id", "urn:study:case-id"),
			identifierColumn("medical_record_number", "urn:study:mrn"),
		},
	}

	result, err := Compile(context.Background(), "project", "explorer", document, fixtureSnapshotForProject("project"))
	if err != nil {
		t.Fatal(err)
	}
	dynamics := result.Bundle.Outputs[0].DynamicColumns
	if len(dynamics) != 1 {
		t.Fatalf("compiled identifier families = %d (%#v), want one family", len(dynamics), dynamics)
	}
	if got, want := dynamics[0].Columns, []string{"case_id", "medical_record_number"}; !equalStrings(got, want) {
		t.Fatalf("compact identifier columns = %#v, want %#v", got, want)
	}
	if dynamics[0].ColumnSourceKeys["case_id"] != "urn:study:case-id" || dynamics[0].ColumnSourceKeys["medical_record_number"] != "urn:study:mrn" {
		t.Fatalf("compact identifier source keys = %#v", dynamics[0].ColumnSourceKeys)
	}
	if dynamics[0].ColumnTypes["case_id"] != "string" || dynamics[0].ColumnTypes["medical_record_number"] != "string" {
		t.Fatalf("compact identifier types = %#v", dynamics[0].ColumnTypes)
	}
}

func TestCompileCompactsCompatibleCodedValueFamilies(t *testing.T) {
	const bindingOwner = "component[]"
	document := authoringv2.Document{
		Rows:             authoringv2.RecordsRowDefinition(),
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "out", Title: "Observations"},
		RootResourceType: "Observation",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		Columns: []authoringv2.Column{
			codedColumn("height_cm", "urn:study:measurements", "height", bindingOwner),
			codedColumn("weight_kg", "urn:study:measurements", "weight", bindingOwner),
		},
	}
	snapshot := fixtureSnapshotForProject("project")
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})

	result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	pivots := result.Bundle.Outputs[0].Pivots
	if len(pivots) != 1 {
		t.Fatalf("compiled coded-value families = %d (%#v), want one family", len(pivots), pivots)
	}
	if got, want := pivots[0].Columns, []string{"height", "weight"}; !equalStrings(got, want) {
		t.Fatalf("compact coded-value columns = %#v, want %#v", got, want)
	}
	if pivots[0].ColumnAliases["height"] != "height_cm" || pivots[0].ColumnAliases["weight"] != "weight_kg" {
		t.Fatalf("compact coded-value aliases = %#v", pivots[0].ColumnAliases)
	}
}

func TestCompileDoesNotCompactIncompatibleCodedValueFamilies(t *testing.T) {
	const bindingOwner = "component[]"
	tests := []struct {
		name       string
		first      authoringv2.Column
		second     authoringv2.Column
		wantPivots int
	}{
		{
			name:       "different systems",
			first:      codedColumn("height_cm", "urn:study:measurements", "height", bindingOwner),
			second:     codedColumn("weight_kg", "urn:other:measurements", "weight", bindingOwner),
			wantPivots: 2,
		},
		{
			name:  "different modes",
			first: codedColumn("height_cm", "urn:study:measurements", "height", bindingOwner),
			second: func() authoringv2.Column {
				column := codedColumn("weight_kg", "urn:study:measurements", "weight", bindingOwner)
				column.Source.Lookup.ProjectionMode = "ALL"
				return column
			}(),
			wantPivots: 2,
		},
		{
			name:  "different bindings",
			first: codedColumn("height_cm", "urn:study:measurements", "height", bindingOwner),
			second: func() authoringv2.Column {
				column := codedColumn("height_unit", "urn:study:measurements", "height", bindingOwner)
				column.Source.Lookup.Binding.ValuePath = "valueQuantity.unit"
				column.Source.Lookup.Binding.LogicalType = "string"
				return column
			}(),
			wantPivots: 2,
		},
		{
			name:       "ambiguous duplicate code",
			first:      codedColumn("height_cm", "urn:study:measurements", "height", bindingOwner),
			second:     codedColumn("height_m", "urn:study:measurements", "height", bindingOwner),
			wantPivots: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := authoringv2.Document{
				Rows:             authoringv2.RecordsRowDefinition(),
				Kind:             authoringv2.Kind,
				Output:           authoringv2.Output{ID: "out", Title: "Observations"},
				RootResourceType: "Observation",
				Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
				Columns:          []authoringv2.Column{test.first, test.second},
			}
			snapshot := fixtureSnapshotForProject("project")
			snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation", RowRootEligible: true, RowGrain: "observation"})
			result, err := Compile(context.Background(), "project", "explorer", document, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(result.Bundle.Outputs[0].Pivots); got != test.wantPivots {
				t.Fatalf("compiled coded-value families = %d (%#v), want %d", got, result.Bundle.Outputs[0].Pivots, test.wantPivots)
			}
		})
	}
}

func identifierColumn(name, system string) authoringv2.Column {
	return authoringv2.Column{
		Column: name, Label: name, LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceIdentifierBySystem, Lookup: &authoringv2.LookupSource{
			Identifier: &fhirschema.IdentifierBinding{OwnerPath: "identifier[]", SystemPath: "system", ValuePath: "value", SystemURI: system, LogicalType: "string"},
		}},
	}
}

func codedColumn(name, system, code, ownerPath string) authoringv2.Column {
	return authoringv2.Column{
		Column: name, Label: name, LogicalType: "decimal", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
			Binding: &fhirschema.CorrelatedBinding{OwnerPath: ownerPath, KeyPath: ownerPath + ".code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
			Key:     &fhirschema.CorrelatedKey{System: system, Code: code},
		}},
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
