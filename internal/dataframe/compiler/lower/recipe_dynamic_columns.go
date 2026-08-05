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
func appendRecipeDynamicColumns(plan *ir.PhysicalPlan, output semantic.OutputPlan, resolvedColumns map[string][]semantic.ResolvedColumn) ([]DynamicColumnMetadata, error) {
	dynamics := flattenRecipeDynamicMaps(output.Root)
	if len(dynamics) == 0 {
		return nil, nil
	}
	returnOp := -1
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalReturnOp && plan.Operations[index].Return != nil {
			returnOp = index
			break
		}
	}
	if returnOp < 0 {
		return nil, fmt.Errorf("canonical plan has no RETURN operation for dynamic columns")
	}
	projectionIndexes := map[string]int{}
	for index, projection := range plan.Operations[returnOp].Return.Projections {
		projectionIndexes[projection.Name] = index
	}
	metadata := make([]DynamicColumnMetadata, 0)
	metadataIndexes := map[string]int{}
	runtimeKeyFields := make([]ir.PhysicalExpressionProjection, 0, len(dynamics))
	setVariables := recipeSetVariables(*plan)
	for _, dynamic := range dynamics {
		columns := resolvedColumns[recipeDynamicMapKey(output.Name, dynamic)]
		if dynamic.MaxColumns > 0 && len(columns) > dynamic.MaxColumns {
			return nil, fmt.Errorf("dynamic map %q resolved %d columns, exceeding max %d", dynamic.Name, len(columns), dynamic.MaxColumns)
		}
		if len(columns) == 0 {
			continue
		}
		resourceType := output.RootResourceType
		if dynamic.ResourceType != "" {
			resourceType = dynamic.ResourceType
		}
		source, err := lowerRecipeExpressionScoped(dynamic.Source.Expression, plan.BindVars, resourceType, recipeExpressionContexts(output))
		if err != nil {
			return nil, fmt.Errorf("dynamic map %q source: %w", dynamic.Name, err)
		}
		if source.Cardinality != ir.PhysicalArrayCardinality {
			return nil, fmt.Errorf("dynamic map %q source must remain array-valued", dynamic.Name)
		}
		key := ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "dynamic_item"}}
		if dynamic.Key != nil {
			key, err = lowerDynamicItemExpression(*dynamic.Key, plan.BindVars, resourceType)
			if err != nil {
				return nil, fmt.Errorf("dynamic map %q key: %w", dynamic.Name, err)
			}
		}
		value := ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "dynamic_item"}}
		if dynamic.Value != nil {
			value, err = lowerDynamicItemExpression(*dynamic.Value, plan.BindVars, resourceType)
			if err != nil {
				return nil, fmt.Errorf("dynamic map %q value: %w", dynamic.Name, err)
			}
		}
		if dynamic.ScopeAlias != "" {
			if variable, ok := setVariables[dynamic.ScopeAlias]; ok {
				rewriteRecipeExpressionVariable(&source, dynamic.ScopeAlias, variable)
				if source.Extract != nil {
					source.Extract.Source.Path = []string{"payload"}
				}
			}
		}
		runtimeName := dynamic.Name
		if dynamic.ScopeAlias != "" && dynamic.ScopeAlias != "root" {
			runtimeName = dynamic.ScopeAlias + "__" + dynamic.Name
		}
		familyVariable := dynamicFamilyVariable(*plan, runtimeName)
		keyedMap := ir.PhysicalExpression{Kind: ir.PhysicalKeyedMapExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, KeyedMap: &ir.PhysicalKeyedMap{Source: ir.ClonePhysicalExpression(source), ItemVariable: "dynamic_item", ItemKey: ir.ClonePhysicalExpression(key), ItemValue: ir.ClonePhysicalExpression(value), Reduction: ir.PhysicalMapFirstSorted, FlattenSource: true}}
		insertPhysicalExpressionLet(plan, returnOp, familyVariable, keyedMap)
		returnOp++
		runtimeKey := ir.PhysicalExpression{Kind: ir.PhysicalObjectKeysExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, ObjectKeys: &ir.PhysicalObjectKeys{ObjectVariable: familyVariable}}
		runtimeKeyFields = append(runtimeKeyFields, ir.PhysicalExpressionProjection{Name: runtimeName, Expression: runtimeKey})
		projectionPrefix := ""
		if dynamic.ScopeAlias != "" && dynamic.ScopeAlias != "root" {
			projectionPrefix = dynamic.ScopeAlias + "__"
		}
		for index, column := range columns {
			outputName := projectionPrefix + column.Column.Name
			if column.Column.Name == "" {
				return nil, fmt.Errorf("dynamic map %q contains an empty resolved column name", dynamic.Name)
			}
			matchBindKey := nextDynamicBindKey(plan.BindVars, projectionPrefix+dynamic.Name, index)
			plan.BindVars[matchBindKey] = column.Column.SourceKey
			lookup := ir.PhysicalExpression{Kind: ir.PhysicalObjectLookupExpression, Cardinality: value.Cardinality, NullBehavior: value.NullBehavior, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: familyVariable, KeyBindKey: matchBindKey}}
			entry := DynamicColumnMetadata{Name: outputName, DynamicName: runtimeName, SourceKey: column.Column.SourceKey, ValueType: column.Column.ValueType}
			if projectionIndex, exists := projectionIndexes[outputName]; exists {
				if output.Collision != "overwrite" {
					return nil, fmt.Errorf("dynamic column %q collides with another output column", outputName)
				}
				// The legacy dataframe flatteners update one row map in declaration
				// order. Preserve that contract for an explicit overwrite policy
				// instead of rejecting a category/identifier or extension collision.
				plan.Operations[returnOp].Return.Projections[projectionIndex] = ir.PhysicalProjection{Name: outputName, Expression: &lookup}
				if metadataIndex, dynamicExists := metadataIndexes[outputName]; dynamicExists {
					metadata[metadataIndex] = entry
				} else {
					metadataIndexes[outputName] = len(metadata)
					metadata = append(metadata, entry)
				}
				continue
			}
			projectionIndexes[outputName] = len(plan.Operations[returnOp].Return.Projections)
			plan.Operations[returnOp].Return.Projections = append(plan.Operations[returnOp].Return.Projections, ir.PhysicalProjection{Name: outputName, Expression: &lookup})
			metadataIndexes[outputName] = len(metadata)
			metadata = append(metadata, entry)
		}
	}
	if len(runtimeKeyFields) > 0 {
		runtime := ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: runtimeKeyFields}}
		plan.Operations[returnOp].Return.Projections = append(plan.Operations[returnOp].Return.Projections, ir.PhysicalProjection{Name: "__loom_dynamic_runtime_keys", Hidden: true, Expression: &runtime})
	}
	return metadata, nil
}

