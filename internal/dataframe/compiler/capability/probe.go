// Package capability contains compiler-backed capability probes.
//
// A probe is deliberately expressed in schema and semantic terms. It does
// not know about catalog rows, Explorer wire types, or a particular storage
// adapter. A successful probe is an executable proof: the request passed the
// generated schema, semantic validation, physical lowering, optimization,
// and canonical AQL renderer.
package capability

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/optimize"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// Scope is the request provenance carried into every successful proof. The
// same values are placed in the physical plan and consequently in rendered
// bind variables; consumers must not treat a query without this provenance as
// an advertised capability.
type Scope struct {
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
}

// Traversal describes one authoring-direction relationship. The source,
// label, and target are all required so an observed edge cannot be silently
// matched to an ambiguous generated relationship.
type Traversal struct {
	FromResourceType string
	EdgeLabel        string
	ToResourceType   string
	Alias            string
	MatchMode        spec.TraversalMatchMode
}

// Filter describes an optional candidate filter operation. Values are typed
// semantic values, not query fragments. When Values is empty, ProbeCandidate
// supplies a harmless typed sample solely to exercise the compiler path.
type Filter struct {
	Operator   spec.FilterOperator
	Quantifier spec.ArrayQuantifier
	FieldKind  spec.FilterValueKind
	Values     []spec.FilterValue
}

// Chart describes an optional candidate chart/aggregate operation.
type Chart struct {
	Operation recipe.AggregateOperation
}

// RootRequest asks whether one concrete generated resource can be a row root.
type RootRequest struct {
	Scope
	ResourceType string
}

// TraversalRequest asks whether one exact relationship can be lowered and
// rendered from a concrete root. It proves storage direction as well as the
// generated logical relationship.
type TraversalRequest struct {
	Scope
	RootResourceType string
	Traversal
}

// CandidateRequest asks whether a selector and its requested operation can be
// compiled at an occurrence. Route is optional for a zero-hop candidate and
// is a sequence of exact one-hop traversals for a candidate on a related node.
type CandidateRequest struct {
	Scope
	RootResourceType string
	ResourceType     string
	FieldRef         string
	Selector         string
	Route            []Traversal
	Projection       spec.ProjectionMode
	Filter           *Filter
	Chart            *Chart
}

// CostEstimate is intentionally descriptive rather than a hidden limit. A
// caller may install a policy that rejects an expensive finite request while
// still allowing arbitrary route depth when its own budget permits it.
type CostEstimate struct {
	TraversalCount int
	CandidateCount int
}

// CostPolicy is an optional request-scoped safety hook. It is called before
// compilation and receives the context supplied to the probe.
type CostPolicy interface {
	Allow(context.Context, CostEstimate) error
}

type CostPolicyFunc func(context.Context, CostEstimate) error

func (f CostPolicyFunc) Allow(ctx context.Context, estimate CostEstimate) error {
	if f == nil {
		return nil
	}
	return f(ctx, estimate)
}

// Options controls the explicitly authorized physical rewrite and cost
// policy. The default is the same conservative policy used by normal recipe
// compilation.
type Options struct {
	Policy    ir.PhysicalOptimizationPolicy
	Cost      CostPolicy
	CostLimit int
}

func (o Options) normalized() Options {
	if !o.Policy.Enabled && o.Policy.MinimumSavings == 0 && o.Policy.RuleOverrides == nil {
		o.Policy = ir.DefaultPhysicalOptimizationPolicy()
	}
	return o
}

// Rendered is the executable proof and its scope provenance.
type Rendered struct {
	Query             string
	BindVars          map[string]any
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
}

type RootCapability struct {
	ResourceType    string
	CanonicalType   string
	RowGrain        spec.RowGrain
	RowRootEligible bool
	Rendered        Rendered
}

type TraversalCapability struct {
	FromResourceType string
	EdgeLabel        string
	ToResourceType   string
	SchemaDirection  fhirschema.TraversalDirection
	StorageDirection ir.PhysicalTraversalDirection
	Multiplicity     fhirschema.TraversalMultiplicity
	Rendered         Rendered
}

