package lower

// This file contains the canonical recipe lowering boundary. Persisted
// recipes are a frontend: after resolution each output is lowered to the same
// ir.PhysicalPlan used by the GraphQL dataframe compiler.

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// CompiledRecipe is orchestration metadata around one canonical physical plan
// per output.  It deliberately contains no recipe-specific traversal,
// projection, or renderer structures.  Output order is the persisted recipe
// order and therefore part of the stable materialization contract.
func cloneRecipeNodeForPhysical(node semantic.SemanticNode) semantic.SemanticNode {
	copy := node
	copy.Fields = append([]semantic.SemanticField(nil), node.Fields...)
	copy.Children = make([]semantic.SemanticNode, len(node.Children))
	for index := range node.Children {
		copy.Children[index] = cloneRecipeNodeForPhysical(node.Children[index])
	}
	// BuildGenericPhysicalPlan currently consumes selector metadata for its
	// structural projections.  Recipe expressions are patched after the
	// generic plan is built, so provide a schema-valid selector seed for call
	// expressions that contain no selector (for example a literal-only call).
	for index := range copy.Fields {
		// The generic lowerer uses SemanticField.Selector only to construct a
		// valid traversal/set skeleton. Recipe expressions may refer to an
		// expanded item (for example member.entity.reference), which is not a
		// selector relative to the root resource. The checked recipe expression
		// replaces this seed immediately after the skeleton is built.
		if copy.Fields[index].Expr == nil || copy.Fields[index].Expr.Selector == nil {
			if selector, err := ParseSelector("id"); err == nil {
				copy.Fields[index].Selector = selector
			}
		}
	}
	return copy
}

// recipeOutputSchema is the single schema capture point for recipe outputs.
// Names and order come from the physical RETURN operation after all recipe
// identity and bounded dynamic projections have been appended. Semantic
// metadata is used only to enrich those already-finalized projections with
// logical type/cardinality information.
func recipeOutputSchema(plan ir.PhysicalPlan, output semantic.OutputPlan) []CompiledOutputColumn {
	logical := make(map[string]CompiledOutputColumn)
	addLogical := func(name, kind, cardinality string, nullable bool) {
		if strings.TrimSpace(name) == "" {
			return
		}
		if _, exists := logical[name]; exists {
			return
		}
		logical[name] = CompiledOutputColumn{Name: name, Kind: kind, Cardinality: cardinality, Nullable: nullable}
	}
	addType := func(name string, typ expression.Type) {
		kind := string(typ.Kind)
		if kind == "" {
			kind = string(expression.KindString)
		}
		cardinality := string(typ.Cardinality)
		if cardinality == "" {
			cardinality = string(expression.RequiredOne)
		}
		addLogical(name, kind, cardinality, typ.Cardinality.Optional())
	}
	for _, field := range output.Fields {
		addType(field.Name, field.Expr.Type)
	}
	var addNode func(semantic.SemanticNode, string)
	addNode = func(node semantic.SemanticNode, prefix string) {
		for _, field := range node.Fields {
			name := prefix + field.Name
			if field.Expr != nil {
				addType(name, field.ExprType)
			} else {
				addLogical(name, string(expression.KindString), string(expression.RequiredOne), true)
			}
		}
		for _, aggregate := range node.Aggregates {
			addLogical(prefix+aggregate.Name, string(expression.KindInteger), string(expression.RequiredOne), true)
		}
		for _, pivot := range node.Pivots {
			kind := pivot.ValueKind
			if kind == "" {
				kind = expression.KindString
			}
			for _, column := range pivot.Columns {
				addLogical(prefix+pivot.Name+"__"+sanitizeColumnName(column), string(kind), string(expression.RequiredOne), true)
			}
		}
		for _, slice := range node.Slices {
			addLogical(prefix+slice.Name, string(expression.KindObject), string(expression.RequiredOne), true)
		}
		for _, child := range node.Children {
			childPrefix := child.Alias
			if prefix != "" {
				childPrefix = strings.TrimSuffix(prefix, "__") + "__" + child.Alias
			}
			addNode(child, childPrefix+"__")
		}
	}
	addNode(output.Root, "")

	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		result := make([]CompiledOutputColumn, 0, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			column, ok := logical[projection.Name]
			if !ok {
				column = CompiledOutputColumn{Name: projection.Name, Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Nullable: true}
			}
			if projection.Expression != nil {
				if column.Cardinality == string(expression.RequiredOne) && projection.Expression.Cardinality == ir.PhysicalArrayCardinality {
					column.Cardinality = string(expression.Many)
					column.Nullable = true
				}
				if projection.Expression.Kind == ir.PhysicalPivotExpression || projection.Expression.Kind == ir.PhysicalObjectExpression {
					column.Kind = string(expression.KindObject)
				}
			}
			column.Name = projection.Name
			column.Internal = projection.Hidden || projection.Name == "_key" || strings.HasPrefix(projection.Name, "__loom_")
			column.Identity = projection.Name == "_key" || projection.Name == "__loom_row_id"
			result = append(result, column)
		}
		return result
	}
	return nil
}

func physicalOutputColumns(schema []CompiledOutputColumn) []string {
	columns := make([]string, 0, len(schema))
	for _, column := range schema {
		if column.Name == "__loom_dynamic_runtime_keys" {
			continue
		}
		columns = append(columns, column.Name)
	}
	return columns
}
