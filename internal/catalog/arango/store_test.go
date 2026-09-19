package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	store "github.com/calypr/loom/internal/store/arango"
)

type fakeClient struct {
	queries []string
	row     map[string]any
}

type evidenceClient struct {
	queries          []string
	vars             []map[string]any
	rows             map[string][]map[string]any
	rowsByCollection map[string][]map[string]any
	collections      map[string]bool
	collectionChecks []string
	collectionErr    error
	err              error
}

func (f *evidenceClient) CollectionExists(_ context.Context, name string) (bool, error) {
	f.collectionChecks = append(f.collectionChecks, name)
	if f.collectionErr != nil {
		return false, f.collectionErr
	}
	if f.collections == nil {
		return true, nil
	}
	return f.collections[name], nil
}

func (f *evidenceClient) QueryRows(_ context.Context, query string, _ int, vars map[string]any, visit store.RowVisitor) error {
	f.queries = append(f.queries, query)
	copyVars := make(map[string]any, len(vars))
	for key, value := range vars {
		copyVars[key] = value
	}
	f.vars = append(f.vars, copyVars)
	if f.err != nil {
		return f.err
	}
	rows := f.rows[query]
	if query == resourceInventoryAQL && f.rowsByCollection != nil {
		rows = f.rowsByCollection[stringValue(vars["@resource_collection"])]
	}
	for _, row := range rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}
func (*evidenceClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}
func (*evidenceClient) ExecuteAQL(context.Context, string, map[string]any) error { return nil }
func (*evidenceClient) Bootstrap(context.Context, store.BootstrapSpec) error     { return nil }

func (*fakeClient) CollectionExists(context.Context, string) (bool, error) { return true, nil }

func (f *fakeClient) QueryRows(_ context.Context, query string, _ int, _ map[string]any, visit store.RowVisitor) error {
	f.queries = append(f.queries, query)
	if query == populatedFieldsAQL || query == relationshipAuditAQL {
		row := f.row
		if row == nil {
			if query == relationshipAuditAQL {
				row = map[string]any{"from_type": "qualification", "label": "link", "to_type": "issuer", "edge_count": int64(2)}
			} else {
				row = map[string]any{"project": "p", "resource_type": "Patient", "path": "id", "doc_count": int64(1), "sample_count": int64(1)}
			}
		}
		return visit(row)
	}
	return nil
}
func (*fakeClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}
func (*fakeClient) ExecuteAQL(context.Context, string, map[string]any) error { return nil }
func (*fakeClient) Bootstrap(context.Context, store.BootstrapSpec) error     { return nil }

func TestStoreUsesInjectedClient(t *testing.T) {
	client := &fakeClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"})
	if err != nil || len(fields) != 1 || fields[0].Path != "id" {
		t.Fatalf("fields=%#v err=%v", fields, err)
	}
	if len(client.queries) != 1 {
		t.Fatalf("queries=%d", len(client.queries))
	}
}

func TestStorePreservesFieldMetadataAndRejectsMalformedRows(t *testing.T) {
	client := &fakeClient{row: map[string]any{
		"project": "p", "resource_type": "Patient", "path": "code", "doc_count": int64(2), "sample_count": int64(1),
		"distinct_values": []any{"A"}, "distinct_truncated": true, "pivot_candidate": true,
		"pivot_kind": "code", "pivot_columns": []any{"A"}, "pivot_family": "family",
		"pivot_column_selector": "coding.display", "pivot_value_selector": "coding.code",
		"pivot_item_source": "item", "pivot_item_resource_type": "Observation", "pivot_value_selectors": []any{"code"},
		"extension_values": []any{map[string]any{"url": "http://example.org/source_path", "source_path": "extension[]", "value_path": "valueUrl", "value_type": "string"}},
	}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	field := fields[0]
	if !field.PivotCandidate || field.PivotKind != "code" || len(field.DistinctValues) != 1 || field.PivotItemResourceType != "Observation" || len(field.ExtensionValues) != 1 || field.ExtensionValues[0].ValuePath != "valueUrl" {
		t.Fatalf("field metadata = %#v", field)
	}

	client.row["doc_count"] = "bad"
	if _, err := adapter.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{Project: "p"}); err == nil {
		t.Fatal("malformed numeric field row was accepted")
	}
}