type CandidateCapability struct {
	ResourceType    string
	FieldRef        string
	Selector        string
	FieldKind       fhirschema.FieldKind
	Primitive       fhirschema.PrimitiveKind
	Cardinality     spec.Cardinality
	Repeated        bool
	ProjectionModes []spec.ProjectionMode
	FilterOperators []spec.FilterOperator
	ChartOperations []recipe.AggregateOperation
	Filterable      bool
	Chartable       bool
	Rendered        Rendered
}

type Result struct {
	Root      *RootCapability
	Traversal *TraversalCapability
	Candidate *CandidateCapability
	Rendered  Rendered
}

// ProbeRoot compiles and renders a zero-hop concrete row root.
func ProbeRoot(ctx context.Context, request RootRequest, options ...Options) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	request.ResourceType = strings.TrimSpace(request.ResourceType)
	canonical, ok := fhirschema.ConcreteResourceType(request.ResourceType)
	if !ok {
		return Result{}, fmt.Errorf("resource type %q is not a concrete generated FHIR resource", request.ResourceType)
	}
	rowGrain, ok := spec.InferRowGrain(canonical)
	if !ok {
		return Result{}, fmt.Errorf("resource type %q has no supported row grain", canonical)
	}
	physical, rendered, err := compile(ctx, request.Scope, semantic.SemanticPlan{Version: 1, Project: request.Project, DatasetGeneration: request.DatasetGeneration, AuthResourcePaths: cloneStrings(request.AuthResourcePaths), AuthScopeMode: request.AuthScopeMode, Root: semantic.SemanticNode{Alias: "root", ResourceType: canonical}}, optionsFor(options))
	_ = physical
	if err != nil {
		return Result{}, err
	}
	capability := RootCapability{ResourceType: request.ResourceType, CanonicalType: canonical, RowGrain: rowGrain, RowRootEligible: true, Rendered: rendered}
	return Result{Root: &capability, Rendered: rendered}, nil
}

// ProbeTraversal compiles one exact one-hop route and reports the direction
// selected by the storage-route proof in the shared lowerer.
func ProbeTraversal(ctx context.Context, request TraversalRequest, options ...Options) (Result, error) {
	if strings.TrimSpace(request.Traversal.FromResourceType) == "" {
		request.Traversal.FromResourceType = request.RootResourceType
	}
	if err := validateTraversalRequest(request.RootResourceType, request.Traversal); err != nil {
		return Result{}, err
	}
	rootType, ok := fhirschema.ConcreteResourceType(request.RootResourceType)
	if !ok {
		return Result{}, fmt.Errorf("resource type %q is not a concrete generated FHIR resource", request.RootResourceType)
	}
	toType, _ := fhirschema.ConcreteResourceType(request.ToResourceType)
	child := semantic.SemanticNode{Alias: routeAlias(request.Alias, 1), ResourceType: toType, EdgeLabel: request.EdgeLabel, MatchMode: request.MatchMode}
	plan := semantic.SemanticPlan{Version: 1, Project: request.Project, DatasetGeneration: request.DatasetGeneration, AuthResourcePaths: cloneStrings(request.AuthResourcePaths), AuthScopeMode: request.AuthScopeMode, Root: semantic.SemanticNode{Alias: "root", ResourceType: rootType, Children: []semantic.SemanticNode{child}}}
	physical, rendered, err := compile(ctx, request.Scope, plan, optionsFor(options))
	if err != nil {
		return Result{}, err
	}
	traversal, ok := findTraversal(physical)
	if !ok {
		return Result{}, fmt.Errorf("route %s -[%s]-> %s did not produce a physical traversal", rootType, request.EdgeLabel, request.ToResourceType)
	}
	relationship, found, err := fhirschema.ResolveCompilerTraversal(rootType, request.EdgeLabel, toType)
	if err != nil {
		return Result{}, err
	}
	if !found {
		return Result{}, fmt.Errorf("route %s -[%s]-> %s is not represented by the active generated FHIR schema", rootType, request.EdgeLabel, toType)
	}
	capability := TraversalCapability{FromResourceType: rootType, EdgeLabel: request.EdgeLabel, ToResourceType: toType, SchemaDirection: relationship.Direction, StorageDirection: traversal.Direction, Multiplicity: relationship.Multiplicity, Rendered: rendered}
	return Result{Traversal: &capability, Rendered: rendered}, nil
}

