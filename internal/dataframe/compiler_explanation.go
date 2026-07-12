package dataframe

import "strings"

// CompilerExplanationVersion is incremented only when the public structure of
// CompilerExplanation changes incompatibly.
const CompilerExplanationVersion = 1

// CompilerExplanation is a pure, renderer-independent inspection result for a
// dataframe Builder. It captures the validated semantic request, normalized
// selection behavior, and safe metadata from the lowered and compiled plans.
//
// It deliberately does not expose a lowered Builder, rendered AQL, or bind
// variables. Those are renderer internals, and older lowered forms can contain
// raw expressions. The identifiers here are FHIR/schema semantics, stable
// compiler rule IDs, or compiler-generated physical-plan variables.
type CompilerExplanation struct {
	Version             int
	SemanticPlan        SemanticPlan
	Selections          []SelectionSemanticSpec
	Lowered             LoweredPlanMetadata
	Compiled            CompiledQueryMetadata
	GenericPhysicalPlan GenericPhysicalPlanExplanation
}

// LoweredPlanMetadata is the safe, stable portion of a lowered Builder. It
// intentionally omits named-set and derived-field internals so explain callers
// cannot couple to a renderer representation while that representation evolves.
type LoweredPlanMetadata struct {
	PlanMode          string
	PlanProfile       string
	NamedSetCount     int
	FileSummaries     bool
	StudyLookup       bool
	RowIdentity       *RowIdentity
	OptimizationRules []string
}

// CompiledQueryMetadata describes the public result shape of a successfully
// compiled request without exposing rendered AQL or bind values.
type CompiledQueryMetadata struct {
	DatasetGeneration string
	RootResourceType  string
	PlanMode          string
	PlanProfile       string
	NamedSetCount     int
	FileSummaries     bool
	StudyLookup       bool
	RowIdentity       *RowIdentity
	OptimizationRules []string
	Columns           []string
	PivotFields       []string
	Limit             int
}

// GenericPhysicalPlanReason records why the current navigation-only generic
// physical-plan builder cannot faithfully represent a semantic request.
// Empty means the generic plan is available.
type GenericPhysicalPlanReason string

const (
	GenericPhysicalPlanReasonSelections        GenericPhysicalPlanReason = "SELECTIONS_NOT_SUPPORTED"
	GenericPhysicalPlanReasonFilters           GenericPhysicalPlanReason = "FILTERS_NOT_SUPPORTED"
	GenericPhysicalPlanReasonShaping           GenericPhysicalPlanReason = "SHAPING_NOT_SUPPORTED"
	GenericPhysicalPlanReasonRelationshipMatch GenericPhysicalPlanReason = "RELATIONSHIP_MATCH_NOT_SUPPORTED"
)

// GenericPhysicalPlanExplanation makes partial physical support explicit. A
// nil Plan always accompanies Available=false; callers must not infer a
// navigation plan that would silently omit fields, filters, or shaping.
type GenericPhysicalPlanExplanation struct {
	Available bool
	Reason    GenericPhysicalPlanReason
	Plan      *PhysicalPlan
}

// ExplainCompilerRequest validates, lowers, and compiles a Builder without
// performing dataset I/O. It returns an all-or-nothing inspection artifact:
// semantic, lowering, and compilation errors return the zero explanation.
//
// The generic physical-plan skeleton is intentionally optional. Requests that
// compile through the current renderer but include features the skeleton cannot
// yet represent still return successful compiled metadata with an explicit
// unavailable reason rather than a misleading partial PhysicalPlan.
func ExplainCompilerRequest(builder Builder, limit int) (CompilerExplanation, error) {
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		return CompilerExplanation{}, err
	}
	selections, err := NormalizeSelectionPlan(semantic)
	if err != nil {
		return CompilerExplanation{}, err
	}
	lowered, err := lowerSemanticBuilder(builder, semantic)
	if err != nil {
		return CompilerExplanation{}, err
	}
	compiled, err := compileRequestPlans(semantic, lowered, limit)
	if err != nil {
		return CompilerExplanation{}, err
	}
	physical, err := explainGenericPhysicalPlan(semantic, limit)
	if err != nil {
		return CompilerExplanation{}, err
	}

	return CompilerExplanation{
		Version:             CompilerExplanationVersion,
		SemanticPlan:        cloneCompilerSemanticPlan(semantic),
		Selections:          cloneCompilerSelectionSemantics(selections),
		Lowered:             loweredPlanMetadata(lowered),
		Compiled:            compiledQueryMetadata(compiled),
		GenericPhysicalPlan: physical,
	}, nil
}