func TestStoreAuditsInvalidRelationshipEndpoints(t *testing.T) {
	adapter, err := New(&fakeClient{})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := adapter.AuditRelationshipEdges(context.Background(), catalog.RelationshipAuditOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if summary.InvalidEdgeCount != 2 || len(summary.InvalidRelations) != 1 || summary.InvalidRelations[0].FromType != "qualification" || summary.InvalidRelations[0].ToType != "issuer" {
		t.Fatalf("audit summary = %#v", summary)
	}
}

func TestReadRetainedSemanticInventoryPageUsesScopedKeysetAndPreservesAuthPath(t *testing.T) {
	client := &evidenceClient{
		rows: map[string][]map[string]any{
			retainedSemanticInventorySourcePageAQL: {{
				"key": "source-2", "auth_resource_path": "scope-a",
				"payload": map[string]any{"resourceType": "Observation", "id": "obs-2"},
			}},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "source-1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Key != "source-2" || page.Rows[0].AuthResourcePath == nil || *page.Rows[0].AuthResourcePath != "scope-a" {
		t.Fatalf("retained source page = %+v", page)
	}
	var payload map[string]any
	if err := json.Unmarshal(page.Rows[0].Payload, &payload); err != nil || payload["resourceType"] != "Observation" {
		t.Fatalf("retained source payload = %s, err %v", page.Rows[0].Payload, err)
	}
	if len(client.vars) != 1 || client.vars[0]["project"] != "project" || client.vars[0]["dataset_generation"] != "generation" || client.vars[0]["after_key"] != "source-1" || client.vars[0]["@collection"] != "Observation" {
		t.Fatalf("retained source bind vars = %+v", client.vars)
	}
	query := client.queries[0]
	for _, fragment := range []string{"FILTER d.project == @project", "FILTER d.dataset_generation == @dataset_generation", "d._key > @after_key", "SORT d._key", "RETURN {key: d._key"} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("retained source query missing %q: %s", fragment, query)
		}
	}
}

func TestReadMissingRetainedSourceCollectionDoesNotQueryOrCreateIt(t *testing.T) {
	client := &evidenceClient{collections: map[string]bool{"Observation": false}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "", 100)
	if err != nil || len(page.Rows) != 0 || len(client.queries) != 0 {
		t.Fatalf("absent retained collection page=%+v err=%v queries=%d", page, err, len(client.queries))
	}
}

