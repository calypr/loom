package ingest

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
	"github.com/google/uuid"
)

type indexUsageContract struct {
	collection   string
	indexType    string
	fields       []string
	storedValues []string
	queryIDs     []string
	provisioning string
}

func TestBootstrapIndexesMatchUsageContract(t *testing.T) {
	contract := readIndexUsageContract(t)
	spec := BootstrapSpecWithReporter([]string{"Patient", "Observation"}, false, nil)
	want := make(map[string]int)
	for _, entry := range contract {
		if entry.provisioning != "bootstrap" {
			continue
		}
		if len(entry.queryIDs) == 0 {
			t.Fatalf("index contract %s has no reader query IDs", entry.collection)
		}
		collections := []string{entry.collection}
		if entry.collection == "FHIR_RESOURCE" {
			collections = []string{"Patient", "Observation"}
		}
		for _, collection := range collections {
			want[indexUsageKey(collection, entry.indexType, entry.fields, entry.storedValues)]++
		}
	}
	got := make(map[string]int)
	for _, collection := range spec.Collections {
		for _, fields := range collection.Indexes {
			got[indexUsageKey(collection.Name, "persistent", fields, nil)]++
		}
		for _, index := range collection.PersistentIndexes {
			got[indexUsageKey(collection.Name, "persistent", index.Fields, index.StoredValues)]++
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrap indexes differ from docs/ARANGO_INDEX_USAGE.csv\nmissing from contract: %v\nmissing from bootstrap: %v", indexUsageDifference(got, want), indexUsageDifference(want, got))
	}
}

func TestBootstrapIndexExplainContractAgainstArango(t *testing.T) {
	owned := arangotest.Owned(t, "loom_test_ingest_index_")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := owned.Client
	if err := client.Bootstrap(ctx, BootstrapSpecWithReporter([]string{"Patient", "Observation"}, false, nil)); err != nil {
		t.Fatal(err)
	}
	project := "index-contract-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	generation := "generation-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	auth := "/programs/index-contract/projects/one"
	patientID := project + "_patient"
	observationID := project + "_observation"
	patient, err := json.Marshal(map[string]any{
		"_key": patientID, "id": "patient", "resourceType": "Patient", "project": project,
		"dataset_generation": generation, "auth_resource_path": auth, "payload": map[string]any{"id": "patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := json.Marshal(map[string]any{
		"_key": observationID, "id": "observation", "resourceType": "Observation", "project": project,
		"dataset_generation": generation, "auth_resource_path": auth, "payload": map[string]any{"id": "observation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	edge, err := json.Marshal(map[string]any{
		"_key": project + "_edge", "_from": "Observation/" + observationID, "_to": "Patient/" + patientID,
		"project": project, "dataset_generation": generation, "auth_resource_path": auth,
		"source_file": "Observation", "from_type": "Observation", "label": "subject_Patient", "to_type": "Patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	for collection, documents := range map[string][]json.RawMessage{
		"Patient": {patient}, "Observation": {observation}, EdgeCollection: {edge},
	} {
		if err := client.InsertBatchRaw(ctx, collection, documents, false, ""); err != nil {
			t.Fatalf("seed %s: %v", collection, err)
		}
	}

	tests := []struct {
		name       string
		query      string
		bindVars   map[string]any
		collection string
		want       string
		fields     []string
		covered    bool
	}{
		{
			name:       "restricted-root-page",
			query:      `FOR d IN Patient FILTER d.project == @project AND d.dataset_generation == @generation AND d.auth_resource_path == @auth SORT d._key LIMIT 1 RETURN d._key`,
			bindVars:   map[string]any{"project": project, "generation": generation, "auth": auth},
			collection: "Patient",
			want:       "persistent", fields: []string{"project", "dataset_generation", "auth_resource_path", "_key"},
		},
		{
			name:       "resource-id-generation",
			query:      `FOR d IN Patient FILTER d.project == @project AND d.dataset_generation == @generation AND d.id == @id RETURN d._key`,
			bindVars:   map[string]any{"project": project, "generation": generation, "id": "patient"},
			collection: "Patient",
			want:       "persistent", fields: []string{"project", "dataset_generation", "id"},
		},
		{
			name: "source-copy-resource-page",
			query: `FOR doc IN Patient OPTIONS { indexHint: "primary", forceIndexHint: true }
FILTER doc.project == @project AND doc.dataset_generation == @generation
FILTER doc._key > @after_key
SORT doc._key LIMIT 10
RETURN {document: doc}`,
			bindVars:   map[string]any{"project": project, "generation": generation, "after_key": ""},
			collection: "Patient",
			want:       "primary", fields: []string{"_key"},
		},
		{
			name: "source-copy-edge-page",
			query: `FOR doc IN fhir_edge OPTIONS { indexHint: "primary", forceIndexHint: true }
FILTER doc.project == @project AND doc.dataset_generation == @generation
FILTER doc._key > @after_key
SORT doc._key LIMIT 10
RETURN {document: doc}`,
			bindVars:   map[string]any{"project": project, "generation": generation, "after_key": ""},
			collection: EdgeCollection,
			want:       "primary", fields: []string{"_key"},
		},
		{
			name: "relationship-edge-count",
			query: `FOR e IN fhir_edge
FILTER e.project == @project
FILTER e.dataset_generation == @dataset_generation
FILTER @auth_resource_paths_unrestricted == true OR e.auth_resource_path IN @auth_resource_paths
FILTER e.from_type IN @resource_types AND e.to_type IN @resource_types
COLLECT auth_resource_path = e.auth_resource_path, from_type = e.from_type, label = e.label, to_type = e.to_type
WITH COUNT INTO edge_count
RETURN {project: @project, dataset_generation: @dataset_generation, auth_resource_path, from_type, label, to_type, edge_count}`,
			bindVars:   map[string]any{"project": project, "dataset_generation": generation, "auth_resource_paths_unrestricted": false, "auth_resource_paths": []string{auth}, "resource_types": []string{"Observation", "Patient"}},
			collection: EdgeCollection,
			want:       "persistent", fields: []string{"project", "dataset_generation", "auth_resource_path", "from_type", "label", "to_type"}, covered: true,
		},
		{
			name:       "source-file-check",
			query:      `FOR edge IN fhir_edge FILTER edge.project == @project AND edge.dataset_generation == @generation AND edge.source_file == @source_file LIMIT 1 RETURN edge._key`,
			bindVars:   map[string]any{"project": project, "generation": generation, "source_file": "Observation"},
			collection: EdgeCollection,
			want:       "persistent", fields: []string{"project", "dataset_generation", "auth_resource_path", "from_type", "label", "to_type"},
		},
		{
			name: "graph-outbound-walk",
			query: `FOR node, edge IN 1..1 OUTBOUND CONCAT("Observation/", @id) fhir_edge
FILTER edge.project == @project AND edge.dataset_generation == @generation
FILTER edge.auth_resource_path == @auth AND edge.label == "subject_Patient" AND edge.to_type == "Patient"
RETURN node._id`,
			bindVars:   map[string]any{"project": project, "generation": generation, "auth": auth, "id": observationID},
			collection: EdgeCollection,
			want:       "edge", fields: []string{"_from"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := client.Explain(ctx, arango.ExplainRequest{Query: test.query, BindVars: test.bindVars})
			if err != nil {
				t.Fatalf("explain query: %v", err)
			}
			assessment := arango.AssessExplainResult(result)
			if len(assessment.FullCollectionScans) != 0 {
				t.Fatalf("query used full collection scans: %+v", assessment.FullCollectionScans)
			}
			found := false
			for _, use := range assessment.Indexes {
				if use.Collection == test.collection && use.Type == test.want && reflect.DeepEqual(use.Fields, test.fields) {
					found = true
				}
			}
			if !found {
				t.Fatalf("query index use = %+v, want %s %v", assessment.Indexes, test.want, test.fields)
			}
			if test.covered {
				plan := result.Plan
				if plan == nil && len(result.Plans) > 0 {
					plan = &result.Plans[0]
				}
				if plan == nil {
					t.Fatal("edge count explain has no plan")
				}
				for _, node := range plan.Nodes {
					if node.Collection == EdgeCollection && node.Type == "IndexNode" &&
						(node.IndexCoversProjections == nil || !*node.IndexCoversProjections || node.IndexCoversOutProjections == nil || !*node.IndexCoversOutProjections) {
						t.Fatalf("edge count index does not cover projections: %+v", node)
					}
					if node.Collection == EdgeCollection && node.Type == "MaterializeNode" {
						t.Fatalf("edge count materialized edge documents: %+v", node)
					}
				}
			}
		})
	}
}

func readIndexUsageContract(t *testing.T) []indexUsageContract {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "ARANGO_INDEX_USAGE.csv")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open index usage contract: %v", err)
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = 6
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read index usage contract: %v", err)
	}
	if len(records) < 2 || strings.Join(records[0], ",") != "collection,index_type,fields,stored_values,query_ids,provisioning" {
		t.Fatalf("unexpected index usage contract header: %v", records[0])
	}
	entries := make([]indexUsageContract, 0, len(records)-1)
	for _, record := range records[1:] {
		entry := indexUsageContract{
			collection: record[0], indexType: record[1], fields: splitIndexUsageList(record[2]),
			storedValues: splitIndexUsageList(record[3]), queryIDs: splitIndexUsageList(record[4]), provisioning: record[5],
		}
		if entry.collection == "" || entry.indexType == "" || len(entry.fields) == 0 || len(entry.queryIDs) == 0 {
			t.Fatalf("incomplete index usage contract row: %v", record)
		}
		entries = append(entries, entry)
	}
	return entries
}

func splitIndexUsageList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func indexUsageKey(collection, indexType string, fields, storedValues []string) string {
	return strings.Join([]string{collection, indexType, strings.Join(fields, ";"), strings.Join(storedValues, ";")}, "\x00")
}

func indexUsageDifference(left, right map[string]int) []string {
	difference := make([]string, 0)
	for key, count := range left {
		if count > right[key] {
			difference = append(difference, key)
		}
	}
	return difference
}
