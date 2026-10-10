package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

func TestLocalMigrationCompilerSpecUsesFullStoredValuesIdentity(t *testing.T) {
	legacy := expectedInventory()[0]
	if fieldOnlyIndexName(collectionName, legacy.Fields) != "loom_pivot_preview_46a620d918ffe1d6" {
		t.Fatalf("legacy field-only hash = %q", fieldOnlyIndexName(collectionName, legacy.Fields))
	}
	spec := expectedCompilerIndexSpec()
	if spec.Name != "loom_pivot_preview_cdeb305dcc142386" {
		t.Fatalf("current full stored-values name = %q", spec.Name)
	}
	if err := validateInputs(localMigrationManifest(), spec, "http://arangodb:8529"); err != nil {
		t.Fatalf("valid compiler spec rejected: %v", err)
	}

	bad := spec
	bad.Name = "loom_pivot_preview_46a620d918ffe1d6"
	if err := validateInputs(localMigrationManifest(), bad, "http://arangodb:8529"); err == nil {
		t.Fatal("legacy field-only name was accepted for the stored-values definition")
	}
	bad = compilerIndexSpec{
		Collection:   collectionName,
		Fields:       []string{"project", "dataset_generation", "auth_resource_path", "_key"},
		StoredValues: []string{"payload.id", "payload.status", "payload.subject.reference", "payload.valueQuantity"},
	}
	bad.Name = storedValuesIndexName(bad.Collection, bad.Fields, bad.StoredValues)
	if bad.Name != "loom_pivot_preview_c6ad049c4c4c0af3" {
		t.Fatalf("stale stored-values name = %q", bad.Name)
	}
	if err := validateInputs(localMigrationManifest(), bad, "http://arangodb:8529"); err == nil {
		t.Fatal("previous candidate tuple with subject.reference was accepted for the current production query")
	}
	bad = spec
	bad.StoredValues = []string{"payload.id", "payload.status", "payload.valueQuantity.value"}
	bad.Name = storedValuesIndexName(bad.Collection, bad.Fields, bad.StoredValues)
	if err := validateInputs(localMigrationManifest(), bad, "http://arangodb:8529"); err == nil {
		t.Fatal("substituted scalar child projection was accepted in place of the current stored parent")
	}
}

func TestLocalMigrationPinsExactInventoryAndOwnedTarget(t *testing.T) {
	manifest := localMigrationManifest()
	if err := validateInputs(manifest, localCompilerIndexSpec(), "http://arangodb:8529"); err != nil {
		t.Fatalf("authorized exact manifest rejected: %v", err)
	}

	manifest.ExpectedOwnedIndexes[0].Fields = append(manifest.ExpectedOwnedIndexes[0].Fields, "payload.unrelated")
	if err := validateInputs(manifest, localCompilerIndexSpec(), "http://arangodb:8529"); err == nil {
		t.Fatal("modified old-index inventory was accepted")
	}

	manifest = localMigrationManifest()
	manifest.Authorization.Project = "another_project"
	if err := validateInputs(manifest, localCompilerIndexSpec(), "http://arangodb:8529"); err == nil {
		t.Fatal("different project label was accepted")
	}
	if err := validateInputs(localMigrationManifest(), localCompilerIndexSpec(), "https://arangodb:8529"); err == nil {
		t.Fatal("non-local or non-HTTP endpoint was accepted")
	}
}

