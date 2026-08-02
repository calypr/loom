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

type defaultStore interface {
	Store
	ReplaceRecipe(context.Context, Entry) error
}

var ErrRecipeNotFound = errors.New("recipe not found")

type Entry struct {
	Bundle recipe.Bundle
	Digest string
}

// PersistentRegistry owns immutable/idempotent recipe registration. It never
// evaluates recipe data.
type PersistentRegistry struct {
	Store Store
}

// RegisterDefault updates the server-owned default recipe when its embedded
// definition changes while keeping the stored digest canonical.
func (r PersistentRegistry) RegisterDefault(ctx context.Context, bundle recipe.Bundle) (Entry, error) {
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	store, ok := r.Store.(defaultStore)
	if !ok {
		return Entry{}, fmt.Errorf("persistent recipe store cannot update the default recipe")
	}
	existing, err := store.LoadRecipe(ctx, bundle.Name)
	if err == nil {
		if existing.Digest == entry.Digest {
			return existing, nil
		}
		if err := store.ReplaceRecipe(ctx, entry); err != nil {
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