func TestReadMissingRetainedSourceFailsWhenGenerationFieldProfileProvesPopulation(t *testing.T) {
	client := &evidenceClient{
		collections: map[string]bool{"Observation": false, catalog.FieldCatalogCollection: true},
		rows: map[string][]map[string]any{
			retainedSemanticInventoryFieldProfileAQL: {{"doc_count": 1}},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "", 100)
	if !errors.Is(err, ErrRetainedSemanticSourceMissing) {
		t.Fatalf("missing profiled source error = %v, want ErrRetainedSemanticSourceMissing", err)
	}
	if len(client.queries) != 1 || client.queries[0] != retainedSemanticInventoryFieldProfileAQL {
		t.Fatalf("missing source evidence queries = %v", client.queries)
	}
}

func TestReadEmptyInitialRetainedSourceFailsWhenGenerationFieldProfileProvesPopulation(t *testing.T) {
	client := &evidenceClient{
		collections: map[string]bool{"Observation": true, catalog.FieldCatalogCollection: true},
		rows: map[string][]map[string]any{
			retainedSemanticInventoryFieldProfileAQL: {{"doc_count": 12}},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "", 100)
	if !errors.Is(err, ErrRetainedSemanticSourceMissing) {
		t.Fatalf("empty profiled source error = %v, want ErrRetainedSemanticSourceMissing", err)
	}
	if !strings.Contains(retainedSemanticInventoryFieldProfileAQL, "RETURN {doc_count: d.doc_count}") {
		t.Fatalf("field-profile query must return an object row for QueryRows: %s", retainedSemanticInventoryFieldProfileAQL)
	}
}

func TestReadEmptyRetainedSourceWithoutProfileEvidenceIsGenuinelyEmpty(t *testing.T) {
	client := &evidenceClient{
		collections: map[string]bool{"Observation": true, catalog.FieldCatalogCollection: true},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "", 100)
	if err != nil || !page.SourceExists || len(page.Rows) != 0 {
		t.Fatalf("genuinely empty retained source page=%+v err=%v", page, err)
	}
	if len(client.queries) != 2 || client.queries[0] != retainedSemanticInventorySourcePageAQL || client.queries[1] != retainedSemanticInventoryFieldProfileAQL {
		t.Fatalf("empty source evidence queries = %v", client.queries)
	}
}

func TestReadExhaustedRetainedSourceDoesNotTreatPositiveProfileAsMissing(t *testing.T) {
	client := &evidenceClient{
		collections: map[string]bool{"Observation": true, catalog.FieldCatalogCollection: true},
		rows: map[string][]map[string]any{
			retainedSemanticInventoryFieldProfileAQL: {{"doc_count": 12}},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.ReadRetainedSemanticInventoryPage(context.Background(), "project", "generation", "Observation", "source-z", 100)
	if err != nil || !page.SourceExists || len(page.Rows) != 0 {
		t.Fatalf("exhausted retained source page=%+v err=%v, want empty without rechecking source population", page, err)
	}
	if len(client.queries) != 1 || client.queries[0] != retainedSemanticInventorySourcePageAQL {
		t.Fatalf("exhausted source unexpectedly queried population evidence: %v", client.queries)
	}
}

func TestSemanticInventoryBackfillMutationQueriesBindOnlyUsedVariables(t *testing.T) {
	build := catalog.NewSemanticInventoryBuild("project", "generation", "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	build.SourceAvailability = catalog.SemanticInventorySourceAvailabilityVerified
	build.LeaseToken = "owner"
	build.SourceCheckpoint = catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "a"}
	encoded, err := json.Marshal(build)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(encoded, &row); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		query string
		call  func(*Store) error
		want  []string
	}{
		{
			name:  "advance",
			query: advanceSemanticInventoryBackfillAQL,
			call: func(adapter *Store) error {
				_, err := adapter.AdvanceSemanticInventoryBackfill(context.Background(), build, "owner", build.SourceCheckpoint, catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "b"}, 7, time.Minute)
				return err
			},
			want: []string{"expected_collection", "expected_key", "key", "lease_expires_at", "next_collection", "next_key", "now", "scanned_resources", "source_availability", "token"},
		},
		{
			name:  "complete",
			query: completeSemanticInventoryBackfillAQL,
			call: func(adapter *Store) error {
				_, err := adapter.CompleteSemanticInventoryBackfill(context.Background(), build, "owner", build.SourceCheckpoint, 7)
				return err
			},
			want: []string{"expected_collection", "expected_key", "key", "now", "scanned_resources", "source_availability", "token"},
		},
		{
			name:  "fail",
			query: failSemanticInventoryBackfillAQL,
			call: func(adapter *Store) error {
				return adapter.FailSemanticInventoryBackfill(context.Background(), build, "owner", "failed")
			},
			want: []string{"diagnostic", "key", "token"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &evidenceClient{rows: map[string][]map[string]any{test.query: {row}}}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(adapter); err != nil {
				t.Fatal(err)
			}
			if len(client.vars) != 1 {
				t.Fatalf("bind variable captures = %d, want 1", len(client.vars))
			}
			got := make([]string, 0, len(client.vars[0]))
			for key := range client.vars[0] {
				got = append(got, key)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("bind variables = %v, want exactly %v", got, test.want)
			}
		})
	}
}

func TestRelationshipRebuildFiltersNonResourceEndpoints(t *testing.T) {
	if !strings.Contains(relationshipRebuildAQL, "e.from_type IN @resource_types") || !strings.Contains(relationshipRebuildAQL, "e.to_type IN @resource_types") {
		t.Fatal("relationship rebuild does not filter invalid resource endpoints")
	}
}

func TestCapabilityEvidenceRelationshipPreservesBuilderAndStorageProvenance(t *testing.T) {
	client := &evidenceClient{rows: map[string][]map[string]any{relationshipObservationsAQL: {{
		"project": "p", "dataset_generation": "g", "auth_resource_path": "scope",
		"storage_from_type": "Condition", "label": "subject_Patient", "storage_to_type": "Patient", "edge_count": int64(7),
	}}}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{Project: "p", DatasetGeneration: "g", AuthResourcePaths: []string{}, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false), ResourceTypes: []string{"Patient", "Condition"}})
	if err != nil || len(result.Values) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	got := result.Values[0]
	if got.FromType != "Patient" || got.ToType != "Condition" || got.BuilderDirection != "INBOUND" || got.StorageFromType != "Condition" || got.StorageToType != "Patient" || got.StorageDirection != "OUTBOUND" || got.EdgeCount != 7 {
		t.Fatalf("provenance = %#v", got)
	}
	if vars := client.vars[0]; vars["project"] != "p" || vars["dataset_generation"] != "g" || vars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("identity binds = %#v", vars)
	}
	if paths, ok := client.vars[0]["auth_resource_paths"].([]string); !ok || len(paths) != 0 {
		t.Fatalf("restricted-empty paths bind = %#v", client.vars[0]["auth_resource_paths"])
	}
	for _, name := range []string{"@project", "@dataset_generation", "@auth_resource_paths", "@auth_resource_paths_unrestricted", "COLLECT", "SUM(d.edge_count)"} {
		if !strings.Contains(client.queries[0], name) {
			t.Fatalf("relationship query missing %q: %s", name, client.queries[0])
		}
	}
}