func TestLocalMigrationInventoryRecognizesOnlyOriginalStagingAndCompletedSets(t *testing.T) {
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()

	original := make([]driver.IndexResponse, 0, 4)
	for _, spec := range expected {
		original = append(original, responseFromSpec(spec))
	}
	owned, err := validateOwnedInventory(original, expected, candidate)
	if err != nil || len(owned) != 4 || owned[expected[0].Name].ID != expected[0].ID {
		t.Fatalf("original exact inventory rejected: owned=%v err=%v", owned, err)
	}

	newSpec := indexSpec{
		Name: candidate.Name, ID: "Observation/555001", Type: "persistent",
		Fields: candidate.Fields, StoredValues: candidate.StoredValues,
	}
	staged := append(append([]driver.IndexResponse(nil), original...), responseFromSpec(newSpec))
	owned, err = validateOwnedInventory(staged, expected, candidate)
	if err != nil || !isMigrationStagingSet(owned, expected, candidate) {
		t.Fatalf("create-before-delete staging inventory rejected: owned=%v err=%v", owned, err)
	}

	completed := make([]driver.IndexResponse, 0, 4)
	for _, spec := range expected[1:] {
		completed = append(completed, responseFromSpec(spec))
	}
	completed = append(completed, responseFromSpec(newSpec))
	owned, err = validateOwnedInventory(completed, expected, candidate)
	if err != nil || !isCompletedOwnedSet(owned, expected, candidate) {
		t.Fatalf("rerun of completed four-index inventory rejected: owned=%v err=%v", owned, err)
	}

	wrong := append([]driver.IndexResponse(nil), original...)
	wrong[1] = responseFromSpec(expected[1])
	wrong[1].RegularIndex.Fields = []string{"project", "wrong"}
	if _, err := validateOwnedInventory(wrong, expected, candidate); err == nil {
		t.Fatal("same-name unrelated index definition was accepted")
	}
	if _, err := validateOwnedInventory(append(completed, responseFromSpec(indexSpec{
		Name: "loom_pivot_preview_unrelated", ID: "Observation/999", Type: "persistent",
		Fields: []string{"project"}, StoredValues: []string{},
	})), expected, candidate); err == nil {
		t.Fatal("unexpected compiler-owned index was accepted")
	}
}

func TestLocalMigrationRequiresExplicitPersistentNonsparseNonuniqueMetadata(t *testing.T) {
	state := indexState{ID: "Observation/1", Name: "owned", Type: "persistent", Fields: []string{"project"}, StoredValues: []string{}}
	if matchesState(state, "owned", []string{"project"}, []string{}) {
		t.Fatal("unspecified sparse/unique flags were treated as verified false")
	}
	state.SparseSet, state.UniqueSet = true, true
	if !matchesState(state, "owned", []string{"project"}, []string{}) {
		t.Fatal("explicit sparse:false/unique:false metadata did not match")
	}
	state.Unique = true
	if matchesState(state, "owned", []string{"project"}, []string{}) {
		t.Fatal("unique index matched a nonunique expected definition")
	}
}

func TestLocalMigrationComparesAllUnrelatedIndexDefinitions(t *testing.T) {
	before := map[string]indexState{
		"old":   {ID: "old", Name: "old", Type: "persistent"},
		"other": {ID: "other", Name: "other", Type: "persistent", Fields: []string{"project"}},
	}
	after := map[string]indexState{
		"other": {ID: "other", Name: "other", Type: "persistent", Fields: []string{"project"}},
		"new":   {ID: "new", Name: "new", Type: "persistent"},
	}
	if !sameUntouchedIndexes(before, after, "old", "new") {
		t.Fatal("unchanged unrelated definition was reported as changed")
	}
	after["other"] = indexState{ID: "other", Name: "other", Type: "persistent", Fields: []string{"different"}}
	if sameUntouchedIndexes(before, after, "old", "new") {
		t.Fatal("changed unrelated definition escaped the preservation guard")
	}
}

func localMigrationManifest() manifest {
	var result manifest
	result.SchemaVersion = 1
	result.Authorization.Kind = "local-cda-preview-index-verification-migration"
	result.Authorization.ComposeProject = composeProjectLabel
	result.Authorization.ArangoContainer = "loom-dev-6d7df93d6a37-arangodb-1"
	result.Authorization.APIContainer = "loom-dev-6d7df93d6a37-loom-api-1"
	result.Authorization.Database = databaseName
	result.Authorization.Collection = collectionName
	result.Authorization.Project = projectLabel
	result.Authorization.Generation = generationLabel
	result.Authorization.NotSamePlanProof = true
	result.ExpectedOwnedIndexes = expectedInventory()
	return result
}

func localCompilerIndexSpec() compilerIndexSpec {
	return expectedCompilerIndexSpec()
}

func responseFromSpec(spec indexSpec) driver.IndexResponse {
	sparse, unique := false, false
	return driver.IndexResponse{
		Name:               spec.Name,
		Type:               driver.IndexType(spec.Type),
		IndexSharedOptions: driver.IndexSharedOptions{ID: spec.ID, Sparse: &sparse, Unique: &unique},
		RegularIndex:       &driver.IndexOptions{Fields: slices.Clone(spec.Fields), StoredValues: slices.Clone(spec.StoredValues)},
	}
}

