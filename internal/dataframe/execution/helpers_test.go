package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

type Control struct {
	Engine            *Engine
	ExplainConnection func(context.Context, CompiledQuery) (ExplainAssessment, error)
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		return Validation{}, err
	}
	if digest, digestErr := entry.Bundle.Digest(); digestErr == nil {
		plan.RecipeDigest = digest
	}
	return Validation{Entry: entry, Plan: plan}, nil
}

func (c Control) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (recipeplan.RecipePlanExplanation, error) {
	validated, err := c.Validate(ctx, name, bindings)
	if err != nil {
		return recipeplan.RecipePlanExplanation{}, err
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
	limit := bindings.PreviewLimit
	if limit <= 0 {
		limit = 25
	}
	result := PhysicalExplanation{Outputs: make([]PhysicalOutputExplanation, 0, len(resolved.Compiled.Outputs))}
	for _, output := range resolved.Compiled.Outputs {
		compiled, err := compiler.CompileRecipeOutputWithPolicy(output, resolved.Semantic.SemanticPlan.Bindings, limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			return PhysicalExplanation{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		item := PhysicalOutputExplanation{
			Name: output.Name, PlanFingerprint: CompiledQueryFingerprint(compiled),
			Columns: append([]string(nil), compiled.PublicColumns...), Diagnostics: compiled.PlanDiagnostics,
		}
		if live {
			if c.ExplainConnection == nil {
				return PhysicalExplanation{}, fmt.Errorf("live physical recipe Explain requires an Arango connection")
			}
			assessment, err := c.ExplainConnection(ctx, compiled)
			if err != nil {
				return PhysicalExplanation{}, recipeControlBackend(fmt.Errorf("output %q live Explain: %w", output.Name, err))
			}
			item.Live = &assessment
		}
		result.Outputs = append(result.Outputs, item)
	}
	return result, nil
}

func (c Control) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (recipeplan.ResolvedRecipePlan, error) {
	if c.Engine == nil {
		return recipeplan.ResolvedRecipePlan{}, fmt.Errorf("recipe engine is required")
	}
	resolved, err := c.Engine.Resolve(ctx, name, bindings)
	if err != nil {
		return recipeplan.ResolvedRecipePlan{}, err
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
	Explain(context.Context, string, recipe.RuntimeBindings) (recipeplan.RecipePlanExplanation, error)
	ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (PhysicalExplanation, error)
	Resolve(context.Context, string, recipe.RuntimeBindings) (recipeplan.ResolvedRecipePlan, error)
	Preview(context.Context, string, recipe.RuntimeBindings) (Preview, error)
} = Control{}

func recipeControlBackend(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, recipe.ErrRecipeNotFound) || errors.Is(err, recipe.ErrRecipeRevisionNotFound) {
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

type oracleReshape struct {
	Kind         string
	GroupedPivot *oraclePivot
	Unpivot      *oracleUnpivot
}

const (
	oracleReshapePivot   = "GROUPED_PIVOT"
	oracleReshapeUnpivot = "UNPIVOT"
)

type oraclePivot struct {
	ConstructionID              string
	GroupKeys                   []string
	CategoryColumn, ValueColumn string
	Categories                  []oraclePivotCategory
	DuplicatePolicy             recipe.PivotDuplicatePolicy
	MissingCellPolicy           recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy      recipe.PivotUnlistedCategoryPolicy
}

type oraclePivotCategory struct {
	Key           recipe.TableScalar
	Output, Label string
}

type oracleUnpivot struct {
	ConstructionID                               string
	Inputs                                       []oracleUnpivotInput
	KeyOutput, KeyLabel, ValueOutput, ValueLabel string
	NullRowPolicy                                recipe.UnpivotNullRowPolicy
}

type oracleUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

func bindOracleCurrentConstruction(output recipe.Output, shape *oracleReshape) recipe.Output {
	if shape == nil {
		return output
	}
	construction := &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{}}
	byName := map[string]recipe.StageColumn{}
	for i := range output.Fields {
		field := &output.Fields[i]
		field.ColumnID = field.Name
		column := recipe.StageColumn{ID: field.Name, Name: field.Name, Label: field.Label}
		construction.SourceColumns = append(construction.SourceColumns, column)
		byName[column.Name] = column
	}
	step := recipe.ConstructionStep{Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}}
	switch shape.Kind {
	case oracleReshapePivot:
		p := shape.GroupedPivot
		step.ID = "pivot"
		pivot := &recipe.ConstructionPivot{ConstructionID: p.ConstructionID, GroupKeyIDs: append([]string{}, p.GroupKeys...), CategoryColumnID: p.CategoryColumn, ValueColumnID: p.ValueColumn, DuplicatePolicy: p.DuplicatePolicy, MissingCellPolicy: p.MissingCellPolicy, UnlistedCategoryPolicy: p.UnlistedCategoryPolicy}
		for _, name := range p.GroupKeys {
			column := byName[name]
			if column.ID == "" {
				column = recipe.StageColumn{ID: name, Name: name}
			}
			step.Outputs = append(step.Outputs, column)
		}
		for _, category := range p.Categories {
			pivot.Categories = append(pivot.Categories, recipe.ConstructionPivotCategory{Key: category.Key, OutputColumnID: category.Output})
			step.Outputs = append(step.Outputs, recipe.StageColumn{ID: category.Output, Name: category.Output, Label: category.Label})
		}
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: pivot}
	case oracleReshapeUnpivot:
		u := shape.Unpivot
		step.ID = "unpivot"
		unpivot := &recipe.ConstructionUnpivot{ConstructionID: u.ConstructionID, KeyOutputColumnID: u.KeyOutput, ValueOutputColumnID: u.ValueOutput, NullRowPolicy: u.NullRowPolicy}
		selected := map[string]bool{}
		for _, input := range u.Inputs {
			unpivot.Inputs = append(unpivot.Inputs, recipe.ConstructionUnpivotInput{ColumnID: input.Column, Key: input.Key})
			selected[input.Column] = true
		}
		for _, column := range construction.SourceColumns {
			if !selected[column.ID] {
				step.Outputs = append(step.Outputs, column)
			}
		}
		step.Outputs = append(step.Outputs, recipe.StageColumn{ID: u.KeyOutput, Name: u.KeyOutput, Label: u.KeyLabel}, recipe.StageColumn{ID: u.ValueOutput, Name: u.ValueOutput, Label: u.ValueLabel})
		step.Operation = recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: unpivot}
	default:
		step.ID = "invalid-shape"
	}
	construction.Steps = append(construction.Steps, step)
	output.Construction = construction
	return output
}

func CompiledQueryFingerprint(compiled CompiledQuery) string {
	keys := make([]string, 0, len(compiled.BindVars))
	for key := range compiled.BindVars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(compiled.Query)
	builder.WriteByte('\x00')
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.Columns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, column := range compiled.PublicColumns {
		builder.WriteString(column)
		builder.WriteByte('\x00')
	}
	for _, field := range compiled.PivotFields {
		builder.WriteString(field)
		builder.WriteByte('\x00')
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func TestSanitizeColumnNamePreservesCompilerShape(t *testing.T) {
	if got, want := sanitizeColumnName("field__--value--"), "field____value__"; got != want {
		t.Fatalf("sanitizeColumnName() = %q, want %q", got, want)
	}
}
