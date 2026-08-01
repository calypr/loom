package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

// CompileResolvedGraphQueryWithPolicy compiles the explicit graph frontend
// through the canonical physical optimizer and renderer. The public limit is
// preserved in metadata while the physical graph return fetches limit+1 rows
// for lookahead-based hasMore calculation.
func CompileResolvedGraphQueryWithPolicy(resolved semantic.ResolvedRecipePlan, limit int, policy ir.PhysicalOptimizationPolicy) (CompiledQuery, error) {
	if limit < 1 || limit > 10000 {
		return CompiledQuery{}, fmt.Errorf("graph limit must be between 1 and 10000")
	}
	physical, err := lower.CompileResolvedGraphPlan(resolved, limit, policy)
	if err != nil {
		return CompiledQuery{}, err
	}
	physical, err = optimize.OptimizePhysicalPlanWithPolicy(physical, policy)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("optimize graph physical plan: %w", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("render graph physical plan: %w", err)
	}
	output := resolved.SemanticPlan.Outputs[0]
	bindings := resolved.SemanticPlan.Bindings
	return CompiledQuery{
		Project: bindings.Project, DatasetGeneration: normalizeDatasetGeneration(bindings.DatasetGeneration),
		RootResourceType: output.RootResourceType, AuthResourcePaths: cloneStrings(bindings.AuthResourcePaths),
		PlanMode: "physical", PlanProfile: "generic_fhir_graph_path", TraversalCount: physicalTraversalCount(physical),
		OptimizationRules: recipeOptimizationRules(physical), Query: rendered.Query, BindVars: rendered.BindVars,
		Columns: []string{"paths"}, PublicColumns: []string{"paths"}, Limit: limit,
		PlanDiagnostics: physicalPlanDiagnostics(physical),
	}, nil
}