type fakeMigrationCollection struct {
	indexes         []driver.IndexResponse
	events          []string
	indexReads      int
	failEnsure      map[string]error
	failDelete      map[string]error
	deleteThenError map[string]error
	failIndexRead   map[int]error
	beforeIndexRead map[int]func(*fakeMigrationCollection)
	nextID          int
}

func (f *fakeMigrationCollection) Indexes(context.Context) ([]driver.IndexResponse, error) {
	f.indexReads++
	f.events = append(f.events, fmt.Sprintf("indexes:%d", f.indexReads))
	if mutate := f.beforeIndexRead[f.indexReads]; mutate != nil {
		mutate(f)
	}
	if err := f.failIndexRead[f.indexReads]; err != nil {
		return nil, err
	}
	return cloneResponses(f.indexes), nil
}

func (f *fakeMigrationCollection) EnsurePersistentIndex(_ context.Context, fields []string, options *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error) {
	name := options.Name
	f.events = append(f.events, "create:"+name)
	if err := f.failEnsure[name]; err != nil {
		return driver.IndexResponse{}, false, err
	}
	for _, index := range f.indexes {
		if index.Name == name {
			return index, false, nil
		}
	}
	f.nextID++
	sparse, unique := false, false
	if options.Sparse != nil {
		sparse = *options.Sparse
	}
	if options.Unique != nil {
		unique = *options.Unique
	}
	index := driver.IndexResponse{
		Name: name,
		Type: driver.PersistentIndexType,
		IndexSharedOptions: driver.IndexSharedOptions{
			ID:     fmt.Sprintf("Observation/%d", 9000000000+f.nextID),
			Sparse: &sparse,
			Unique: &unique,
		},
		RegularIndex: &driver.IndexOptions{
			Fields:       slices.Clone(fields),
			StoredValues: slices.Clone(options.StoredValues),
		},
	}
	f.indexes = append(f.indexes, index)
	return index, true, nil
}

func (f *fakeMigrationCollection) DeleteIndexByID(_ context.Context, id string) error {
	f.events = append(f.events, "delete:"+id)
	if err := f.failDelete[id]; err != nil {
		return err
	}
	deleteErr := f.deleteThenError[id]
	for index := range f.indexes {
		if f.indexes[index].ID == id {
			f.indexes = append(f.indexes[:index], f.indexes[index+1:]...)
			if deleteErr != nil {
				return deleteErr
			}
			return nil
		}
	}
	return fmt.Errorf("index %q not found", id)
}

func cloneResponses(indexes []driver.IndexResponse) []driver.IndexResponse {
	result := make([]driver.IndexResponse, len(indexes))
	for i, index := range indexes {
		result[i] = index
		if index.RegularIndex != nil {
			regular := *index.RegularIndex
			regular.Fields = slices.Clone(index.RegularIndex.Fields)
			regular.StoredValues = slices.Clone(index.RegularIndex.StoredValues)
			result[i].RegularIndex = &regular
		}
		if index.Sparse != nil {
			sparse := *index.Sparse
			result[i].Sparse = &sparse
		}
		if index.Unique != nil {
			unique := *index.Unique
			result[i].Unique = &unique
		}
	}
	return result
}

func newFakeMigrationCollection() (*fakeMigrationCollection, map[string]indexState, map[string]indexState) {
	expected := expectedInventory()
	indexes := make([]driver.IndexResponse, 0, len(expected))
	for _, spec := range expected {
		indexes = append(indexes, responseFromSpec(spec))
	}
	all := snapshotAll(indexes)
	owned, err := validateOwnedInventory(indexes, expected, localCompilerIndexSpec())
	if err != nil {
		panic(err)
	}
	return &fakeMigrationCollection{
		indexes:         indexes,
		failEnsure:      map[string]error{},
		failDelete:      map[string]error{},
		deleteThenError: map[string]error{},
		failIndexRead:   map[int]error{},
		beforeIndexRead: map[int]func(*fakeMigrationCollection){},
		nextID:          100,
	}, all, owned
}

