package datasetstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestCollectionSpecsKeepLifecycleMetadataOutOfTruncateBootstrap(t *testing.T) {
	specs := CollectionSpecs()
	if len(specs) != 1 {
		t.Fatalf("CollectionSpecs() length = %d, want 1", len(specs))
	}
	spec := specs[0]
	if spec.Name != LifecycleCollection {
		t.Fatalf("collection name = %q, want %q", spec.Name, LifecycleCollection)
	}
	if spec.Edge || spec.Truncate {
		t.Fatalf("lifecycle collection edge/truncate = %t/%t, want false/false", spec.Edge, spec.Truncate)
	}
	for _, required := range [][]string{
		{"recordType", "dataset.project", "dataset.generation"},
		{"recordType", "state", "dataset.project"},
		{"recordType", "project"},
	} {
		if !hasIndex(spec.Indexes, required) {
			t.Fatalf("indexes %#v do not contain %#v", spec.Indexes, required)
		}
	}

	bootstrap := BootstrapSpec()
	if len(bootstrap.Collections) != 1 || bootstrap.Collections[0].Name != LifecycleCollection {
		t.Fatalf("BootstrapSpec() = %#v, want only %q", bootstrap, LifecycleCollection)
	}
}

func TestNewValidatesDependencyAndCursorBatch(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNilQueryClient) {
		t.Fatalf("New(nil) error = %v, want ErrNilQueryClient", err)
	}
	if _, err := NewWithBatchSize(&fakeQueryClient{}, 0); !errors.Is(err, ErrInvalidCursorBatchSize) {
		t.Fatalf("NewWithBatchSize(..., 0) error = %v, want ErrInvalidCursorBatchSize", err)
	}
	store, err := NewWithBatchSize(&fakeQueryClient{}, 7)
	if err != nil {
		t.Fatalf("NewWithBatchSize: %v", err)
	}
	if store.batchSize != 7 {
		t.Fatalf("batch size = %d, want 7", store.batchSize)
	}
}

func TestCreateManifestBuildsOpaqueBoundDocumentsAndNeverPersistsAuthScope(t *testing.T) {
	manifest := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStatePreflight)
	fake := &fakeQueryClient{responses: [][]map[string]any{{
		{
			"recordType": manifestRecordType,
			"manifest":   jsonObject(t, manifest),
		},
		{"recordType": activeRecordType, "manifest": nil},
	}}}
	store := mustStore(t, fake)

	created, err := store.CreateManifest(context.Background(), manifest)
	if err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	if !manifestIdentityEqual(created, manifest) {
		t.Fatalf("created manifest = %#v, want %#v", created, manifest)
	}
	call := fake.onlyCall(t)
	assertAllAQLBindsSupplied(t, call)
	if strings.Count(call.query, "INSERT ") != 1 || !strings.Contains(call.query, "@@lifecycle_collection") {
		t.Fatalf("create query is not the expected single INSERT statement:\n%s", call.query)
	}
	if strings.Contains(call.query, manifest.Dataset.Project) || strings.Contains(call.query, manifest.Dataset.Generation) {
		t.Fatalf("create query interpolated request identity:\n%s", call.query)
	}
	if got := call.bindVars["@lifecycle_collection"]; got != LifecycleCollection {
		t.Fatalf("collection bind = %#v, want %q", got, LifecycleCollection)
	}
	stored, ok := call.bindVars["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("manifest bind type = %T, want map[string]any", call.bindVars["manifest"])
	}
	if stored["_key"] != manifestDocumentKey(manifest.Dataset) || stored["recordType"] != manifestRecordType {
		t.Fatalf("manifest bind identity = %#v", stored)
	}
	if call.bindVars["manifest_key"] != manifestDocumentKey(manifest.Dataset) || call.bindVars["active_key"] != activeDocumentKey(manifest.Dataset.Project) {
		t.Fatalf("create document key binds = %#v", call.bindVars)
	}
	activePlaceholder, ok := call.bindVars["active_placeholder"].(map[string]any)
	if !ok {
		t.Fatalf("active placeholder bind type = %T", call.bindVars["active_placeholder"])
	}
	if activePlaceholder["_key"] != activeDocumentKey(manifest.Dataset.Project) || activePlaceholder["project"] != manifest.Dataset.Project {
		t.Fatalf("active placeholder = %#v", activePlaceholder)
	}
	assertNoAuthScope(t, call.bindVars)

	if strings.Contains(manifestDocumentKey(manifest.Dataset), manifest.Dataset.Project) || strings.Contains(activeDocumentKey(manifest.Dataset.Project), manifest.Dataset.Project) {
		t.Fatal("deterministic document key leaked the raw project")
	}
}

