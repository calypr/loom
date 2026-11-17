package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
)

const (
	fdProject = "fd-p1"
	fdGenA    = "generation-a"
	fdPath1   = "/fhir/clinical"
	fdPath2   = "/fhir/finance"
)

func fdDoc(project, generation, authPath, resourceType, path string, docCount int64, distinct ...string) catalog.FieldCatalogDocument {
	complete := true
	return catalog.FieldCatalogDocument{
		Key:     resourceType + "|" + path,
		Project: project, DatasetGeneration: generation, AuthResourcePath: authPath,
		ResourceType: resourceType, Path: path, Kind: "scalar",
		DocCount: docCount, SampleCount: len(distinct), DistinctValues: append([]string(nil), distinct...),
		EnrichmentComplete: &complete,
	}
}

type fdRecordingStore struct {
	real                 *storepostgres.FieldProfileRepository
	filteredCalls        int
	lastFilter           storepostgres.FieldProfileFilter
	injectReadError      error
	distinctCalls        int
	lastDistinctScope    string
	injectDistinctErr    error
	reverseDistinctOrder bool
}

func (w *fdRecordingStore) UpsertFieldProfileBatch(ctx context.Context, scope storepostgres.FieldProfileScope, docs []catalog.FieldCatalogDocument, coverage storepostgres.FieldProfileCoverage, batchSize int) error {
	return w.real.UpsertFieldProfileBatch(ctx, scope, docs, coverage, batchSize)
}

func (w *fdRecordingStore) ReadFieldProfileRows(ctx context.Context, scope storepostgres.FieldProfileScope) ([]catalog.FieldCatalogDocument, error) {
	return w.real.ReadFieldProfileRows(ctx, scope)
}

func (w *fdRecordingStore) ReadFieldProfileRowsFiltered(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]catalog.FieldCatalogDocument, error) {
	w.filteredCalls++
	w.lastFilter = filter
	if w.injectReadError != nil {
		return nil, w.injectReadError
	}
	return w.real.ReadFieldProfileRowsFiltered(ctx, filter)
}

func (w *fdRecordingStore) ReadProfileCoverage(ctx context.Context, filter storepostgres.FieldProfileFilter) ([]storepostgres.FieldProfileCoverage, error) {
	return w.real.ReadProfileCoverage(ctx, filter)
}

func (w *fdRecordingStore) ListDistinctAuthResourcePaths(ctx context.Context, project string) ([]string, error) {
	w.distinctCalls++
	w.lastDistinctScope = project
	if w.injectDistinctErr != nil {
		return nil, w.injectDistinctErr
	}
	paths, err := w.real.ListDistinctAuthResourcePaths(ctx, project)
	if err != nil {
		return nil, err
	}
	if w.reverseDistinctOrder {
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
	}
	return paths, nil
}

func fdWrite(t *testing.T, s *fieldProfileService, scope storepostgres.FieldProfileScope, docs ...catalog.FieldCatalogDocument) {
	t.Helper()
	store := s.adapter(t)
	if err := store.WriteFieldProfile(s.ctx, scope, docs, 100); err != nil {
		s.fail("write field profile "+scope.ResourceType+" "+scope.AuthResourcePath, err)
	}
}

func fdScope(project, generation, authPath, resourceType string) storepostgres.FieldProfileScope {
	return storepostgres.FieldProfileScope{Project: project, DatasetGeneration: generation, AuthResourcePath: authPath, ResourceType: resourceType}
}

func fdUnrestricted() *bool { return catalog.ExplicitAuthResourcePathsUnrestricted(true) }

