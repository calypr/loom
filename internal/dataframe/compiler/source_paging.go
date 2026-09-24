package compiler

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// CompiledSourceQuery returns one source value for each selected root. The
// value is the scoped root payload or one compiler-owned related-record set.
type CompiledSourceQuery struct {
	Variable string
	Query    string
	BindVars map[string]any
}

// CompiledOutputSourcePage separates source acquisition from terminal column
// evaluation. The canonical physical plan remains the source of both stages.
type CompiledOutputSourcePage struct {
	RootKeysQuery    string
	RootKeysBindVars map[string]any
	Sources          []CompiledSourceQuery
	ExecutionQuery   CompiledQuery
}

// CompileRecipeOutputSourcePageWithPolicy builds bounded, independently
// scoped source queries for an evaluator that has already proved it supports
// the terminal plan. sourceSets contains only sets read by terminal values;
// dependency sets are retained automatically by physical dependency pruning.
func CompileRecipeOutputSourcePageWithPolicy(output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings, pageSize int, policy ir.PhysicalOptimizationPolicy, sourceSets []string) (CompiledOutputSourcePage, error) {
	if pageSize < 1 {
		return CompiledOutputSourcePage{}, fmt.Errorf("root page size must be positive")
	}
	physical, err := optimizedOutputPlan(output, policy)
	if err != nil {
		return CompiledOutputSourcePage{}, err
	}
	keysPlan, err := rootKeysPagePlan(physical, pageSize)
	if err != nil {
		return CompiledOutputSourcePage{}, fmt.Errorf("build root-key page: %w", err)
	}
	rowsPlan, err := selectedRootsPlan(physical)
	if err != nil {
		return CompiledOutputSourcePage{}, fmt.Errorf("build selected-root page: %w", err)
	}
	rowsPlan, err = withGenericPhysicalExecutionWindow(rowsPlan, 0)
	if err != nil {
		return CompiledOutputSourcePage{}, fmt.Errorf("apply selected-root execution window: %w", err)
	}
	keys, err := aql.RenderPhysicalPlan(keysPlan)
	if err != nil {
		return CompiledOutputSourcePage{}, fmt.Errorf("render root-key page: %w", err)
	}
	root := rowsPlan.Operations[0].RootScan.Variable
	setDefinitions := make(map[string]bool)
	for _, operation := range rowsPlan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			setDefinitions[operation.Set.Variable] = true
		}
	}
	sources := make([]CompiledSourceQuery, 0, len(sourceSets)+1)
	rootSource, err := renderPhysicalSourcePage(rowsPlan, root, root, []string{"payload"})
	if err != nil {
		return CompiledOutputSourcePage{}, fmt.Errorf("render root source: %w", err)
	}
	sources = append(sources, rootSource)
	seen := make(map[string]bool, len(sourceSets))
	for _, variable := range sourceSets {
		if !setDefinitions[variable] || seen[variable] {
			return CompiledOutputSourcePage{}, fmt.Errorf("invalid or duplicate source set %q", variable)
		}
		seen[variable] = true
		source, sourceErr := renderPhysicalSourcePage(rowsPlan, root, variable, nil)
		if sourceErr != nil {
			return CompiledOutputSourcePage{}, fmt.Errorf("render source set %q: %w", variable, sourceErr)
		}
		sources = append(sources, source)
	}
	columns := publicOutputColumns(output.OutputSchema)
	if len(columns) == 0 {
		columns, _ = physicalProjectionMetadata(physical)
	}
	query := CompiledQuery{
		Project: bindings.Project, DatasetGeneration: normalizeDatasetGeneration(bindings.DatasetGeneration), RootResourceType: output.RootResourceType,
		TranslationVersion: output.TranslationVersion, AuthResourcePaths: cloneStrings(bindings.AuthResourcePaths), PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe",
		TraversalCount: physicalTraversalCount(rowsPlan), RowIdentity: output.RowIdentity.Clone(), OptimizationRules: recipeOptimizationRules(rowsPlan),
		Query: rootSource.Query, BindVars: rootSource.BindVars, Columns: append([]string(nil), output.Columns...), OutputSchema: lower.CloneCompiledOutputSchema(output.OutputSchema),
		PublicColumns: columns, PlanDiagnostics: physicalPlanDiagnostics(rowsPlan),
	}
	return CompiledOutputSourcePage{
		RootKeysQuery: keys.Query, RootKeysBindVars: keys.BindVars,
		Sources: sources, ExecutionQuery: query,
	}, nil
}

func renderPhysicalSourcePage(rowsPlan ir.PhysicalPlan, root, variable string, path []string) (CompiledSourceQuery, error) {
	plan := ir.ClonePhysicalPlan(rowsPlan)
	returnIndex := len(plan.Operations) - 1
	if returnIndex < 0 || plan.Operations[returnIndex].Kind != ir.PhysicalReturnOp {
		return CompiledSourceQuery{}, fmt.Errorf("source page requires a terminal RETURN")
	}
	plan.Operations[returnIndex].Return.Projections = []ir.PhysicalProjection{
		{Name: "_key", Value: ir.PhysicalValue{Variable: root, Path: []string{"_key"}}},
	}
	value := ir.PhysicalValue{Variable: variable, Path: path}
	if variable == root {
		plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections,
			ir.PhysicalProjection{Name: "value", Value: value})
	} else {
		plan.Operations[returnIndex].Return.Projections = append(plan.Operations[returnIndex].Return.Projections,
			ir.PhysicalProjection{Name: "value", Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &value,
			}})
	}
	plan, err := pruneProjectionDependencies(plan, returnIndex)
	if err != nil {
		return CompiledSourceQuery{}, err
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		return CompiledSourceQuery{}, err
	}
	return CompiledSourceQuery{Variable: variable, Query: rendered.Query, BindVars: rendered.BindVars}, nil
}
