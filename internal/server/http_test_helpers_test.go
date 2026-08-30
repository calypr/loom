package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/gofiber/fiber/v3"
)

type compilerTestRegistry struct{}

type testCapabilityStore struct {
	byToken    map[string]capability.Snapshot
	byIdentity map[capability.SnapshotIdentity]string
}

func newTestCapabilityStore() *testCapabilityStore {
	return &testCapabilityStore{byToken: make(map[string]capability.Snapshot), byIdentity: make(map[capability.SnapshotIdentity]string)}
}

func (s *testCapabilityStore) Put(_ context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	stored := snapshot.Clone()
	s.byToken[stored.Token] = stored
	s.byIdentity[stored.Identity] = stored.Token
	result := stored.Clone()
	return &result, nil
}

func (s *testCapabilityStore) GetByToken(_ context.Context, token string) (*capability.Snapshot, error) {
	stored, ok := s.byToken[token]
	if !ok {
		return nil, capability.ErrNotFound
	}
	result := stored.Clone()
	return &result, nil
}

func (s *testCapabilityStore) GetByIdentity(_ context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	token, ok := s.byIdentity[identity]
	if !ok {
		return nil, capability.ErrNotFound
	}
	return s.GetByToken(context.Background(), token)
}

func (compilerTestRegistry) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, nil
}

func (r compilerTestRegistry) LoadRecipeVersion(ctx context.Context, name, _ string) (exec.Entry, error) {
	return r.LoadRecipe(ctx, name)
}

type testHTTPResponse struct {
	StatusCode int
	Body       string
}

func requestJSON(t *testing.T, app *fiber.App, method, path, body string) testHTTPResponse {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return testHTTPResponse{StatusCode: response.StatusCode, Body: string(raw)}
}

type testExplorerStore struct {
	explorer.Store
	mu        sync.Mutex
	explorers map[string]explorer.Explorer
	receipts  map[string]explorer.CompilationReceipt
	revisions map[string]explorer.Revision
}

func newTestExplorerStore() *testExplorerStore {
	return &testExplorerStore{explorers: map[string]explorer.Explorer{}, receipts: map[string]explorer.CompilationReceipt{}, revisions: map[string]explorer.Revision{}}
}

func testExplorerKey(project, id string) string { return project + "\x00" + id }

func (s *testExplorerStore) List(_ context.Context, project string) ([]explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := []explorer.Explorer{}
	for _, value := range s.explorers {
		if value.Project == project {
			values = append(values, value)
		}
	}
	return values, nil
}

func (s *testExplorerStore) Get(_ context.Context, project, id string) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.explorers[testExplorerKey(project, id)]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *testExplorerStore) Create(_ context.Context, value explorer.Explorer) (*explorer.Explorer, error) {
	return s.create(value)
}

func (s *testExplorerStore) create(value explorer.Explorer) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := testExplorerKey(value.Project, value.ExplorerID)
	if _, exists := s.explorers[key]; exists {
		return nil, explorer.ErrDraftConflict
	}
	s.explorers[key] = value
	return &value, nil
}

func (s *testExplorerStore) SaveDraft(_ context.Context, value explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := testExplorerKey(value.Project, value.ExplorerID)
	prior, exists := s.explorers[key]
	if !exists {
		return nil, explorer.ErrNotFound
	}
	if prior.DraftVersion != expected || (len(expectedDigest) > 0 && expectedDigest[0] != "" && prior.DraftDigest != expectedDigest[0]) {
		return nil, explorer.ErrDraftConflict
	}
	value.DraftVersion++
	s.explorers[key] = value
	return &value, nil
}

func (s *testExplorerStore) InsertCompilationReceipt(_ context.Context, value explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.receipts[value.ID]; ok {
		return &existing, nil
	}
	s.receipts[value.ID] = value
	return &value, nil
}

func (s *testExplorerStore) GetCompilationReceipt(_ context.Context, id string) (*explorer.CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.receipts[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *testExplorerStore) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.CompilationReceipt, error) {
	value, err := s.GetCompilationReceipt(ctx, id)
	if err != nil || value.Project != project || value.ExplorerID != explorerID {
		return nil, explorer.ErrNotFound
	}
	return value, nil
}

func (s *testExplorerStore) InsertRevision(_ context.Context, value explorer.Revision) (*explorer.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.revisions[value.ID]; ok {
		return &existing, nil
	}
	s.revisions[value.ID] = value
	return &value, nil
}

func (s *testExplorerStore) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *testExplorerStore) FailRevision(_ context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	value.Status, value.Diagnostics = explorer.RevisionFailed, append([]explorer.Diagnostic(nil), diagnostics...)
	s.revisions[id] = value
	return &value, nil
}

func (s *testExplorerStore) ActivateRepositoryGeneration(_ context.Context, project, _ string, revisionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activateLocked(project, "default", revisionID)
}

func (s *testExplorerStore) activateLocked(project, explorerID, revisionID string) error {
	revision, ok := s.revisions[revisionID]
	if !ok {
		return explorer.ErrNotFound
	}
	key := testExplorerKey(project, explorerID)
	owner, ok := s.explorers[key]
	if !ok {
		return explorer.ErrNotFound
	}
	owner.ActiveRevisionID = revision.ID
	owner.UpdatedAt = time.Now().UTC()
	s.explorers[key] = owner
	revision.Status = explorer.RevisionActive
	s.revisions[revisionID] = revision
	return nil
}

func baselineExplorerWorkspaceV2() []byte {
	visible, order := true, 0
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"}, Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients", RowLabel: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Columns: []authoringv2.Column{{Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "id", ProjectionMode: "FIRST"}, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order}}}}}, Tabs: []authoringv2.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}}
	raw, _ := json.Marshal(workspace)
	return raw
}