func TestDiscoverFieldsPostgreSQLExactSetOrderAndMapping(t *testing.T) {
	s := newFieldProfileService(t)
	full := fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 3, "female", "male")
	full.MaxItems = 1
	full.DistinctTruncated = true
	full.ExtensionValues = []catalog.ExtensionValueObservation{{
		URL: "http://ext/gender-identity", SourcePath: "gender.extension",
		ValuePath: "valueCoding.code", ValueType: "code", URLPath: []string{"http://ext/root", "http://ext/gender-identity"},
	}}
	full.SemanticObservations = []catalog.SemanticObservation{{
		SchemaVersion: 1, Status: "observed", Population: 2,
		Examples: []string{"female"}, ExtensionURLPath: []string{"http://ext/root"},
	}}
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Patient"),
		full,
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "active", 5),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "birthDate", 3),
		func() catalog.FieldCatalogDocument {
			markerComplete := true
			return catalog.FieldCatalogDocument{Key: "Patient|status", RecordType: catalog.FieldProfileStatusRecordType,
				Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePath: fdPath1, ResourceType: "Patient",
				EnrichmentComplete: &markerComplete}
		}(),
	)
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath2, "Patient"), fdDoc(fdProject, fdGenA, fdPath2, "Patient", "deceasedBoolean", 1))
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Observation"), fdDoc(fdProject, fdGenA, fdPath1, "Observation", "status", 9))

	store := s.adapter(t)
	fields, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("discover fields", err)
	}

	wantOrder := []struct{ resourceType, path, authPath string }{
		{"Observation", "status", fdPath1},
		{"Patient", "active", fdPath1},
		{"Patient", "birthDate", fdPath1},
		{"Patient", "deceasedBoolean", fdPath2},
		{"Patient", "gender", fdPath1},
	}
	if len(fields) != len(wantOrder) {
		t.Fatalf("discovered %d fields, want %d: %#v", len(fields), len(wantOrder), fields)
	}
	for i, want := range wantOrder {
		got := fields[i]
		if got.ResourceType != want.resourceType || got.Path != want.path || got.AuthResourcePath != want.authPath {
			t.Fatalf("order position %d = %s.%s (%q), want %s.%s (%q): full order %#v",
				i, got.ResourceType, got.Path, got.AuthResourcePath, want.resourceType, want.path, want.authPath, fields)
		}
		if got.Project != fdProject || got.DatasetGeneration != fdGenA || got.Kind != "scalar" {
			t.Fatalf("field %s.%s lost identity: %+v", got.ResourceType, got.Path, got)
		}
	}
	t.Logf("registered PG discovery order witness (resource_type, path): %v", wantOrder)

	var gender *catalog.PopulatedField
	for i := range fields {
		if fields[i].Path == "gender" {
			gender = &fields[i]
		}
	}
	if gender == nil {
		t.Fatal("gender missing from discovery")
	}
	if gender.DocCount != 3 || gender.MaxItems != 1 || gender.SampleCount != 2 ||
		!gender.DistinctTruncated || !reflect.DeepEqual(gender.DistinctValues, []string{"female", "male"}) {
		t.Fatalf("gender scalar columns lost in mapping: %+v", gender)
	}
	if !reflect.DeepEqual(gender.ExtensionValues, full.ExtensionValues) {
		t.Fatalf("extension values lost in mapping: got %#v want %#v", gender.ExtensionValues, full.ExtensionValues)
	}
	if len(gender.SemanticObservations) != 1 || gender.SemanticObservations[0].Status != "observed" ||
		gender.SemanticObservations[0].Population != 2 ||
		!reflect.DeepEqual(gender.SemanticObservations[0].Examples, []string{"female"}) {
		t.Fatalf("semantic observations lost in mapping: %#v", gender.SemanticObservations)
	}

	gender.DistinctValues[0] = "poisoned"
	reread, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("re-read fields", err)
	}
	for _, f := range reread {
		if f.Path == "gender" && reflect.DeepEqual(f.DistinctValues, []string{"female", "male"}) == false {
			t.Fatalf("returned view aliases persisted state: %#v", f.DistinctValues)
		}
	}
}

func TestDiscoverFieldsRestrictedEmptyMakesZeroRoundTrips(t *testing.T) {
	s := newFieldProfileService(t)
	wrapper := &fdRecordingStore{real: s.realRepository(t)}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}
	fields, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA,
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil {
		s.fail("restricted-empty discovery", err)
	}
	if len(fields) != 0 {
		t.Fatalf("restricted-empty returned %d fields: must be empty: %#v", len(fields), fields)
	}
	if wrapper.filteredCalls != 0 {
		t.Fatalf("restricted-empty issued %d store calls: the no-query law was violated", wrapper.filteredCalls)
	}
}

