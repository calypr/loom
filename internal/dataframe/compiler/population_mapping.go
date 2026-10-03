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
	if len(physical.Operations) == 0 || physical.Operations[0].Kind != ir.PhysicalRootScanOp || physical.Operations[0].RootScan == nil || physical.Operations[0].RootScan.Population == nil {
		return ir.PhysicalPlan{}, fmt.Errorf("population mapping requires a membership-driven population root source")
	}
	if physical.StageSequence != nil {
		rootKeyColumn, err := addConstructionPopulationMembersSourceProjection(&physical)
		if err != nil {
			return ir.PhysicalPlan{}, err
		}
		terminal, err := finalConstructionPopulationMappingIdentity(physical, output)
		if err != nil {
			return ir.PhysicalPlan{}, err
		}
		terminal.SourceRootKeyColumn = rootKeyColumn
		terminal.SourceMemberIDsColumn = ir.PhysicalPopulationMappingMembersColumn
		physical.StageSequence.PopulationMappingReturn = terminal
	} else {
		physical.Operations[0].RootScan.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
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
	}
	if err := physical.Validate(); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("validate population mapping physical plan: %w", err)
	}
	if err := ir.ValidateGenericPhysicalPlanScope(physical); err != nil {
		return ir.PhysicalPlan{}, fmt.Errorf("verify population mapping physical scope: %w", err)
	}
	return physical, nil
}

func addConstructionPopulationMembersSourceProjection(plan *ir.PhysicalPlan) (string, error) {
	if plan == nil || plan.StageSequence == nil || len(plan.Operations) == 0 || plan.Operations[0].RootScan == nil || plan.Operations[0].RootScan.Population == nil {
		return "", fmt.Errorf("construction population mapping requires a population root source")
	}
	root := plan.Operations[0].RootScan
	root.Population.CollectMembersVariable = ir.PopulationMappingMembersVariable
	rootKeyFound := false
	for _, column := range plan.StageSequence.SourceColumns {
		if column.Name == "_key" && column.Internal && column.RootContributorResourceType == plan.Source.ResourceType &&
			column.Kind == "string" && column.Cardinality == "required_one" {
			rootKeyFound = true
			break
		}
	}
	if !rootKeyFound {
		return "", fmt.Errorf("construction population mapping source does not retain the exact root storage key")
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != ir.PhysicalPopulationMappingMembersColumn {
				continue
			}
			if !projection.Hidden || projection.Expression != nil || projection.Value.Variable != ir.PopulationMappingMembersVariable || len(projection.Value.Path) != 0 {
				return "", fmt.Errorf("construction population mapping source projection %q is not the collected selected member IDs", projection.Name)
			}
			return "_key", nil
		}
	}
	for index := range plan.Operations {
		operation := &plan.Operations[index]
		if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
			operation.Return.Projections = append(operation.Return.Projections, ir.PhysicalProjection{
				Name: ir.PhysicalPopulationMappingMembersColumn, Hidden: true,
				Value: ir.PhysicalValue{Variable: ir.PopulationMappingMembersVariable},
			})
			return "_key", nil
		}
	}
	return "", fmt.Errorf("construction population mapping source has no terminal source RETURN")
}

func finalConstructionPopulationMappingIdentity(plan ir.PhysicalPlan, output lower.CompiledRecipeOutput) (*ir.PhysicalStagePopulationMappingReturn, error) {
	sequence := plan.StageSequence
	if sequence == nil || output.RowIdentity == nil || len(output.RowIdentity.Fields) != 1 || output.RowIdentity.Fields[0] != sequence.FinalRowIdentity {
		return nil, fmt.Errorf("construction population mapping requires the exact final row identity")
	}
	identityFound := false
	for _, column := range sequence.FinalColumns {
		if column.Name == sequence.FinalRowIdentity && column.Internal && column.Identity {
			identityFound = true
			break
		}
	}
	if !identityFound {
		return nil, fmt.Errorf("construction identity field %q is not present in the final typed stage", sequence.FinalRowIdentity)
	}
	for _, name := range []string{"__loom_root_contributor_keys", "_key"} {
		for _, column := range sequence.FinalColumns {
			if !column.Internal || column.Name != name || column.RootContributorResourceType != output.RootResourceType || column.Kind != "string" {
				continue
			}
			many := column.Cardinality == "many"
			if !many && column.Cardinality != "required_one" && column.Cardinality != "optional_one" {
				continue
			}
			return &ir.PhysicalStagePopulationMappingReturn{
				FinalRootContributorColumn: column.Name, FinalRootContributorsMany: many,
				RowIdentityColumn: sequence.FinalRowIdentity,
			}, nil
		}
	}
	return nil, fmt.Errorf("construction population mapping requires compiler-proven root contributor identities in its final stage")
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
	if plan.StageSequence != nil && plan.StageSequence.PopulationMappingReturn != nil {
		return ir.PhysicalPopulationMappingIdentityPartsField
	}
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
