package recipe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RecipeRevision is an immutable, project-scoped recipe document. The digest
// is the canonical Bundle digest and is therefore safe to use as an optimistic
// concurrency and publication address.
type RecipeRevision struct {
	ID                 string                 `json:"id,omitempty"`
	Project            string                 `json:"project"`
	Name               string                 `json:"name"`
	Digest             string                 `json:"digest"`
	Parent             string                 `json:"parentDigest,omitempty"`
	Bundle             Bundle                 `json:"bundle"`
	RevisionNumber     int64                  `json:"revisionNumber,omitempty"`
	Status             RecipeRevisionStatus   `json:"status,omitempty"`
	RecipeName         string                 `json:"recipeName,omitempty"`
	TranslationVersion string                 `json:"translationVersion,omitempty"`
	ExecutionID        string                 `json:"executionId,omitempty"`
	Outputs            []RecipeRevisionOutput `json:"outputs,omitempty"`
	Diagnostics        []BuilderDiagnostic    `json:"diagnostics,omitempty"`
	SourceGeneration   string                 `json:"sourceGeneration,omitempty"`
	CreatedAt          time.Time              `json:"createdAt"`
}

type RecipeRevisionStatus string

const (
	RecipeRevisionValidating    RecipeRevisionStatus = "VALIDATING"
	RecipeRevisionMaterializing RecipeRevisionStatus = "MATERIALIZING"
	RecipeRevisionReady         RecipeRevisionStatus = "READY"
	RecipeRevisionFailed        RecipeRevisionStatus = "FAILED"
)

type RecipeRevisionOutput struct {
	Output            string `json:"output"`
	MaterializationID string `json:"materializationId,omitempty"`
}

type BuilderDiagnostic struct {
	Severity   string         `json:"severity"`
	Code       string         `json:"code"`
	ConfigPath string         `json:"configPath,omitempty"`
	Message    string         `json:"message"`
	Retryable  bool           `json:"retryable,omitempty"`
	RequestID  string         `json:"requestId,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

type RecipeDraft struct {
	Project         string    `json:"project"`
	DraftVersion    int64     `json:"draftVersion"`
	Document        Bundle    `json:"document"`
	AuthoringDigest string    `json:"authoringDigest"`
	BaseRevisionID  string    `json:"baseRevisionId,omitempty"`
	UpdatedBy       string    `json:"updatedBy,omitempty"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

var ErrDraftConflict = errors.New("recipe draft compare-and-swap conflict")

type DraftConflictError struct {
	Current RecipeDraft
}

func (e *DraftConflictError) Error() string { return ErrDraftConflict.Error() }
func (e *DraftConflictError) Unwrap() error { return ErrDraftConflict }

type DraftStore interface {
	GetDraft(context.Context, string) (RecipeDraft, error)
	SaveDraft(context.Context, RecipeDraft, int64) (RecipeDraft, error)
	DeleteProject(context.Context, string) error
}

var ErrDraftNotFound = errors.New("recipe draft not found")

type MemoryDraftStore struct {
	mu     sync.RWMutex
	drafts map[string]RecipeDraft
}

func NewMemoryDraftStore() *MemoryDraftStore {
	return &MemoryDraftStore{drafts: make(map[string]RecipeDraft)}
}

func (s *MemoryDraftStore) GetDraft(_ context.Context, project string) (RecipeDraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	draft, ok := s.drafts[project]
	if !ok {
		return RecipeDraft{}, ErrDraftNotFound
	}
	return cloneDraft(draft), nil
}

func (s *MemoryDraftStore) SaveDraft(_ context.Context, draft RecipeDraft, expectedVersion int64) (RecipeDraft, error) {
	if s == nil {
		return RecipeDraft{}, fmt.Errorf("recipe draft store is required")
	}
	if strings.TrimSpace(draft.Project) == "" {
		return RecipeDraft{}, fmt.Errorf("project is required")
	}
	if draft.Document.Name == "" {
		draft.Document.Name = ProjectRecipeName(draft.Project)
	}
	if draft.Document.TranslationVersion == "" {
		draft.Document.TranslationVersion = "draft"
	}
	if err := draft.Document.Validate(); err != nil {
		return RecipeDraft{}, err
	}
	digest, err := draft.Document.Digest()
	if err != nil {
		return RecipeDraft{}, err
	}
	draft.Document = canonicalBundle(draft.Document)
	draft.AuthoringDigest = "sha256:" + digest
	draft.UpdatedAt = time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.drafts[draft.Project]
	if current.DraftVersion != expectedVersion {
		return RecipeDraft{}, &DraftConflictError{Current: cloneDraft(current)}
	}
	draft.DraftVersion = expectedVersion + 1
	s.drafts[draft.Project] = cloneDraft(draft)
	return cloneDraft(draft), nil
}

func (s *MemoryDraftStore) DeleteProject(_ context.Context, project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drafts, project)
	return nil
}

func cloneDraft(draft RecipeDraft) RecipeDraft {
	draft.Document = canonicalBundle(draft.Document)
	return draft
}

func canonicalBundle(bundle Bundle) Bundle {
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return bundle
	}
	var cloned Bundle
	if jsonErr := unmarshalJSON(canonical, &cloned); jsonErr == nil {
		return cloned
	}
	return bundle
}

// ProjectRecipeName is the managed identity for project-scoped recipes.
func ProjectRecipeName(project string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(project)))
	return "project_" + hex.EncodeToString(sum[:])[:16]
}