func TestDiscoverFieldsUnrestrictedIgnoresStrayPaths(t *testing.T) {
	s := newFieldProfileService(t)
	wrapper := &fdRecordingStore{real: s.realRepository(t)}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}
	if _, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA,
		AuthResourcePathsUnrestricted: fdUnrestricted(),
		AuthResourcePaths:             []string{fdPath1, "not/authorized"},
	}); err != nil {
		s.fail("unrestricted discovery", err)
	}
	if wrapper.filteredCalls != 1 {
		t.Fatalf("store calls = %d, want exactly 1", wrapper.filteredCalls)
	}
	if wrapper.lastFilter.AuthResourcePathsUnrestricted == nil || !*wrapper.lastFilter.AuthResourcePathsUnrestricted {
		t.Fatalf("filter lost unrestricted mode: %+v", wrapper.lastFilter)
	}
	if len(wrapper.lastFilter.AuthResourcePaths) != 0 {
		t.Fatalf("unrestricted read carried stray paths %v: they must be dropped, mirroring the reference adapter", wrapper.lastFilter.AuthResourcePaths)
	}
}

func TestDiscoverFieldsNamedTypePivotAndConcreteLaws(t *testing.T) {
	s := newFieldProfileService(t)
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Patient"),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 3),
		func() catalog.FieldCatalogDocument {
			d := fdDoc(fdProject, fdGenA, fdPath1, "Patient", "maritalStatus", 2)
			d.PivotCandidate = true
			d.PivotKind = "CODED_PIVOT"
			d.PivotColumns = []string{"married", "single"}
			return d
		}(),
	)
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "FakeResource"),
		fdDoc(fdProject, fdGenA, fdPath1, "FakeResource", "anything", 1))

	store := s.adapter(t)

	named, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceType: "Patient",
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("named-type discovery", err)
	}
	if len(named) != 2 {
		t.Fatalf("named-type discovery returned %#v, want the two Patient fields", named)
	}

	pivots, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceType: "Patient", PivotOnly: true,
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("pivot-only discovery", err)
	}
	if len(pivots) != 1 || pivots[0].Path != "maritalStatus" ||
		pivots[0].PivotKind != "CODED_PIVOT" || !reflect.DeepEqual(pivots[0].PivotColumns, []string{"married", "single"}) {
		t.Fatalf("pivot-only discovery = %#v, want exactly the pivot candidate", pivots)
	}

	fake, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, ResourceType: "FakeResource",
		AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("non-concrete discovery", err)
	}
	if len(fake) != 0 {
		t.Fatalf("non-concrete rows surfaced as fields: %#v", fake)
	}
}

func TestDiscoverFieldsStoreFailureIsWrappedNotEmpty(t *testing.T) {
	s := newFieldProfileService(t)
	boom := errors.New("injected filtered read failure")
	wrapper := &fdRecordingStore{real: s.realRepository(t), injectReadError: boom}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}
	fields, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err == nil {
		t.Fatal("store failure was swallowed into a successful read")
	}
	if fields != nil {
		t.Fatalf("failed discovery still returned fields: %#v", fields)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store failure lost its identity in wrapping: %v", err)
	}
}