// ProbeCandidate compiles a candidate projection, filter, or chart operation.
// It also probes every operation supported by the shared compiler so the
// returned metadata can be used directly by a capability builder.
func ProbeCandidate(ctx context.Context, request CandidateRequest, options ...Options) (Result, error) {
	optionsValue := optionsFor(options)
	prepared, err := prepareCandidate(request)
	if err != nil {
		return Result{}, err
	}
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	if optionsValue.Cost != nil {
		if err := optionsValue.Cost.Allow(ctx, CostEstimate{TraversalCount: len(request.Route), CandidateCount: 1}); err != nil {
			return Result{}, err
		}
	}
	fieldKind := prepared.fieldKind
	metadata := CandidateCapability{ResourceType: prepared.resourceType, FieldRef: prepared.fieldRef, Selector: prepared.selector.CanonicalPath(), FieldKind: fieldKind, Primitive: prepared.primitive, Cardinality: prepared.cardinality, Repeated: prepared.repeated}
	projectionModes := []spec.ProjectionMode{spec.ProjectionScalar, spec.ProjectionFirst, spec.ProjectionArray, spec.ProjectionDistinctArray}
	for _, mode := range projectionModes {
		if _, _, err := compileCandidate(ctx, prepared, mode, nil, nil, optionsValue); err == nil {
			metadata.ProjectionModes = append(metadata.ProjectionModes, mode)
		}
	}
	if len(metadata.ProjectionModes) == 0 {
		return Result{}, fmt.Errorf("candidate %s.%s has no compiler-supported projection mode", prepared.resourceType, prepared.selector.CanonicalPath())
	}
	for _, operator := range []spec.FilterOperator{spec.FilterEquals, spec.FilterNotEquals, spec.FilterIn, spec.FilterExists, spec.FilterMissing, spec.FilterContains, spec.FilterGreaterThan, spec.FilterGreaterEq, spec.FilterLessThan, spec.FilterLessEq} {
		filter, ok := sampleFilter(prepared, operator)
		if !ok {
			continue
		}
		if _, _, err := compileCandidate(ctx, prepared, defaultProjection(prepared.repeated), &filter, nil, optionsValue); err == nil {
			metadata.FilterOperators = append(metadata.FilterOperators, operator)
		}
	}
	for _, operation := range []recipe.AggregateOperation{recipe.AggregateCount, recipe.AggregateCountDistinct, recipe.AggregateExists, recipe.AggregateDistinctValues, recipe.AggregateMin, recipe.AggregateMax} {
		if _, _, err := compileCandidate(ctx, prepared, defaultProjection(prepared.repeated), nil, &Chart{Operation: operation}, optionsValue); err == nil {
			metadata.ChartOperations = append(metadata.ChartOperations, operation)
		}
	}
	metadata.Filterable = len(metadata.FilterOperators) != 0
	metadata.Chartable = len(metadata.ChartOperations) != 0
	mode := request.Projection
	if mode == "" {
		mode = defaultProjection(prepared.repeated)
	}
	filter := request.Filter
	chart := request.Chart
	if filter != nil && filter.FieldKind == "" {
		filterCopy := *filter
		filterCopy.FieldKind = filterKindForPrimitive(prepared.primitive)
		filter = &filterCopy
	}
	_, rendered, err := compileCandidate(ctx, prepared, mode, filter, chart, optionsValue)
	if err != nil {
		return Result{}, err
	}
	metadata.Rendered = rendered
	return Result{Candidate: &metadata, Rendered: rendered}, nil
}

// Request groups probes for snapshot construction. Unsupported entries are
// returned as diagnostics so a caller can retain audit evidence while only
// publishing successful capabilities.
type Request struct {
	Scope            Scope
	RootResourceType string
	Roots            []string
	Traversals       []Traversal
	Candidates       []CandidateRequest
}

type Diagnostic struct {
	Kind    string
	Message string
}

type Snapshot struct {
	Roots       []RootCapability
	Traversals  []TraversalCapability
	Candidates  []CandidateCapability
	Diagnostics []Diagnostic
}

