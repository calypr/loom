package authoringv2

import (
	"strings"
	"testing"
)

func TestTemporalCapabilitiesUseGeneratedSchemaForUnfamiliarResource(t *testing.T) {
	catalog := CatalogSnapshot{
		Nodes: []CatalogNode{
			{ID: "diagnostic-report", ResourceType: "DiagnosticReport", RowRootEligible: true},
			{ID: "observation", ResourceType: "Observation"},
		},
		Candidates: []CatalogCandidate{
			{ID: "report-id", NodeID: "diagnostic-report", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "report-issued", NodeID: "diagnostic-report", FieldPath: "issued", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "report-last-updated", NodeID: "diagnostic-report", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "observation-id", NodeID: "observation", FieldPath: "id", LogicalType: "id", Cardinality: "optional_one"},
			{ID: "observation-status", NodeID: "observation", FieldPath: "status", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "observation-effective", NodeID: "observation", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "optional_one"},
		},
	}
	choices := AggregateTransformationCapabilitiesForCatalog(catalog, "report-id")
	if !choices.Temporal.SupportsTimestamp("DiagnosticReport", "issued") || !choices.Temporal.SupportsAnchor("DiagnosticReport", "issued") {
		t.Fatalf("generated-schema timestamp was not advertised: %#v", choices.Temporal)
	}
	if choices.Temporal.SupportsTimestamp("DiagnosticReport", "status") {
		t.Fatalf("string field was advertised as a timestamp: %#v", choices.Temporal)
	}

	document := Document{Rows: RecordsRowDefinition(), RootResourceType: "DiagnosticReport", Route: RouteNode{
		OccurrenceID: RootOccurrenceID, ResourceType: "DiagnosticReport",
		Children: []RouteNode{{OccurrenceID: "observation", ResourceType: "Observation"}},
	}}
	source := ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{
		Operation: "COUNT", Path: "id",
		ContributorWindow: &ContributorWindowSource{TimestampPath: "status", AnchorPath: "meta.lastUpdated", Precision: "INSTANT"},
	}}
	if err := validateEditableSource(document, catalog, "observation", source); err == nil || !strings.Contains(err.Error(), "contributor window timestamp") {
		t.Fatalf("wrong timestamp type error = %v", err)
	}
	pathlessCount := ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{
		Operation:         "COUNT",
		ContributorWindow: &ContributorWindowSource{TimestampPath: "effectiveDateTime", AnchorPath: "meta.lastUpdated", LowerOffset: -86400, UpperOffset: 0, Precision: "INSTANT"},
	}}
	if err := validateEditableSource(document, catalog, "observation", pathlessCount); err != nil {
		t.Fatalf("pathless COUNT did not use the advertised timestamp candidate capability: %v", err)
	}
	if err := validateEditableSource(document, catalog, RootOccurrenceID, pathlessCount); err == nil || !strings.Contains(err.Error(), "requires a related resource occurrence") {
		t.Fatalf("root-row contributor window error = %v, want related resource requirement", err)
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

func TestUnitNormalizationPresetsAreNotPoisonedByOtherPopulationUnits(t *testing.T) {
	catalog := CatalogSnapshot{
		Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []CatalogCandidate{{
			ID: "observation-value", NodeID: "observation", FieldPath: "valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one",
			ConceptCandidates: []ConceptCandidate{{
				SourceResourceType: "Observation", ValueSelector: "valueQuantity.value",
				ObservedUnits: []string{"cm", "kg", "furlong"}, ObservedUnitsTruncated: true,
			}},
		}},
	}

	choices := AggregateTransformationCapabilitiesForCatalog(catalog, "observation-value").UnitNormalization
	available := map[string]bool{}
	for _, preset := range choices.Presets {
		available[preset.PolicyID] = preset.Available
	}
	if !available["to-centimeters"] || !available["to-kilograms"] {
		t.Fatalf("population-applicable presets were globally poisoned: %#v", choices)
	}
}
