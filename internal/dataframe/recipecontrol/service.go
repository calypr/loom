// Package recipecontrol exposes the backend-neutral control-plane operations
// needed by GraphQL or HTTP adapters. It deliberately contains no transport
// types and no translation-specific output names.
package recipecontrol

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeexec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Registry interface {
	Get(string) (recipeexec.Entry, bool)
}

// ContextRegistry is the durable registry shape. Durable adapters keep
// storage errors distinct from a missing recipe; the control-plane adapter
// exposes the small transport-neutral Registry interface used by this
// package's existing in-memory tests.
type ContextRegistry interface {
	LoadRecipe(context.Context, string) (recipeexec.Entry, error)
}

type DurableRegistry struct{ Store ContextRegistry }

func (r DurableRegistry) Get(name string) (recipeexec.Entry, bool) {
	if r.Store == nil {
		return recipeexec.Entry{}, false
	}
	entry, err := r.Store.LoadRecipe(context.Background(), name)
	return entry, err == nil
}

type Service struct {
	Registry    Registry
	Discover    semantic.DiscoverFunc
	ScopeDigest func(recipe.RuntimeBindings) string
}

type Validation struct {
	Entry recipeexec.Entry
	Plan  semantic.RecipePlan
}

type Preview struct {
	Plan semantic.ResolvedRecipePlan
	Rows map[string][]map[string]any
}

type ExecuteFunc func(context.Context, semantic.ResolvedRecipePlan, int) (map[string][]map[string]any, error)

func (s Service) Validate(_ context.Context, name string, bindings recipe.RuntimeBindings) (Validation, error) {
	if s.Registry == nil {
		return Validation{}, fmt.Errorf("recipe registry is required")
	}
	entry, ok := s.Registry.Get(name)
	if !ok {
		return Validation{}, fmt.Errorf("recipe %q is not registered", name)
	}
	plan, err := semantic.BuildRecipePlan(entry.Bundle, bindings)
	if err != nil {
		return Validation{}, err
	}
	return Validation{Entry: entry, Plan: plan}, nil
}

func (s Service) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	validated, err := s.Validate(ctx, name, bindings)
	if err != nil {
		return semantic.RecipePlanExplanation{}, err
	}
	return validated.Plan.Explain(), nil
}

func (s Service) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	validated, err := s.Validate(ctx, name, bindings)
	if err != nil {
		return semantic.ResolvedRecipePlan{}, err
	}
	scopeDigest := ""
	if s.ScopeDigest != nil {
		scopeDigest = s.ScopeDigest(bindings)
	}
	return semantic.ResolveRecipePlan(ctx, validated.Plan, scopeDigest, bindings.DatasetGeneration, s.Discover)
}

func (s Service) Preview(ctx context.Context, name string, bindings recipe.RuntimeBindings, execute ExecuteFunc) (Preview, error) {
	if execute == nil {
		return Preview{}, fmt.Errorf("preview executor is required")
	}
	plan, err := s.Resolve(ctx, name, bindings)
	if err != nil {
		return Preview{}, err
	}
	rows, err := execute(ctx, plan, bindings.PreviewLimit)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Plan: plan, Rows: rows}, nil
}
