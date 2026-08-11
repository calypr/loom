package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// Store is the persistence boundary for recipe registration. Implementations
// may use Arango, a file-backed registry, or another durable store; the
// registry itself owns validation, canonicalization, and idempotency rules.
type Store interface {
	SaveRecipe(context.Context, Entry) error
	LoadRecipe(context.Context, string) (Entry, error)
}

// VersionedStore is the durable contract used by new recipe workflows. The
// name-only Store methods remain above for the compatibility window only.
type VersionedStore interface {
	Store
	LoadRecipeVersion(context.Context, string, string) (Entry, error)
	ReplaceRecipeVersion(context.Context, Entry) error
	RecipeVersionUsed(context.Context, string, string) (bool, error)
}

// ScopedStore is the project-aware extension used by the authoring workflow.
// The legacy Store methods intentionally remain global so existing registered
// and default recipes keep their historical lookup behavior.
type ScopedStore interface {
	VersionedStore
	SaveRecipeForProject(context.Context, string, Entry) error
	LoadRecipeForProject(context.Context, string, string) (Entry, error)
	LoadRecipeVersionForProject(context.Context, string, string, string) (Entry, error)
	ReplaceRecipeVersionForProject(context.Context, string, Entry) error
	RecipeVersionUsedForProject(context.Context, string, string, string) (bool, error)
}

var ErrRecipeNotFound = errors.New("recipe not found")
var ErrRecipeVersionImmutable = errors.New("recipe version is immutable after its first execution")

type Entry struct {
	Bundle       recipe.Bundle `json:"bundle"`
	Digest       string        `json:"digest"`
	ScopeProject string        `json:"scopeProject,omitempty"`
}

// PersistentRegistry owns immutable/idempotent recipe registration. It never
// evaluates recipe data.
type PersistentRegistry struct {
	Store Store
}

// RegisterVersion retains recipe history by (name, translationVersion). An
// exact version is replaceable only until its first durable execution exists.
func (r PersistentRegistry) RegisterVersion(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	store, ok := r.Store.(VersionedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support versioned recipes")
	}
	existing, err := store.LoadRecipeVersion(ctx, bundle.Name, bundle.TranslationVersion)
	if err == nil {
		if existing.Digest == entry.Digest {
			return existing, nil
		}
		used, err := store.RecipeVersionUsed(ctx, bundle.Name, bundle.TranslationVersion)
		if err != nil {
			return Entry{}, fmt.Errorf("inspect recipe version usage: %w", err)
		}
		if used {
			return Entry{}, fmt.Errorf("%w: %s@%s", ErrRecipeVersionImmutable, bundle.Name, bundle.TranslationVersion)
		}
		if err := store.ReplaceRecipeVersion(ctx, entry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	if !errors.Is(err, ErrRecipeNotFound) {
		return Entry{}, fmt.Errorf("load recipe %q: %w", bundle.Name, err)
	}
	if err := store.SaveRecipe(ctx, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (r PersistentRegistry) LoadVersion(ctx context.Context, name, translationVersion string) (Entry, error) {
	store, ok := r.Store.(VersionedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support versioned recipes")
	}
	return store.LoadRecipeVersion(ctx, name, translationVersion)
}

// RegisterVersionForProject stores an exact immutable recipe version in a
// project scope. It mirrors RegisterVersion's idempotency and pre-execution
// replacement semantics without changing the global registry.
func (r PersistentRegistry) RegisterVersionForProject(ctx context.Context, project string, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	entry.ScopeProject = project
	store, ok := r.Store.(ScopedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support scoped recipes")
	}
	existing, err := store.LoadRecipeVersionForProject(ctx, project, bundle.Name, bundle.TranslationVersion)
	if err == nil {
		if existing.Digest == entry.Digest {
			return existing, nil
		}
		used, usedErr := store.RecipeVersionUsedForProject(ctx, project, bundle.Name, bundle.TranslationVersion)
		if usedErr != nil {
			return Entry{}, fmt.Errorf("inspect scoped recipe version usage: %w", usedErr)
		}
		if used {
			return Entry{}, fmt.Errorf("%w: %s/%s@%s", ErrRecipeVersionImmutable, project, bundle.Name, bundle.TranslationVersion)
		}
		if err := store.ReplaceRecipeVersionForProject(ctx, project, entry); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	if !errors.Is(err, ErrRecipeNotFound) {
		return Entry{}, fmt.Errorf("load scoped recipe %q: %w", bundle.Name, err)
	}
	if err := store.SaveRecipeForProject(ctx, project, entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (r PersistentRegistry) LoadVersionForProject(ctx context.Context, project, name, translationVersion string) (Entry, error) {
	store, ok := r.Store.(ScopedStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store does not support scoped recipes")
	}
	return store.LoadRecipeVersionForProject(ctx, project, name, translationVersion)
}

func canonicalEntry(bundle recipe.Bundle) (Entry, error) {
	if bundle.Fragments != nil {
		expanded, err := bundle.ExpandFragments()
		if err != nil {
			return Entry{}, err
		}
		bundle = expanded
	}
	if err := bundle.Validate(); err != nil {
		return Entry{}, err
	}
	canonical, err := bundle.CanonicalJSON()
	if err != nil {
		return Entry{}, err
	}
	var immutable recipe.Bundle
	if err := json.Unmarshal(canonical, &immutable); err != nil {
		return Entry{}, fmt.Errorf("clone recipe: %w", err)
	}
	digest, err := immutable.Digest()
	if err != nil {
		return Entry{}, err
	}
	return Entry{Bundle: immutable, Digest: digest}, nil
}
