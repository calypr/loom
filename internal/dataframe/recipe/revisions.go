package recipe

import (
	"context"
	"errors"
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