func loweredPlanMetadata(lowered Builder) LoweredPlanMetadata {
	return LoweredPlanMetadata{
		PlanMode:          planMode(lowered.PlanHint),
		PlanProfile:       planProfile(lowered.PlanHint),
		NamedSetCount:     planNamedSetCount(lowered.PlanHint),
		FileSummaries:     planFileSummaries(lowered.PlanHint),
		StudyLookup:       planStudyLookup(lowered.PlanHint),
		RowIdentity:       cloneRowIdentity(planRowIdentity(lowered.PlanHint)),
		OptimizationRules: normalizeCompilerRules(planAppliedRules(lowered.PlanHint)),
	}
}

func compiledQueryMetadata(compiled CompiledQuery) CompiledQueryMetadata {
	return CompiledQueryMetadata{
		DatasetGeneration: compiled.DatasetGeneration,
		RootResourceType:  compiled.RootResourceType,
		PlanMode:          compiled.PlanMode,
		PlanProfile:       compiled.PlanProfile,
		NamedSetCount:     compiled.NamedSetCount,
		FileSummaries:     compiled.FileSummaries,
		StudyLookup:       compiled.StudyLookup,
		RowIdentity:       cloneRowIdentity(compiled.RowIdentity),
		OptimizationRules: normalizeCompilerRules(compiled.OptimizationRules),
		Columns:           cloneStrings(compiled.Columns),
		PivotFields:       cloneStrings(compiled.PivotFields),
		Limit:             compiled.Limit,
	}
}

func normalizeCompilerRules(rules []string) []string {
	if len(rules) == 0 {
		return nil
	}
	trimmed := make([]string, 0, len(rules))
	for _, rule := range rules {
		if rule = strings.TrimSpace(rule); rule != "" {
			trimmed = append(trimmed, rule)
		}
	}
	if len(trimmed) == 0 {
		return nil
	}
	return sortedUniqueStrings(trimmed)
}

func explainGenericPhysicalPlan(semantic SemanticPlan, limit int) (GenericPhysicalPlanExplanation, error) {
	if reason := genericPhysicalPlanUnavailableReason(semantic.Root); reason != "" {
		return GenericPhysicalPlanExplanation{Reason: reason}, nil
	}
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		return GenericPhysicalPlanExplanation{}, err
	}
	plan, err = withGenericPhysicalExecutionWindow(plan, limit)
	if err != nil {
		return GenericPhysicalPlanExplanation{}, err
	}
	planCopy := cloneCompilerPhysicalPlan(plan)
	return GenericPhysicalPlanExplanation{Available: true, Plan: &planCopy}, nil
}

func genericPhysicalPlanUnavailableReason(node SemanticNode) GenericPhysicalPlanReason {
	if node.MatchMode.required() {
		return GenericPhysicalPlanReasonRelationshipMatch
	}
	if len(node.Fields) != 0 {
		return GenericPhysicalPlanReasonSelections
	}
	if len(node.Filters) != 0 {
		return GenericPhysicalPlanReasonFilters
	}
	if len(node.Pivots) != 0 || len(node.Aggregates) != 0 || len(node.Slices) != 0 {
		return GenericPhysicalPlanReasonShaping
	}
	for _, child := range node.Children {
		if reason := genericPhysicalPlanUnavailableReason(child); reason != "" {
			return reason
		}
	}
	return ""
}

func cloneCompilerSemanticPlan(plan SemanticPlan) SemanticPlan {
	copy := plan
	copy.AuthResourcePaths = cloneStrings(plan.AuthResourcePaths)
	copy.RowIdentity = cloneRowIdentity(plan.RowIdentity)
	copy.Root = cloneCompilerSemanticNode(plan.Root)
	return copy
}

