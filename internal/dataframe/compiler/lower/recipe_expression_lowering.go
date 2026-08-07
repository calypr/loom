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
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// CompiledRecipe is orchestration metadata around one canonical physical plan
// per output.  It deliberately contains no recipe-specific traversal,
// projection, or renderer structures.  Output order is the persisted recipe
// order and therefore part of the stable materialization contract.
func patchRecipeExpressions(plan *ir.PhysicalPlan, output semantic.OutputPlan) error {
	if plan == nil {
		return fmt.Errorf("physical plan is nil")
	}
	fields := map[string]semantic.SemanticExpression{}
	for _, field := range output.Fields {
		fields[field.Name] = field.Expr
	}
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for index := range operation.Return.Projections {
			projection := &operation.Return.Projections[index]
			field, ok := fields[projection.Name]
			if !ok {
				continue
			}
			// Selector expressions are already lowered by the generic semantic
			// node, including AUTO/FIRST/ALL/DISTINCT, fallbacks, and prepared
			// selector metadata. Replacing that physical extract with the raw
			// recipe expression would drop the legacy AUTO cardinality contract
			// (for example, repeated selectors become FIRST). Only richer
			// expression ASTs need a post-lowering patch.
			if field.Expression.Selector != nil {
				continue
			}
			physical, err := lowerRecipeExpressionScoped(field.Expression, plan.BindVars, output.RootResourceType, recipeExpressionContexts(output))
			if err != nil {
				return fmt.Errorf("field expression: %w", err)
			}
			projection.Expression = &physical
			projection.Value = ir.PhysicalValue{}
		}
	}
	setVariables := recipeSetVariables(*plan)

	// Child fields are returned as flattened projections named alias__field by
	// the generic lowerer.  Match those names deterministically and retain the
	// child resource type for selector provenance.
	var walk func(semantic.SemanticNode) error
	walk = func(node semantic.SemanticNode) error {
		for _, child := range node.Children {
			for _, field := range child.Fields {
				name := child.Alias + "__" + field.Name
				for index := range plan.Operations {
					op := &plan.Operations[index]
					if op.Kind != ir.PhysicalReturnOp || op.Return == nil {
						continue
					}
					for projectionIndex := range op.Return.Projections {
						projection := &op.Return.Projections[projectionIndex]
						if projection.Name != name {
							continue
						}
						if field.Expr != nil && field.Expr.Selector != nil {
							continue
						}
						physical, err := lowerRecipeExpressionScoped(*field.Expr, plan.BindVars, output.RootResourceType, recipeExpressionContexts(output))
						if err != nil {
							return fmt.Errorf("child field %q: %w", name, err)
						}
						setVariable, ok := setVariables[child.Alias]
						if !ok {
							return fmt.Errorf("child field %q has no canonical set variable for alias %q", name, child.Alias)
						}
						rewriteRecipeExpressionVariable(&physical, child.Alias, setVariable)
						projection.Expression = &physical
						projection.Value = ir.PhysicalValue{}
					}
				}
			}
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(output.Root)
}

func recipeSetVariables(plan ir.PhysicalPlan) map[string]string {
	variables := map[string]string{"root": "root"}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Source.SemanticNode != "" {
			variables[operation.Source.SemanticNode] = operation.Set.Variable
		}
	}
	return variables
}

func insertPhysicalExpressionLet(plan *ir.PhysicalPlan, returnIndex int, variable string, expression ir.PhysicalExpression) {
	operation := ir.PhysicalOperation{Kind: ir.PhysicalExpressionLetOp, Source: ir.PhysicalSource{SemanticField: "shared_family"}, ExpressionLet: &ir.PhysicalExpressionLet{Variable: variable, Expression: expression}}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[returnIndex+1:], plan.Operations[returnIndex:])
	plan.Operations[returnIndex] = operation
}

func dynamicFamilyVariable(plan ir.PhysicalPlan, name string) string {
	base := "__loom_family_" + sanitizeColumnName(name)
	if base == "__loom_family_" {
		base = "__loom_family_dynamic"
	}
	used := map[string]bool{}
	for _, operation := range plan.Operations {
		if operation.ExpressionLet != nil {
			used[operation.ExpressionLet.Variable] = true
		}
		if operation.Set != nil {
			used[operation.Set.Variable] = true
		}
	}
	if !used[base] {
		return base
	}
	for index := 1; ; index++ {
		candidate := fmt.Sprintf("%s_%d", base, index)
		if !used[candidate] {
			return candidate
		}
	}
}

func rewriteRecipeExpressionVariable(value *ir.PhysicalExpression, from, to string) {
	if value == nil {
		return
	}
	if value.Extract != nil && value.Extract.Source.Variable == from {
		value.Extract.Source.Variable = to
	}
	if value.Call != nil {
		for index := range value.Call.Args {
			rewriteRecipeExpressionVariable(&value.Call.Args[index], from, to)
		}
	}
}

func recipeExpressionContexts(output semantic.OutputPlan) map[string]string {
	contexts := map[string]string{"root": output.RootResourceType}
	var walk func(semantic.SemanticNode)
	walk = func(node semantic.SemanticNode) {
		for _, child := range node.Children {
			contexts[child.Alias] = child.ResourceType
			walk(child)
		}
	}
	walk(output.Root)
	if unnest := recipeUnnest(output); unnest != nil && unnest.Source.Expression.Selector != nil {
		selector := unnest.Source.Expression.Selector
		path := strings.TrimSuffix(strings.TrimPrefix(selector.Path, "."), "[]")
		if semantics, ok := fhirschema.ResolveFieldSemantics(output.RootResourceType, path+"[]"); ok && semantics.Reference != "" {
			contexts[unnest.As] = semantics.Reference
		}
	}
	return contexts
}

