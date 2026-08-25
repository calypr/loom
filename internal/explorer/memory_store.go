package explorer

import (
	"context"
	"encoding/json"
	"reflect"
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
	receipts          map[string]CompilationReceipt
	configs           map[string]RepositoryConfig
	repositoryConfigs map[string]RepositoryConfig
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{explorers: map[string]Explorer{}, revisions: map[string]Revision{}, receipts: map[string]CompilationReceipt{}, configs: map[string]RepositoryConfig{}, repositoryConfigs: map[string]RepositoryConfig{}}
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
func (s *MemoryStore) InsertCompilationReceipt(_ context.Context, receipt CompilationReceipt) (*CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateCompilationReceipt(receipt); err != nil {
		return nil, err
	}
	if prior, ok := s.receipts[receipt.ID]; ok {
		if !sameCompilationReceipt(prior, receipt) {
			return nil, ErrCorruptReceipt
		}
		copy := cloneReceipt(prior)
		return &copy, nil
	}
	s.receipts[receipt.ID] = cloneReceipt(receipt)
	copy := cloneReceipt(receipt)
	return &copy, nil
}
func (s *MemoryStore) GetCompilationReceipt(_ context.Context, id string) (*CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[id]
	if !ok {
		return nil, ErrNotFound
	}
	if err := validateCompilationReceipt(receipt); err != nil {
		return nil, err
	}
	copy := cloneReceipt(receipt)
	return &copy, nil
}

func (s *MemoryStore) GetCompilationReceiptForExplorer(_ context.Context, project, explorerID, id string) (*CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[id]
	if !ok || receipt.Project != project || receipt.ExplorerID != explorerID {
		return nil, ErrNotFound
	}
	if err := validateCompilationReceipt(receipt); err != nil {
		return nil, err
	}
	copy := cloneReceipt(receipt)
	return &copy, nil
}

func (s *MemoryStore) GetCompilationReceiptByCompilationKey(_ context.Context, project, explorerID, compilationKey string) (*CompilationReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found *CompilationReceipt
	for _, receipt := range s.receipts {
		if receipt.Project != project || receipt.ExplorerID != explorerID || receiptCompilationKey(receipt) != compilationKey {
			continue
		}
		if err := validateCompilationReceipt(receipt); err != nil {
			return nil, err
		}
		candidate := cloneReceipt(receipt)
		if found == nil || candidate.ID < found.ID {
			found = &candidate
		}
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

func (s *MemoryStore) CompilationReceiptStats(_ context.Context, project string) (ReceiptStoreStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := ReceiptStoreStats{}
	referenced := map[string]bool{}
	for _, revision := range s.revisions {
		if revision.CompilationReceiptID != "" {
			referenced[revision.CompilationReceiptID] = true
		}
	}
	for id, receipt := range s.receipts {
		if project != "" && receipt.Project != project {
			continue
		}
		if err := validateCompilationReceipt(receipt); err != nil {
			return ReceiptStoreStats{}, err
		}
		stats.Count++
		if raw, err := json.Marshal(receipt); err == nil {
			stats.ApproxBytes += int64(len(raw))
		}
		if stats.OldestCreatedAt.IsZero() || (!receipt.CreatedAt.IsZero() && receipt.CreatedAt.Before(stats.OldestCreatedAt)) {
			stats.OldestCreatedAt = receipt.CreatedAt
		}
		if !referenced[id] {
			stats.UnreferencedCount++
		}
	}
	return stats, nil
}

func (s *MemoryStore) PublishAuthoring(_ context.Context, receipt CompilationReceipt, revision Revision) (*Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := explorerKey(revision.Project, revision.ExplorerID)
	owner, ok := s.explorers[key]
	if !ok {
		return nil, ErrNotFound
	}
	if existing, ok := s.revisions[revision.ID]; ok && owner.ActiveRevisionID == revision.ID {
		copy := existing
		return &copy, nil
	}
	if receipt.ID == "" || revision.CompilationReceiptID != receipt.ID {
		return nil, ErrImmutableRevision
	}
	if err := validateCompilationReceipt(receipt); err != nil {
		return nil, err
	}
	if prior, ok := s.receipts[receipt.ID]; ok && !sameCompilationReceipt(prior, receipt) {
		return nil, ErrCorruptReceipt
	}
	now := time.Now().UTC()
	if owner.ActiveRevisionID != "" {
		prior := s.revisions[owner.ActiveRevisionID]
		prior.Status = RevisionSuperseded
		s.revisions[prior.ID] = prior
	}
	revision.Status = RevisionActive
	revision.ActivatedAt = &now
	revision.Publication.State = string(RevisionActive)
	revision.Publication.RevisionID = revision.ID
	revision.Publication.UpdatedAt = now
	if _, ok := s.receipts[receipt.ID]; !ok {
		s.receipts[receipt.ID] = cloneReceipt(receipt)
	}
	s.revisions[revision.ID] = revision
	owner.ActiveRevisionID = revision.ID
	owner.ActiveConfig = append([]byte(nil), revision.Config...)
	owner.RecipeDigest = revision.RecipeDigest
	owner.ResolvedSchemaDigest = revision.ResolvedSchemaDigest
	owner.SourceGeneration = revision.SourceGeneration
	owner.Materializations = append([]Materialization(nil), revision.Materializations...)
	owner.EmittedColumns = append([]EmittedColumn(nil), revision.EmittedColumns...)
	owner.Dataset = revision.Dataset
	owner.Publication = revision.Publication
	owner.Diagnostics = append([]Diagnostic(nil), revision.Diagnostics...)
	owner.UpdatedAt = now
	s.explorers[key] = owner
	copy := revision
	return &copy, nil
}
func cloneReceipt(in CompilationReceipt) CompilationReceipt {
	return cloneReflect(reflect.ValueOf(in)).Interface().(CompilationReceipt)
}

var timeType = reflect.TypeOf(time.Time{})

// cloneReflect keeps the memory adapter honest as the immutable receipt grows:
// nested recipe slices/maps and raw JSON must never alias caller-owned state.
func cloneReflect(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	if v.Type() == timeType {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		value := cloneReflect(v.Elem())
		out.Set(value)
		return out
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneReflect(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneReflect(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneReflect(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneReflect(iter.Key()), cloneReflect(iter.Value()))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if out.Field(i).CanSet() && v.Field(i).CanInterface() {
				out.Field(i).Set(cloneReflect(v.Field(i)))
			}
		}
		return out
	default:
		return v
	}
}

func receiptCompilationKey(receipt CompilationReceipt) string {
	value := reflect.ValueOf(receipt)
	field := value.FieldByName("CompilationKey")
	if field.IsValid() && field.Kind() == reflect.String && field.String() != "" {
		return field.String()
	}
	// IntentDigest was the pre-receipt compilation key and is retained as a
	// compatibility fallback while old documents are being migrated.
	return receipt.IntentDigest
}

func (s *MemoryStore) PurgeDraftAuthoring(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	referenced := map[string]bool{}
	for _, revision := range s.revisions {
		if revision.CompilationReceiptID != "" {
			referenced[revision.CompilationReceiptID] = true
		}
	}
	for id := range s.receipts {
		if !referenced[id] {
			delete(s.receipts, id)
		}
	}
	for key, value := range s.explorers {
		value.DraftConfig = nil
		value.DraftVersion = 0
		value.DraftDigest = ""
		s.explorers[key] = value
	}
	return nil
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
	if r.Status != RevisionReady && !(r.Status == RevisionActive && e.ActiveRevisionID == revisionID) {
		return ErrImmutableRevision
	}
	if r.Status == RevisionActive && e.ActiveRevisionID == revisionID {
		return nil
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
