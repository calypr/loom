package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type resourceInventoryWrite struct {
	collection string
	recordType string
	buildID    string
}

type resourceInventoryTestClient struct {
	collections  map[string]bool
	sourceCounts map[string][]catalog.ResourceInventoryCount
	markerRows   []map[string]any
	countRows    []catalog.ResourceInventoryCount
	queries      []string
	vars         []map[string]any
	writes       []resourceInventoryWrite
}

func inventoryTestRow(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func (c *resourceInventoryTestClient) CollectionExists(_ context.Context, name string) (bool, error) {
	if c.collections == nil {
		return true, nil
	}
	exists, found := c.collections[name]
	if !found {
		return true, nil
	}
	return exists, nil
}

func (c *resourceInventoryTestClient) QueryRows(_ context.Context, query string, _ int, vars map[string]interface{}, visit store.RowVisitor) error {
	c.queries = append(c.queries, query)
	copyVars := make(map[string]any, len(vars))
	for key, value := range vars {
		copyVars[key] = value
	}
	c.vars = append(c.vars, copyVars)
	switch query {
	case rebuildResourceInventoryCountsAQL:
		resourceType := vars["@resource_collection"].(string)
		for _, count := range c.sourceCounts[resourceType] {
			if err := visit(map[string]any{"auth_resource_path": count.AuthResourcePath, "document_count": count.DocumentCount}); err != nil {
				return err
			}
		}
	case readResourceInventoryBuildAQL:
		for _, row := range c.markerRows {
			if err := visit(row); err != nil {
				return err
			}
		}
	case readResourceInventoryCountsAQL:
		allowed := make(map[string]struct{})
		for _, value := range vars["auth_resource_paths"].([]string) {
			allowed[value] = struct{}{}
		}
		types := make(map[string]struct{})
		for _, value := range vars["resource_types"].([]string) {
			types[value] = struct{}{}
		}
		sums := make(map[string]int64)
		for _, count := range c.countRows {
			if count.BuildID != vars["build_id"] || count.Project != vars["project"] || count.DatasetGeneration != vars["dataset_generation"] {
				continue
			}
			if _, ok := types[count.ResourceType]; !ok {
				continue
			}
			if vars["auth_resource_paths_unrestricted"] != true {
				if _, ok := allowed[count.AuthResourcePath]; !ok {
					continue
				}
			}
			sums[count.ResourceType] += count.DocumentCount
		}
		for resourceType, count := range sums {
			if err := visit(map[string]any{"resource_type": resourceType, "document_count": count}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *resourceInventoryTestClient) InsertBatchRaw(_ context.Context, collection string, docs []json.RawMessage, _ bool, _ string) error {
	for _, encoded := range docs {
		var record struct {
			RecordType        string `json:"record_type"`
			Project           string `json:"project"`
			DatasetGeneration string `json:"dataset_generation"`
			AuthResourcePath  string `json:"auth_resource_path"`
			ResourceType      string `json:"resource_type"`
			BuildID           string `json:"build_id"`
			DocumentCount     int64  `json:"document_count"`
		}
		if err := json.Unmarshal(encoded, &record); err != nil {
			return err
		}
		c.writes = append(c.writes, resourceInventoryWrite{collection: collection, recordType: record.RecordType, buildID: record.BuildID})
		if record.RecordType == catalog.ResourceInventoryBuildRecordType {
			var build catalog.ResourceInventoryBuild
			if err := json.Unmarshal(encoded, &build); err != nil {
				return err
			}
			c.markerRows = []map[string]any{inventoryTestRowFromJSON(encoded)}
		} else {
			c.countRows = append(c.countRows, catalog.ResourceInventoryCount{
				Project: record.Project, DatasetGeneration: record.DatasetGeneration, AuthResourcePath: record.AuthResourcePath,
				ResourceType: record.ResourceType, BuildID: record.BuildID, DocumentCount: record.DocumentCount,
			})
		}
	}
	return nil
}

func inventoryTestRowFromJSON(encoded []byte) map[string]any {
	var row map[string]any
	_ = json.Unmarshal(encoded, &row)
	return row
}

func (*resourceInventoryTestClient) ExecuteAQL(context.Context, string, map[string]interface{}) error {
	return nil
}
func (*resourceInventoryTestClient) Bootstrap(context.Context, store.BootstrapSpec) error { return nil }

func TestResourceInventoryRebuildGroupsSourceRowsAndPublishesMarkerLast(t *testing.T) {
	client := &resourceInventoryTestClient{
		sourceCounts: map[string][]catalog.ResourceInventoryCount{
			"Patient": {
				{AuthResourcePath: "scope-b", DocumentCount: 3},
				{AuthResourcePath: "scope-a", DocumentCount: 2},
			},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := adapter.RebuildResourceInventory(context.Background(), catalog.ResourceInventoryRebuildOptions{
		Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Observation", "Patient"}, BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.BuildID == "" || summary.CoverageDigest == "" || summary.ResourceTypeCount != 2 || summary.CountRowCount != 3 || summary.DocumentCount != 5 {
		t.Fatalf("rebuild summary = %#v", summary)
	}
	var sourceQueries int
	for _, query := range client.queries {
		if query == rebuildResourceInventoryCountsAQL {
			sourceQueries++
			if !strings.Contains(query, "COLLECT auth_resource_path") || !strings.Contains(query, "WITH COUNT INTO document_count") {
				t.Fatalf("source query is not grouped by authorization path: %s", query)
			}
		}
	}
	if sourceQueries != 2 {
		t.Fatalf("source scans = %d, want one per type", sourceQueries)
	}
	if len(client.writes) != 4 || client.writes[len(client.writes)-1].recordType != catalog.ResourceInventoryBuildRecordType {
		t.Fatalf("write order = %#v", client.writes)
	}
	for _, write := range client.writes[:len(client.writes)-1] {
		if write.collection != catalog.ResourceInventoryCollection || write.recordType != catalog.ResourceInventoryCountRecordType || write.buildID != summary.BuildID {
			t.Fatalf("count write = %#v", write)
		}
	}
	if client.writes[len(client.writes)-1].buildID != summary.BuildID {
		t.Fatalf("published marker = %#v", client.writes[len(client.writes)-1])
	}
}

func TestResourceInventoryReadSumsOnlyAuthorizedPaths(t *testing.T) {
	countInput := []catalog.ResourceInventoryCount{
		{Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope-a", ResourceType: "Patient", DocumentCount: 2},
		{Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope-b", ResourceType: "Patient", DocumentCount: 5},
		{Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope-a", ResourceType: "Observation", DocumentCount: 0},
	}
	build, counts, err := catalog.NewResourceInventoryBuild("p", "g", []string{"Patient", "Observation"}, countInput)
	if err != nil {
		t.Fatal(err)
	}
	client := &resourceInventoryTestClient{markerRows: []map[string]any{inventoryTestRow(t, build)}, countRows: counts}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{
		Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient", "Observation"},
		AuthResourcePaths: []string{"scope-a"}, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil || result.Status != catalog.EvidenceAvailable || len(result.Values) != 2 {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	got := map[string]int64{}
	for _, value := range result.Values {
		got[value.ResourceType] = value.DocumentCount
	}
	if !reflect.DeepEqual(got, map[string]int64{"Patient": 2, "Observation": 0}) {
		t.Fatalf("authorized inventory totals = %#v", got)
	}
	if len(client.queries) != 2 || client.queries[1] != readResourceInventoryCountsAQL {
		t.Fatalf("projection queries = %#v", client.queries)
	}
	if client.vars[1]["build_id"] != build.BuildID || client.vars[1]["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("projection bind variables = %#v", client.vars[1])
	}
}

func TestResourceInventoryRestrictedEmptyScopeReturnsNoRows(t *testing.T) {
	build, counts, err := catalog.NewResourceInventoryBuild("p", "g", []string{"Patient"}, []catalog.ResourceInventoryCount{{
		Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope-a", ResourceType: "Patient", DocumentCount: 9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	client := &resourceInventoryTestClient{markerRows: []map[string]any{inventoryTestRow(t, build)}, countRows: counts}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{
		Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}, AuthResourcePaths: []string{},
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil || !result.Available || !result.Complete || result.Status != catalog.EvidenceEmpty || len(result.Values) != 0 {
		t.Fatalf("restricted empty inventory = %#v err=%v", result, err)
	}
	if len(client.queries) != 1 || client.queries[0] != readResourceInventoryBuildAQL {
		t.Fatalf("restricted empty scope queried counts or source: %#v", client.queries)
	}
}

func TestResourceInventoryStaleMarkerIsUnavailable(t *testing.T) {
	build, _, err := catalog.NewResourceInventoryBuild("p", "g", []string{"Patient"}, []catalog.ResourceInventoryCount{{
		Project: "p", DatasetGeneration: "g", ResourceType: "Patient", DocumentCount: 0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	build.Complete = false
	client := &resourceInventoryTestClient{markerRows: []map[string]any{inventoryTestRow(t, build)}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if !errors.Is(err, ErrResourceInventoryBuildUnavailable) || result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable || len(result.Values) != 0 {
		t.Fatalf("stale inventory = %#v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "arango-fhir-proto rebuild-resource-inventory --generation ID") {
		t.Fatalf("stale-marker error is not actionable: %v", err)
	}
	if len(client.queries) != 1 || client.queries[0] != readResourceInventoryBuildAQL {
		t.Fatalf("stale marker queried counts or source: %#v", client.queries)
	}
}

func TestFieldEnrichmentCoverageSeparatesLegacyUnknownFromIncomplete(t *testing.T) {
	legacyClient := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"project": "p", "dataset_generation": "g", "resource_type": "Patient", "path": "id", "kind": "scalar", "doc_count": int64(1), "sample_count": int64(1),
	}}}}
	legacyStore, _ := New(legacyClient)
	legacy, err := legacyStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if err != nil || legacy.FieldCoverageKnown || !legacy.Complete || len(legacy.Values) != 1 {
		t.Fatalf("legacy field profile = %#v err=%v", legacy, err)
	}

	explicitClient := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"project": "p", "dataset_generation": "g", "resource_type": "Patient", "path": "id", "kind": "scalar", "doc_count": int64(1), "sample_count": int64(1),
		"enrichment_complete": true, "enrichment_truncated": false,
	}}}}
	explicitStore, _ := New(explicitClient)
	explicit, err := explicitStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if err != nil || !explicit.FieldCoverageKnown || !explicit.Complete || explicit.Digest == legacy.Digest {
		t.Fatalf("explicit field profile = %#v err=%v", explicit, err)
	}

	truncatedClient := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"record_type": catalog.FieldProfileStatusRecordType, "project": "p", "dataset_generation": "g", "resource_type": "Patient",
		"enrichment_complete": false, "enrichment_truncated": true,
	}}}}
	truncatedStore, _ := New(truncatedClient)
	truncated, err := truncatedStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if err != nil || !truncated.FieldCoverageKnown || truncated.Complete || !truncated.Truncated || len(truncated.Values) != 0 {
		t.Fatalf("truncated zero-row field profile = %#v err=%v", truncated, err)
	}

	emptyStore, _ := New(&evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: nil}})
	empty, err := emptyStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if err != nil || empty.FieldCoverageKnown || !empty.Complete {
		t.Fatalf("empty profile without marker = %#v err=%v", empty, err)
	}
}
