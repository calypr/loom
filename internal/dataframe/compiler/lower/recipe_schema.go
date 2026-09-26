package lower

// This file contains the canonical recipe lowering boundary. Persisted
// recipes are a frontend: after resolution each output is lowered to the same
// ir.PhysicalPlan used by the GraphQL dataframe compiler.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/unit"
)

// CompiledRecipe is orchestration metadata around one canonical physical plan
// per output.  It deliberately contains no recipe-specific traversal,
// projection, or renderer structures.  Output order is the persisted recipe
// order and therefore part of the stable materialization contract.
// recipeOutputSchema is the single schema capture point for recipe outputs.
// Names and order come from the physical RETURN operation after all recipe
// identity and bounded dynamic projections have been appended. Semantic
// metadata is used only to enrich those already-finalized projections with
// logical type/cardinality information.
func recipeOutputSchema(plan ir.PhysicalPlan, output semantic.OutputPlan, dynamicMetadata []DynamicColumnMetadata, derivedTypes map[string]derivedColumnMetadata) ([]CompiledOutputColumn, error) {
	if output.GroupRows != nil {
		return []CompiledOutputColumn{
			{Name: "group_revision_id", SemanticPath: "groups.revision_id", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Nullable: false, Internal: true, Identity: true},
			{Name: "group_id", SemanticPath: "groups.group_id", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Nullable: false, Identity: true},
			{Name: "group_label", SemanticPath: "groups.label", Kind: string(expression.KindString), Cardinality: string(expression.RequiredOne), Nullable: false},
			{Name: "group_ordinal", SemanticPath: "groups.ordinal", Kind: string(expression.KindInteger), Cardinality: string(expression.RequiredOne), Nullable: false},
			{Name: "members", SemanticPath: "groups.members", Kind: string(expression.KindObject), Cardinality: string(expression.Many), Nullable: false},
			{Name: "__loom_row_id", SemanticPath: "groups.identity", Kind: string(expression.KindObject), Cardinality: string(expression.RequiredOne), Nullable: false, Internal: true, Identity: true},
		}, nil
	}
	logical := make(map[string]CompiledOutputColumn)
	fieldMetadata := make(map[string]struct{ id, label string })
	for _, dynamic := range dynamicMetadata {
		kind := dynamic.ValueType
		if kind == "" || kind == "unknown" {
			kind = string(expression.KindString)
		}
		logical[dynamic.Name] = CompiledOutputColumn{Name: dynamic.Name, SemanticPath: dynamic.SemanticPath, Kind: kind, Cardinality: string(expression.OptionalOne), Nullable: true, Discovered: dynamic.Discovered}
	}
	addLogical := func(name, semanticPath, kind, cardinality string, nullable, discovered bool, normalizedUnit *unit.UnitIdentity) {
		if strings.TrimSpace(name) == "" {
			return
		}
		if existing, exists := logical[name]; exists {
			// Explicit declarations win collisions under overwrite policies.
			if existing.Discovered && !discovered {
				logical[name] = CompiledOutputColumn{Name: name, SemanticPath: semanticPath, Kind: kind, Cardinality: cardinality, Nullable: nullable, NormalizedUnit: cloneUnitIdentity(normalizedUnit), Discovered: false}
			}
			return
		}
		logical[name] = CompiledOutputColumn{Name: name, SemanticPath: semanticPath, Kind: kind, Cardinality: cardinality, Nullable: nullable, NormalizedUnit: cloneUnitIdentity(normalizedUnit), Discovered: discovered}
	}
	addType := func(name, semanticPath string, typ expression.Type, discovered bool, normalizedUnit *unit.UnitIdentity) {
		kind := string(typ.Kind)
		if kind == "" {
			kind = string(expression.KindString)
		}
		cardinality := string(typ.Cardinality)
		if cardinality == "" {
			cardinality = string(expression.RequiredOne)
		}
		addLogical(name, semanticPath, kind, cardinality, typ.Cardinality.Optional(), discovered, normalizedUnit)
	}
	var addNode func(semantic.SemanticNode, string)
	addNode = func(node semantic.SemanticNode, prefix string) {
		for _, field := range node.Fields {
			name := prefix + field.Name
			addType(name, recipeSemanticPath(output.RootResourceType, node.ResourceType, field.FieldRef, field.Expr.Expression), field.Expr.Type, field.Discovered, nil)
			fieldMetadata[name] = struct{ id, label string }{field.ColumnID, field.Label}
		}
		for _, aggregate := range node.Aggregates {
			name := aggregate.Name
			if aggregate.OutputName != "" {
				name = aggregate.OutputName
			} else if prefix != "" {
				name = prefix + aggregate.Name
			}
			kind := aggregate.ValueKind
			if kind == "" {
				kind = expression.KindInteger
			}
			cardinality := expression.RequiredOne
			if aggregate.Operation == string(recipe.AggregateDistinctValues) {
				cardinality = expression.Many
			}
			if aggregate.Operation == string(recipe.AggregateMin) || aggregate.Operation == string(recipe.AggregateMax) || aggregate.Operation == string(recipe.AggregateSum) || aggregate.Operation == string(recipe.AggregateMean) {
				cardinality = expression.OptionalOne
			}
			var normalizedUnit *unit.UnitIdentity
			if aggregate.UnitNormalization != nil {
				target := aggregate.UnitNormalization.Target
				normalizedUnit = &target
			}
			addLogical(name, recipeSemanticPath(output.RootResourceType, node.ResourceType, aggregate.FieldRef, expression.Expression{}), string(kind), string(cardinality), true, false, normalizedUnit)
		}
		for _, pivot := range node.Pivots {
			kind := pivot.ValueKind
			if kind == "" {
				kind = expression.KindString
			}
			cardinality := expression.RequiredOne
			switch strings.ToUpper(strings.TrimSpace(pivot.ProjectionMode)) {
			case "ALL", "DISTINCT":
				cardinality = expression.Many
			}
			for _, column := range pivot.Columns {
				name := prefix + pivot.Name + "__" + sanitizeColumnName(column)
				if alias, ok := pivot.ColumnAliases[column]; ok {
					name = prefix + alias
				}
				addLogical(name, recipeSemanticPath(output.RootResourceType, node.ResourceType, pivot.FieldRef, expression.Expression{})+"["+column+"]", string(kind), string(cardinality), true, pivot.Discovered, nil)
			}
		}
		for _, ownerRecords := range node.OwnerRecords {
			addLogical(prefix+ownerRecords.Name, recipeSemanticPath(output.RootResourceType, node.ResourceType, ownerRecords.FieldRef, expression.Expression{}), string(expression.KindObject), string(expression.Many), true, false, nil)
		}
		for _, slice := range node.Slices {
			addLogical(prefix+slice.Name, recipeSemanticPath(output.RootResourceType, node.ResourceType, "", expression.Expression{})+"."+slice.Name, string(expression.KindObject), string(expression.RequiredOne), true, false, nil)
		}
		for _, child := range node.Children {
			childPrefix := traversalColumnPrefix(output.TraversalColumnNaming, prefix, child.Alias)
			addNode(child, traversalColumnNamePrefix(childPrefix))
		}
	}
	addNode(output.Root, "")
	for _, derived := range output.DerivedColumns {
		if typ, ok := derivedTypes[derived.Name]; ok {
			addType(derived.Name, "derived:"+derived.ConstructionID, typ.Type, false, typ.NormalizedUnit)
		}
	}

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
				if projection.Expression.Cardinality == ir.PhysicalArrayCardinality && column.Cardinality != string(expression.Many) {
					column.Cardinality = string(expression.Many)
					column.Nullable = true
				}
				if projection.Expression.Kind == ir.PhysicalPivotExpression || projection.Expression.Kind == ir.PhysicalOwnerRecordsExpression || projection.Expression.Kind == ir.PhysicalObjectExpression {
					column.Kind = string(expression.KindObject)
				}
			}
			column.Name = projection.Name
			if field, ok := fieldMetadata[projection.Name]; ok {
				column.ID = field.id
				column.Label = field.label
			}
			if column.Label == "" {
				column.Label = column.Name
			}
			column.Internal = projection.Hidden || projection.Name == "_key" || strings.HasPrefix(projection.Name, "__loom_")
			column.Identity = projection.Name == "_key" || projection.Name == "__loom_row_id" || projection.Name == "__loom_expansion_identity"
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