func ProbeCapabilities(ctx context.Context, request Request, options ...Options) (Snapshot, error) {
	optionsValue := optionsFor(options)
	roots := append([]string(nil), request.Roots...)
	if len(roots) == 0 && strings.TrimSpace(request.RootResourceType) != "" {
		roots = []string{request.RootResourceType}
	}
	if optionsValue.Cost != nil {
		if err := optionsValue.Cost.Allow(ctx, CostEstimate{TraversalCount: len(request.Traversals), CandidateCount: len(request.Candidates)}); err != nil {
			return Snapshot{}, err
		}
	}
	snapshot := Snapshot{}
	for _, root := range roots {
		result, err := ProbeRoot(ctx, RootRequest{Scope: request.Scope, ResourceType: root}, optionsValue)
		if err != nil {
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{Kind: "root", Message: err.Error()})
			continue
		}
		snapshot.Roots = append(snapshot.Roots, *result.Root)
	}
	for _, route := range request.Traversals {
		result, err := ProbeTraversal(ctx, TraversalRequest{Scope: request.Scope, RootResourceType: request.RootResourceType, Traversal: route}, optionsValue)
		if err != nil {
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{Kind: "traversal", Message: err.Error()})
			continue
		}
		snapshot.Traversals = append(snapshot.Traversals, *result.Traversal)
	}
	for _, candidate := range request.Candidates {
		if candidate.Project == "" {
			candidate.Scope = request.Scope
		}
		result, err := ProbeCandidate(ctx, candidate, optionsValue)
		if err != nil {
			snapshot.Diagnostics = append(snapshot.Diagnostics, Diagnostic{Kind: "candidate", Message: err.Error()})
			continue
		}
		snapshot.Candidates = append(snapshot.Candidates, *result.Candidate)
	}
	return snapshot, nil
}

// Probe is the concise entry point for callers constructing a capability
// snapshot. The named ProbeCapabilities form remains useful at call sites
// where the distinction from the individual probes matters.
func Probe(ctx context.Context, request Request, options ...Options) (Snapshot, error) {
	return ProbeCapabilities(ctx, request, options...)
}

// ProbeZeroHop and ProbeOneHop are descriptive aliases for the primitive
// probes, kept small so backend adapters do not need to know the package's
// internal naming choices.
func ProbeZeroHop(ctx context.Context, request RootRequest, options ...Options) (Result, error) {
	return ProbeRoot(ctx, request, options...)
}

func ProbeOneHop(ctx context.Context, request TraversalRequest, options ...Options) (Result, error) {
	return ProbeTraversal(ctx, request, options...)
}

func ProbeSelection(ctx context.Context, request CandidateRequest, options ...Options) (Result, error) {
	return ProbeCandidate(ctx, request, options...)
}

type preparedCandidate struct {
	scope        Scope
	resourceType string
	fieldRef     string
	selector     spec.Selector
	fieldKind    fhirschema.FieldKind
	primitive    fhirschema.PrimitiveKind
	cardinality  spec.Cardinality
	repeated     bool
	root         semantic.SemanticNode
}

