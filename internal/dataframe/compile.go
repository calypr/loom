package dataframe

import "fmt"

type CompiledQuery struct {
	Project           string
	DatasetGeneration string
	RootResourceType  string
	AuthResourcePaths []string
	PlanMode          string
	PlanProfile       string
	TraversalCount    int
	FileSummaries     bool
	StudyLookup       bool
	OptimizationRules []string
	// RowIdentity describes the stable resource identity behind each returned
	// row. It is metadata for exporters and recipe consumers; the existing row
	// object keeps its backwards-compatible _key column.
	RowIdentity     *RowIdentity
	Query           string
	BindVars        map[string]any
	Columns         []string
	PivotFields     []string
	Limit           int
	PlanDiagnostics CompilerPlanDiagnostics
}

// CompileRequest is the sole production compiler entrypoint for a dataframe
// request. It validates semantic meaning, lowers to typed physical operators,
// applies semantics-preserving physical rewrites, and renders parameterized
// AQL. Unsupported shapes fail explicitly.
func CompileRequest(builder Builder, limit int) (CompiledQuery, error) {
	return CompileRequestWithPolicy(builder, limit, DefaultPhysicalOptimizationPolicy())
}

// CompileRequestWithPolicy is the explicit ablation entrypoint used by
// compiler tests and benchmark tooling. Normal service execution uses
// CompileRequest and the production default policy.
func CompileRequestWithPolicy(builder Builder, limit int, policy PhysicalOptimizationPolicy) (CompiledQuery, error) {
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		return CompiledQuery{}, err
	}
	// The physical route owns navigation-only requests directly from semantic
	// meaning.
	physical, err := BuildPhysicalPlanWithPolicy(semantic, policy)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("unsupported physical dataframe shape: %w", err)
	}
	physical, err = OptimizePhysicalPlanWithPolicy(physical, policy)
	if err != nil {
		return CompiledQuery{}, fmt.Errorf("optimize physical plan: %w", err)
	}
	return compilePhysicalExecution(physical, semantic, limit)
}
