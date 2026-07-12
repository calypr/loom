package dataframe

import "fmt"

type CompiledQuery struct {
	Project           string
	DatasetGeneration string
	RootResourceType  string
	AuthResourcePaths []string
	PlanMode          string
	PlanProfile       string
	NamedSetCount     int
	FileSummaries     bool
	StudyLookup       bool
	OptimizationRules []string
	// RowIdentity describes the stable resource identity behind each returned
	// row. It is metadata for exporters and recipe consumers; the existing row
	// object keeps its backwards-compatible _key column.
	RowIdentity *RowIdentity
	Query       string
	BindVars    map[string]any
	Columns     []string
	PivotFields []string
	Limit       int
}

func Compile(builder Builder, limit int) (CompiledQuery, error) {
	if !usesLoweredBuilder(builder) {
		return CompiledQuery{}, fmt.Errorf("unsupported dataframe query shape: request was not lowered into the optimized lowered plan")
	}
	// Compile is intentionally safe for callers that receive a pre-lowered
	// builder (for example, persisted recipes or conformance fixtures). The
	// renderer interpolates only a generated resource collection name; validate
	// that boundary here rather than relying on a service caller to have done so.
	if err := validateLoweredBuilder(builder); err != nil {
		return CompiledQuery{}, err
	}
	return compileLowered(builder, limit)
}

// CompileRequest is the public compiler entrypoint for a dataframe request.
// It preserves the validated semantic plan alongside the compatibility
// lowered Builder so generic navigation requests can execute through the typed
// physical renderer. Requests whose selections or shaping are not represented
// by the physical IR retain the established lowered renderer.
func CompileRequest(builder Builder, limit int) (CompiledQuery, error) {
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		return CompiledQuery{}, err
	}
	planned, err := lowerSemanticBuilder(builder, semantic)
	if err != nil {
		return CompiledQuery{}, err
	}
	return compileRequestPlans(semantic, planned, limit)
}

// compileRequestPlans selects the typed execution path only when the semantic
// plan is wholly represented by the frozen generic navigation physical IR.
// The compatibility lowered renderer remains the explicit fallback for every
// richer request; it must never receive a partial physical plan that silently
// drops a user field, filter, pivot, aggregate, slice, or required match.
func compileRequestPlans(semantic SemanticPlan, planned Builder, limit int) (CompiledQuery, error) {
	if !usesLoweredBuilder(planned) {
		return CompiledQuery{}, fmt.Errorf("unsupported dataframe query shape: request was not lowered into the optimized lowered plan")
	}
	if err := validateLoweredBuilder(planned); err != nil {
		return CompiledQuery{}, err
	}
	if planProfile(planned.PlanHint) == "generic_fhir_graph" && genericPhysicalPlanUnavailableReason(semantic.Root) == "" {
		return compileGenericPhysicalExecution(semantic, planned, limit)
	}
	return Compile(planned, limit)
}

func planMode(hint *PlanHint) string {
	if hint == nil || hint.Mode == "" {
		return "unsupported"
	}
	return hint.Mode
}

func planProfile(hint *PlanHint) string {
	if hint == nil {
		return ""
	}
	return hint.Profile
}

func planNamedSetCount(hint *PlanHint) int {
	if hint == nil {
		return 0
	}
	return hint.NamedSetCount
}

func planFileSummaries(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.ClassifiedFileSummaries
}

func planStudyLookup(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.StudyLookup
}

func planRowIdentity(hint *PlanHint) *RowIdentity {
	if hint == nil {
		return nil
	}
	return cloneRowIdentity(hint.RowIdentity)
}

func planAppliedRules(hint *PlanHint) []string {
	if hint == nil || len(hint.AppliedRules) == 0 {
		return nil
	}
	return append([]string(nil), hint.AppliedRules...)
}

type compiler struct {
	builder          Builder
	bindVars         map[string]any
	columns          []string
	pivotFields      []string
	bindCount        int
	pivotExprs       map[string]string
	genericSetRoutes map[string]storageRoute
}
