package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// CompiledPopulationMappingQuery is an internal witness query. Its rows are
// (selected member, final row identity) pairs and are not a dataframe schema.
// Default identities are returned as ordered IdentityPartsColumn values for
// the execution layer to hash with RowIdentity. Explicit recipe identities
// use ExplicitIdentityColumn and preserve the compiled identity expression.
type CompiledPopulationMappingQuery struct {
	Query                  string
	BindVars               map[string]any
	MemberColumn           string
	IdentityPartsColumn    string
	ExplicitIdentityColumn string
	RowIdentity            *spec.RowIdentity
	Diagnostics            ir.CompilerPlanDiagnostics
}

// CompilePopulationMappingOutputWithPolicy builds the complete mapping query
// from the canonical final output plan. Unlike ordinary Preview/Publish, it
// has no execution limit: required filters, authored filters, child sets, and
// explicit expansion all run before the internal witness terminal.
func CompilePopulationMappingOutputWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, policy ir.PhysicalOptimizationPolicy) (CompiledPopulationMappingQuery, error) {
	if output.RowIdentity == nil {
		return CompiledPopulationMappingQuery{}, fmt.Errorf("population mapping requires a row identity")
	}
	physical, err := mappingPhysicalPlan(output, policy)
	if err != nil {
		return CompiledPopulationMappingQuery{}, err
	}
	physical, err = withGenericPhysicalExecutionWindow(physical, 0)
	if err != nil {
		return CompiledPopulationMappingQuery{}, fmt.Errorf("apply population mapping execution window: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledPopulationMappingQuery{}, fmt.Errorf("render population mapping physical plan: %w", err)
	}
	return CompiledPopulationMappingQuery{
		Query: rendered.Query, BindVars: rendered.BindVars,
		MemberColumn:           ir.PhysicalPopulationMappingMemberField,
		IdentityPartsColumn:    mappingIdentityPartsColumn(physical),
		ExplicitIdentityColumn: mappingExplicitIdentityColumn(physical),
		RowIdentity:            output.RowIdentity.Clone(),
		Diagnostics:            physicalPlanDiagnostics(physical),
	}, nil
}

func mappingPhysicalPlan(output lower.CompiledRecipeOutput, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	var physical ir.PhysicalPlan
	if output.OptimizedPlan != nil {
		physical = clonePhysicalPlan(*output.OptimizedPlan)
	} else {
		optimized, err := optimize.OptimizePhysicalPlanWithPolicy(output.Plan, policy)
		if err != nil {
			return ir.PhysicalPlan{}, fmt.Errorf("optimize canonical population mapping plan: %w", err)
		}
		physical = optimized
	}
	populationIndex, subplan, err := findPopulationSemijoin(physical)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	memberVariable, err := populationMemberVariable(*subplan)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	subplan.Return = ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Value: &ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}},
	}
	subplan.Sort = &ir.PhysicalValue{Variable: memberVariable, Path: []string{"id"}}
	subplan.Unique = true
	members := ir.PhysicalExpression{
		Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
		Subplan: subplan,
	}
	let := ir.PhysicalOperation{
		Kind:          ir.PhysicalExpressionLetOp,
		Source:        physical.Operations[populationIndex].Source,
		ExpressionLet: &ir.PhysicalExpressionLet{Variable: ir.PopulationMappingMembersVariable, Expression: members},
	}
	filter := ir.PhysicalOperation{
		Kind:   ir.PhysicalFilterOp,
		Source: physical.Operations[populationIndex].Source,
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalComparisonPredicate,
			Comparison: &ir.PhysicalPredicate{
				Operator: "EXISTS", ValueKind: spec.FilterString,
				LeftExpression: &ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull,
					Value: &ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
				},
			},
		}},
	}
	operations := make([]ir.PhysicalOperation, 0, len(physical.Operations)+1)
	operations = append(operations, physical.Operations[:populationIndex]...)
	operations = append(operations, let, filter)
	operations = append(operations, physical.Operations[populationIndex+1:]...)
	physical.Operations = operations
	terminalIndex, identityParts, explicitIdentity, err := finalPopulationMappingIdentity(physical, output.RowIdentity)
	if err != nil {
		return ir.PhysicalPlan{}, err
	}
	physical.Operations[terminalIndex] = ir.PhysicalOperation{
		Kind:   ir.PhysicalPopulationMappingReturnOp,
		Source: physical.Operations[terminalIndex].Source,
		PopulationMappingReturn: &ir.PhysicalPopulationMappingReturn{
			Members:          ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalEmptyOnNull, Value: &ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable}},
			IdentityParts:    identityParts,
			ExplicitIdentity: explicitIdentity,
		},
	}
	if err := physical.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate population mapping physical plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("verify population mapping physical scope: %w", err)
	}
	return physical, nil
}