func prepareCandidate(request CandidateRequest) (preparedCandidate, error) {
	rootType := strings.TrimSpace(request.RootResourceType)
	if rootType == "" {
		rootType = strings.TrimSpace(request.ResourceType)
	}
	canonicalRoot, ok := fhirschema.ConcreteResourceType(rootType)
	if !ok {
		return preparedCandidate{}, fmt.Errorf("resource type %q is not a concrete generated FHIR resource", rootType)
	}
	resourceType, ok := fhirschema.ConcreteResourceType(request.ResourceType)
	if !ok {
		return preparedCandidate{}, fmt.Errorf("candidate resource type %q is not a concrete generated FHIR resource", request.ResourceType)
	}
	selector, err := spec.ParseSelector(request.Selector)
	if err != nil {
		return preparedCandidate{}, fmt.Errorf("candidate selector: %w", err)
	}
	fieldKind := fhirschema.FieldKindUnknown
	if semantics, found := fhirschema.ResolveFieldSemantics(resourceType, selector.CanonicalPath()); found {
		fieldKind = semantics.Kind
	} else {
		return preparedCandidate{}, fmt.Errorf("candidate selector %q is not in the active FHIR schema for %s", request.Selector, resourceType)
	}
	terminal, found := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !found {
		return preparedCandidate{}, fmt.Errorf("candidate selector %q has no terminal metadata for %s", request.Selector, resourceType)
	}
	repeated, _, err := spec.SelectorCardinality(resourceType, selector)
	if err != nil {
		return preparedCandidate{}, fmt.Errorf("candidate selector %q: %w", request.Selector, err)
	}
	if terminal.Repeated {
		repeated = true
	}
	cardinality := spec.CardinalityOptionalOne
	if repeated {
		cardinality = spec.CardinalityMany
	}
	root := semantic.SemanticNode{Alias: "root", ResourceType: canonicalRoot}
	current := &root
	for index, route := range request.Route {
		if strings.TrimSpace(route.FromResourceType) == "" {
			route.FromResourceType = current.ResourceType
		}
		if err := validateTraversalRequest(current.ResourceType, route); err != nil {
			return preparedCandidate{}, err
		}
		toType, _ := fhirschema.ConcreteResourceType(route.ToResourceType)
		child := semantic.SemanticNode{Alias: routeAlias(route.Alias, index+1), ResourceType: toType, EdgeLabel: route.EdgeLabel, MatchMode: route.MatchMode}
		current.Children = append(current.Children, child)
		current = &current.Children[len(current.Children)-1]
	}
	if current.ResourceType != resourceType {
		return preparedCandidate{}, fmt.Errorf("candidate resource type %q does not match route terminal %q", resourceType, current.ResourceType)
	}
	fieldRef := strings.TrimSpace(request.FieldRef)
	if fieldRef == "" {
		fieldRef = resourceType + "." + selector.CanonicalPath()
	}
	return preparedCandidate{scope: request.Scope, resourceType: resourceType, fieldRef: fieldRef, selector: selector, fieldKind: fieldKind, primitive: terminal.Primitive, cardinality: cardinality, repeated: repeated, root: root}, nil
}

func compileCandidate(ctx context.Context, prepared preparedCandidate, mode spec.ProjectionMode, filter *Filter, chart *Chart, options Options) (ir.PhysicalPlan, Rendered, error) {
	if err := contextErr(ctx); err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	if err := validateCandidateProjection(prepared, mode); err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	field := semantic.SemanticField{Name: "candidate", FieldRef: prepared.fieldRef, Selector: prepared.selector, ValueMode: valueMode(mode), ExprType: expression.Type{Kind: expression.KindObject, Cardinality: expression.OptionalOne}}
	if prepared.primitive != fhirschema.PrimitiveUnknown {
		field.ExprType.Kind = expressionKind(prepared.primitive)
	}
	if prepared.repeated {
		field.ExprType.Cardinality = expression.Many
	}
	root := prepared.root
	target := &root
	for len(target.Children) != 0 {
		target = &target.Children[len(target.Children)-1]
	}
	target.Fields = []semantic.SemanticField{field}
	if filter != nil {
		typed, err := typedFilter(prepared, *filter)
		if err != nil {
			return ir.PhysicalPlan{}, Rendered{}, err
		}
		target.Filters = []spec.TypedFilter{typed}
	}
	if chart != nil {
		if chart.Operation == "" {
			return ir.PhysicalPlan{}, Rendered{}, fmt.Errorf("chart operation is required")
		}
		selector := prepared.selector
		target.Aggregates = []semantic.SemanticAggregate{{Name: "chart", Operation: string(chart.Operation), FieldRef: prepared.fieldRef, Selector: &selector}}
	}
	plan := semantic.SemanticPlan{Version: 1, Root: root}
	physical, rendered, err := compile(ctx, prepared.scope, plan, options)
	return physical, rendered, err
}

func validateCandidateProjection(prepared preparedCandidate, mode spec.ProjectionMode) error {
	if err := spec.ValidateProjection(prepared.cardinality, mode); err != nil {
		return err
	}
	if !prepared.repeated && (mode == spec.ProjectionArray || mode == spec.ProjectionDistinctArray) {
		return fmt.Errorf("%s projection cannot represent scalar candidate %s", mode, prepared.selector.CanonicalPath())
	}
	return nil
}

