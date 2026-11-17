package recipe

import (
	"context"
	"errors"
	"time"
)

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
