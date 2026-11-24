package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog/fieldcatalog"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit"
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
	fields, err := adapter.DiscoverFields(context.Background(), fieldcatalog.PopulatedFieldOptions{Project: "p"})
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
	fields, err := adapter.DiscoverFields(context.Background(), fieldcatalog.PopulatedFieldOptions{Project: "p"})
	if err != nil {
		t.Fatal(err)
	}
	field := fields[0]
	if !field.PivotCandidate || field.PivotKind != "code" || len(field.DistinctValues) != 1 || field.PivotItemResourceType != "Observation" || len(field.ExtensionValues) != 1 || field.ExtensionValues[0].ValuePath != "valueUrl" {
		t.Fatalf("field metadata = %#v", field)
	}

	client.row["doc_count"] = "bad"
	if _, err := adapter.DiscoverFields(context.Background(), fieldcatalog.PopulatedFieldOptions{Project: "p"}); err == nil {
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

func TestCapabilityEvidenceInventoryBindsBuildAndAuthorization(t *testing.T) {
	build, _, err := catalog.NewResourceInventoryBuild("p", "g", []string{"Patient"}, []catalog.ResourceInventoryCount{{
		Project: "p", DatasetGeneration: "g", AuthResourcePath: "scope-a", ResourceType: "Patient", DocumentCount: 4,
	}})
	if err != nil {
		t.Fatal(err)
	}
	marker := testkit.JSONMap(t, build)
	client := &evidenceClient{rows: map[string][]map[string]any{
		readResourceInventoryBuildAQL:  {marker},
		readResourceInventoryCountsAQL: {{"resource_type": "Patient", "document_count": int64(4)}},
	}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g", AuthResourcePaths: []string{"scope-a"}, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false), ResourceTypes: []string{"Patient"}})
	if err != nil || len(result.Values) != 1 || result.Values[0].DocumentCount != 4 || !result.Available || !result.Complete {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	if len(client.queries) != 2 || client.queries[0] != readResourceInventoryBuildAQL || client.queries[1] != readResourceInventoryCountsAQL {
		t.Fatalf("inventory queries = %#v", client.queries)
	}
	vars := client.vars[1]
	if vars["project"] != "p" || vars["dataset_generation"] != "g" || vars["build_id"] != build.BuildID || vars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("inventory binds = %#v", vars)
	}
	if paths, ok := vars["auth_resource_paths"].([]string); !ok || !reflect.DeepEqual(paths, []string{"scope-a"}) {
		t.Fatalf("authorized paths = %#v", vars["auth_resource_paths"])
	}
	for _, query := range client.queries {
		if strings.Contains(query, "@@resource_collection") || strings.Contains(query, "COLLECT WITH COUNT") {
			t.Fatalf("discovery queried a source resource collection: %s", query)
		}
	}
}

func TestCapabilityEvidenceInventoryMissingMarkerIsUnavailable(t *testing.T) {
	client := &evidenceClient{collections: map[string]bool{"Patient": true, catalog.ResourceInventoryCollection: true}}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", DatasetGeneration: "g", ResourceTypes: []string{"Patient"}})
	if !errors.Is(err, ErrResourceInventoryBuildUnavailable) || result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable || len(result.Values) != 0 {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "arango-fhir-proto rebuild-resource-inventory --generation ID") {
		t.Fatalf("missing-marker error is not actionable: %v", err)
	}
	if len(client.queries) != 1 || client.queries[0] != readResourceInventoryBuildAQL || len(client.collectionChecks) != 1 || client.collectionChecks[0] != catalog.ResourceInventoryCollection {
		t.Fatalf("missing marker queries=%#v collections=%#v", client.queries, client.collectionChecks)
	}
}

func TestCapabilityEvidenceInventoryCollectionLookupFailureIsUnavailable(t *testing.T) {
	client := &evidenceClient{collectionErr: errors.New("database unavailable")}
	adapter, _ := New(client)
	result, err := adapter.DiscoverResourceInventory(context.Background(), catalog.ResourceInventoryOptions{Project: "p", ResourceTypes: []string{"Patient"}})
	if err == nil || result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "RESOURCE_INVENTORY_UNAVAILABLE" || len(client.queries) != 0 {
		t.Fatalf("diagnostics = %#v queries=%#v", result.Diagnostics, client.queries)
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
			"schema_version": int64(semantic.ObservationSchemaVersion), "owning_scope": "component[]", "choice_arm": "valueCodeableConcept", "logical_type": "string",
			"completeness": "complete", "status": "supported", "population": int64(2), "examples": []any{"Stage I", "Stage II"},
			"source": map[string]any{"canonical": "Observation.component", "type": "Observation", "profile": "http://hl7.org/fhir/StructureDefinition/Observation", "path": "component"},
			"key":    map[string]any{"selector": "code.coding[]", "system": "urn:study:A", "code": "shared", "display": "Shared"},
			"value": map[string]any{
				"selector": "valueCodeableConcept.text", "type": "string",
				"fallback_selectors": []any{"valueCodeableConcept.coding[].display"}, "fallback_on_blank_string": true,
			},
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
	if observation.Key.System != "urn:study:A" || observation.Key.Code != "shared" || observation.Value.Selector != "valueCodeableConcept.text" ||
		observation.Value.Type != "string" || !reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
		!observation.Value.FallbackOnBlankString || observation.LogicalType != "string" || observation.Population != 2 {
		t.Fatalf("semantic observation = %#v", observation)
	}
}
