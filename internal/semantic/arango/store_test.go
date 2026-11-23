package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/semantic"
	store "github.com/calypr/loom/internal/store/arango"
)

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

func TestResolveSemanticInventorySelectionsUsesIndexedScopedRows(t *testing.T) {
	build := semantic.NewBuild("project", "generation", "")
	build.State = semantic.Complete
	build.SourceAvailability = semantic.SourceAvailabilityUnproven
	build.EntryIndexVersion = semantic.EntryIndexVersion
	buildRow := semanticInventoryTestRow(t, build)
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:system", Code: "code", Display: "Label"},
		Value: semantic.Value{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
		LogicalType:  "string",
		Population:   2,
		Completeness: semantic.CompletenessComplete,
		Status:       "SUPPORTED",
		RuleHint:     "OBSERVATION_CODE_VALUE",
		RuleVersion:  strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	entryRow := semanticInventoryTestRow(t, semantic.Entry{ConceptID: "concept-a", BindingID: "binding-a", Observation: observation})
	client := &evidenceClient{
		rows: map[string][]map[string]any{
			semanticInventoryBuildByKeyAQL:               {buildRow},
			semanticInventoryResolveIndexedSelectionsAQL: {entryRow},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	unrestricted := false
	result, err := adapter.ResolveSemanticInventorySelections(context.Background(), semantic.ResolveOptions{
		Project: "project", DatasetGeneration: "generation", AuthResourcePathsUnrestricted: &unrestricted,
		AuthResourcePaths: []string{"/scope-a"},
		References:        []semantic.Reference{{ConceptID: "concept-a", BindingID: "binding-a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != semantic.Complete || len(result.Entries) != 1 || result.Entries[0].ConceptID != "concept-a" || result.Entries[0].Observation.Population != 2 {
		t.Fatalf("resolved inventory = %#v", result)
	}
	resolvedValue := result.Entries[0].Observation.Value
	if resolvedValue.Selector != "valueCodeableConcept.text" || resolvedValue.Type != "string" ||
		!reflect.DeepEqual(resolvedValue.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) || !resolvedValue.FallbackOnBlankString {
		t.Fatalf("indexed resolver lost value fallback policy: %#v", resolvedValue)
	}
	if len(client.queries) != 2 || client.queries[1] != semanticInventoryResolveIndexedSelectionsAQL {
		t.Fatalf("selection queries = %v, want indexed exact lookup", client.queries)
	}
	vars := client.vars[1]
	if vars["project"] != "project" || vars["dataset_generation"] != "generation" || vars["build_id"] != build.BuildID || vars["auth_resource_paths_unrestricted"] != false || vars["auth_resource_paths"] == nil || vars["observation_schema"] != build.ObservationSchema {
		t.Fatalf("indexed resolver identity/scope binds = %#v", vars)
	}
	refs, ok := vars["references"].([]map[string]interface{})
	if !ok || !reflect.DeepEqual(refs, []map[string]interface{}{{"binding_id": "binding-a", "concept_id": "concept-a"}}) {
		t.Fatalf("indexed resolver refs = %#v", vars["references"])
	}
	query := client.queries[1]
	for _, fragment := range []string{
		"FOR d IN fhir_semantic_inventory_entries",
		"FILTER d.project == @project",
		"FILTER d.dataset_generation == @dataset_generation",
		"FILTER d.build_id == @build_id",
		"FILTER @auth_resource_paths_unrestricted == true OR d.auth_resource_path IN @auth_resource_paths",
		"FILTER d.binding_id == requested.binding_id",
		"FILTER d.concept_id == requested.concept_id",
		"COLLECT AGGREGATE population = SUM(d.observation.population)",
		"fallback_selectors = MIN(d.observation.value.fallback_selectors)",
		"fallback_on_blank_string = MIN(d.observation.value.fallback_on_blank_string)",
		"fallback_selectors: fallback_selectors",
		"fallback_on_blank_string: fallback_on_blank_string",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("indexed selection query missing %q: %s", fragment, query)
		}
	}
	if strings.Index(query, "FILTER @auth_resource_paths_unrestricted") > strings.Index(query, "COLLECT AGGREGATE") {
		t.Fatalf("resolver aggregates before authorization filtering: %s", query)
	}
}

func TestSemanticInventoryMaterializationAndPageQueriesPreserveValueFallbackPolicy(t *testing.T) {
	for name, query := range map[string]string{
		"materialization": materializeSemanticInventoryEntriesAQL,
		"page":            semanticInventoryPageAQL,
	} {
		t.Run(name, func(t *testing.T) {
			for _, fragment := range []string{
				"fallback_selectors = MIN(d.observation.value.fallback_selectors)",
				"fallback_on_blank_string = MIN(d.observation.value.fallback_on_blank_string)",
				"fallback_selectors: fallback_selectors",
				"fallback_on_blank_string: fallback_on_blank_string",
			} {
				if !strings.Contains(query, fragment) {
					t.Fatalf("%s query omits fallback-policy projection %q", name, fragment)
				}
			}
		})
	}
}

func TestResolveSemanticInventorySelectionsFallsBackForLegacyBuild(t *testing.T) {
	build := semantic.NewBuild("project", "generation", "")
	build.State = semantic.Complete
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	buildRow := semanticInventoryTestRow(t, build)
	entry := semantic.Entry{ConceptID: "concept-a", BindingID: "binding-a", Observation: semantic.Observation{SchemaVersion: semantic.ObservationSchemaVersion}}
	entryRow := semanticInventoryTestRow(t, entry)
	client := &evidenceClient{
		rows: map[string][]map[string]any{
			semanticInventoryBuildByKeyAQL:        {buildRow},
			semanticInventoryResolveSelectionsAQL: {entryRow},
		},
	}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	unrestricted := true
	_, err = adapter.ResolveSemanticInventorySelections(context.Background(), semantic.ResolveOptions{
		Project: "project", DatasetGeneration: "generation", AuthResourcePathsUnrestricted: &unrestricted,
		References: []semantic.Reference{{ConceptID: "concept-a", BindingID: "binding-a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(client.queries) != 2 || client.queries[1] != semanticInventoryResolveSelectionsAQL {
		t.Fatalf("legacy selection queries = %v, want contribution fallback", client.queries)
	}
}

func semanticInventoryTestRow(t *testing.T, value any) map[string]any {
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
		collections: map[string]bool{"Observation": false, fieldprofile.FieldCatalogCollection: true},
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
		collections: map[string]bool{"Observation": true, fieldprofile.FieldCatalogCollection: true},
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
		collections: map[string]bool{"Observation": true, fieldprofile.FieldCatalogCollection: true},
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
		collections: map[string]bool{"Observation": true, fieldprofile.FieldCatalogCollection: true},
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
	build := semantic.NewBuild("project", "generation", "")
	build.SourceKind = semantic.SourceRetained
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	build.LeaseToken = "owner"
	build.SourceCheckpoint = semantic.Checkpoint{Collection: "Observation", Key: "a"}
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
				_, err := adapter.AdvanceSemanticInventoryBackfill(context.Background(), build, "owner", build.SourceCheckpoint, semantic.Checkpoint{Collection: "Observation", Key: "b"}, 7, time.Minute)
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

func TestPageSemanticInventoryTreatsMissingCollectionsAsUnknown(t *testing.T) {
	client := &evidenceClient{collections: map[string]bool{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	page, err := adapter.PageSemanticInventory(context.Background(), semantic.PageOptions{
		Project: "p", DatasetGeneration: "legacy-generation",
	})
	if err != nil || page.State != semantic.Unknown || len(page.Entries) != 0 {
		t.Fatalf("page = %#v err=%v, want unknown empty inventory", page, err)
	}
	if len(client.queries) != 0 {
		t.Fatalf("queries = %d, want no AQL against absent legacy collections", len(client.queries))
	}
}
