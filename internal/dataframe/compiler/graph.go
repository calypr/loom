package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func CompileResolvedGraphQueryWithPolicy(resolved recipeplan.ResolvedRecipePlan, limit int, policy ir.PhysicalOptimizationPolicy) (model.CompiledQuery, error) {
	if limit < 1 || limit > 10000 {
		return model.CompiledQuery{}, fmt.Errorf("graph limit must be between 1 and 10000")
	}
	physical, err := lower.CompileResolvedGraphPlan(resolved, limit, policy)
	if err != nil {
		return model.CompiledQuery{}, err
	}
	physical, err = optimize.OptimizePhysicalPlanWithPolicy(physical, policy)
	if err != nil {
		return model.CompiledQuery{}, fmt.Errorf("optimize graph physical plan: %w", err)
	}
	rendered, err := aql.Render(physical, aql.Options{})
	if err != nil {
		return model.CompiledQuery{}, fmt.Errorf("render graph physical plan: %w", err)
	}
	output := resolved.SemanticPlan.Outputs[0]
	bindings := resolved.SemanticPlan.Bindings
	return model.CompiledQuery{
		Project: bindings.Project, DatasetGeneration: ir.NormalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType: output.RootResourceType, AuthResourcePaths: ir.CloneStrings(bindings.AuthResourcePaths),
		PlanMode: "physical", PlanProfile: "generic_fhir_graph_path", TraversalCount: physicalTraversalCount(physical),
		OptimizationRules: recipeOptimizationRules(physical), Query: rendered.Query, BindVars: rendered.BindVars,
		Columns: []string{"paths"}, PublicColumns: []string{"paths"}, Limit: limit,
		PlanDiagnostics: ir.PhysicalPlanDiagnostics(physical),
	}, nil
}
