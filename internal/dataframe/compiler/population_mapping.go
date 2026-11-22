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

type CompiledPopulationMappingQuery struct {
	Query                  string
	BindVars               map[string]any
	MemberColumn           string
	IdentityPartsColumn    string
	ExplicitIdentityColumn string
	RowIdentity            *spec.RowIdentity
	Diagnostics            ir.CompilerPlanDiagnostics
}

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
	rowIdentity := output.RowIdentity.Clone()
	if physical.StageSequence != nil {
		_, effectiveIdentity, identityErr := finalConstructionPopulationMappingIdentity(physical, output)
		if identityErr != nil {
			return CompiledPopulationMappingQuery{}, identityErr
		}
		rowIdentity = effectiveIdentity
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
		RowIdentity:            rowIdentity,
		Diagnostics:            physicalPlanDiagnostics(physical),
	}, nil
}

func mappingPhysicalPlan(output lower.CompiledRecipeOutput, policy ir.PhysicalOptimizationPolicy) (ir.PhysicalPlan, error) {
	var physical ir.PhysicalPlan
	if output.OptimizedPlan != nil {
		physical = ir.ClonePhysicalPlan(*output.OptimizedPlan)
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
		terminal, _, err := finalConstructionPopulationMappingIdentity(physical, output)
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