func TestDiscoverExistingAuthResourcePathsSortedDistinctIncludingMarker(t *testing.T) {
	s := newFieldProfileService(t)
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath2, "Observation"), fdDoc(fdProject, fdGenA, fdPath2, "Observation", "status", 4))
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Patient"),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 3),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "birthDate", 3))
	truncated := false
	complete := false
	fdWrite(t, s, fdScope(fdProject, fdGenA, "/fhir/research", "Patient"),
		catalog.FieldCatalogDocument{
			Key: "Patient|status", RecordType: catalog.FieldProfileStatusRecordType,
			Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePath: "/fhir/research",
			ResourceType: "Patient", EnrichmentComplete: &complete, EnrichmentTruncated: truncated,
		})
	fdWrite(t, s, fdScope(fdProject, fdGenA, "/fhir/custom", "CustomCarePlan"),
		fdDoc(fdProject, fdGenA, "/fhir/custom", "CustomCarePlan", "status", 2))
	fdWrite(t, s, fdScope("fd-other", fdGenA, "/fhir/foreign", "Patient"), fdDoc("fd-other", fdGenA, "/fhir/foreign", "Patient", "gender", 1))
	fdWrite(t, s, fdScope(fdProject, "generation-z", fdPath1, "Patient"), fdDoc(fdProject, "generation-z", fdPath1, "Patient", "gender", 1))

	store := s.adapter(t)
	paths, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, DatasetGeneration: fdGenA,
	})
	if err != nil {
		s.fail("auth-path listing", err)
	}
	want := []string{fdPath1, "/fhir/custom", fdPath2, "/fhir/research"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("auth paths = %v, want sorted distinct %v", paths, want)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsConflictRefusedNoRoundTrips(t *testing.T) {
	s := newFieldProfileService(t)
	wrapper := &fdRecordingStore{real: s.realRepository(t)}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}

	_, err = store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, DatasetGeneration: fdGenA, AllGenerations: true,
	})
	if !errors.Is(err, ErrAllGenerationsWithGeneration) {
		t.Fatalf("AllGenerations+generation error = %v, want %v", err, ErrAllGenerationsWithGeneration)
	}
	if wrapper.filteredCalls != 0 || wrapper.distinctCalls != 0 {
		t.Fatalf("conflict refused after %d filtered + %d distinct store calls; want zero of both",
			wrapper.filteredCalls, wrapper.distinctCalls)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsIsCrossGenerationUnion(t *testing.T) {
	s := newFieldProfileService(t)
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Patient"),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 3),
		fdDoc(fdProject, fdGenA, fdPath1, "Patient", "birthDate", 3))
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath2, "Observation"), fdDoc(fdProject, fdGenA, fdPath2, "Observation", "status", 4))
	fdWrite(t, s, fdScope(fdProject, "generation-z", fdPath1, "Patient"), fdDoc(fdProject, "generation-z", fdPath1, "Patient", "gender", 1))
	fdWrite(t, s, fdScope(fdProject, "generation-z", "/fhir/archive", "Observation"), fdDoc(fdProject, "generation-z", "/fhir/archive", "Observation", "status", 2))
	fdWrite(t, s, fdScope(fdProject, "", "/fhir/legacy", "Patient"), fdDoc(fdProject, "", "/fhir/legacy", "Patient", "gender", 1))
	truncated := false
	complete := false
	fdWrite(t, s, fdScope(fdProject, fdGenA, "/fhir/research", "Patient"),
		catalog.FieldCatalogDocument{
			Key: "Patient|status", RecordType: catalog.FieldProfileStatusRecordType,
			Project: fdProject, DatasetGeneration: fdGenA, AuthResourcePath: "/fhir/research",
			ResourceType: "Patient", EnrichmentComplete: &complete, EnrichmentTruncated: truncated,
		})
	fdWrite(t, s, fdScope(fdProject, "generation-z", "/fhir/custom", "CustomCarePlan"),
		fdDoc(fdProject, "generation-z", "/fhir/custom", "CustomCarePlan", "status", 2))
	fdWrite(t, s, fdScope(fdProject, fdGenA, "", "Patient"), fdDoc(fdProject, fdGenA, "", "Patient", "gender", 1))
	fdWrite(t, s, fdScope("fd-other", fdGenA, "/fhir/foreign", "Patient"), fdDoc("fd-other", fdGenA, "/fhir/foreign", "Patient", "gender", 1))

	wrapper := &fdRecordingStore{real: s.realRepository(t)}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}

	paths, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, AllGenerations: true,
	})
	if err != nil {
		s.fail("all-generations listing", err)
	}
	want := []string{"/fhir/archive", fdPath1, "/fhir/custom", fdPath2, "/fhir/legacy", "/fhir/research"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("all-generations union = %v, want sorted distinct %v", paths, want)
	}
	if wrapper.distinctCalls != 1 || wrapper.lastDistinctScope != fdProject {
		t.Fatalf("union issued %d distinct reads (project %q); want exactly 1 for %q",
			wrapper.distinctCalls, wrapper.lastDistinctScope, fdProject)
	}
	if wrapper.filteredCalls != 0 {
		t.Fatalf("union issued %d filtered reads; the union is one distinct read, not per-generation composition",
			wrapper.filteredCalls)
	}

	ws, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, DatasetGeneration: "  ", AllGenerations: true,
	})
	if err != nil {
		s.fail("whitespace-generation all-generations listing", err)
	}
	if !reflect.DeepEqual(ws, want) {
		t.Fatalf("whitespace-generation union = %v, want %v", ws, want)
	}

	scoped, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, DatasetGeneration: "generation-z",
	})
	if err != nil {
		s.fail("scoped generation-z listing", err)
	}
	if wantZ := []string{"/fhir/archive", fdPath1, "/fhir/custom"}; !reflect.DeepEqual(scoped, wantZ) {
		t.Fatalf("scoped generation-z = %v, want %v", scoped, wantZ)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsAdapterOwnsByteOrder(t *testing.T) {
	s := newFieldProfileService(t)
	for _, path := range []string{"/fhir/Beta", "/fhir/_x", "/fhir/alpha", "/fhir/Ω"} {
		fdWrite(t, s, fdScope(fdProject, fdGenA, path, "Patient"), fdDoc(fdProject, fdGenA, path, "Patient", "gender", 1))
	}
	wrapper := &fdRecordingStore{real: s.realRepository(t), reverseDistinctOrder: true}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}
	paths, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, AllGenerations: true,
	})
	if err != nil {
		s.fail("all-generations listing", err)
	}
	if want := []string{"/fhir/Beta", "/fhir/_x", "/fhir/alpha", "/fhir/Ω"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("adapter output = %v, want ascending byte order %v", paths, want)
	}
	if wrapper.distinctCalls != 1 {
		t.Fatalf("boundary reads = %d, want exactly 1", wrapper.distinctCalls)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsUnrecordedProjectIsHonestEmpty(t *testing.T) {
	s := newFieldProfileService(t)
	fdWrite(t, s, fdScope("fd-other", fdGenA, "/fhir/foreign", "Patient"), fdDoc("fd-other", fdGenA, "/fhir/foreign", "Patient", "gender", 1))

	wrapper := &fdRecordingStore{real: s.realRepository(t)}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}

	paths, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: "fd-unrecorded", AllGenerations: true,
	})
	if err != nil {
		t.Fatalf("unrecorded project must list an honest empty, got error: %v", err)
	}
	if paths == nil || len(paths) != 0 {
		t.Fatalf("unrecorded project union = %#v, want a non-nil empty slice", paths)
	}
	if wrapper.distinctCalls != 1 || wrapper.lastDistinctScope != "fd-unrecorded" {
		t.Fatalf("honest empty came from %d distinct reads (project %q); it must be a real zero-row read",
			wrapper.distinctCalls, wrapper.lastDistinctScope)
	}
}

