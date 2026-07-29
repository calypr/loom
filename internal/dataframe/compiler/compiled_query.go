package compiler

// CompiledQuery is the executable result of the canonical recipe compiler.
// It contains parameterized AQL plus stable metadata for execution, export,
// and diagnostics; it does not expose a transport-specific request builder.
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
	RowIdentity       *RowIdentity
	Query             string
	BindVars          map[string]any
	Columns           []string
	// OutputSchema is the compiler-owned ordered schema for the finalized
	// physical RETURN projections. PublicColumns is the transport-safe view;
	// Columns remains the legacy execution metadata used by generic runtime
	// callers and may include the stable physical row identity.
	OutputSchema    []CompiledOutputColumn
	PublicColumns   []string
	PivotFields     []string
	Limit           int
	PlanDiagnostics CompilerPlanDiagnostics
}
