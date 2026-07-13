package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRelationshipCountsRequireCompleteCommittedEdgeShape(t *testing.T) {
	docs := []json.RawMessage{
		json.RawMessage(`{"project":"P1","from_type":"Patient","label":"subject","to_type":"Specimen"}`),
		json.RawMessage(`{"project":"P1","from_type":"Patient","label":"subject","to_type":"Specimen"}`),
	}
	counts, err := RelationshipCountsFromRawEdges(docs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[RelationshipKey]int64{{Project: "P1", FromType: "Patient", Label: "subject", ToType: "Specimen"}: 2}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("counts = %#v, want %#v", counts, want)
	}
	if _, err := RelationshipCountsFromRawEdges([]json.RawMessage{json.RawMessage(`{"project":"P1","from_type":"Patient"}`)}); err == nil {
		t.Fatal("incomplete edge was accepted")
	}
}

func TestRelationshipCatalogDocumentsHaveStableGenerationSafeKeys(t *testing.T) {
	counts := map[RelationshipKey]int64{
		{Project: "P1", DatasetGeneration: "gen/a", AuthResourcePath: "path", FromType: "Patient", Label: "subject", ToType: "Specimen"}: 3,
	}
	docs := RelationshipCatalogDocuments(counts)
	if len(docs) != 1 || !strings.HasPrefix(docs[0].Key, relationshipCatalogKeyPrefix) {
		t.Fatalf("documents = %#v", docs)
	}
	if docs[0].DatasetGeneration != "gen/a" || docs[0].EdgeCount != 3 {
		t.Fatalf("document = %#v", docs[0])
	}
	other := RelationshipCatalogDocuments(map[RelationshipKey]int64{
		{Project: "P1", DatasetGeneration: "gen a", AuthResourcePath: "path", FromType: "Patient", Label: "subject", ToType: "Specimen"}: 3,
	})
	if docs[0].Key == other[0].Key {
		t.Fatal("distinct generations collided in relationship key")
	}
}

func TestRuntimeReferenceDiscoveryUsesCatalogAndKeepsRepairExplicit(t *testing.T) {
	if !strings.Contains(relationshipCatalogBuilderAQL, "FOR d IN fhir_relationship_catalog") || !strings.Contains(relationshipCatalogStorageAQL, "FOR d IN fhir_relationship_catalog") {
		t.Fatal("runtime relationship queries do not use the catalog")
	}
	if !strings.Contains(relationshipRebuildAQL, "FOR e IN fhir_edge") || strings.Contains(relationshipCatalogBuilderAQL, "fhir_edge") || strings.Contains(relationshipCatalogStorageAQL, "fhir_edge") {
		t.Fatal("direct edge aggregation leaked into runtime discovery")
	}
}