func TestCapabilityEvidenceInventoryBindsCollectionAndIdentity(t *testing.T) {
	client := &evidenceClient{rows: map[string][]map[string]any{resourceInventoryAQL: {{
		"project": "p", "dataset_generation": "g", "resource_type": "Patient", "document_count": int64(4),
	}}}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g", AuthResourcePaths: []string{}, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false), ResourceTypes: []string{"Patient"}})
	if err != nil || len(result.Values) != 1 || result.Values[0].DocumentCount != 4 {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	vars := client.vars[0]
	if vars["project"] != "p" || vars["dataset_generation"] != "g" || vars["@resource_collection"] != "Patient" || vars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("inventory binds = %#v", vars)
	}
	if _, exists := vars["resource_collection"]; exists {
		t.Fatalf("collection bind used value-variable syntax: %#v", vars)
	}
	for _, name := range []string{"@@resource_collection", "@project", "@dataset_generation", "@auth_resource_paths", "@auth_resource_paths_unrestricted", "COLLECT WITH COUNT"} {
		if !strings.Contains(client.queries[0], name) {
			t.Fatalf("inventory query missing %q: %s", name, client.queries[0])
		}
	}
}

func TestCapabilityEvidenceInventoryTreatsMissingCollectionAsEmpty(t *testing.T) {
	client := &evidenceClient{
		collections: map[string]bool{"Patient": true},
		rowsByCollection: map[string][]map[string]any{"Patient": {{
			"project": "p", "dataset_generation": "g", "resource_type": "Patient", "document_count": int64(7),
		}}},
	}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{
		Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient", "DiagnosticReport"},
	})
	if err != nil || !result.Available || !result.Complete || len(result.Values) != 2 {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	counts := map[string]int64{}
	for _, value := range result.Values {
		counts[value.ResourceType] = value.DocumentCount
	}
	if counts["Patient"] != 7 || counts["DiagnosticReport"] != 0 {
		t.Fatalf("inventory counts = %#v", counts)
	}
	if len(client.collectionChecks) != 2 || len(client.queries) != 1 {
		t.Fatalf("collection checks=%#v queries=%d", client.collectionChecks, len(client.queries))
	}
}

func TestCapabilityEvidenceInventoryCollectionLookupFailureIsUnavailable(t *testing.T) {
	client := &evidenceClient{collectionErr: errors.New("database unavailable")}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", ResourceTypes: []string{"Patient"}})
	if err == nil || result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "RESOURCE_INVENTORY_UNAVAILABLE" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestCapabilityEvidenceFieldEmptyAndFailureAreDistinct(t *testing.T) {
	emptyClient := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: nil}}
	emptyStore, _ := New(emptyClient)
	empty, err := emptyStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p"})
	if err != nil || empty.Status != catalog.EvidenceEmpty || !empty.Available || !empty.Complete {
		t.Fatalf("empty enrichment = %#v err=%v", empty, err)
	}
	failingClient := &evidenceClient{err: errors.New("catalog unavailable")}
	failingStore, _ := New(failingClient)
	failing, err := failingStore.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p"})
	if err == nil || failing.Status != catalog.EvidenceUnavailable || failing.Available || failing.Complete {
		t.Fatalf("failed enrichment = %#v err=%v", failing, err)
	}
}

