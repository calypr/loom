// Package exec owns generic recipe registration and execution. A caller
// supplies the root-resource and relationship resolvers; the package never
// names a storage backend or a translation-specific output.
package exec

import (
	"context"
	"sync"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/reference"
)

type Registry struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

type Entry struct {
	Bundle recipe.Bundle
	Digest string
}

func NewRegistry() *Registry { return &Registry{entries: map[string]Entry{}} }

// Register stores an immutable bundle. Re-registering the same name is only
// allowed when the canonical digest is identical, making retries idempotent.

type Roots func(context.Context, recipe.Output, recipe.RuntimeBindings) ([]map[string]any, error)

type Runner struct {
	Registry *Registry
	Roots    Roots
	Related  reference.Related
}

type Result struct {
	RecipeName string
	Digest     string
	Outputs    map[string][]map[string]any
}

// Run evaluates every output before returning. An error discards the complete
// result, so consumers never publish a partially translated bundle.
