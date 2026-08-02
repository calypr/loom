package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestBootstrapHasNoSecondaryIndexes(t *testing.T) {
	specs := CollectionSpecs()
	if len(specs) != 1 || specs[0].Name != LifecycleCollection || specs[0].Truncate || specs[0].Edge || len(specs[0].Indexes) != 0 {
		t.Fatalf("CollectionSpecs = %#v", specs)
	}
	if got := BootstrapSpec().Collections; !reflect.DeepEqual(got, specs) {
		t.Fatalf("BootstrapSpec = %#v", got)
	}
}

func TestCreateManifestCreatesActivePlaceholder(t *testing.T) {
	manifest := fixtureManifest(t, "project-a", "generation-a", publication.StateLoading)
	fake := &fakeQueryClient{responses: [][]map[string]any{{
		{"recordType": manifestRecordType, "manifest": jsonObject(t, manifest)},
		{"recordType": activeRecordType},
	}}}
	store := mustStore(t, fake)
	created, err := store.CreateManifest(context.Background(), manifest)
	if err != nil || !reflect.DeepEqual(created, manifest) {
		t.Fatalf("CreateManifest = %#v, %v", created, err)
	}
	call := fake.onlyCall(t)
	placeholder := call.bindVars["active_placeholder"].(map[string]any)
	if placeholder["_key"] != activeDocumentKey("project-a") || placeholder["project"] != "project-a" {
		t.Fatalf("placeholder = %#v", placeholder)
	}
	if call.batch != metadataBatchSize {
		t.Fatalf("batch = %d", call.batch)
	}
}

func TestTransitionAndResolveManifest(t *testing.T) {
	loading := fixtureManifest(t, "project-a", "generation-a", publication.StateLoading)
	ready, _ := loading.Transition(publication.StateReady)
	fake := &fakeQueryClient{responses: [][]map[string]any{{jsonObject(t, ready)}, {jsonObject(t, ready)}}}
	store := mustStore(t, fake)
	if got, err := store.TransitionManifest(context.Background(), loading, publication.StateReady); err != nil || !reflect.DeepEqual(got, ready) {
		t.Fatalf("TransitionManifest = %#v, %v", got, err)
	}
	if got, err := store.ResolveActiveManifest(context.Background(), "project-a"); err != nil || !reflect.DeepEqual(got, ready) {
		t.Fatalf("ResolveActiveManifest = %#v, %v", got, err)
	}
	if strings.Contains(fake.calls[0].query, "analysisVersion") || !strings.Contains(fake.calls[0].query, "schemaIdentity == @schema_identity") {
		t.Fatalf("transition query = %s", fake.calls[0].query)
	}
}

func TestActivateOnlyRevisionChecksActivePointer(t *testing.T) {
	ready := fixtureManifest(t, "project-a", "generation-a", publication.StateReady)
	fake := &fakeQueryClient{responses: [][]map[string]any{{{"dataset": jsonObject(t, ready.Dataset)}}}}
	store := mustStore(t, fake)
	if err := store.Activate(context.Background(), ready); err != nil {
		t.Fatal(err)
	}
	call := fake.onlyCall(t)
	for _, required := range []string{"manifest.state == @ready_state", "manifest.schemaIdentity == @schema_identity", "UPDATE active WITH", "ignoreRevs: false", "manifestKey: candidate._key"} {
		if !strings.Contains(call.query, required) {
			t.Fatalf("activation query missing %q:\n%s", required, call.query)
		}
	}
	for _, forbidden := range []string{"superseded", "patch: { state", "UPDATE candidate"} {
		if strings.Contains(strings.ToLower(call.query), strings.ToLower(forbidden)) {
			t.Fatalf("activation query contains %q:\n%s", forbidden, call.query)
		}
	}
}

func TestActivateRejectsNonReadyMissingAndConflictingCandidates(t *testing.T) {
	loading := fixtureManifest(t, "project-a", "generation-a", publication.StateLoading)
	fake := &fakeQueryClient{}
	store := mustStore(t, fake)
	if err := store.Activate(context.Background(), loading); !errors.Is(err, publication.ErrGenerationNotReady) {
		t.Fatalf("loading activation = %v", err)
	}
	ready, _ := loading.Transition(publication.StateReady)
	if err := store.Activate(context.Background(), ready); !errors.Is(err, ErrActivationConflict) {
		t.Fatalf("missing candidate = %v", err)
	}
	fake.err = errors.New("revision conflict")
	if err := store.Activate(context.Background(), ready); err == nil || !strings.Contains(err.Error(), "revision conflict") {
		t.Fatalf("concurrent conflict = %v", err)
	}
}

func TestLegacyManifestStatesNormalizeAndMetadataIsIgnored(t *testing.T) {
	base := jsonObject(t, fixtureManifest(t, "project-a", "generation-a", publication.StateLoading))
	base["analysisVersion"] = "legacy-v1"
	for legacy, want := range map[string]publication.State{
		"PREFLIGHT":  publication.StateLoading,
		"ANALYZING":  publication.StateLoading,
		"SUPERSEDED": publication.StateReady,
	} {
		row := cloneMap(base)
		row["state"] = legacy
		manifest, err := manifestFromValue(row)
		if err != nil || manifest.State != want {
			t.Errorf("state %s = %s, %v", legacy, manifest.State, err)
		}
	}
	base["state"] = "UNKNOWN"
	if _, err := manifestFromValue(base); !errors.Is(err, ErrUnexpectedStoreResult) {
		t.Fatalf("unknown state = %v", err)
	}
}

func TestNewAndMissingActive(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrNilQueryClient) {
		t.Fatalf("New(nil) = %v", err)
	}
	store := mustStore(t, &fakeQueryClient{})
	if _, err := store.ResolveActiveManifest(context.Background(), "project-a"); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("missing active = %v", err)
	}
}

func fixtureManifest(t *testing.T, project, id string, state publication.State) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, id)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:test", "R5", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	if state != publication.StateLoading {
		manifest, err = manifest.Transition(state)
		if err != nil {
			t.Fatal(err)
		}
	}
	return manifest
}

func mustStore(t *testing.T, client QueryRowsClient) *Store {
	t.Helper()
	store, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
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

func (f *fakeQueryClient) QueryRows(_ context.Context, query string, batch int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	f.calls = append(f.calls, queryCall{query, batch, cloneMap(bindVars)})
	i := len(f.calls) - 1
	if i < len(f.responses) {
		for _, row := range f.responses[i] {
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
		t.Fatalf("calls = %d", len(f.calls))
	}
	return f.calls[0]
}

func cloneMap(source map[string]any) map[string]any {
	data, _ := json.Marshal(source)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}
