// Package recipecompile adapts the selector/traversal subset of a recipe to
// Loom's existing semantic Builder. It is intentionally a structural adapter:
// recipe names and operations are data, while physical storage remains owned
// by the normal compiler pipeline.
package recipecompile

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

type CompiledOutput struct {
	Output  recipe.Output
	Builder compiler.Builder
}

// CompileBundle lowers every output that is expressible by the current
// selector/traversal Builder. Rich expression calls remain available to the
// generic recipe evaluator until their physical expression operators are
// introduced; they fail explicitly rather than silently changing semantics.
func CompileBundle(bundle recipe.Bundle, bindings recipe.RuntimeBindings) ([]CompiledOutput, error) {
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	compiled := make([]CompiledOutput, 0, len(bundle.Outputs))
	for _, output := range bundle.Outputs {
		builder, err := compileOutput(output, bindings)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", output.Name, err)
		}
		compiled = append(compiled, CompiledOutput{Output: output, Builder: builder})
	}
	return compiled, nil
}

func compileOutput(output recipe.Output, bindings recipe.RuntimeBindings) (compiler.Builder, error) {
	if output.Expand != nil {
		return compiler.Builder{}, fmt.Errorf("row expansion requires the recipe physical expansion operator")
	}
	if output.Identity != nil {
		return compiler.Builder{}, fmt.Errorf("custom identity requires the recipe physical identity operator")
	}
	builder := compiler.Builder{Project: bindings.Project, DatasetGeneration: bindings.DatasetGeneration, AuthResourcePaths: append([]string(nil), bindings.AuthResourcePaths...), RootResourceType: output.RootResourceType, RowGrain: compiler.RowGrain(output.RowGrain)}
	for _, field := range output.Fields {
		selectText, mode, fallbacks, err := fieldSelector(field.Expr)
		if err != nil {
			return compiler.Builder{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		builder.Fields = append(builder.Fields, compiler.FieldSelect{Name: field.Name, Select: selectText, FallbackSelects: fallbacks, ValueMode: mode})
	}
	for _, traversal := range output.Traversals {
		step, err := compileTraversal(traversal)
		if err != nil {
			return compiler.Builder{}, err
		}
		builder.Traversals = append(builder.Traversals, step)
	}
	if len(output.DynamicColumns) > 0 {
		return compiler.Builder{}, fmt.Errorf("dynamic columns require schema discovery before physical compilation")
	}
	return builder, nil
}

func compileTraversal(input recipe.Traversal) (compiler.TraversalStep, error) {
	if input.From != nil {
		return compiler.TraversalStep{}, fmt.Errorf("traversal %q: from expressions require the recipe relationship operator", input.Name)
	}
	step := compiler.TraversalStep{Label: input.Name, Alias: input.Alias, ToResourceType: input.ToResourceType, MatchMode: compiler.TraversalMatchMode(strings.ToUpper(input.MatchMode))}
	if step.MatchMode == "" {
		step.MatchMode = compiler.TraversalMatchOptional
	}
	for _, field := range input.Fields {
		selectText, mode, fallbacks, err := fieldSelector(field.Expr)
		if err != nil {
			return compiler.TraversalStep{}, fmt.Errorf("traversal %q field %q: %w", input.Name, field.Name, err)
		}
		step.Fields = append(step.Fields, compiler.FieldSelect{Name: field.Name, Select: selectText, FallbackSelects: fallbacks, ValueMode: mode})
	}
	for _, child := range input.Traversals {
		nested, err := compileTraversal(child)
		if err != nil {
			return compiler.TraversalStep{}, err
		}
		step.Traversals = append(step.Traversals, nested)
	}
	return step, nil
}

func fieldSelector(expr recipe.Expression) (string, string, []string, error) {
	if expr.Select != "" {
		return stripContext(expr.Select), "AUTO", nil, nil
	}
	if strings.EqualFold(expr.Call, "first") || strings.EqualFold(expr.Call, "all") || strings.EqualFold(expr.Call, "distinct") {
		if len(expr.Args) != 1 || expr.Args[0].Select == "" {
			return "", "", nil, fmt.Errorf("%s requires one selector argument", expr.Call)
		}
		mode := map[string]string{"first": "FIRST", "all": "ALL", "distinct": "DISTINCT"}[strings.ToLower(expr.Call)]
		return stripContext(expr.Args[0].Select), mode, nil, nil
	}
	if strings.EqualFold(expr.Call, "coalesce") || strings.EqualFold(expr.Call, "fallback") {
		if len(expr.Args) == 0 {
			return "", "", nil, fmt.Errorf("%s requires selector arguments", expr.Call)
		}
		first, mode, _, err := fieldSelector(expr.Args[0])
		if err != nil {
			return "", "", nil, err
		}
		fallbacks := make([]string, 0, len(expr.Args)-1)
		for _, arg := range expr.Args[1:] {
			path, _, _, err := fieldSelector(arg)
			if err != nil {
				return "", "", nil, err
			}
			fallbacks = append(fallbacks, path)
		}
		return first, mode, fallbacks, nil
	}
	return "", "", nil, fmt.Errorf("expression call %q is not representable by selector Builder", expr.Call)
}

func stripContext(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "root.") {
		return strings.TrimPrefix(path, "root.")
	}
	return path
}
