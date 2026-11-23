package explorerfixtures

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

type Store struct {
	explorer.ExplorerStore
	explorer.RevisionStore
	Mu             sync.Mutex
	Explorers      map[string]explorer.Explorer
	Receipts       map[string]explorer.CompilationReceipt
	Revisions      map[string]explorer.Revision
	DraftRevisions map[string]explorer.DraftRevision
}

func NewStore() *Store {
	return &Store{Explorers: map[string]explorer.Explorer{}, Receipts: map[string]explorer.CompilationReceipt{}, Revisions: map[string]explorer.Revision{}, DraftRevisions: map[string]explorer.DraftRevision{}}
}

func Key(project, id string) string { return project + "\x00" + id }

func (s *Store) List(_ context.Context, project string) ([]explorer.Explorer, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	values := []explorer.Explorer{}
	for _, value := range s.Explorers {
		if value.Project == project {
			values = append(values, value)
		}
	}
	return values, nil
}

func (s *Store) Get(_ context.Context, project, id string) (*explorer.Explorer, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	value, ok := s.Explorers[Key(project, id)]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *Store) Create(_ context.Context, value explorer.Explorer) (*explorer.Explorer, error) {
	return s.create(value)
}

func (s *Store) create(value explorer.Explorer) (*explorer.Explorer, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	key := Key(value.Project, value.ExplorerID)
	if _, exists := s.Explorers[key]; exists {
		return nil, explorer.ErrDraftConflict
	}
	s.Explorers[key] = value
	return &value, nil
}

func (s *Store) SaveDraft(_ context.Context, value explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	key := Key(value.Project, value.ExplorerID)
	prior, exists := s.Explorers[key]
	if !exists {
		return nil, explorer.ErrNotFound
	}
	if prior.DraftVersion != expected || (len(expectedDigest) > 0 && expectedDigest[0] != "" && prior.DraftDigest != expectedDigest[0]) {
		return nil, explorer.ErrDraftConflict
	}
	if s.DraftRevisions == nil {
		s.DraftRevisions = map[string]explorer.DraftRevision{}
	}
	revisionID := fmt.Sprintf("draft_revision_%s_%d", prior.ExplorerID, prior.DraftVersion)
	s.DraftRevisions[Key(prior.Project, prior.ExplorerID)+"\x00"+revisionID] = explorer.DraftRevision{
		ID: revisionID, Project: prior.Project, ExplorerID: prior.ExplorerID, DraftVersion: prior.DraftVersion,
		DraftDigest: prior.DraftDigest, DraftConfig: append([]byte(nil), prior.DraftConfig...), Title: prior.Title,
		SnapshotToken: prior.DraftSnapshotToken, SourceGeneration: prior.DraftSourceGeneration,
		AuthorizationScopeDigest: prior.DraftAuthorizationScopeDigest, UpdatedBy: prior.UpdatedBy, UpdatedAt: prior.UpdatedAt,
	}
	value.PreviousDraftRevisionID = revisionID
	value.DraftVersion++
	s.Explorers[key] = value
	return &value, nil
}

func (s *Store) GetDraftRevision(_ context.Context, project, explorerID, revisionID string) (*explorer.DraftRevision, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	value, ok := s.DraftRevisions[Key(project, explorerID)+"\x00"+revisionID]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	value.DraftConfig = append([]byte(nil), value.DraftConfig...)
	return &value, nil
}

func (s *Store) InsertCompilationReceipt(_ context.Context, value explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if existing, ok := s.Receipts[value.ID]; ok {
		return &existing, nil
	}
	s.Receipts[value.ID] = value
	return &value, nil
}

func (s *Store) GetCompilationReceipt(_ context.Context, id string) (*explorer.CompilationReceipt, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	value, ok := s.Receipts[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *Store) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.CompilationReceipt, error) {
	value, err := s.GetCompilationReceipt(ctx, id)
	if err != nil || value.Project != project || value.ExplorerID != explorerID {
		return nil, explorer.ErrNotFound
	}
	return value, nil
}

func (s *Store) InsertRevision(_ context.Context, value explorer.Revision) (*explorer.Revision, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if existing, ok := s.Revisions[value.ID]; ok {
		return &existing, nil
	}
	s.Revisions[value.ID] = value
	return &value, nil
}

func (s *Store) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	value, ok := s.Revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return &value, nil
}

func (s *Store) FailRevision(_ context.Context, id string, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	value, ok := s.Revisions[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	value.Status, value.Diagnostics = explorer.RevisionFailed, append([]explorer.Diagnostic(nil), diagnostics...)
	s.Revisions[id] = value
	return &value, nil
}

func (s *Store) ActivateRepositoryGeneration(_ context.Context, project, _ string, revisionID string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.activateLocked(project, "default", revisionID)
}

func (s *Store) activateLocked(project, explorerID, revisionID string) error {
	revision, ok := s.Revisions[revisionID]
	if !ok {
		return explorer.ErrNotFound
	}
	key := Key(project, explorerID)
	owner, ok := s.Explorers[key]
	if !ok {
		return explorer.ErrNotFound
	}
	owner.ActiveRevisionID = revision.ID
	owner.UpdatedAt = time.Now().UTC()
	s.Explorers[key] = owner
	revision.Status = explorer.RevisionActive
	s.Revisions[revisionID] = revision
	return nil
}