func findPopulationSemijoin(plan ir.PhysicalPlan) (int, *ir.PhysicalSubplan, error) {
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalFilterOp || operation.Source.SemanticNode != "population" || operation.Filter == nil || operation.Filter.Expression == nil {
			continue
		}
		predicate := operation.Filter.Expression
		if predicate.Kind != ir.PhysicalExistsPredicate || predicate.Exists == nil {
			continue
		}
		subplan := ir.ClonePhysicalSubplan(*predicate.Exists)
		return index, &subplan, nil
	}
	return 0, nil, fmt.Errorf("population mapping requires the canonical population semijoin")
}

func populationMemberVariable(subplan ir.PhysicalSubplan) (string, error) {
	for _, operation := range subplan.Operations {
		if operation.Kind == ir.PhysicalCollectionScanOp && operation.CollectionScan != nil {
			return operation.CollectionScan.Variable, nil
		}
	}
	return "", fmt.Errorf("population mapping semijoin has no member collection scan")
}

func finalPopulationMappingIdentity(plan ir.PhysicalPlan, identity *spec.RowIdentity) (int, []ir.PhysicalPopulationMappingIdentityPart, *ir.PhysicalExpression, error) {
	if identity == nil {
		return 0, nil, nil, fmt.Errorf("population mapping requires a row identity")
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		projections := make(map[string]ir.PhysicalProjection, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			projections[projection.Name] = projection
		}
		parts := make([]ir.PhysicalPopulationMappingIdentityPart, 0, len(identity.Fields))
		for _, field := range identity.Fields {
			projection, projected := projections[field]
			if projected {
				expression, err := physicalProjectionExpression(projection)
				if err != nil {
					return 0, nil, nil, fmt.Errorf("identity field %q: %w", field, err)
				}
				parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: expression})
				continue
			}
			if _, bound := plan.BindVars[field]; bound {
				parts = append(parts, ir.PhysicalPopulationMappingIdentityPart{Name: field, Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: field}}})
				continue
			}
			return 0, nil, nil, fmt.Errorf("identity field %q is not present in the final return projection or compiler bind vars", field)
		}
		var explicit *ir.PhysicalExpression
		if projection, ok := projections["__loom_row_id"]; ok {
			expression, err := physicalProjectionExpression(projection)
			if err != nil {
				return 0, nil, nil, fmt.Errorf("explicit row identity: %w", err)
			}
			explicit = &expression
		}
		return index, parts, explicit, nil
	}
	return 0, nil, nil, fmt.Errorf("population mapping final output has no RETURN operation")
}

func physicalProjectionExpression(projection ir.PhysicalProjection) (ir.PhysicalExpression, error) {
	if projection.Expression != nil {
		return ir.ClonePhysicalExpression(*projection.Expression), nil
	}
	if projection.Value.Variable == "" && projection.Value.BindKey == "" {
		return ir.PhysicalExpression{}, fmt.Errorf("final return projection has no typed expression or value")
	}
	return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &projection.Value}, nil
}

func mappingIdentityPartsColumn(plan ir.PhysicalPlan) string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp && operation.PopulationMappingReturn != nil && operation.PopulationMappingReturn.ExplicitIdentity == nil && len(operation.PopulationMappingReturn.IdentityParts) > 0 {
			return ir.PhysicalPopulationMappingIdentityPartsField
		}
	}
	return ""
}

func mappingExplicitIdentityColumn(plan ir.PhysicalPlan) string {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalPopulationMappingReturnOp && operation.PopulationMappingReturn != nil && operation.PopulationMappingReturn.ExplicitIdentity != nil {
			return ir.PhysicalPopulationMappingExplicitIdentityField
		}
	}
	return ""
}