func flattenRecipeDynamicMaps(root semantic.SemanticNode) []semantic.SemanticDynamicMap {
	result := make([]semantic.SemanticDynamicMap, 0)
	var walk func(semantic.SemanticNode)
	walk = func(node semantic.SemanticNode) {
		result = append(result, node.DynamicMaps...)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return result
}

func recipeDynamicMapKey(output string, dynamic semantic.SemanticDynamicMap) string {
	if dynamic.ScopeAlias == "" || dynamic.ScopeAlias == "root" {
		return output + ":" + dynamic.Name
	}
	return output + ":" + dynamic.ScopeAlias + ":" + dynamic.Name
}

func nextDynamicBindKey(bindVars map[string]any, name string, index int) string {
	base := "recipe_dynamic_" + strings.TrimSpace(name) + "_key_" + fmt.Sprintf("%d", index)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, base)
	key := base
	for suffix := 1; ; suffix++ {
		if _, exists := bindVars[key]; !exists {
			return key
		}
		key = fmt.Sprintf("%s_%d", base, suffix)
	}
}

func lowerDynamicItemExpression(input semantic.SemanticExpression, bindVars map[string]any, resourceType string) (ir.PhysicalExpression, error) {
	if input.Expression.Selector != nil && strings.EqualFold(strings.TrimSpace(input.Expression.Selector.Context), "item") {
		selector, err := spec.ParseSelector(input.Expression.Selector.Path)
		if err != nil {
			return ir.PhysicalExpression{}, err
		}
		path := make([]string, 0, len(selector.Steps))
		for _, step := range selector.Steps {
			if step.Iterate || step.Index != nil {
				return ir.PhysicalExpression{}, fmt.Errorf("dynamic item selector %q must be a scalar path", input.Expression.Selector.Path)
			}
			path = append(path, step.Field)
		}
		cardinality := ir.PhysicalScalarCardinality
		if input.Type.Cardinality == expression.Many {
			cardinality = ir.PhysicalArrayCardinality
		}
		behavior := ir.PhysicalPreserveNull
		if input.Expression.NullBehavior == expression.NullEmpty {
			behavior = ir.PhysicalEmptyOnNull
		}
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: cardinality, NullBehavior: behavior, Value: &ir.PhysicalValue{Variable: "dynamic_item", Path: path}}, nil
	}
	return lowerDynamicItemAST(input.Expression, bindVars, resourceType)
}

