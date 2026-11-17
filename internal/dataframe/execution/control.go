package execution

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	recipeexec "github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Control struct {
	Engine                      *Engine
	ExplainConnection           func(context.Context, compiler.CompiledQuery) (ExplainAssessment, error)
	ExplainPostgreSQLConnection func(context.Context, compiler.CompiledQuery) (PostgreSQLPlanAssessment, error)
}

func (c Control) Validate(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Validation, error) {
	if c.Engine == nil {
		return Validation{}, fmt.Errorf("recipe engine is required")
	}
	entry, err := c.Engine.entry(ctx, name, bindings)
	if err != nil {
		return Validation{}, recipeControlBackend(err)
	}
	bundle := entry.Bundle
	if c.Engine.resolveBundle != nil {
		bundle, err = c.Engine.resolveBundle(ctx, bundle, bindings)
		if err != nil {
			return Validation{}, fmt.Errorf("resolve recipe schema: %w", err)
		}
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return Validation{}, err
	}
	if digest, digestErr := entry.Bundle.Digest(); digestErr == nil {
		plan.RecipeDigest = digest
	}
	return Validation{Entry: entry, Plan: plan}, nil
}

func (c Control) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	validated, err := c.Validate(ctx, name, bindings)
	if err != nil {
		return semantic.RecipePlanExplanation{}, err
	}
	return validated.Plan.Explain(), nil
}

func (c Control) ExplainPhysical(ctx context.Context, name string, bindings recipe.RuntimeBindings, live bool) (PhysicalExplanation, error) {
	if c.Engine == nil {
		return PhysicalExplanation{}, fmt.Errorf("recipe engine is required")
	}
	resolved, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return PhysicalExplanation{}, err
	}
	return c.ExplainResolved(ctx, resolved, live)
}

func (c Control) ExplainResolved(ctx context.Context, resolved Resolved, live bool) (PhysicalExplanation, error) {
	if c.Engine == nil {
		return PhysicalExplanation{}, fmt.Errorf("recipe engine is required")
	}
	limit := resolved.Semantic.SemanticPlan.Bindings.PreviewLimit
	if limit <= 0 {
		limit = 25
	}
	result := PhysicalExplanation{Outputs: make([]PhysicalOutputExplanation, 0, len(resolved.Compiled.Outputs))}
	for _, output := range resolved.Compiled.Outputs {
		compiled, err := c.explainCompiledForOutput(ctx, resolved, output, limit)
		if err != nil {
			return PhysicalExplanation{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		fingerprint := SourcePlanFingerprint(compiled)
		if err := requireProjectedPlanFingerprint(output.Name, fingerprint); err != nil {
			return PhysicalExplanation{}, err
		}
		item := PhysicalOutputExplanation{
			Name: output.Name, PlanFingerprint: fingerprint,
			Columns: append([]string(nil), compiled.PublicColumns...), Diagnostics: compiled.PlanDiagnostics,
		}
		if live {
			switch compiled.Command.Backend() {
			case command.CompiledBackendPostgreSQL:
				if c.ExplainPostgreSQLConnection == nil {
					return PhysicalExplanation{}, fmt.Errorf("live physical recipe Explain requires a PostgreSQL connection")
				}
				assessment, err := c.ExplainPostgreSQLConnection(ctx, compiled)
				if err != nil {
					return PhysicalExplanation{}, recipeControlBackend(fmt.Errorf("output %q live Explain: %w", output.Name, err))
				}
				item.LivePostgreSQL = &assessment
			default:
				if c.ExplainConnection == nil {
					return PhysicalExplanation{}, fmt.Errorf("live physical recipe Explain requires an Arango connection")
				}
				assessment, err := c.ExplainConnection(ctx, compiled)
				if err != nil {
					return PhysicalExplanation{}, recipeControlBackend(fmt.Errorf("output %q live Explain: %w", output.Name, err))
				}
				item.Live = &assessment
			}
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

func (c Control) Preview(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Preview, error) {
	if c.Engine == nil {
		return Preview{}, fmt.Errorf("recipe engine is required")
	}
	full, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return Preview{}, err
	}
	resolved := full.Semantic
	rows, err := c.Engine.Preview(ctx, full, bindings.PreviewLimit)
	if err != nil {
		return Preview{}, recipeControlBackend(err)
	}
	return Preview{Plan: resolved, Outputs: outputRows(full, rows)}, nil
}

func (c Control) Run(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Preview, error) {
	if c.Engine == nil {
		return Preview{}, fmt.Errorf("recipe engine is required")
	}
	full, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return Preview{}, err
	}
	rows, err := c.Engine.Run(ctx, full)
	if err != nil {
		return Preview{}, recipeControlBackend(err)
	}
	return Preview{Plan: full.Semantic, Outputs: outputRows(full, rows)}, nil
}

func outputRows(resolved Resolved, rows map[string][]map[string]any) []OutputRows {
	outputs := make([]OutputRows, 0, len(resolved.Compiled.Outputs))
	for _, output := range resolved.Compiled.Outputs {
		outputRows := rows[output.Name]
		outputs = append(outputs, OutputRows{
			Name: output.Name, Columns: compiler.PublicOutputColumns(output.OutputSchema),
			Rows: outputRows,
		})
	}
	return outputs
}

var _ interface {
	Validate(context.Context, string, recipe.RuntimeBindings) (Validation, error)
	Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error)
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (PhysicalExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings) (Preview, error)
} = Control{}

func recipeControlBackend(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, recipeexec.ErrRecipeNotFound) || errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeClientCanceled, "")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
