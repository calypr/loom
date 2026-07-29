package ir

// PhysicalPlan is the renderer-independent AQL operation graph produced after
// semantic planning. Operations are ordered because AQL variables have lexical
// scope: an operation may reference only variables introduced before it.
type PhysicalPlan struct {
	Version    int
	Source     PhysicalSource
	BindVars   map[string]any
	Operations []PhysicalOperation
	// DeferredExpressionLets are construction-time shared family bindings.
	// Lowering appends them after all source sets exist and before RETURN;
	// completed plans must have this list empty.
	DeferredExpressionLets []PhysicalOperation
	// AppliedRules records physical rewrites without exposing renderer
	// implementation details to callers.
	AppliedRules         []string
	SharedTraversalCount int
	// OptimizationPolicy records every optional rewrite decision made by the
	// physical optimizer, including conservative rejections.
	OptimizationPolicy PhysicalOptimizationReport
	// RequiredMatchReuseCount records duplicate required EXISTS predicates
	// removed during physical lowering. It is deliberately separate from
	// shared traversal count: required predicates remain pre-window
	// semi-joins, while optional sets are post-window materializations.
	RequiredMatchReuseCount int
}

// PhysicalSource retains semantic provenance through physical optimization so
// explain output and compiler errors can point back to user intent.
type PhysicalSource struct {
	RecipeID      string
	TemplateID    string
	SemanticNode  string
	SemanticField string
	ResourceType  string
	Relationship  string
}

type PhysicalOperationKind string

const (
	PhysicalRootScanOp      PhysicalOperationKind = "ROOT_SCAN"
	PhysicalTraversalOp     PhysicalOperationKind = "TRAVERSAL"
	PhysicalFilterOp        PhysicalOperationKind = "FILTER"
	PhysicalDerivedLetOp    PhysicalOperationKind = "DERIVED_LET"
	PhysicalExpressionLetOp PhysicalOperationKind = "EXPRESSION_LET"
	// PhysicalSetOp materializes a correlated, array-valued subplan. It is the
	// only operation that can introduce a set variable; selectors, aggregates,
	// pivots, and slices consume that variable through typed expressions.
	PhysicalSetOp PhysicalOperationKind = "SET"
	// PhysicalUnnestOp is the cardinality-changing operation used for a
	// correlated UNNEST. It introduces an item binding (and optionally an
	// ordinality binding) for downstream operations.
	PhysicalUnnestOp PhysicalOperationKind = "UNNEST"
	// PhysicalSortOp and PhysicalLimitOp describe the root execution window.
	// They are intentionally typed so preview ordering and bounds cannot be
	// smuggled into an AQL string by a caller.
	PhysicalSortOp   PhysicalOperationKind = "SORT"
	PhysicalLimitOp  PhysicalOperationKind = "LIMIT"
	PhysicalReturnOp PhysicalOperationKind = "RETURN"
)

// PhysicalOperation is a tagged union. Exactly one payload matching Kind must
// be set. Source can be more specific than the plan-level provenance.
type PhysicalOperation struct {
	Kind          PhysicalOperationKind
	Source        PhysicalSource
	RootScan      *PhysicalRootScan
	Traversal     *PhysicalTraversal
	Filter        *PhysicalFilter
	DerivedLet    *PhysicalDerivedLet
	ExpressionLet *PhysicalExpressionLet
	Set           *PhysicalSet
	Unnest        *PhysicalUnnest
	Sort          *PhysicalSort
	Limit         *PhysicalLimit
	Return        *PhysicalReturn
}

type PhysicalRootScan struct {
	Variable          string
	CollectionBindKey string
}

type PhysicalTraversalDirection string

const (
	PhysicalOutbound PhysicalTraversalDirection = "OUTBOUND"
	PhysicalInbound  PhysicalTraversalDirection = "INBOUND"
	PhysicalAny      PhysicalTraversalDirection = "ANY"
)

// PhysicalTraversalStrategy selects the execution shape for a validated
// depth-one relationship. Native graph traversal is the conservative
// fallback. EndpointLookup is only legal when storage-route metadata proves
// the endpoint/discriminator fields and their compound index contract.
type PhysicalTraversalStrategy string

const (
	PhysicalTraversalNative         PhysicalTraversalStrategy = "NATIVE"
	PhysicalTraversalEndpointLookup PhysicalTraversalStrategy = "ENDPOINT_LOOKUP"
)

type PhysicalTraversal struct {
	SourceVariable        string
	TargetVariable        string
	EdgeVariable          string
	Direction             PhysicalTraversalDirection
	EdgeCollectionBindKey string
	EdgeLabelBindKey      string
	TargetTypeBindKey     string
	// EdgeTargetTypeField is a compiler-owned fhir_edge discriminator used
	// alongside TargetTypeBindKey. For a parent-to-child INBOUND route it is
	// from_type; for a proven forward OUTBOUND route it is to_type. The node
	// resourceType check remains independently mandatory.
	EdgeTargetTypeField string
	// Strategy is deliberately typed rather than an AQL fragment. Endpoint
	// fields are supplied by resolveStorageRoute and validated against the
	// direction before the renderer can use them.
	Strategy            PhysicalTraversalStrategy
	EndpointField       string
	EndpointJoinField   string
	EndpointIndexFields []string
}

// PhysicalValue is either a variable/path reference or a bind variable. A
// renderer must never interpret Path segments as AQL source text.