// lowerRecipeExpressionScoped is the recipe-expression counterpart to the
// generic lowerer's selector classifier. A recipe expression may combine
// selectors from several lexical bindings (root plus an expanded item), so a
// single resourceType argument is insufficient. Each selector is lowered in
// its binding's generated schema type and item selectors are rooted at the
// item value rather than at a FHIR document payload.
func lowerRecipeExpressionScoped(input expression.Expression, bindVars map[string]any, rootResourceType string, contexts map[string]string) (ir.PhysicalExpression, error) {
	if input.Document != nil {
		context := strings.TrimSpace(input.Document.Context)
		if context == "" {
			context = "root"
		}
		if _, ok := contexts[context]; !ok {
			return ir.PhysicalExpression{}, fmt.Errorf("document context %q is not in scope", context)
		}
		return lowerDocumentRef(*input.Document, physicalNullBehavior(input.NullBehavior)), nil
	}
	if input.Selector != nil {
		variable := strings.TrimSpace(input.Selector.Context)
		if variable == "" {
			variable = "root"
		}
		resourceType := rootResourceType
		if resolved, ok := contexts[variable]; ok {
			resourceType = resolved
		}
		physical, err := LowerRecipeExpression(input, bindVars, resourceType)
		if err != nil {
			return ir.PhysicalExpression{}, err
		}
		if variable != "root" {
			physical.Extract.Source.Variable = variable
			physical.Extract.Source.Path = nil
		}
		return physical, nil
	}
	if input.Literal != nil {
		return LowerRecipeExpression(input, bindVars, rootResourceType)
	}
	if input.Call == nil {
		return ir.PhysicalExpression{}, fmt.Errorf("recipe expression has no selector, literal, or call")
	}
	cardinality := ir.PhysicalScalarCardinality
	if input.Type.Cardinality == expression.Many {
		cardinality = ir.PhysicalArrayCardinality
	}
	behavior := ir.PhysicalPreserveNull
	if input.NullBehavior == expression.NullEmpty {
		behavior = ir.PhysicalEmptyOnNull
	}
	call := &ir.PhysicalCall{Name: strings.ToLower(strings.TrimSpace(input.Call.Name))}
	if input.Call.Target != nil {
		call.TargetKind = string(input.Call.Target.Kind)
	}
	for index, argument := range input.Call.Args {
		if call.Name == "cast" && input.Call.Target != nil && index == 1 {
			continue
		}
		lowered, err := lowerRecipeExpressionScoped(argument, bindVars, rootResourceType, contexts)
		if err != nil {
			return ir.PhysicalExpression{}, fmt.Errorf("call %q argument %d: %w", input.Call.Name, index, err)
		}
		call.Args = append(call.Args, lowered)
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalCallExpression, Cardinality: cardinality, NullBehavior: behavior, Call: call}, nil
}

func physicalNullBehavior(behavior expression.NullBehavior) ir.PhysicalNullBehavior {
	if behavior == expression.NullEmpty {
		return ir.PhysicalEmptyOnNull
	}
	return ir.PhysicalPreserveNull
}

func appendRecipeIdentity(plan *ir.PhysicalPlan, output semantic.OutputPlan) error {
	if output.Identity == nil {
		return nil
	}
	physical, err := lowerRecipeExpressionScoped(output.Identity.Expression, plan.BindVars, output.RootResourceType, recipeExpressionContexts(output))
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{Name: "__loom_row_id", Expression: &physical})
		return nil
	}
	return fmt.Errorf("canonical plan has no RETURN operation for identity")
}

func recipeUnnest(output semantic.OutputPlan) *semantic.SemanticUnnest {
	if output.Unnest != nil {
		copy := *output.Unnest
		return &copy
	}
	return nil
}

func appendRecipeUnnest(plan *ir.PhysicalPlan, unnest semantic.SemanticUnnest, resourceType string) error {
	if err := unnest.Validate(); err != nil {
		return fmt.Errorf("unnest: %w", err)
	}
	expression, err := lowerRecipeExpressionScoped(unnest.Source.Expression, plan.BindVars, resourceType, map[string]string{"root": resourceType})
	if err != nil {
		return fmt.Errorf("unnest source: %w", err)
	}
	joinMode := ir.PhysicalUnnestJoinMode(unnest.JoinMode)
	operation := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Source: ir.PhysicalSource{ResourceType: resourceType, SemanticField: "expand"}, Unnest: &ir.PhysicalUnnest{InputVariable: "root", OutputVariable: unnest.As, Ordinality: unnest.Ordinality, Expression: expression, JoinMode: joinMode}}
	// Place the cardinality barrier immediately after root qualification and
	// before any child set materialization.  This ensures child operations can
	// never accidentally consume an item binding from a later scope.
	insert := len(plan.Operations)
	for index, current := range plan.Operations {
		if current.Kind == ir.PhysicalTraversalOp || current.Kind == ir.PhysicalSetOp || current.Kind == ir.PhysicalReturnOp {
			insert = index
			break
		}
	}
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{})
	copy(plan.Operations[insert+1:], plan.Operations[insert:])
	plan.Operations[insert] = operation
	return nil
}
