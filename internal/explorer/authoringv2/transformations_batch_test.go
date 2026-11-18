package authoringv2

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestCatalogAggregateBatchMatchesIndividualChoices(t *testing.T) {
	base := CatalogSnapshot{
		Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation"}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true}},
		Candidates: []CatalogCandidate{
			{ID: "obs-id", NodeID: "observation", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"},
			{ID: "patient-birth", NodeID: "patient", FieldPath: "birthDate", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-effective", NodeID: "observation", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-updated", NodeID: "observation", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "report-issued", NodeID: "report", FieldPath: "issued", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "patient-updated", NodeID: "patient", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
			{ID: "obs-value", NodeID: "observation", FieldPath: "valueQuantity.value", LogicalType: "decimal", Cardinality: "optional_one", ConceptCandidates: []ConceptCandidate{{ValueSelector: "valueQuantity.value", ObservedUnits: []string{"kg", "cm", "furlong"}}}},
			{ID: "repeated", NodeID: "observation", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "many", Repeated: true},
			{ID: "missing-node", NodeID: "absent", FieldPath: "id", LogicalType: "string"},
		},
	}
	cases := map[string]CatalogSnapshot{"mixed": base, "empty": {}}
	noRoot := base
	noRoot.Nodes = append([]CatalogNode(nil), base.Nodes...)
	for i := range noRoot.Nodes {
		noRoot.Nodes[i].RowRootEligible = false
	}
	cases["no-root-anchors"] = noRoot
	duplicates := base
	duplicates.Candidates = append(append([]CatalogCandidate(nil), base.Candidates...), CatalogCandidate{ID: "obs-id", NodeID: "patient", LogicalType: "decimal", FieldPath: "id"})
	duplicates.Nodes = append(append([]CatalogNode(nil), base.Nodes...), CatalogNode{ID: "observation", ResourceType: "DiagnosticReport", RowRootEligible: true})
	cases["duplicate-identifiers"] = duplicates
	noTimestamps := base
	noTimestamps.Candidates = []CatalogCandidate{base.Candidates[0], base.Candidates[6], base.Candidates[8]}
	cases["no-timestamps"] = noTimestamps
	for name, catalog := range cases {
		t.Run(name, func(t *testing.T) {
			before, _ := json.Marshal(catalog)
			got := AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
			if len(got) != len(catalog.Candidates) {
				t.Fatalf("results=%d candidates=%d", len(got), len(catalog.Candidates))
			}
			for i, candidate := range catalog.Candidates {
				want := AggregateTransformationCapabilitiesForCatalog(catalog, candidate.ID)
				if !reflect.DeepEqual(got[i], want) {
					t.Fatalf("candidate %s: batch=%#v individual=%#v", candidate.ID, got[i], want)
				}
			}
			after, _ := json.Marshal(catalog)
			if string(after) != string(before) {
				t.Fatal("batch mutated catalog")
			}
		})
	}
}

func TestCatalogAggregateBatchResultsDoNotShareMutableChoices(t *testing.T) {
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "obs", ResourceType: "Observation", RowRootEligible: true}}, Candidates: []CatalogCandidate{
		{ID: "one", NodeID: "obs", FieldPath: "effectiveDateTime", LogicalType: "date_time", Cardinality: "optional_one"},
		{ID: "two", NodeID: "obs", FieldPath: "meta.lastUpdated", LogicalType: "date_time", Cardinality: "optional_one"},
	}}
	got := AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
	want := AggregateTransformationCapabilitiesForCatalog(catalog, "two")
	got[0].Temporal.TimestampFields[0].Label = "changed"
	got[0].Temporal.AnchorFields[0].Label = "changed"
	got[0].UnitNormalization.Presets[0].Reason = "changed"
	if !reflect.DeepEqual(got[1], want) {
		t.Fatal("mutating one result changed another candidate's choices")
	}
}

func syntheticWideTransformationCatalog(size int) CatalogSnapshot {
	catalog := CatalogSnapshot{Nodes: []CatalogNode{{ID: "obs", ResourceType: "Observation"}, {ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true}}}
	for i := 0; i < size; i++ {
		candidate := CatalogCandidate{ID: fmt.Sprintf("candidate-%05d", i), NodeID: "obs", FieldPath: "id", LogicalType: "string", Cardinality: "optional_one"}
		switch i % 16 {
		case 0:
			candidate.FieldPath = "effectiveDateTime"
			candidate.LogicalType = "date_time"
		case 1:
			candidate.NodeID = "patient"
			candidate.FieldPath = "meta.lastUpdated"
			candidate.LogicalType = "date_time"
		case 2:
			candidate.NodeID = "report"
			candidate.FieldPath = "issued"
			candidate.LogicalType = "date_time"
		case 3:
			candidate.FieldPath = "valueQuantity.value"
			candidate.LogicalType = "decimal"
			candidate.ConceptCandidates = []ConceptCandidate{{ValueSelector: "valueQuantity.value", ObservedUnits: []string{"kg"}}}
		}
		catalog.Candidates = append(catalog.Candidates, candidate)
	}
	return catalog
}

var aggregateTransformationBenchmarkSink []AggregateTransformationCapabilities

func BenchmarkSyntheticWideCatalogAggregateTransformations(b *testing.B) {
	for _, size := range []int{256, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			catalog := syntheticWideTransformationCatalog(size)
			b.Run("individual", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					results := make([]AggregateTransformationCapabilities, len(catalog.Candidates))
					for j, candidate := range catalog.Candidates {
						results[j] = AggregateTransformationCapabilitiesForCatalog(catalog, candidate.ID)
					}
					aggregateTransformationBenchmarkSink = results
				}
			})
			b.Run("batch", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					aggregateTransformationBenchmarkSink = AggregateTransformationCapabilitiesForCatalogCandidates(catalog)
				}
			})
		})
	}
}