func ProjectRecipeTranslationVersion(revisionNumber int64, id string) string {
	if revisionNumber < 0 {
		revisionNumber = 0
	}
	clean := strings.ReplaceAll(strings.ReplaceAll(id, "-", ""), " ", "")
	if len(clean) > 8 {
		clean = clean[:8]
	}
	return fmt.Sprintf("r%06d_%s", revisionNumber, clean)
}

// NormalizeProjectBundle returns the global/default recipe as a non-persisted
// project draft with managed identity fields applied.
func NormalizeProjectBundle(project string, defaultBundle Bundle) (RecipeDraft, error) {
	if strings.TrimSpace(project) == "" {
		return RecipeDraft{}, fmt.Errorf("project is required")
	}
	defaultBundle.Name = ProjectRecipeName(project)
	defaultBundle.TranslationVersion = "draft"
	if err := defaultBundle.Validate(); err != nil {
		return RecipeDraft{}, err
	}
	digest, err := defaultBundle.Digest()
	if err != nil {
		return RecipeDraft{}, err
	}
	return RecipeDraft{Project: project, Document: canonicalBundle(defaultBundle), AuthoringDigest: "sha256:" + digest, UpdatedAt: time.Now().UTC()}, nil
}

var ErrRecipeRevisionNotFound = errors.New("recipe revision not found")

type RevisionStore interface {
	Register(context.Context, string, Bundle, string) (RecipeRevision, error)
	Get(context.Context, string, string, string) (RecipeRevision, error)
	List(context.Context, string, string) ([]RecipeRevision, error)
}

// MemoryRevisionStore is the default process-local implementation. Production
// deployments can replace it with a durable project registry without changing
// compiler or GraphQL contracts.
type MemoryRevisionStore struct {
	mu      sync.RWMutex
	values  map[string]RecipeRevision
	current map[string]string
}

func NewMemoryRevisionStore() *MemoryRevisionStore {
	return &MemoryRevisionStore{values: make(map[string]RecipeRevision), current: make(map[string]string)}
}

func revisionKey(project, name, digest string) string {
	return project + "\x00" + name + "\x00" + digest
}
func currentKey(project, name string) string { return project + "\x00" + name }

func (s *MemoryRevisionStore) Register(_ context.Context, project string, bundle Bundle, parent string) (RecipeRevision, error) {
	if s == nil {
		return RecipeRevision{}, fmt.Errorf("recipe revision store is required")
	}
	if project == "" || bundle.Name == "" {
		return RecipeRevision{}, fmt.Errorf("project and recipe name are required")
	}
	if err := bundle.Validate(); err != nil {
		return RecipeRevision{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return RecipeRevision{}, err
	}
	now := time.Now().UTC()
	revisionID, revisionIDErr := uuid.NewV7()
	if revisionIDErr != nil {
		return RecipeRevision{}, revisionIDErr
	}
	revision := RecipeRevision{ID: revisionID.String(), Project: project, Name: bundle.Name, Digest: digest, Parent: parent, Bundle: bundle, CreatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := revisionKey(project, bundle.Name, digest)
	if existing, ok := s.values[key]; ok {
		return existing, nil
	}
	if parent != "" {
		if current := s.current[currentKey(project, bundle.Name)]; current != "" && current != parent {
			return RecipeRevision{}, fmt.Errorf("recipe revision parent %q is not current (current %q)", parent, current)
		}
	}
	s.values[key] = revision
	s.current[currentKey(project, bundle.Name)] = digest
	return revision, nil
}

// RegisterProjectRevision creates an immutable UUIDv7-backed authoring
// revision. It is separate from the legacy digest revision API above so old
// registered/default callers retain their behavior.
func RegisterProjectRevision(ctx context.Context, store RevisionStore, project string, bundle Bundle, parent string, revisionNumber int64) (RecipeRevision, error) {
	if store == nil {
		return RecipeRevision{}, fmt.Errorf("recipe revision store is required")
	}
	if strings.TrimSpace(project) == "" {
		return RecipeRevision{}, fmt.Errorf("project is required")
	}
	if bundle.Name == "" {
		bundle.Name = ProjectRecipeName(project)
	}
	if bundle.TranslationVersion == "" {
		bundle.TranslationVersion = "draft"
	}
	value, err := store.Register(ctx, project, bundle, parent)
	if err != nil {
		return RecipeRevision{}, err
	}
	if parsed, parseErr := uuid.NewV7(); parseErr == nil {
		value.ID = parsed.String()
	}
	value.RevisionNumber = revisionNumber
	value.Status = RecipeRevisionValidating
	value.RecipeName = ProjectRecipeName(project)
	value.TranslationVersion = ProjectRecipeTranslationVersion(revisionNumber, value.ID)
	return value, nil
}

func (s *MemoryRevisionStore) Get(_ context.Context, project, name, digest string) (RecipeRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[revisionKey(project, name, digest)]
	if !ok {
		return RecipeRevision{}, ErrRecipeRevisionNotFound
	}
	return value, nil
}

func (s *MemoryRevisionStore) List(_ context.Context, project, name string) ([]RecipeRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]RecipeRevision, 0)
	for _, value := range s.values {
		if value.Project == project && value.Name == name {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

// unmarshalJSON is a tiny indirection kept here to avoid exposing the
// strict-json decoder implementation to persistence users.
func unmarshalJSON(data []byte, value any) error {
	return json.Unmarshal(data, value)
}
