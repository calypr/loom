package explorer

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is used by service tests and local wiring; durable deployments
// use the Arango implementation.
type MemoryStore struct {
	mu                sync.Mutex
	explorers         map[string]Explorer
	revisions         map[string]Revision
	configs           map[string]RepositoryConfig
	repositoryConfigs map[string]RepositoryConfig
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{explorers: map[string]Explorer{}, revisions: map[string]Revision{}, configs: map[string]RepositoryConfig{}, repositoryConfigs: map[string]RepositoryConfig{}}
}
func configKey(project, id string) string { return project + "\x00" + id }
func (s *MemoryStore) ListConfigs(_ context.Context, project string) ([]RepositoryConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []RepositoryConfig{}
	for _, value := range s.configs {
		if value.Project == project {
			out = append(out, value)
		}
	}
	if value, ok := s.repositoryConfigs[project]; ok {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExplorerID < out[j].ExplorerID })
	return out, nil
}
func (s *MemoryStore) GetConfig(_ context.Context, project, id string) (*RepositoryConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.configs[configKey(project, id)]
	if !ok {
		return nil, ErrNotFound
	}
	return &v, nil
}
func (s *MemoryStore) SaveConfig(_ context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.UpdatedAt = time.Now().UTC()
	value.Config = append([]byte(nil), value.Config...)
	s.configs[configKey(value.Project, value.ExplorerID)] = value
	return &value, nil
}
func (s *MemoryStore) GetRepositoryConfig(ctx context.Context, project string) (*RepositoryConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.repositoryConfigs[project]
	if !ok {
		return nil, ErrNotFound
	}
	return &v, nil
}
func (s *MemoryStore) SaveRepositoryConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.ExplorerID, value.Management = "default", ManagementRepository
	value.UpdatedAt = time.Now().UTC()
	value.Config = append([]byte(nil), value.Config...)
	s.repositoryConfigs[value.Project] = value
	return &value, nil
}
func explorerKey(project, id string) string { return project + "\x00" + id }
func (s *MemoryStore) List(_ context.Context, project string) ([]Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Explorer{}
	for _, e := range s.explorers {
		if e.Project == project {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExplorerID < out[j].ExplorerID })
	return out, nil
}
func (s *MemoryStore) Get(_ context.Context, p, id string) (*Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.explorers[explorerKey(p, id)]
	if !ok {
		return nil, ErrNotFound
	}
	return &e, nil
}
func (s *MemoryStore) CreateInteractive(_ context.Context, e Explorer) (*Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := explorerKey(e.Project, e.ExplorerID)
	if _, ok := s.explorers[k]; ok {
		return nil, ErrDraftConflict
	}
	s.explorers[k] = e
	return &e, nil
}
func (s *MemoryStore) CreateRepository(ctx context.Context, e Explorer) (*Explorer, error) {
	return s.CreateInteractive(ctx, e)
}
func (s *MemoryStore) SaveDraft(_ context.Context, e Explorer, expected int64, expectedDigest ...string) (*Explorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := explorerKey(e.Project, e.ExplorerID)
	old, ok := s.explorers[k]
	if !ok {
		return nil, ErrNotFound
	}
	e.DraftVersion = old.DraftVersion + 1
	e.UpdatedAt = time.Now().UTC()
	e.DraftConfig = append([]byte(nil), e.DraftConfig...)
	e.ActiveConfig = append([]byte(nil), e.ActiveConfig...)
	s.explorers[k] = e
	return &e, nil
}
func (s *MemoryStore) InsertRevision(_ context.Context, r Revision) (*Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.revisions[r.ID]; ok {
		return &old, nil
	}
	s.revisions[r.ID] = r
	return &r, nil
}
func (s *MemoryStore) GetRevision(_ context.Context, id string) (*Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.revisions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &r, nil
}
func (s *MemoryStore) TransitionRevision(_ context.Context, id string, status RevisionStatus, diags []Diagnostic) (*Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.revisions[id]
	if !ok {
		return nil, ErrNotFound
	}
	r.Status = status
	r.Diagnostics = append([]Diagnostic(nil), diags...)
	now := time.Now().UTC()
	if status == RevisionReady {
		r.ReadyAt = &now
	}
	if status == RevisionFailed {
		r.FailedAt = &now
	}
	s.revisions[id] = r
	return &r, nil
}
func (s *MemoryStore) ActivateInteractive(_ context.Context, p, id, revisionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := explorerKey(p, id)
	e, ok := s.explorers[k]
	if !ok {
		return ErrNotFound
	}
	r, ok := s.revisions[revisionID]
	if !ok {
		return ErrNotFound
	}
	if r.Status != RevisionReady {
		return ErrImmutableRevision
	}
	if e.ActiveRevisionID != "" {
		prior := s.revisions[e.ActiveRevisionID]
		prior.Status = RevisionSuperseded
		s.revisions[prior.ID] = prior
	}
	now := time.Now().UTC()
	r.Status = RevisionActive
	r.ActivatedAt = &now
	s.revisions[r.ID] = r
	e.ActiveRevisionID = r.ID
	e.ActiveConfig = append([]byte(nil), r.Config...)
	e.RecipeDigest = r.RecipeDigest
	e.ResolvedSchemaDigest = r.ResolvedSchemaDigest
	e.SourceGeneration = r.SourceGeneration
	e.Dataset = r.Dataset
	e.Publication = r.Publication
	e.Publication.State = string(RevisionActive)
	e.Publication.RevisionID = r.ID
	e.Publication.UpdatedAt = now
	e.EmittedColumns = append([]EmittedColumn(nil), r.EmittedColumns...)
	e.Materializations = append([]Materialization(nil), r.Materializations...)
	e.Diagnostics = append([]Diagnostic(nil), r.Diagnostics...)
	s.explorers[k] = e
	return nil
}
func (s *MemoryStore) ActivateRepository(_ context.Context, project, revisionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.explorers[explorerKey(project, "default")]
	if !ok {
		return ErrNotFound
	}
	r, ok := s.revisions[revisionID]
	if !ok {
		return ErrNotFound
	}
	if e.ManagementMode != ManagementRepository || (r.Status != RevisionReady && !(r.Status == RevisionActive && e.ActiveRevisionID == revisionID)) {
		return ErrImmutableRevision
	}
	if e.ActiveRevisionID != "" && e.ActiveRevisionID != revisionID {
		prior := s.revisions[e.ActiveRevisionID]
		prior.Status = RevisionSuperseded
		s.revisions[prior.ID] = prior
	}
	now := time.Now().UTC()
	r.Status = RevisionActive
	r.ActivatedAt = &now
	s.revisions[r.ID] = r
	e.ActiveRevisionID = r.ID
	e.ActiveConfig = append([]byte(nil), r.Config...)
	e.RecipeDigest = r.RecipeDigest
	e.ResolvedSchemaDigest = r.ResolvedSchemaDigest
	e.SourceGeneration = r.SourceGeneration
	e.Materializations, e.Dataset = WithDataframeSelectors(r.Recipe, r.Materializations, r.Dataset)
	e.Publication = r.Publication
	e.Publication.State = string(RevisionActive)
	e.Publication.RevisionID = r.ID
	e.Publication.UpdatedAt = now
	e.EmittedColumns = append([]EmittedColumn(nil), r.EmittedColumns...)
	e.Diagnostics = append([]Diagnostic(nil), r.Diagnostics...)
	s.explorers[explorerKey(project, "default")] = e
	return nil
}
func (s *MemoryStore) ActivateRepositoryGeneration(ctx context.Context, project, _ string, revisionID string) error {
	return s.ActivateRepository(ctx, project, revisionID)
}
