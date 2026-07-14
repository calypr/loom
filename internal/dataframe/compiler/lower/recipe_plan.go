package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// RecipePhysicalPlan is the backend-neutral physical lowering of a resolved
// recipe. It is deliberately an expression/traversal plan rather than an AQL
// string; a backend renderer can choose its execution shape without changing
// the resolved recipe contract.
type RecipePhysicalPlan struct {
	Version              int
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	BindVars             map[string]any
	Outputs              []RecipePhysicalOutput
}

type RecipePhysicalOutput struct {
	Name             string
	RootResourceType string
	RowGrain         semantic.RowGrain
	Identity         *ir.PhysicalExpression
	Expansion        *RecipePhysicalExpansion
	Fields           []RecipePhysicalProjection
	DynamicMaps      []RecipePhysicalDynamicMap
	Root             []RecipePhysicalTraversal
}

type RecipePhysicalExpansion struct {
	As   string
	From ir.PhysicalExpression
}

type RecipePhysicalProjection struct {
	Name string
	Expr ir.PhysicalExpression
}

type RecipePhysicalDynamicMap struct {
	Name            string
	Source          ir.PhysicalExpression
	Key             *ir.PhysicalExpression
	Value           *ir.PhysicalExpression
	Columns         []string
	ResolvedColumns []recipeplan.Column
	MaxColumns      int
}

type RecipePhysicalTraversal struct {
	Name           string
	Alias          string
	ToResourceType string
	MatchMode      string
	Direction      string
	From           *ir.PhysicalExpression
	Fields         []RecipePhysicalProjection
	Children       []RecipePhysicalTraversal
}