func TestCreateManifestRejectsNonPreflightAndDoesNotQuery(t *testing.T) {
	fake := &fakeQueryClient{}
	store := mustStore(t, fake)
	ready := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateReady)
	if _, err := store.CreateManifest(context.Background(), ready); !errors.Is(err, dataset.ErrInvalidTransition) {
		t.Fatalf("CreateManifest(READY) error = %v, want ErrInvalidTransition", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("CreateManifest(READY) made %d queries", len(fake.calls))
	}
}

func TestReadAndTransitionManifestUseExactIdentityAndBoundValues(t *testing.T) {
	loading := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateLoading)
	analyzing, err := loading.Transition(dataset.ManifestStateAnalyzing)
	if err != nil {
		t.Fatalf("loading.Transition(ANALYZING): %v", err)
	}
	fake := &fakeQueryClient{responses: [][]map[string]any{
		{jsonObject(t, loading)},
		{jsonObject(t, analyzing)},
	}}
	store := mustStore(t, fake)

	read, err := store.ReadManifest(context.Background(), loading.Dataset)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if !manifestIdentityEqual(read, loading) {
		t.Fatalf("ReadManifest = %#v, want %#v", read, loading)
	}
	readCall := fake.calls[0]
	assertAllAQLBindsSupplied(t, readCall)
	if !strings.Contains(readCall.query, "manifest._key == @manifest_key") || strings.Contains(readCall.query, loading.Dataset.Project) {
		t.Fatalf("read query/binding is unsafe:\n%s", readCall.query)
	}
	if got := readCall.bindVars["manifest_key"]; got != manifestDocumentKey(loading.Dataset) {
		t.Fatalf("read manifest key = %#v", got)
	}

	transitioned, err := store.TransitionManifest(context.Background(), loading, dataset.ManifestStateAnalyzing)
	if err != nil {
		t.Fatalf("TransitionManifest: %v", err)
	}
	if !manifestIdentityEqual(transitioned, analyzing) {
		t.Fatalf("TransitionManifest = %#v, want %#v", transitioned, analyzing)
	}
	transitionCall := fake.calls[1]
	assertAllAQLBindsSupplied(t, transitionCall)
	if strings.Count(transitionCall.query, "UPDATE ") != 1 || !strings.Contains(transitionCall.query, "manifest.schemaIdentity == @schema_identity") || !strings.Contains(transitionCall.query, "manifest.analysisVersion == @analysis_version") {
		t.Fatalf("transition query is missing immutable guards:\n%s", transitionCall.query)
	}
	if transitionCall.bindVars["expected_state"] != string(dataset.ManifestStateLoading) || transitionCall.bindVars["next_state"] != string(dataset.ManifestStateAnalyzing) {
		t.Fatalf("transition state binds = %#v", transitionCall.bindVars)
	}
	if _, ok := transitionCall.bindVars["schema_identity"].(map[string]any); !ok {
		t.Fatalf("schema identity bind type = %T", transitionCall.bindVars["schema_identity"])
	}
	assertNoAuthScope(t, transitionCall.bindVars)
}

func TestTransitionManifestRejectsInvalidTransitionBeforeQuery(t *testing.T) {
	fake := &fakeQueryClient{}
	store := mustStore(t, fake)
	ready := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateReady)
	if _, err := store.TransitionManifest(context.Background(), ready, dataset.ManifestStateLoading); !errors.Is(err, dataset.ErrInvalidTransition) {
		t.Fatalf("TransitionManifest(READY -> LOADING) error = %v, want ErrInvalidTransition", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("invalid transition made %d queries", len(fake.calls))
	}
}

func TestReadActiveRequiresPersistedReadyManifest(t *testing.T) {
	ready := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateReady)
	fake := &fakeQueryClient{responses: [][]map[string]any{{
		{
			"active":   map[string]any{"dataset": jsonObject(t, ready.Dataset)},
			"manifest": jsonObject(t, ready),
		},
	}}}
	store := mustStore(t, fake)

	active, err := store.ReadActive(context.Background(), ready.Dataset.Project)
	if err != nil {
		t.Fatalf("ReadActive: %v", err)
	}
	if !active.Dataset.Equal(ready.Dataset) {
		t.Fatalf("ReadActive dataset = %#v, want %#v", active.Dataset, ready.Dataset)
	}
	call := fake.onlyCall(t)
	assertAllAQLBindsSupplied(t, call)
	if !strings.Contains(call.query, "manifest.state == @ready_state") || !strings.Contains(call.query, "manifest.dataset == active.dataset") {
		t.Fatalf("read-active query does not validate persisted READY manifest:\n%s", call.query)
	}
	if call.bindVars["ready_state"] != string(dataset.ManifestStateReady) {
		t.Fatalf("read-active ready state bind = %#v", call.bindVars["ready_state"])
	}
	if got := call.bindVars["active_key"]; got != activeDocumentKey(ready.Dataset.Project) {
		t.Fatalf("active key = %#v", got)
	}
	assertNoAuthScope(t, call.bindVars)
}