func compile(ctx context.Context, scope Scope, plan semantic.SemanticPlan, options Options) (ir.PhysicalPlan, Rendered, error) {
	if err := contextErr(ctx); err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	options = options.normalized()
	if options.Cost != nil {
		if err := options.Cost.Allow(ctx, CostEstimate{TraversalCount: countSemanticTraversals(plan.Root)}); err != nil {
			return ir.PhysicalPlan{}, Rendered{}, err
		}
	}
	plan.Project = scope.Project
	plan.DatasetGeneration = scope.DatasetGeneration
	plan.AuthResourcePaths = cloneStrings(scope.AuthResourcePaths)
	plan.AuthScopeMode = scope.AuthScopeMode
	physical, err := lower.BuildGenericPhysicalPlanWithPolicy(plan, options.Policy)
	if err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	optimized, err := optimize.OptimizePhysicalPlanWithPolicy(physical, options.Policy)
	if err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	if err := contextErr(ctx); err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	rendered, err := aql.RenderPhysicalPlan(optimized)
	if err != nil {
		return ir.PhysicalPlan{}, Rendered{}, err
	}
	return optimized, Rendered{Query: rendered.Query, BindVars: rendered.BindVars, Project: scope.Project, DatasetGeneration: scope.DatasetGeneration, AuthResourcePaths: cloneStrings(scope.AuthResourcePaths), AuthScopeMode: scope.AuthScopeMode}, nil
}

func optionsFor(options []Options) Options {
	if len(options) == 0 {
		return Options{}.normalized()
	}
	return options[0].normalized()
}

func validateTraversalRequest(root string, route Traversal) error {
	canonicalRoot, ok := fhirschema.ConcreteResourceType(root)
	if !ok {
		return fmt.Errorf("resource type %q is not a concrete generated FHIR resource", root)
	}
	from, ok := fhirschema.ConcreteResourceType(route.FromResourceType)
	if !ok {
		return fmt.Errorf("traversal source resource type %q is not a concrete generated FHIR resource", route.FromResourceType)
	}
	if from != canonicalRoot {
		return fmt.Errorf("traversal source %q does not match current resource %q", from, canonicalRoot)
	}
	if strings.TrimSpace(route.EdgeLabel) == "" || strings.TrimSpace(route.ToResourceType) == "" {
		return fmt.Errorf("traversal source, edge label, and target are required")
	}
	to, ok := fhirschema.ConcreteResourceType(route.ToResourceType)
	if !ok {
		return fmt.Errorf("traversal target resource type %q is not a concrete generated FHIR resource", route.ToResourceType)
	}
	if _, found := fhirschema.LookupTraversal(from, route.EdgeLabel, to); !found {
		return fmt.Errorf("traversal %s -[%s]-> %s is not represented by the active generated FHIR schema", from, route.EdgeLabel, to)
	}
	if err := route.MatchMode.Validate(); err != nil {
		return err
	}
	return nil
}

func routeAlias(alias string, index int) string {
	if strings.TrimSpace(alias) != "" {
		return strings.TrimSpace(alias)
	}
	return fmt.Sprintf("route_%d", index)
}

func typedFilter(prepared preparedCandidate, filter Filter) (spec.TypedFilter, error) {
	kind := filter.FieldKind
	if kind == "" {
		kind = filterKindForPrimitive(prepared.primitive)
	}
	if kind == "" {
		return spec.TypedFilter{}, fmt.Errorf("candidate %s is not a scalar filter candidate", prepared.selector.CanonicalPath())
	}
	quantifier := filter.Quantifier
	if prepared.repeated && quantifier == "" {
		quantifier = spec.QuantifierAny
	}
	typed := spec.TypedFilter{FieldRef: prepared.fieldRef, Selector: prepared.selector.CanonicalPath(), FieldKind: kind, Repeated: prepared.repeated, Quantifier: quantifier, Operator: filter.Operator, Values: append([]spec.FilterValue(nil), filter.Values...)}
	if len(typed.Values) == 0 && typed.Operator != spec.FilterExists && typed.Operator != spec.FilterMissing {
		sample, ok := sampleFilter(prepared, typed.Operator)
		if !ok {
			return spec.TypedFilter{}, fmt.Errorf("operator %s is not supported for candidate %s", typed.Operator, prepared.selector.CanonicalPath())
		}
		typed.Values = sample.Values
	}
	if err := typed.Validate(); err != nil {
		return spec.TypedFilter{}, err
	}
	if err := spec.ValidateTypedFilterForResource(prepared.resourceType, typed); err != nil {
		return spec.TypedFilter{}, err
	}
	return typed, nil
}