// lowerDynamicItemAST lowers a dynamic value/key expression after semantic
// checking has already proven every selector. It intentionally handles item
// selectors as raw payload paths: the item resource type is a nested FHIR
// datatype (Extension, Identifier, ObservationComponent, ...), not the parent
// resource type carried by the dynamic map. Re-validating it against the
// parent schema would reject valid generic choice fields.
func lowerDynamicItemAST(input expression.Expression, bindVars map[string]any, resourceType string) (ir.PhysicalExpression, error) {
	if input.Selector != nil && strings.EqualFold(strings.TrimSpace(input.Selector.Context), "item") {
		selector, err := spec.ParseSelector(input.Selector.Path)
		if err != nil {
			return ir.PhysicalExpression{}, err
		}
		path := make([]string, 0, len(selector.Steps))
		for _, step := range selector.Steps {
			if step.Iterate || step.Index != nil {
				return ir.PhysicalExpression{}, fmt.Errorf("dynamic item selector %q must be a scalar path", input.Selector.Path)
			}
			path = append(path, step.Field)
		}
		cardinality := ir.PhysicalScalarCardinality
		if input.Type.Cardinality == expression.Many {
			cardinality = ir.PhysicalArrayCardinality
		}
		behavior := ir.PhysicalPreserveNull
		if input.NullBehavior == expression.NullEmpty {
			behavior = ir.PhysicalEmptyOnNull
		}
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: cardinality, NullBehavior: behavior, Value: &ir.PhysicalValue{Variable: "dynamic_item", Path: path}}, nil
	}
	if input.Call == nil {
		return lowerRecipeExpressionScoped(input, bindVars, resourceType, map[string]string{"root": resourceType})
	}
	call := &ir.PhysicalCall{Name: strings.ToLower(strings.TrimSpace(input.Call.Name))}
	if input.Call.Target != nil {
		call.TargetKind = string(input.Call.Target.Kind)
	}
	for index, argument := range input.Call.Args {
		if call.Name == "cast" && input.Call.Target != nil && index == 1 {
			continue
		}
		lowered, err := lowerDynamicItemAST(argument, bindVars, resourceType)
		if err != nil {
			return ir.PhysicalExpression{}, fmt.Errorf("call %q argument %d: %w", input.Call.Name, index, err)
		}
		call.Args = append(call.Args, lowered)
	}
	cardinality := ir.PhysicalScalarCardinality
	if input.Type.Cardinality == expression.Many {
		cardinality = ir.PhysicalArrayCardinality
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Cardinality: cardinality, NullBehavior: ir.PhysicalPreserveNull, Call: call}, nil
}
