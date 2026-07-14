// Package recipeexec owns generic recipe registration and execution. A caller
// supplies the root-resource and relationship resolvers; the package never
// names a storage backend or a translation-specific output.
package recipeexec

import (
	"context"
	"fmt"
	"sync"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeeval"
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
func (r *Registry) Register(bundle recipe.Bundle) (Entry, error) {
	if r == nil {
		return Entry{}, fmt.Errorf("recipe registry is nil")
	}
	entry, err := canonicalEntry(bundle)
	if err != nil {
		return Entry{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.entries[bundle.Name]; ok {
		if existing.Digest != entry.Digest {
			return Entry{}, fmt.Errorf("recipe %q is already registered with a different digest", bundle.Name)
		}
		return existing, nil
	}
	r.entries[bundle.Name] = entry
	return entry, nil
}

func (r *Registry) Get(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.entries[name]
	return entry, ok
}

type Roots func(context.Context, recipe.Output, recipe.RuntimeBindings) ([]map[string]any, error)

type Runner struct {
	Registry *Registry
	Roots    Roots
	Related  recipeeval.Related
}

type Result struct {
	RecipeName string
	Digest     string
	Outputs    map[string][]map[string]any
}

// Run evaluates every output before returning. An error discards the complete
// result, so consumers never publish a partially translated bundle.
func (r Runner) Run(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Result, error) {
	if r.Registry == nil || r.Roots == nil {
		return Result{}, fmt.Errorf("recipe registry and root resolver are required")
	}
	entry, ok := r.Registry.Get(name)
	if !ok {
		return Result{}, fmt.Errorf("recipe %q is not registered", name)
	}
	if bindings.Project == "" {
		return Result{}, fmt.Errorf("recipe project is required")
	}
	result := Result{RecipeName: entry.Bundle.Name, Digest: entry.Digest, Outputs: make(map[string][]map[string]any, len(entry.Bundle.Outputs))}
	for _, output := range entry.Bundle.Outputs {
		roots, err := r.Roots(ctx, output, bindings)
		if err != nil {
			return Result{}, fmt.Errorf("output %q roots: %w", output.Name, err)
		}
		rows := make([]map[string]any, 0)
		for _, root := range roots {
			translated, err := recipeeval.EvaluateOutput(output, root, r.Related)
			if err != nil {
				return Result{}, fmt.Errorf("output %q: %w", output.Name, err)
			}
			rows = append(rows, translated...)
		}
		result.Outputs[output.Name] = rows
	}
	return result, nil
}