func TestDiscoverExistingAuthResourcePathsAllGenerationsStoreFailureIsWrappedNotEmpty(t *testing.T) {
	s := newFieldProfileService(t)
	boom := errors.New("injected distinct listing failure")
	wrapper := &fdRecordingStore{real: s.realRepository(t), injectDistinctErr: boom}
	store, err := NewFieldProfileAdapter(wrapper)
	if err != nil {
		s.fail("new field profile store", err)
	}
	paths, err := store.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: fdProject, AllGenerations: true,
	})
	if err == nil {
		t.Fatal("store failure was swallowed into a successful listing")
	}
	if paths != nil {
		t.Fatalf("failed listing still returned paths: %#v", paths)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("store failure lost its identity in wrapping: %v", err)
	}
}

func TestDiscoverFieldsLegacyNullGenerationIsolation(t *testing.T) {
	s := newFieldProfileService(t)
	fdWrite(t, s, fdScope(fdProject, "", fdPath1, "Patient"), fdDoc(fdProject, "", fdPath1, "Patient", "gender", 1))
	fdWrite(t, s, fdScope(fdProject, fdGenA, fdPath1, "Patient"), fdDoc(fdProject, fdGenA, fdPath1, "Patient", "gender", 1))

	store := s.adapter(t)
	legacy, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("legacy discovery", err)
	}
	if len(legacy) != 1 || legacy[0].DatasetGeneration != "" {
		t.Fatalf("legacy read crossed into a generation: %#v", legacy)
	}
	padded, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: "  " + fdGenA + " ", AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("padded discovery", err)
	}
	if len(padded) != 1 || padded[0].DatasetGeneration != fdGenA {
		t.Fatalf("padded generation selection = %#v, want exactly the generation-a row", padded)
	}
	absent, err := store.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: fdProject, DatasetGeneration: "generation-b", AuthResourcePathsUnrestricted: fdUnrestricted(),
	})
	if err != nil {
		s.fail("absent-generation discovery", err)
	}
	if len(absent) != 0 {
		t.Fatalf("unprofiled generation matched stored rows: %#v", absent)
	}
}