func sampleFilter(prepared preparedCandidate, operator spec.FilterOperator) (Filter, bool) {
	kind := filterKindForPrimitive(prepared.primitive)
	if kind == "" || !spec.OperatorSupportsKind(operator, kind) {
		return Filter{}, false
	}
	filter := Filter{Operator: operator, FieldKind: kind}
	if prepared.repeated {
		filter.Quantifier = spec.QuantifierAny
	}
	if operator == spec.FilterExists || operator == spec.FilterMissing {
		filter.Quantifier = ""
		return filter, true
	}
	value := sampleValue(kind)
	if operator == spec.FilterIn {
		filter.Values = []spec.FilterValue{value}
	} else {
		filter.Values = []spec.FilterValue{value}
	}
	return filter, true
}

func sampleValue(kind spec.FilterValueKind) spec.FilterValue {
	value := spec.FilterValue{Kind: kind}
	switch kind {
	case spec.FilterString:
		v := "probe"
		value.String = &v
	case spec.FilterCode:
		value.Code = &spec.CodeValue{Code: "probe"}
	case spec.FilterBoolean:
		v := true
		value.Boolean = &v
	case spec.FilterInteger:
		v := int64(1)
		value.Integer = &v
	case spec.FilterDecimal:
		v := 1.0
		value.Decimal = &v
	case spec.FilterDate:
		v := "2020-01-01"
		value.Date = &v
	case spec.FilterDateTime:
		v := "2020-01-01T00:00:00Z"
		value.DateTime = &v
	}
	return value
}

func filterKindForPrimitive(primitive fhirschema.PrimitiveKind) spec.FilterValueKind {
	switch primitive {
	case fhirschema.PrimitiveString:
		return spec.FilterString
	case fhirschema.PrimitiveBoolean:
		return spec.FilterBoolean
	case fhirschema.PrimitiveInteger:
		return spec.FilterInteger
	case fhirschema.PrimitiveDecimal:
		return spec.FilterDecimal
	case fhirschema.PrimitiveDate:
		return spec.FilterDate
	case fhirschema.PrimitiveDateTime:
		return spec.FilterDateTime
	default:
		return ""
	}
}

func expressionKind(primitive fhirschema.PrimitiveKind) expression.ValueKind {
	switch primitive {
	case fhirschema.PrimitiveBoolean:
		return expression.KindBoolean
	case fhirschema.PrimitiveInteger:
		return expression.KindInteger
	case fhirschema.PrimitiveDecimal:
		return expression.KindDecimal
	case fhirschema.PrimitiveDate:
		return expression.KindDate
	case fhirschema.PrimitiveDateTime:
		return expression.KindDateTime
	case fhirschema.PrimitiveString:
		return expression.KindString
	default:
		return expression.KindObject
	}
}

func valueMode(mode spec.ProjectionMode) string {
	switch mode {
	case spec.ProjectionFirst:
		return "FIRST"
	case spec.ProjectionArray:
		return "ALL"
	case spec.ProjectionDistinctArray:
		return "DISTINCT"
	default:
		return "AUTO"
	}
}

func defaultProjection(repeated bool) spec.ProjectionMode {
	if repeated {
		return spec.ProjectionFirst
	}
	return spec.ProjectionScalar
}

func findTraversal(plan ir.PhysicalPlan) (ir.PhysicalTraversal, bool) {
	var walk func([]ir.PhysicalOperation) (ir.PhysicalTraversal, bool)
	walk = func(operations []ir.PhysicalOperation) (ir.PhysicalTraversal, bool) {
		for _, operation := range operations {
			if operation.Traversal != nil {
				return *operation.Traversal, true
			}
			if operation.Set != nil {
				if traversal, ok := walk(operation.Set.Subplan.Operations); ok {
					return traversal, true
				}
			}
		}
		return ir.PhysicalTraversal{}, false
	}
	return walk(plan.Operations)
}

func countSemanticTraversals(node semantic.SemanticNode) int {
	total := len(node.Children)
	for _, child := range node.Children {
		total += countSemanticTraversals(child)
	}
	return total
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func cloneStrings(values []string) []string { return append([]string(nil), values...) }