func TestLocalMigrationCreatesCandidateBeforeDeletingExactOldID(t *testing.T) {
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	r, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err != nil {
		t.Fatalf("transition failed: %v", err)
	}
	if r.Outcome != "migrated" || len(fake.indexes) != 4 {
		t.Fatalf("outcome=%q final inventory count=%d", r.Outcome, len(fake.indexes))
	}
	createAt, oldDeleteAt := eventIndex(fake.events, "create:"+candidate.Name), eventIndex(fake.events, "delete:"+expected[0].ID)
	if createAt < 0 || oldDeleteAt < 0 || createAt >= oldDeleteAt {
		t.Fatalf("expected candidate creation before exact old-ID deletion, got %v", fake.events)
	}
	for _, item := range expected[1:] {
		if eventIndex(fake.events, "delete:"+item.ID) >= 0 {
			t.Fatalf("unrelated index %q was deleted: %v", item.ID, fake.events)
		}
	}
}

func TestLocalMigrationPreexistingCandidateSurvivesPredeleteFailure(t *testing.T) {
	fake, _, _ := newFakeMigrationCollection()
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	preexisting := indexSpec{
		Name: candidate.Name, ID: "Observation/9000000101", Type: "persistent",
		Fields: candidate.Fields, StoredValues: candidate.StoredValues,
	}
	fake.indexes = append(fake.indexes, responseFromSpec(preexisting))
	initialAll := snapshotAll(fake.indexes)
	initialOwned, err := validateOwnedInventory(fake.indexes, expected, candidate)
	if err != nil {
		t.Fatalf("preexisting candidate inventory rejected: %v", err)
	}
	fake.failIndexRead[1] = errors.New("predelete inventory read failed")

	_, err = transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || !strings.Contains(err.Error(), "predelete inventory read failed") {
		t.Fatalf("expected predelete inventory read failure, got %v", err)
	}
	assertIDsPresent(t, fake.indexes, expected[0].ID, expected[1].ID, expected[2].ID, expected[3].ID, preexisting.ID)
	if eventIndex(fake.events, "delete:"+preexisting.ID) >= 0 {
		t.Fatalf("preexisting candidate was deleted while handling a failed retry: %v", fake.events)
	}
}

func TestLocalMigrationCreationFailureLeavesOldAndUnrelatedIndexes(t *testing.T) {
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	fake.failEnsure[candidate.Name] = errors.New("create rejected")
	_, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || len(fake.indexes) != 4 || eventIndex(fake.events, "delete:"+expected[0].ID) >= 0 {
		t.Fatalf("create failure did not leave original inventory intact: err=%v events=%v indexes=%d", err, fake.events, len(fake.indexes))
	}
	assertIDsPresent(t, fake.indexes, expected[0].ID, expected[1].ID, expected[2].ID, expected[3].ID)
}

func TestLocalMigrationRefusesPredeleteInventoryChangeAndRemovesOnlyCandidate(t *testing.T) {
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	fake.beforeIndexRead[1] = func(collection *fakeMigrationCollection) {
		for i := range collection.indexes {
			if collection.indexes[i].ID == expected[1].ID {
				collection.indexes[i].RegularIndex.Fields = []string{"project", "concurrent-change"}
			}
		}
	}
	_, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || !strings.Contains(err.Error(), "no longer matches the exact authorized inventory tuple") {
		t.Fatalf("predelete inventory change was not rejected: %v", err)
	}
	if eventIndex(fake.events, "delete:"+expected[0].ID) >= 0 {
		t.Fatalf("old index was deleted after inventory drift: %v", fake.events)
	}
	assertIDsPresent(t, fake.indexes, expected[0].ID, expected[1].ID, expected[2].ID, expected[3].ID)
	assertIDAbsent(t, fake.indexes, "Observation/9000000101")
}