func TestResolveActiveManifestReturnsReadySnapshotFromSameQuery(t *testing.T) {
	ready := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateReady)
	fake := &fakeQueryClient{responses: [][]map[string]any{{
		{
			"active":   map[string]any{"dataset": jsonObject(t, ready.Dataset)},
			"manifest": jsonObject(t, ready),
		},
	}}}
	store := mustStore(t, fake)

	resolved, err := store.ResolveActiveManifest(context.Background(), ready.Dataset.Project)
	if err != nil {
		t.Fatalf("ResolveActiveManifest: %v", err)
	}
	if !manifestIdentityEqual(resolved, ready) {
		t.Fatalf("ResolveActiveManifest = %#v, want %#v", resolved, ready)
	}
	call := fake.onlyCall(t)
	assertAllAQLBindsSupplied(t, call)
	if !strings.Contains(call.query, "active: { dataset: active.dataset }") || !strings.Contains(call.query, "manifest: {") {
		t.Fatalf("resolve query does not return the active/manifest snapshot:\n%s", call.query)
	}
}

func TestReadActiveRejectsInvalidProjectAndMissingReadyPointer(t *testing.T) {
	fake := &fakeQueryClient{}
	store := mustStore(t, fake)
	if _, err := store.ReadActive(context.Background(), " project-a"); !errors.Is(err, dataset.ErrInvalidDatasetRef) {
		t.Fatalf("ReadActive(invalid project) error = %v, want ErrInvalidDatasetRef", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("invalid project made %d queries", len(fake.calls))
	}
	if _, err := store.ReadActive(context.Background(), "project-a"); !errors.Is(err, ErrActiveGenerationNotFound) {
		t.Fatalf("ReadActive(missing) error = %v, want ErrActiveGenerationNotFound", err)
	}
}

func TestActivateUsesOneUpdateToSupersedeAndSelectReadyCandidate(t *testing.T) {
	previous := fixtureManifest(t, "project-a", "generation-old", dataset.ManifestStateReady)
	candidate := fixtureManifest(t, "project-a", "generation-new", dataset.ManifestStateReady)
	fake := &fakeQueryClient{responses: [][]map[string]any{{
		{
			"role":     "candidate_guard",
			"dataset":  jsonObject(t, candidate.Dataset),
			"previous": nil,
		},
		{
			"role":     "superseded_manifest",
			"dataset":  nil,
			"previous": jsonObject(t, previous.Dataset),
		},
		{
			"role":     activeRecordType,
			"dataset":  jsonObject(t, candidate.Dataset),
			"previous": nil,
		},
	}}}
	store := mustStore(t, fake)

	plan, err := store.Activate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !plan.Active.Dataset.Equal(candidate.Dataset) {
		t.Fatalf("activation active = %#v, want %#v", plan.Active.Dataset, candidate.Dataset)
	}
	if plan.Previous == nil || !plan.Previous.Equal(previous.Dataset) {
		t.Fatalf("activation previous = %#v, want %#v", plan.Previous, previous.Dataset)
	}
	call := fake.onlyCall(t)
	assertAllAQLBindsSupplied(t, call)
	if strings.Count(call.query, "UPDATE ") != 1 || strings.Contains(call.query, "UPSERT") || strings.Contains(call.query, "INSERT ") {
		t.Fatalf("activation must be exactly one UPDATE operation:\n%s", call.query)
	}
	for _, fragment := range []string{
		"FILTER manifest.state == @ready_state",
		"active.dataset.project == @project",
		"active.manifestKey != null AND",
		"previous != null",
		"state: @superseded_state",
		"document: candidate, patch: { state: @ready_state }, role: @candidate_guard_role",
		"manifestKey: candidate._key",
		"OPTIONS { ignoreRevs: false, mergeObjects: false }",
	} {
		if !strings.Contains(call.query, fragment) {
			t.Fatalf("activation query missing %q:\n%s", fragment, call.query)
		}
	}
	if call.bindVars["candidate_key"] != manifestDocumentKey(candidate.Dataset) || call.bindVars["active_key"] != activeDocumentKey(candidate.Dataset.Project) {
		t.Fatalf("activation document keys = %#v", call.bindVars)
	}
	if call.bindVars["ready_state"] != string(dataset.ManifestStateReady) || call.bindVars["superseded_state"] != string(dataset.ManifestStateSuperseded) {
		t.Fatalf("activation state binds = %#v", call.bindVars)
	}
	assertNoAuthScope(t, call.bindVars)
}

func TestActivateRejectsNonReadyAndMissingPersistedCandidate(t *testing.T) {
	loading := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateLoading)
	fake := &fakeQueryClient{}
	store := mustStore(t, fake)
	if _, err := store.Activate(context.Background(), loading); !errors.Is(err, dataset.ErrGenerationNotReady) {
		t.Fatalf("Activate(LOADING) error = %v, want ErrGenerationNotReady", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("non-ready activation made %d queries", len(fake.calls))
	}

	ready := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStateReady)
	if _, err := store.Activate(context.Background(), ready); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("Activate(missing persisted candidate) error = %v, want ErrActivationConflict", err)
	}
}

