package engine

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/control"
	"github.com/calypr/loom/internal/dataframe/runtime"
	"github.com/calypr/loom/internal/dataframe/semantic"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// Control adapts the production recipe engine to the backend-neutral
// recipe control contract consumed by GraphQL and other transport adapters.
// It resolves a recipe exactly once per operation and exposes only sanitized
// physical diagnostics through ExplainPhysical; raw IR and AQL stay private.
type Control struct {
	Engine            *Engine
	ExplainConnection *arangostore.ConnectionOptions
}

func (c Control) Validate(ctx context.Context, name string, bindings recipe.RuntimeBindings) (control.Validation, error) {
	if c.Engine == nil {
		return control.Validation{}, fmt.Errorf("recipe engine is required")
	}
	entry, err := c.Engine.registry.LoadRecipe(ctx, name)
	if err != nil {
		return control.Validation{}, err
	}
	bundle := entry.Bundle
	if c.Engine.resolveBundle != nil {
		bundle, err = c.Engine.resolveBundle(ctx, bundle, bindings)
		if err != nil {
			return control.Validation{}, fmt.Errorf("resolve recipe schema: %w", err)
		}
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return control.Validation{}, err
	}
	if digest, digestErr := entry.Bundle.Digest(); digestErr == nil {
		plan.RecipeDigest = digest
	}
	return control.Validation{Entry: entry, Plan: plan}, nil
}

func (c Control) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	validated, err := c.Validate(ctx, name, bindings)
	if err != nil {
		return semantic.RecipePlanExplanation{}, err
	}
	return validated.Plan.Explain(), nil
}

// ExplainPhysical compiles the same resolved canonical outputs consumed by
// Preview and Materialize. The compiler-only path is always available; live
// Arango assessment is opt-in and requires ExplainConnection. No AQL or bind
// values cross the recipe control boundary.
func (c Control) ExplainPhysical(ctx context.Context, name string, bindings recipe.RuntimeBindings, live bool) (control.PhysicalExplanation, error) {
	if c.Engine == nil {
		return control.PhysicalExplanation{}, fmt.Errorf("recipe engine is required")
	}
	resolved, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return control.PhysicalExplanation{}, err
	}
	limit := bindings.PreviewLimit
	if limit <= 0 {
		limit = 25
	}
	result := control.PhysicalExplanation{Outputs: make([]control.PhysicalOutputExplanation, 0, len(resolved.Compiled.Outputs))}
	for _, output := range resolved.Compiled.Outputs {
		compiled, err := compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, compiler.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			return control.PhysicalExplanation{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		item := control.PhysicalOutputExplanation{
			Name: output.Name, PlanFingerprint: runtime.CompiledQueryFingerprint(compiled),
			Columns: append([]string(nil), compiled.PublicColumns...), Diagnostics: compiled.PlanDiagnostics,
		}
		if live {
			if c.ExplainConnection == nil {
				return control.PhysicalExplanation{}, fmt.Errorf("live physical recipe Explain requires an Arango connection")
			}
			assessment, err := runtime.ExplainCompiledQueryAssessment(ctx, *c.ExplainConnection, compiled)
			if err != nil {
				return control.PhysicalExplanation{}, fmt.Errorf("output %q live Explain: %w", output.Name, err)
			}
			converted := control.AssessmentFromArango(assessment)
			item.Live = &converted
		}
		result.Outputs = append(result.Outputs, item)
	}
	return result, nil
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

func (c Control) Preview(ctx context.Context, name string, bindings recipe.RuntimeBindings, execute control.ExecuteFunc) (control.Preview, error) {
	if c.Engine == nil {
		return control.Preview{}, fmt.Errorf("recipe engine is required")
	}
	full, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return control.Preview{}, err
	}
	// The callback remains in the transport-neutral interface for source
	// compatibility, but the production engine never delegates preview work
	// to it. Doing so would reintroduce a caller-selected renderer and bypass
	// the canonical physical optimizer. All rows come from Engine.Preview.
	_ = execute
	resolved := full.Semantic
	rows, err := c.Engine.Preview(ctx, full, bindings.PreviewLimit)
	if err != nil {
		return control.Preview{}, err
	}
	return control.Preview{Plan: resolved, Rows: rows, Outputs: outputRows(full, rows)}, nil
}

// Run executes the complete resolved recipe without a preview limit. It is an
// explicit, bounded-by-the-caller's-memory transport operation; production
// bulk workflows should use Materialize instead.
func (c Control) Run(ctx context.Context, name string, bindings recipe.RuntimeBindings) (control.Preview, error) {
	if c.Engine == nil {
		return control.Preview{}, fmt.Errorf("recipe engine is required")
	}
	full, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return control.Preview{}, err
	}
	rows, err := c.Engine.Run(ctx, full)
	if err != nil {
		return control.Preview{}, err
	}
	return control.Preview{Plan: full.Semantic, Rows: rows, Outputs: outputRows(full, rows)}, nil
}

func outputRows(resolved Resolved, rows map[string][]map[string]any) []control.OutputRows {
	outputs := make([]control.OutputRows, 0, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		outputRows := rows[output.Name]
		outputs = append(outputs, control.OutputRows{
			Name: output.Name, Columns: compiler.PublicOutputColumns(output.OutputSchema),
			Rows: outputRows,
		})
	}
	return outputs
}

var _ interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (control.Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (control.PhysicalExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings, control.ExecuteFunc) (control.Preview, error)
} = Control{}
