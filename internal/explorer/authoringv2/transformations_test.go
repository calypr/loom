package authoringv2

import (
	"strings"
	"testing"
)

func TestTemporalCapabilitiesUseGeneratedSchemaForUnfamiliarResource(t *testing.T) {
	catalog := CatalogSnapshot{
		Nodes: []CatalogNode{{ID: "diagnostic-report", ResourceType: "DiagnosticReport", RowRootEligible: true}},
		Candidates: []CatalogCandidate{
			{ID: "report-id", NodeID: "diagnostic-report", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "report-status", NodeID: "diagnostic-report", FieldPath: "status", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "report-issued", NodeID: "diagnostic-report", FieldPath: "issued", LogicalType: "date_time", Cardinality: "optional_one"},
		},
	}
	choices := AggregateTransformationCapabilitiesForCatalog(catalog, "report-id")
	if !choices.Temporal.SupportsTimestamp("DiagnosticReport", "issued") || !choices.Temporal.SupportsAnchor("DiagnosticReport", "issued") {
		t.Fatalf("generated-schema timestamp was not advertised: %#v", choices.Temporal)
	}
	if choices.Temporal.SupportsTimestamp("DiagnosticReport", "status") {
		t.Fatalf("string field was advertised as a timestamp: %#v", choices.Temporal)
	}

	document := Document{Rows: RecordsRowDefinition(), RootResourceType: "DiagnosticReport", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "DiagnosticReport"}}
	source := ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{
		Operation: "COUNT", Path: "id",
		Temporal: &TemporalReductionSource{TimestampPath: "status", AnchorPath: "issued", Direction: "DESC", Precision: "INSTANT", TiePolicy: "REQUIRE_UNIQUE"},
	}}
	if err := validateEditableSource(document, catalog, RootOccurrenceID, source); err == nil || !strings.Contains(err.Error(), "temporal timestamp") {
		t.Fatalf("wrong timestamp type error = %v", err)
	}
}

func TestUnitNormalizationRequiresAnAdvertisedCompatiblePreset(t *testing.T) {
	catalog := CatalogSnapshot{
		Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []CatalogCandidate{{
			ID: "observation-value", NodeID: "observation", FieldPath: "valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one",
			ConceptCandidates: []ConceptCandidate{{SourceResourceType: "Observation", ValueSelector: "valueQuantity.value", ObservedUnits: []string{"kg"}}},
		}},
	}
	choices := AggregateTransformationCapabilitiesForCatalog(catalog, "observation-value").UnitNormalization
	var centimetersAvailable, kilogramsAvailable bool
	for _, preset := range choices.Presets {
		switch preset.PolicyID {
		case "to-centimeters":
			centimetersAvailable = preset.Available
		case "to-kilograms":
			kilogramsAvailable = preset.Available
		}
	}
	if centimetersAvailable || !kilogramsAvailable {
		t.Fatalf("unit presets for kg source: %#v", choices)
	}

	document := Document{Rows: RecordsRowDefinition(), RootResourceType: "Observation", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}}
	source := func(policy string) ColumnSource {
		return ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{
			Operation: "SUM", Path: "valueQuantity.value",
			UnitNormalization: &UnitNormalizationPolicy{PolicyID: policy, Version: "1"},
		}}
	}
	if err := validateEditableSource(document, catalog, RootOccurrenceID, source("to-centimeters")); err == nil || !strings.Contains(err.Error(), "UNIT_PRESET_INCOMPATIBLE") {
		t.Fatalf("incompatible unit preset error = %v", err)
	}
	if err := validateEditableSource(document, catalog, RootOccurrenceID, source("to-kilograms")); err != nil {
		t.Fatalf("compatible unit preset rejected: %v", err)
	}
}
