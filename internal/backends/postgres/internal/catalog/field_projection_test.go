package postgres

import (
	"context"
	"reflect"
	"testing"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

type projectionStubStore struct {
	rows          []catalog.FieldCatalogDocument
	filteredCalls int
	coverageCalls int
	upsertCalls   int
	rowsCalls     int
	distinctCalls int
}

func (s *projectionStubStore) UpsertFieldProfileBatch(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage storepostgres.FieldProfileCoverage, batchSize int) error {
	s.upsertCalls++
	return nil
}

func (s *projectionStubStore) ReadFieldProfileRows(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	s.rowsCalls++
	return nil, nil
}

func (s *projectionStubStore) ReadFieldProfileRowsFiltered(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error) {
	s.filteredCalls++
	return s.rows, nil
}

func (s *projectionStubStore) ReadProfileCoverage(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]storepostgres.FieldProfileCoverage, error) {
	s.coverageCalls++
	return nil, nil
}

func (s *projectionStubStore) ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error) {
	s.distinctCalls++
	return nil, nil
}

func projectionFullDoc() catalog.FieldCatalogDocument {
	doc := fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 3, "female", "male")
	doc.MaxItems = 1
	doc.DistinctTruncated = true
	doc.ExtensionValues = []catalog.ExtensionValueObservation{{
		URL: "http://ext/gender-identity", SourcePath: "gender.extension",
		ValuePath: "valueCoding.code", ValueType: "code",
		URLPath: []string{"http://ext/root", "http://ext/gender-identity"},
	}}
	doc.SemanticObservations = []catalog.SemanticObservation{{
		SchemaVersion: 1, Status: "observed", Population: 2,
		Examples: []string{"female"}, ExtensionURLPath: []string{"http://ext/root"},
		ObservedUnits: []string{"kg"},
	}}
	doc.PivotCandidate = true
	doc.PivotKind = "codes"
	doc.PivotColumns = []string{"code.coding"}
	doc.PivotFamily = "observation"
	doc.PivotColumnSelect = "code"
	doc.PivotValueSelect = "value"
	doc.PivotItemSource = "item"
	doc.PivotItemResourceType = "Observation"
	doc.PivotValueSelectors = []string{"valueQuantity", "valueString"}
	doc.EnrichmentTruncated = true
	return doc
}

func projectionExpected() catalog.FieldEnrichmentObservation {
	return catalog.FieldEnrichmentObservation{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePath: fdPath1,
		ResourceType: "Patient", Path: "gender", Kind: "scalar",
		DocCount: 3, MaxItems: 1, SampleCount: 2,
		DistinctValues: []string{"female", "male"}, DistinctTruncated: true,
		ExtensionValues: []catalog.ExtensionValueObservation{{
			URL: "http://ext/gender-identity", SourcePath: "gender.extension",
			ValuePath: "valueCoding.code", ValueType: "code",
			URLPath: []string{"http://ext/root", "http://ext/gender-identity"},
		}},
		SemanticObservations: []catalog.SemanticObservation{{
			SchemaVersion: 1, Status: "observed", Population: 2,
			Examples: []string{"female"}, ExtensionURLPath: []string{"http://ext/root"},
			ObservedUnits: []string{"kg"},
		}},
		PivotCandidate: true, PivotKind: "codes", PivotColumns: []string{"code.coding"},
		PivotFamily: "observation", PivotColumnSelect: "code", PivotValueSelect: "value",
		PivotItemSource: "item", PivotItemResourceType: "Observation",
		PivotValueSelectors: []string{"valueQuantity", "valueString"},
	}
}

func projectionStatusDoc() catalog.FieldCatalogDocument {
	complete := true
	return catalog.FieldCatalogDocument{
		Key: "Patient|status", RecordType: catalog.FieldProfileStatusRecordType,
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePath: fdPath1, ResourceType: "Patient",
		EnrichmentComplete: &complete,
	}
}

func projectionInvalidDoc() catalog.FieldCatalogDocument {
	return fdDoc(fdProject, fdGenA, fdPath1, "Resource", "reference", 1)
}

func TestFieldProjectionFullFieldEquivalenceBothReadSurfaces(t *testing.T) {
	stub := &projectionStubStore{rows: []catalog.FieldCatalogDocument{
		projectionFullDoc(), projectionStatusDoc(), projectionInvalidDoc(),
	}}
	store, err := NewFieldProfileAdapter(stub)
	if err != nil {
		t.Fatalf("new field profile adapter: %v", err)
	}
	expected := projectionExpected()

	fields, err := store.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("discover fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("discovery rows = %d, want only the fully populated data row: %#v", len(fields), fields)
	}
	if want := catalog.PopulatedField(expected); !reflect.DeepEqual(fields[0], want) {
		t.Fatalf("discovery projection lost or altered a field:\n got %+v\nwant %+v", fields[0], want)
	}

	enrichment, err := store.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceTypes: []string{"Patient"},
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("discover field enrichment: %v", err)
	}
	if len(enrichment.Values) != 1 {
		t.Fatalf("enrichment rows = %d, want only the fully populated data row: %#v", len(enrichment.Values), enrichment.Values)
	}
	if !reflect.DeepEqual(enrichment.Values[0], expected) {
		t.Fatalf("enrichment projection lost or altered a field:\n got %+v\nwant %+v", enrichment.Values[0], expected)
	}
	if !enrichment.Available || enrichment.Complete || !enrichment.Truncated || !enrichment.FieldCoverageKnown {
		t.Fatalf("enrichment posture = available=%v complete=%v truncated=%v coverageKnown=%v, want true/false/true/true",
			enrichment.Available, enrichment.Complete, enrichment.Truncated, enrichment.FieldCoverageKnown)
	}
	if want := catalog.EvidenceStatusFor(true, false, true, 1); enrichment.Status != want {
		t.Fatalf("enrichment status = %v, want %v", enrichment.Status, want)
	}
	wantDigest, digestErr := catalog.FieldEnrichmentDigest([]catalog.FieldEnrichmentObservation{expected}, true, false, true)
	if digestErr != nil {
		t.Fatalf("expected digest: %v", digestErr)
	}
	if enrichment.Digest != wantDigest {
		t.Fatalf("enrichment digest = %q, want %q", enrichment.Digest, wantDigest)
	}
	if len(enrichment.Diagnostics) != 1 ||
		enrichment.Diagnostics[0].Code != "INVALID_FIELD_RESOURCE_TYPE" ||
		enrichment.Diagnostics[0].ResourceType != "Resource" {
		t.Fatalf("invalid-typed row must be refused with exactly one typed diagnostic, got %+v", enrichment.Diagnostics)
	}

	if stub.filteredCalls != 2 || stub.coverageCalls != 0 || stub.upsertCalls != 0 ||
		stub.rowsCalls != 0 || stub.distinctCalls != 0 {
		t.Fatalf("projection boundary calls: filtered=%d coverage=%d upsert=%d rows=%d distinct=%d, want 2/0/0/0/0",
			stub.filteredCalls, stub.coverageCalls, stub.upsertCalls, stub.rowsCalls, stub.distinctCalls)
	}
}