func TestManifestReadRejectsInconsistentStoreResult(t *testing.T) {
	want := fixtureManifest(t, "project-a", "generation-a", dataset.ManifestStatePreflight)
	wrong := fixtureManifest(t, "project-a", "generation-b", dataset.ManifestStatePreflight)
	fake := &fakeQueryClient{responses: [][]map[string]any{{jsonObject(t, wrong)}}}
	store := mustStore(t, fake)
	if _, err := store.ReadManifest(context.Background(), want.Dataset); !errors.Is(err, ErrUnexpectedStoreResult) {
		t.Fatalf("ReadManifest(inconsistent row) error = %v, want ErrUnexpectedStoreResult", err)
	}
}

func fixtureManifest(t *testing.T, project, generation string, state dataset.ManifestState) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewDatasetRef(project, generation)
	if err != nil {
		t.Fatalf("NewDatasetRef: %v", err)
	}
	schema, err := dataset.NewSchemaIdentitySnapshot(
		"https://example.test/loom-fhir-schema",
		"4.0.1",
		strings.Repeat("a", 64),
		[]string{"Observation", "Patient"},
	)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot: %v", err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	for manifest.State != state {
		var next dataset.ManifestState
		switch manifest.State {
		case dataset.ManifestStatePreflight:
			next = dataset.ManifestStateLoading
		case dataset.ManifestStateLoading:
			next = dataset.ManifestStateAnalyzing
		case dataset.ManifestStateAnalyzing:
			next = dataset.ManifestStateReady
		default:
			t.Fatalf("cannot construct fixture state %s from %s", state, manifest.State)
		}
		manifest, err = manifest.Transition(next)
		if err != nil {
			t.Fatalf("fixture transition: %v", err)
		}
	}
	return manifest
}

func mustStore(t *testing.T, client QueryRowsClient) *Store {
	t.Helper()
	store, err := New(client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%T): %v", value, err)
	}
	return decoded
}

func hasIndex(indexes [][]string, want []string) bool {
	for _, index := range indexes {
		if reflect.DeepEqual(index, want) {
			return true
		}
	}
	return false
}

func assertNoAuthScope(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal(%T): %v", value, err)
	}
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"auth_resource_path", "authscope", "authorization", "token", "claims", "subject"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("persistent bind contains forbidden authorization data %q: %s", forbidden, encoded)
		}
	}
}

var aqlBindPattern = regexp.MustCompile(`@@?[A-Za-z_][A-Za-z0-9_]*`)

func assertAllAQLBindsSupplied(t *testing.T, call queryCall) {
	t.Helper()
	for _, token := range aqlBindPattern.FindAllString(call.query, -1) {
		key := token[1:]
		if _, found := call.bindVars[key]; !found {
			t.Fatalf("query bind %q from %q is missing from %#v", key, token, call.bindVars)
		}
	}
}

type queryCall struct {
	query    string
	batch    int
	bindVars map[string]any
}

type fakeQueryClient struct {
	responses [][]map[string]any
	err       error
	calls     []queryCall
}

func (f *fakeQueryClient) QueryRows(_ context.Context, query string, batchSize int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	call := queryCall{query: query, batch: batchSize, bindVars: cloneMap(bindVars)}
	f.calls = append(f.calls, call)
	responseIndex := len(f.calls) - 1
	if responseIndex < len(f.responses) {
		for _, row := range f.responses[responseIndex] {
			if err := visit(cloneMap(row)); err != nil {
				return err
			}
		}
	}
	return f.err
}

func (f *fakeQueryClient) onlyCall(t *testing.T) queryCall {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("query call count = %d, want 1", len(f.calls))
	}
	return f.calls[0]
}

func cloneMap(source map[string]any) map[string]any {
	data, _ := json.Marshal(source)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}