func TestCapabilityEvidenceFieldSuggestionTruncationIsPerField(t *testing.T) {
	client := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"project": "p", "resource_type": "Patient", "path": "name", "kind": "scalar", "doc_count": int64(1), "sample_count": int64(1), "distinct_values": []any{"a"}, "distinct_truncated": true,
	}}}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p"})
	if err != nil || result.Status != catalog.EvidenceAvailable || !result.Complete || result.Truncated || len(result.Values) != 1 || !result.Values[0].DistinctTruncated {
		t.Fatalf("per-field truncation = %#v err=%v", result, err)
	}
}

func TestCapabilityEvidenceFieldEnrichmentDecodesMaxItems(t *testing.T) {
	client := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"project": "p", "resource_type": "Patient", "path": "name", "kind": "array", "doc_count": int64(1), "max_items": int64(8), "sample_count": int64(1),
	}}}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Values) != 1 || result.Values[0].MaxItems != 8 {
		t.Fatalf("max items = %#v", result.Values)
	}
}

func TestCapabilityEvidenceFieldEnrichmentPreservesSemanticObservations(t *testing.T) {
	client := &evidenceClient{rows: map[string][]map[string]any{fieldEnrichmentAQL: {{
		"project": "p", "resource_type": "Observation", "path": "component[]", "kind": "array", "doc_count": int64(2), "sample_count": int64(2),
		"semantic_observations": []any{map[string]any{
			"schema_version": int64(2), "owning_scope": "component[]", "choice_arm": "valueQuantity", "logical_type": "decimal",
			"completeness": "complete", "status": "supported", "population": int64(2), "examples": []any{"111", "222"},
			"source": map[string]any{"canonical": "Observation.component", "type": "Observation", "profile": "http://hl7.org/fhir/StructureDefinition/Observation", "path": "component"},
			"key":    map[string]any{"selector": "code.coding[]", "system": "urn:study:A", "code": "shared", "display": "Shared"},
			"value":  map[string]any{"selector": "valueQuantity.value", "type": "decimal"},
		}},
	}}}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Values) != 1 || len(result.Values[0].SemanticObservations) != 1 {
		t.Fatalf("semantic observations = %#v", result.Values)
	}
	observation := result.Values[0].SemanticObservations[0]
	if observation.Key.System != "urn:study:A" || observation.Key.Code != "shared" || observation.Value.Selector != "valueQuantity.value" || observation.LogicalType != "decimal" || observation.Population != 2 {
		t.Fatalf("semantic observation = %#v", observation)
	}
}

func TestPageSemanticInventoryTreatsMissingCollectionsAsUnknown(t *testing.T) {
	client := &evidenceClient{collections: map[string]bool{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.PageSemanticInventory(context.Background(), catalog.SemanticInventoryPageOptions{
		Project: "p", DatasetGeneration: "legacy-generation",
	})
	if err != nil || page.State != catalog.SemanticInventoryUnknown || len(page.Entries) != 0 {
		t.Fatalf("page = %#v err=%v, want unknown empty inventory", page, err)
	}
	if len(client.queries) != 0 {
		t.Fatalf("queries = %d, want no AQL against absent legacy collections", len(client.queries))
	}
}
