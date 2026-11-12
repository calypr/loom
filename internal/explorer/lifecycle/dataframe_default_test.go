package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type dataframeDefaultStore struct {
	explorer.Store
	mu      sync.Mutex
	owners  map[string]explorer.Explorer
	creates int
	saves   int
}

func (s *dataframeDefaultStore) Get(_ context.Context, project, id string) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.owners[project+"\x00"+id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	copy := value
	copy.DraftConfig = append(json.RawMessage(nil), value.DraftConfig...)
	return &copy, nil
}

func (s *dataframeDefaultStore) Create(_ context.Context, value explorer.Explorer) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := value.Project + "\x00" + value.ExplorerID
	if _, ok := s.owners[key]; ok {
		return nil, explorer.ErrDraftConflict
	}
	s.owners[key] = value
	s.creates++
	copy := value
	return &copy, nil
}

func (s *dataframeDefaultStore) SaveDraft(_ context.Context, value explorer.Explorer, expected int64, digests ...string) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := value.Project + "\x00" + value.ExplorerID
	current, ok := s.owners[key]
	if !ok || current.DraftVersion != expected || (len(digests) > 0 && current.DraftDigest != digests[0]) {
		return nil, explorer.ErrDraftConflict
	}
	value.DraftVersion = expected + 1
	s.owners[key] = value
	s.saves++
	copy := value
	return &copy, nil
}

func defaultDataframeFixture() authoringv2.Workspace {
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer:  authoringv2.ExplorerMetadata{Title: defaultDataframeTitle},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func defaultDataframeConfig(snapshot capability.Snapshot) Config {
	config := testConfig(snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil }
	config.DefaultDataframeWorkspace = func(context.Context, string, capability.Snapshot, authoringv2.CatalogSnapshot) (authoringv2.Workspace, error) {
		return defaultDataframeFixture(), nil
	}
	return config
}

func TestBuilderUsesVirtualDefaultDataframeWithoutWritingOwner(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	viewerWorkspace := []byte(`{"viewer":"unchanged"}`)
	store := &dataframeDefaultStore{owners: map[string]explorer.Explorer{
		"project-a\x00default": {Project: "project-a", ExplorerID: "default", DraftConfig: append(json.RawMessage(nil), viewerWorkspace...)},
	}}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, defaultDataframeConfig(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.Builder(context.Background(), BuilderRequest{Project: "project-a", ExplorerID: DefaultDataframeID})
	if err != nil {
		t.Fatal(err)
	}
	workspace := defaultDataframeFixture()
	digest, _ := workspace.Digest()
	if state.DraftVersion != 1 || state.DraftDigest != digest || state.Workspace == nil || state.LifecycleState != authoringv2.LifecycleReady {
		t.Fatalf("virtual Builder state = %#v", state)
	}
	if store.creates != 0 || store.saves != 0 {
		t.Fatalf("GET wrote owner state: creates=%d saves=%d", store.creates, store.saves)
	}
	if got := store.owners["project-a\x00default"].DraftConfig; string(got) != string(viewerWorkspace) {
		t.Fatalf("viewer default owner changed: %s", got)
	}
}

func TestBuilderCorruptStoredDefaultDraftFailsClosedBeforeActiveBundle(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := &dataframeDefaultStore{owners: map[string]explorer.Explorer{
		"project-a\x00" + DefaultDataframeID: {Project: "project-a", ExplorerID: DefaultDataframeID, DraftVersion: 2, DraftConfig: json.RawMessage(`not-json`), ActiveRevisionID: "broken-active"},
	}}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, defaultDataframeConfig(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Builder(context.Background(), BuilderRequest{Project: "project-a", ExplorerID: DefaultDataframeID})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_STATE_INVALID" {
		t.Fatalf("Builder error = %v, want corrupt saved draft failure", err)
	}
}

func TestConcurrentFirstDefaultDataframeApplySeedsAndCASesOnce(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := &dataframeDefaultStore{owners: map[string]explorer.Explorer{}}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	config := defaultDataframeConfig(snapshot)
	service, err := New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	workspace := defaultDataframeFixture()
	digest, _ := workspace.Digest()
	makeRequest := func(commandID, title string) authoringv2.ApplyCommandsRequest {
		return authoringv2.ApplyCommandsRequest{
			CommandID: commandID, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
			SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
			Commands: []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: title}},
		}
	}
	type result struct {
		response *authoringv2.ApplyCommandsResponse
		err      error
	}
	results := make(chan result, 2)
	var start sync.WaitGroup
	start.Add(1)
	for index, title := range []string{"People", "Subjects"} {
		go func(index int, title string) {
			start.Wait()
			response, applyErr := service.ApplyCommands(context.Background(), "project-a", DefaultDataframeID, makeRequest(fmt.Sprintf("command-%d", index), title), "actor")
			results <- result{response: response, err: applyErr}
		}(index, title)
	}
	start.Done()
	wins := 0
	for range 2 {
		value := <-results
		if value.err == nil {
			wins++
		} else if !errors.Is(value.err, explorer.ErrDraftConflict) {
			t.Fatalf("first Apply error = %v, want draft CAS conflict or success", value.err)
		}
	}
	if wins != 1 || store.creates != 1 || store.saves != 1 {
		t.Fatalf("first Apply race: wins=%d creates=%d saves=%d", wins, store.creates, store.saves)
	}
	owner := store.owners["project-a\x00"+DefaultDataframeID]
	if owner.DraftVersion != 2 {
		t.Fatalf("saved default dataframe draft version = %d, want 2", owner.DraftVersion)
	}
}