func TestLocalMigrationDeleteFailureRestoresOriginalAndRemovesCandidate(t *testing.T) {
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	fake.deleteThenError[expected[0].ID] = errors.New("old delete response lost")
	_, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || !strings.Contains(err.Error(), "old delete response lost") {
		t.Fatalf("old-index deletion failure not returned: %v", err)
	}
	assertIDsPresent(t, fake.indexes, expected[1].ID, expected[2].ID, expected[3].ID)
	assertIndexDefinitionPresent(t, fake.indexes, expected[0].Name, expected[0].Fields, expected[0].StoredValues)
	assertIDAbsent(t, fake.indexes, "Observation/9000000101")
	if eventIndex(fake.events, "delete:"+expected[0].ID) < 0 || eventIndex(fake.events, "create:"+expected[0].Name) < 0 || eventIndex(fake.events, "delete:Observation/9000000101") < 0 {
		t.Fatalf("expected exact old delete attempt and exact candidate cleanup: %v", fake.events)
	}
}

func TestLocalMigrationFinalReadFailureRestoresOldAndRemovesCandidate(t *testing.T) {
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	fake.failIndexRead[2] = errors.New("final read failed")
	_, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || !strings.Contains(err.Error(), "final read failed") {
		t.Fatalf("final inventory read failure not returned: %v", err)
	}
	assertIDsPresent(t, fake.indexes, expected[1].ID, expected[2].ID, expected[3].ID)
	assertIndexDefinitionPresent(t, fake.indexes, expected[0].Name, expected[0].Fields, expected[0].StoredValues)
	assertIDAbsent(t, fake.indexes, "Observation/9000000101")
	if eventIndex(fake.events, "create:"+expected[0].Name) < 0 {
		t.Fatalf("old definition was not restored after final read failure: %v", fake.events)
	}
}

func TestLocalMigrationReportsCandidateCleanupFailure(t *testing.T) {
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	fake.failDelete[expected[0].ID] = errors.New("old delete rejected")
	fake.failDelete["Observation/9000000101"] = errors.New("candidate cleanup rejected")
	_, err := transitionLocalIndex(context.Background(), fake, expected, candidate, true, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err == nil || !strings.Contains(err.Error(), "remove candidate after restoring old inventory") || !strings.Contains(err.Error(), "candidate cleanup rejected") {
		t.Fatalf("cleanup failure was not reported with its context: %v", err)
	}
	assertIDsPresent(t, fake.indexes, expected[0].ID, expected[1].ID, expected[2].ID, expected[3].ID, "Observation/9000000101")
}

func TestLocalMigrationReadonlyTransitionPerformsNoMutations(t *testing.T) {
	fake, initialAll, initialOwned := newFakeMigrationCollection()
	expected := expectedInventory()
	candidate := localCompilerIndexSpec()
	r, err := transitionLocalIndex(context.Background(), fake, expected, candidate, false, initialAll, initialOwned, report{OldIndexID: expected[0].ID})
	if err != nil || r.Outcome != "dry-run-ready" {
		t.Fatalf("readonly transition result=%q err=%v", r.Outcome, err)
	}
	for _, event := range fake.events {
		if strings.HasPrefix(event, "create:") || strings.HasPrefix(event, "delete:") {
			t.Fatalf("readonly transition invoked a mutation: %v", fake.events)
		}
	}
	if len(fake.indexes) != 4 {
		t.Fatalf("readonly inventory changed: %d indexes", len(fake.indexes))
	}
}

func eventIndex(events []string, event string) int {
	for i, candidate := range events {
		if candidate == event {
			return i
		}
	}
	return -1
}

func assertIDsPresent(t *testing.T, indexes []driver.IndexResponse, ids ...string) {
	t.Helper()
	present := make(map[string]bool, len(indexes))
	for _, index := range indexes {
		present[index.ID] = true
	}
	for _, id := range ids {
		if !present[id] {
			t.Errorf("expected index ID %q to remain; inventory=%v", id, present)
		}
	}
}

func assertIDAbsent(t *testing.T, indexes []driver.IndexResponse, id string) {
	t.Helper()
	for _, index := range indexes {
		if index.ID == id {
			t.Errorf("expected index ID %q to be absent", id)
		}
	}
}

func assertIndexDefinitionPresent(t *testing.T, indexes []driver.IndexResponse, name string, fields, storedValues []string) {
	t.Helper()
	for _, index := range indexes {
		if index.Name == name && matchesIndex(index, name, fields, storedValues) {
			return
		}
	}
	t.Errorf("expected exact index definition %q fields=%v storedValues=%v to remain", name, fields, storedValues)
}