func cloneCompilerSemanticNode(node SemanticNode) SemanticNode {
	copy := node
	copy.Fields = make([]SemanticField, len(node.Fields))
	for index, field := range node.Fields {
		copy.Fields[index] = cloneCompilerSemanticField(field)
	}
	copy.Filters = cloneCompilerTypedFilters(node.Filters)
	copy.Pivots = make([]SemanticPivot, len(node.Pivots))
	for index, pivot := range node.Pivots {
		copy.Pivots[index] = cloneCompilerSemanticPivot(pivot)
	}
	copy.Aggregates = make([]SemanticAggregate, len(node.Aggregates))
	for index, aggregate := range node.Aggregates {
		copy.Aggregates[index] = cloneCompilerSemanticAggregate(aggregate)
	}
	copy.Slices = make([]SemanticSlice, len(node.Slices))
	for index, slice := range node.Slices {
		copy.Slices[index] = cloneCompilerSemanticSlice(slice)
	}
	copy.Children = make([]SemanticNode, len(node.Children))
	for index, child := range node.Children {
		copy.Children[index] = cloneCompilerSemanticNode(child)
	}
	return copy
}

func cloneCompilerSemanticField(field SemanticField) SemanticField {
	copy := field
	copy.Selector = cloneCompilerSelector(field.Selector)
	copy.Fallbacks = make([]Selector, len(field.Fallbacks))
	for index, fallback := range field.Fallbacks {
		copy.Fallbacks[index] = cloneCompilerSelector(fallback)
	}
	return copy
}

func cloneCompilerSemanticPivot(pivot SemanticPivot) SemanticPivot {
	copy := pivot
	copy.ColumnSelector = cloneCompilerSelector(pivot.ColumnSelector)
	copy.ValueSelector = cloneCompilerSelector(pivot.ValueSelector)
	copy.Columns = cloneStrings(pivot.Columns)
	return copy
}

func cloneCompilerSemanticAggregate(aggregate SemanticAggregate) SemanticAggregate {
	copy := aggregate
	copy.Selector = cloneCompilerSelectorPointer(aggregate.Selector)
	copy.Predicate = cloneCompilerSelectorPointer(aggregate.Predicate)
	return copy
}

func cloneCompilerSemanticSlice(slice SemanticSlice) SemanticSlice {
	copy := slice
	copy.Predicate = cloneCompilerSelectorPointer(slice.Predicate)
	copy.Fields = make([]SemanticField, len(slice.Fields))
	for index, field := range slice.Fields {
		copy.Fields[index] = cloneCompilerSemanticField(field)
	}
	return copy
}

func cloneCompilerSelectionSemantics(in []SelectionSemanticSpec) []SelectionSemanticSpec {
	if in == nil {
		return nil
	}
	out := make([]SelectionSemanticSpec, len(in))
	for index, selection := range in {
		copy := selection
		copy.Selector = cloneCompilerSelector(selection.Selector)
		copy.Fallbacks = make([]Selector, len(selection.Fallbacks))
		for fallbackIndex, fallback := range selection.Fallbacks {
			copy.Fallbacks[fallbackIndex] = cloneCompilerSelector(fallback)
		}
		copy.RepeatedPaths = cloneStrings(selection.RepeatedPaths)
		out[index] = copy
	}
	return out
}

func cloneCompilerSelectorPointer(selector *Selector) *Selector {
	if selector == nil {
		return nil
	}
	copy := cloneCompilerSelector(*selector)
	return &copy
}

func cloneCompilerSelector(selector Selector) Selector {
	copy := selector
	copy.Steps = make([]SelectorStep, len(selector.Steps))
	for index, step := range selector.Steps {
		stepCopy := step
		if step.Index != nil {
			indexCopy := *step.Index
			stepCopy.Index = &indexCopy
		}
		copy.Steps[index] = stepCopy
	}
	if selector.Filter != nil {
		filterCopy := *selector.Filter
		copy.Filter = &filterCopy
	}
	return copy
}

func cloneCompilerTypedFilters(filters []TypedFilter) []TypedFilter {
	if filters == nil {
		return nil
	}
	out := make([]TypedFilter, len(filters))
	for index, filter := range filters {
		copy := filter
		copy.Values = make([]FilterValue, len(filter.Values))
		for valueIndex, value := range filter.Values {
			copy.Values[valueIndex] = cloneCompilerFilterValue(value)
		}
		out[index] = copy
	}
	return out
}