func TestCreateCopiesVirtualDefaultWithoutPersistingDefaultOwner(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := &dataframeDefaultStore{owners: map[string]explorer.Explorer{}}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, defaultDataframeConfig(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), CreateRequest{Project: "project-a", Name: "custom", Title: "Custom dataframe", SourceExplorerID: DefaultDataframeID, Actor: "actor"})
	if err != nil {
		t.Fatal(err)
	}
	if store.creates != 1 || store.saves != 0 {
		t.Fatalf("copy writes: create=%d save=%d", store.creates, store.saves)
	}
	if _, ok := store.owners["project-a\x00"+DefaultDataframeID]; ok {
		t.Fatal("copying wrote the inherited default")
	}
	owner, err := store.Get(context.Background(), "project-a", created.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Explorer.Title != "Custom dataframe" || len(workspace.Documents) != 1 {
		t.Fatalf("copy=%+v", workspace)
	}
}

type reviewHistoryStore struct {
	*dataframeDefaultStore
	revision explorer.DraftRevision
}

func (s *reviewHistoryStore) SaveDraft(ctx context.Context, value explorer.Explorer, expected int64, digests ...string) (*explorer.Explorer, error) {
	previous, _ := s.Get(ctx, value.Project, value.ExplorerID)
	s.revision = explorer.DraftRevision{ID: "review-prior", Project: previous.Project, ExplorerID: previous.ExplorerID, DraftVersion: previous.DraftVersion, DraftDigest: previous.DraftDigest, DraftConfig: previous.DraftConfig, SnapshotToken: previous.DraftSnapshotToken, SourceGeneration: previous.DraftSourceGeneration, AuthorizationScopeDigest: previous.DraftAuthorizationScopeDigest}
	value.PreviousDraftRevisionID = s.revision.ID
	return s.dataframeDefaultStore.SaveDraft(ctx, value, expected, digests...)
}
func (s *reviewHistoryStore) GetDraftRevision(context.Context, string, string, string) (*explorer.DraftRevision, error) {
	return &s.revision, nil
}
func TestUndoFirstDefaultApply(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	store := &reviewHistoryStore{dataframeDefaultStore: &dataframeDefaultStore{owners: map[string]explorer.Explorer{}}}
	domain, _ := explorer.NewService(store)
	service, _ := New(domain, defaultDataframeConfig(snapshot))
	workspace := defaultDataframeFixture()
	digest, _ := workspace.Digest()
	response, err := service.ApplyCommands(context.Background(), "project-a", DefaultDataframeID, authoringv2.ApplyCommandsRequest{CommandID: "rename-first", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Commands: []authoringv2.Command{{Type: authoringv2.CommandRenameTable, OutputID: "patients", Title: "People"}}}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", DefaultDataframeID, authoringv2.ApplyCommandsRequest{CommandID: "undo-first", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: response.DraftVersion, ExpectedDraftDigest: response.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandRestoreDraftRevision, DraftRevisionID: response.PreviousDraftRevisionID}}}, "actor")
	if err != nil {
		t.Fatalf("Undo first Apply failed: %v; prior provenance token=%q generation=%q scope=%q", err, store.revision.SnapshotToken, store.revision.SourceGeneration, store.revision.AuthorizationScopeDigest)
	}
}
