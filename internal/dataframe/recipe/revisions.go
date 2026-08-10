package recipe

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RecipeRevision is an immutable, project-scoped recipe document. The digest
// is the canonical Bundle digest and is therefore safe to use as an optimistic
// concurrency and publication address.
type RecipeRevision struct {
	Project   string    `json:"project"`
	Name      string    `json:"name"`
	Digest    string    `json:"digest"`
	Parent    string    `json:"parentDigest,omitempty"`
	Bundle    Bundle    `json:"bundle"`
	CreatedAt time.Time `json:"createdAt"`
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
	revision := RecipeRevision{Project: project, Name: bundle.Name, Digest: digest, Parent: parent, Bundle: bundle, CreatedAt: now}
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
	return result, nil
}