func cloneCompilerFilterValue(value FilterValue) FilterValue {
	copy := value
	copy.String = cloneCompilerStringPointer(value.String)
	if value.Code != nil {
		codeCopy := *value.Code
		copy.Code = &codeCopy
	}
	copy.Boolean = cloneCompilerBoolPointer(value.Boolean)
	copy.Integer = cloneCompilerInt64Pointer(value.Integer)
	copy.Decimal = cloneCompilerFloat64Pointer(value.Decimal)
	copy.Date = cloneCompilerStringPointer(value.Date)
	copy.DateTime = cloneCompilerStringPointer(value.DateTime)
	return copy
}

func cloneCompilerStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCompilerBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCompilerInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCompilerFloat64Pointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneCompilerPhysicalPlan(plan PhysicalPlan) PhysicalPlan {
	copy := plan
	copy.BindVars = cloneCompilerBindVars(plan.BindVars)
	copy.Operations = make([]PhysicalOperation, len(plan.Operations))
	for index, operation := range plan.Operations {
		copy.Operations[index] = cloneCompilerPhysicalOperation(operation)
	}
	return copy
}

func cloneCompilerBindVars(bindVars map[string]any) map[string]any {
	if bindVars == nil {
		return nil
	}
	copy := make(map[string]any, len(bindVars))
	for key, value := range bindVars {
		copy[key] = cloneCompilerBindValue(value)
	}
	return copy
}

func cloneCompilerBindValue(value any) any {
	switch value := value.(type) {
	case []string:
		return cloneStrings(value)
	case []any:
		copy := make([]any, len(value))
		for index, item := range value {
			copy[index] = cloneCompilerBindValue(item)
		}
		return copy
	case map[string]any:
		return cloneCompilerBindVars(value)
	case map[string]string:
		copy := make(map[string]string, len(value))
		for key, item := range value {
			copy[key] = item
		}
		return copy
	default:
		return value
	}
}

func cloneCompilerPhysicalOperation(operation PhysicalOperation) PhysicalOperation {
	copy := operation
	if operation.RootScan != nil {
		rootScanCopy := *operation.RootScan
		copy.RootScan = &rootScanCopy
	}
	if operation.Traversal != nil {
		traversalCopy := *operation.Traversal
		copy.Traversal = &traversalCopy
	}
	if operation.Filter != nil {
		filterCopy := *operation.Filter
		filterCopy.Predicate = cloneCompilerPhysicalPredicate(operation.Filter.Predicate)
		copy.Filter = &filterCopy
	}
	if operation.DerivedLet != nil {
		derivedCopy := *operation.DerivedLet
		derivedCopy.Inputs = make([]PhysicalValue, len(operation.DerivedLet.Inputs))
		for index, input := range operation.DerivedLet.Inputs {
			derivedCopy.Inputs[index] = cloneCompilerPhysicalValue(input)
		}
		copy.DerivedLet = &derivedCopy
	}
	if operation.Sort != nil {
		sortCopy := *operation.Sort
		sortCopy.Value = cloneCompilerPhysicalValue(operation.Sort.Value)
		copy.Sort = &sortCopy
	}
	if operation.Limit != nil {
		limitCopy := *operation.Limit
		copy.Limit = &limitCopy
	}
	if operation.Return != nil {
		returnCopy := *operation.Return
		returnCopy.Projections = make([]PhysicalProjection, len(operation.Return.Projections))
		for index, projection := range operation.Return.Projections {
			projectionCopy := projection
			projectionCopy.Value = cloneCompilerPhysicalValue(projection.Value)
			returnCopy.Projections[index] = projectionCopy
		}
		copy.Return = &returnCopy
	}
	return copy
}

func cloneCompilerPhysicalPredicate(predicate PhysicalPredicate) PhysicalPredicate {
	copy := predicate
	copy.Left = cloneCompilerPhysicalValue(predicate.Left)
	if predicate.Right != nil {
		rightCopy := cloneCompilerPhysicalValue(*predicate.Right)
		copy.Right = &rightCopy
	}
	return copy
}

func cloneCompilerPhysicalValue(value PhysicalValue) PhysicalValue {
	copy := value
	copy.Path = cloneStrings(value.Path)
	return copy
}
