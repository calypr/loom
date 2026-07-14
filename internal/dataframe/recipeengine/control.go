package recipeengine

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipecontrol"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// Control adapts the production recipe engine to the backend-neutral
// recipecontrol contract consumed by GraphQL and other transport adapters.
// It resolves a recipe exactly once per operation and never exposes its
// physical plan.
type Control struct {
	Engine *Engine
}

func (c Control) Validate(ctx context.Context, name string, bindings recipe.RuntimeBindings) (recipecontrol.Validation, error) {
	if c.Engine == nil {
		return recipecontrol.Validation{}, fmt.Errorf("recipe engine is required")
	}
	entry, err := c.Engine.registry.LoadRecipe(ctx, name)
	if err != nil {
		return recipecontrol.Validation{}, err
	}
	plan, err := semantic.BuildRecipePlan(entry.Bundle, bindings)
	if err != nil {
		return recipecontrol.Validation{}, err
	}
	return recipecontrol.Validation{Entry: entry, Plan: plan}, nil
}

func (c Control) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	validated, err := c.Validate(ctx, name, bindings)
	if err != nil {
		return semantic.RecipePlanExplanation{}, err
	}
	return validated.Plan.Explain(), nil
}

func (c Control) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	if c.Engine == nil {
		return semantic.ResolvedRecipePlan{}, fmt.Errorf("recipe engine is required")
	}
	resolved, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return semantic.ResolvedRecipePlan{}, err
	}
	return resolved.Semantic, nil
}

func (c Control) Preview(ctx context.Context, name string, bindings recipe.RuntimeBindings, execute recipecontrol.ExecuteFunc) (recipecontrol.Preview, error) {
	if c.Engine == nil {
		return recipecontrol.Preview{}, fmt.Errorf("recipe engine is required")
	}
	full, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return recipecontrol.Preview{}, err
	}
	resolved := full.Semantic
	var rows map[string][]map[string]any
	if execute != nil {
		rows, err = execute(ctx, resolved, bindings.PreviewLimit)
	} else {
		rows, err = c.Engine.Preview(ctx, full, bindings.PreviewLimit)
	}
	if err != nil {
		return recipecontrol.Preview{}, err
	}
	return recipecontrol.Preview{Plan: resolved, Rows: rows}, nil
}

var _ interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (recipecontrol.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings, recipecontrol.ExecuteFunc) (recipecontrol.Preview, error)
} = Control{}