func poisonProjectionObservation(obs *catalog.FieldEnrichmentObservation) {
	obs.DistinctValues[0] = "poisoned"
	obs.DistinctValues = append(obs.DistinctValues, "appended")
	obs.PivotColumns[0] = "poisoned"
	obs.PivotColumns = append(obs.PivotColumns, "appended")
	obs.PivotValueSelectors[0] = "poisoned"
	obs.PivotValueSelectors = append(obs.PivotValueSelectors, "appended")
	obs.ExtensionValues[0] = catalog.ExtensionValueObservation{}
	obs.SemanticObservations[0] = catalog.SemanticObservation{}
}

func TestFieldProjectionCopiedSlicesIsolatePersistedState(t *testing.T) {
	stub := &projectionStubStore{rows: []catalog.FieldCatalogDocument{projectionFullDoc(), projectionStatusDoc()}}
	store, err := NewFieldProfileAdapter(stub)
	if err != nil {
		t.Fatalf("new field profile adapter: %v", err)
	}
	expected := projectionExpected()
	pristine := []catalog.FieldCatalogDocument{projectionFullDoc(), projectionStatusDoc()}

	fields, err := store.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("discover fields: %v", err)
	}
	if len(fields) != 1 {
		t.Fatalf("discovery rows = %d, want 1", len(fields))
	}
	discovered := catalog.FieldEnrichmentObservation(fields[0])
	poisonProjectionObservation(&discovered)
	if !reflect.DeepEqual(stub.rows, pristine) {
		t.Fatalf("mutating a discovery result altered persisted state: %+v", stub.rows)
	}
	rereadFields, err := store.DiscoverFields(context.Background(), catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("re-read discover fields: %v", err)
	}
	if len(rereadFields) != 1 || !reflect.DeepEqual(rereadFields[0], catalog.PopulatedField(expected)) {
		t.Fatalf("re-read discovery result diverged after poisoning the first result: %#v", rereadFields)
	}

	enrichment, err := store.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceTypes: []string{"Patient"},
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("discover field enrichment: %v", err)
	}
	if len(enrichment.Values) != 1 {
		t.Fatalf("enrichment rows = %d, want 1", len(enrichment.Values))
	}
	poisonProjectionObservation(&enrichment.Values[0])
	if !reflect.DeepEqual(stub.rows, pristine) {
		t.Fatalf("mutating an enrichment result altered persisted state: %+v", stub.rows)
	}
	reread, err := store.DiscoverFieldEnrichment(context.Background(), catalog.FieldEnrichmentOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceTypes: []string{"Patient"},
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		t.Fatalf("re-read discover field enrichment: %v", err)
	}
	if len(reread.Values) != 1 || !reflect.DeepEqual(reread.Values[0], expected) {
		t.Fatalf("re-read enrichment result diverged after poisoning the first result: %#v", reread.Values)
	}

	if stub.coverageCalls != 0 || stub.upsertCalls != 0 || stub.rowsCalls != 0 || stub.distinctCalls != 0 {
		t.Fatalf("projection boundary calls: coverage=%d upsert=%d rows=%d distinct=%d, want 0/0/0/0",
			stub.coverageCalls, stub.upsertCalls, stub.rowsCalls, stub.distinctCalls)
	}
}

func TestFieldProjectionDomainStructsRemainConvertible(t *testing.T) {
	enrichment := reflect.TypeOf(catalog.FieldEnrichmentObservation{})
	populated := reflect.TypeOf(catalog.PopulatedField{})
	if enrichment.NumField() != populated.NumField() {
		t.Fatalf("field counts diverged: FieldEnrichmentObservation=%d PopulatedField=%d",
			enrichment.NumField(), populated.NumField())
	}
	for i := range enrichment.NumField() {
		left, right := enrichment.Field(i), populated.Field(i)
		if left.Name != right.Name || left.Type != right.Type || left.Tag != right.Tag {
			t.Fatalf("field %d diverged: %s %s %q vs %s %s %q",
				i, left.Name, left.Type, left.Tag, right.Name, right.Type, right.Tag)
		}
	}
}
