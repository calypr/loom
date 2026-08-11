package lower

// This file contains the canonical recipe lowering boundary. Persisted
// recipes are a frontend: after resolution each output is lowered to the same
// ir.PhysicalPlan used by the GraphQL dataframe compiler.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
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
			if selector, err := spec.ParseSelector("id"); err == nil {
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
func recipeOutputSchema(plan ir.PhysicalPlan, output semantic.OutputPlan, dynamicMetadata []DynamicColumnMetadata) ([]CompiledOutputColumn, error) {
	logical := make(map[string]CompiledOutputColumn)
	for _, dynamic := range dynamicMetadata {
		kind := dynamic.ValueType
		if kind == "" || kind == "unknown" {
			kind = string(expression.KindString)
		}
		logical[dynamic.Name] = CompiledOutputColumn{Name: dynamic.Name, SemanticPath: dynamic.SemanticPath, Kind: kind, Cardinality: string(expression.OptionalOne), Nullable: true, Discovered: dynamic.Discovered}
	}
	addLogical := func(name, semanticPath, kind, cardinality string, nullable, discovered bool) {
		if strings.TrimSpace(name) == "" {
			return
		}
		if existing, exists := logical[name]; exists {
			// Explicit declarations win collisions under overwrite policies.
			if existing.Discovered && !discovered {
				logical[name] = CompiledOutputColumn{Name: name, SemanticPath: semanticPath, Kind: kind, Cardinality: cardinality, Nullable: nullable, Discovered: false}
			}
			return
		}
		logical[name] = CompiledOutputColumn{Name: name, SemanticPath: semanticPath, Kind: kind, Cardinality: cardinality, Nullable: nullable, Discovered: discovered}
	}
	addType := func(name, semanticPath string, typ expression.Type, discovered bool) {
		kind := string(typ.Kind)
		if kind == "" {
			kind = string(expression.KindString)
		}
		cardinality := string(typ.Cardinality)
		if cardinality == "" {
			cardinality = string(expression.RequiredOne)
		}
		addLogical(name, semanticPath, kind, cardinality, typ.Cardinality.Optional(), discovered)
	}
	for _, field := range output.Fields {
		addType(field.Name, recipeSemanticPath(output.RootResourceType, output.RootResourceType, field.FieldRef, field.Expr.Expression), field.Expr.Type, field.Discovered)
	}
	var addNode func(semantic.SemanticNode, string)
	addNode = func(node semantic.SemanticNode, prefix string) {
		for _, field := range node.Fields {
			name := prefix + field.Name
			if field.Expr != nil {
				addType(name, recipeSemanticPath(output.RootResourceType, node.ResourceType, field.FieldRef, *field.Expr), field.ExprType, field.Discovered)
			} else {
				addLogical(name, recipeSemanticPath(output.RootResourceType, node.ResourceType, field.FieldRef, expression.Expression{}), string(expression.KindString), string(expression.RequiredOne), true, field.Discovered)
			}
		}
		for _, aggregate := range node.Aggregates {
			addLogical(prefix+aggregate.Name, recipeSemanticPath(output.RootResourceType, node.ResourceType, aggregate.FieldRef, expression.Expression{}), string(expression.KindInteger), string(expression.RequiredOne), true, false)
		}
		for _, pivot := range node.Pivots {
			kind := pivot.ValueKind
			if kind == "" {
				kind = expression.KindString
			}
			for _, column := range pivot.Columns {
				addLogical(prefix+pivot.Name+"__"+sanitizeColumnName(column), recipeSemanticPath(output.RootResourceType, node.ResourceType, pivot.FieldRef, expression.Expression{})+"["+column+"]", string(kind), string(expression.RequiredOne), true, pivot.Discovered)
			}
		}
		for _, slice := range node.Slices {
			addLogical(prefix+slice.Name, recipeSemanticPath(output.RootResourceType, node.ResourceType, "", expression.Expression{})+"."+slice.Name, string(expression.KindObject), string(expression.RequiredOne), true, false)
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
				column = CompiledOutputColumn{Name: projection.Name, SemanticPath: "recipe:" + output.Name + "/" + projection.Name, Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Nullable: true}
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
		semanticCounts := make(map[string]int, len(result))
		for _, column := range result {
			if column.Internal {
				continue
			}
			path := strings.TrimSpace(column.SemanticPath)
			if path == "" {
				return nil, fmt.Errorf("output %q column %q has no semantic identity", output.Name, column.Name)
			}
			semanticCounts[path]++
		}
		// Legacy recipes may intentionally project the same FHIR value more than
		// once. Keep those columns addressable by qualifying the ambiguous source
		// with the authored projection name. New recipes should use fieldRef when
		// they need rename-stable identities for such duplicate projections.
		for index := range result {
			if !result[index].Internal && semanticCounts[result[index].SemanticPath] > 1 {
				result[index].SemanticPath += "#" + result[index].Name
			}
		}
		return result, nil
	}
	return nil, nil
}

// recipeSemanticPath produces a storage-independent identity. Explicit
// fieldRef values are frontend/catalog provenance and are retained verbatim;
// selector expressions fall back to resource type plus normalized FHIR path.
func recipeSemanticPath(rootResource, resource, fieldRef string, expr expression.Expression) string {
	ref := strings.TrimSpace(fieldRef)
	if ref != "" {
		return strings.TrimPrefix(ref, ".")
	}
	if expr.Selector != nil {
		path := strings.Trim(strings.TrimSpace(expr.Selector.Path), ".")
		if path != "" {
			if strings.Contains(path, ".") && strings.HasPrefix(path, resource+".") {
				return path
			}
			return strings.TrimSpace(resource) + "." + path
		}
	}
	if strings.TrimSpace(resource) == "" {
		return strings.TrimSpace(rootResource)
	}
	return strings.TrimSpace(resource)
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