// LowerResolvedRecipePlan is the single semantic-to-physical entry point for
// persisted recipes. Every expression is lowered through the typed physical
// IR and literals are collected into one bind map.
func LowerResolvedRecipePlan(plan semantic.ResolvedRecipePlan) (RecipePhysicalPlan, error) {
	if plan.SemanticPlan.Version <= 0 || strings.TrimSpace(plan.SemanticPlan.RecipeDigest) == "" {
		return RecipePhysicalPlan{}, fmt.Errorf("resolved recipe plan is missing semantic provenance")
	}
	physical := RecipePhysicalPlan{
		Version: 1, RecipeDigest: plan.SemanticPlan.RecipeDigest,
		ResolvedSchemaDigest: plan.ResolvedSchemaDigest,
		SourceGeneration:     plan.SourceGeneration, BindVars: map[string]any{},
		Outputs: make([]RecipePhysicalOutput, 0, len(plan.SemanticPlan.Outputs)),
	}
	for _, output := range plan.SemanticPlan.Outputs {
		lowered, err := lowerRecipeOutput(output, plan.ResolvedColumns, physical.BindVars)
		if err != nil {
			return RecipePhysicalPlan{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		physical.Outputs = append(physical.Outputs, lowered)
	}
	return physical, nil
}

func lowerRecipeOutput(output semantic.OutputPlan, resolvedColumns map[string][]semantic.ResolvedColumn, binds map[string]any) (RecipePhysicalOutput, error) {
	result := RecipePhysicalOutput{Name: output.Name, RootResourceType: output.RootResourceType, RowGrain: output.RowGrain}
	for _, field := range output.Fields {
		expr, err := LowerRecipeExpression(field.Expr.Expression, binds, output.RootResourceType)
		if err != nil {
			return RecipePhysicalOutput{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		result.Fields = append(result.Fields, RecipePhysicalProjection{Name: field.Name, Expr: expr})
	}
	if output.Identity != nil {
		expr, err := LowerRecipeExpression(output.Identity.Expression, binds, output.RootResourceType)
		if err != nil {
			return RecipePhysicalOutput{}, fmt.Errorf("identity: %w", err)
		}
		result.Identity = &expr
	}
	if output.Expansion != nil {
		expr, err := LowerRecipeExpression(output.Expansion.From.Expression, binds, output.RootResourceType)
		if err != nil {
			return RecipePhysicalOutput{}, fmt.Errorf("expansion: %w", err)
		}
		result.Expansion = &RecipePhysicalExpansion{As: output.Expansion.As, From: expr}
	}
	for _, dynamic := range output.DynamicMaps {
		item := RecipePhysicalDynamicMap{Name: dynamic.Name, Columns: append([]string(nil), dynamic.Columns...), MaxColumns: dynamic.MaxColumns}
		if resolved, ok := resolvedColumns[output.Name+":"+dynamic.Name]; ok {
			item.ResolvedColumns = make([]recipeplan.Column, 0, len(resolved))
			for _, column := range resolved {
				item.ResolvedColumns = append(item.ResolvedColumns, column.Column)
			}
		}
		var err error
		item.Source, err = LowerRecipeExpression(dynamic.Source.Expression, binds, output.RootResourceType)
		if err != nil {
			return RecipePhysicalOutput{}, fmt.Errorf("dynamic map %q source: %w", dynamic.Name, err)
		}
		if dynamic.Key != nil {
			expr, err := lowerDynamicExpression(dynamic.Key.Expression, binds, output.RootResourceType)
			if err != nil {
				return RecipePhysicalOutput{}, err
			}
			item.Key = &expr
		}
		if dynamic.Value != nil {
			expr, err := lowerDynamicExpression(dynamic.Value.Expression, binds, output.RootResourceType)
			if err != nil {
				return RecipePhysicalOutput{}, err
			}
			item.Value = &expr
		}
		result.DynamicMaps = append(result.DynamicMaps, item)
	}
	for _, child := range output.Root.Children {
		traversal, err := lowerRecipeTraversal(child, output.RootResourceType, binds)
		if err != nil {
			return RecipePhysicalOutput{}, err
		}
		result.Root = append(result.Root, traversal)
	}
	return result, nil
}

func lowerDynamicExpression(input expression.Expression, binds map[string]any, resourceType string) (ir.PhysicalExpression, error) {
	result, err := LowerRecipeExpression(input, binds, resourceType)
	if err != nil {
		return ir.PhysicalExpression{}, err
	}
	var rewrite func(*ir.PhysicalExpression)
	rewrite = func(value *ir.PhysicalExpression) {
		if value == nil {
			return
		}
		if value.Extract != nil && value.Extract.Source.Variable == "item" {
			value.Extract.Source.Path = nil
		}
		if value.Call != nil {
			for index := range value.Call.Args {
				rewrite(&value.Call.Args[index])
			}
		}
	}
	rewrite(&result)
	return result, nil
}

func lowerRecipeTraversal(node semantic.SemanticNode, parentType string, binds map[string]any) (RecipePhysicalTraversal, error) {
	// The semantic validator has already checked this tuple. Resolve it again
	// here so the physical plan carries the storage direction required by a
	// backend renderer; a renderer must never infer direction from edge IDs.
	relationship, found, err := fhirschema.ResolveCompilerTraversal(parentType, node.EdgeLabel, node.ResourceType)
	if err != nil {
		return RecipePhysicalTraversal{}, err
	}
	if !found {
		return RecipePhysicalTraversal{}, fmt.Errorf("traversal %s -> %s (%s) has no generated compiler metadata", parentType, node.ResourceType, node.EdgeLabel)
	}
	result := RecipePhysicalTraversal{Name: node.EdgeLabel, Alias: node.Alias, ToResourceType: node.ResourceType, MatchMode: string(node.MatchMode), Direction: string(relationship.Direction)}
	if node.From != nil {
		expr, err := LowerRecipeExpression(node.From.Expression, binds, node.ResourceType)
		if err != nil {
			return RecipePhysicalTraversal{}, fmt.Errorf("from: %w", err)
		}
		result.From = &expr
	}
	for _, field := range node.Fields {
		expr, err := LowerRecipeExpression(*field.Expr, binds, node.ResourceType)
		if err != nil {
			return RecipePhysicalTraversal{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		result.Fields = append(result.Fields, RecipePhysicalProjection{Name: field.Name, Expr: expr})
	}
	for _, child := range node.Children {
		nested, err := lowerRecipeTraversal(child, node.ResourceType, binds)
		if err != nil {
			return RecipePhysicalTraversal{}, err
		}
		result.Children = append(result.Children, nested)
	}
	return result, nil
}
