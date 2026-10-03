package ir

// PhysicalPlan is the renderer-independent backend plan produced after
// semantic planning. AQL operation graphs retain lexical variable scope;
// ClickHouse construction plans use their own closed typed payload.
type PhysicalPlan struct {
	Version    int
	Engine     PhysicalEngine
	Source     PhysicalSource
	BindVars   map[string]any
	Operations []PhysicalOperation
	// ClickHouseCombine is present only for a terminal exact-revision combine
	// plan. It is immutable after lowering and executed by the ClickHouse
	// renderer/stream path, never by the AQL renderer.
	ClickHouseCombine *PhysicalClickHouseCombine
	// ClickHousePrefix is present only when a typed AQL stage sequence feeds one
	// private ClickHouse artifact input at the terminal Combine boundary.
	ClickHousePrefix *PhysicalClickHousePrefix
	// StageSequence composes typed operators over materialized intermediate
	// rows. The ordinary operations form the source projection; stage execution
	// remains part of this physical plan and shares its bind scope and renderer.
	StageSequence *PhysicalStageSequence
	// PreviewSourceWindowByRootID permits the one typed root-id sort used to
	// bound a proven unique-id Pivot sample after its stage sequence is stripped
	// for rendering the underlying source query.
	PreviewSourceWindowByRootID bool
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

const PopulationMappingMembersVariable = "__loom_population_members_value"

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
	PhysicalSortOp           PhysicalOperationKind = "SORT"
	PhysicalLimitOp          PhysicalOperationKind = "LIMIT"
	PhysicalReturnOp         PhysicalOperationKind = "RETURN"
	PhysicalPathSeedOp       PhysicalOperationKind = "PATH_SEED"
	PhysicalPathExtendOp     PhysicalOperationKind = "PATH_EXTEND"
	PhysicalGraphReturnOp    PhysicalOperationKind = "GRAPH_RETURN"
	PhysicalCollectionScanOp PhysicalOperationKind = "COLLECTION_SCAN"
	// PhysicalKeySetLookupOp iterates an exact array of resource _keys and
	// resolves each document without scanning the whole collection.
	PhysicalKeySetLookupOp PhysicalOperationKind = "KEY_SET_LOOKUP"
	// PhysicalDocumentLookupOp reads one compiler-pinned resource collection
	// using an exact active document identity captured from an earlier stage.
	PhysicalDocumentLookupOp PhysicalOperationKind = "DOCUMENT_LOOKUP"
	// PhysicalPopulationMappingReturnOp expands matched members only after the
	// canonical final-row plan has completed. Its witness rows are internal.
	PhysicalPopulationMappingReturnOp PhysicalOperationKind = "POPULATION_MAPPING_RETURN"
	// PhysicalCellTraceReturnOp replaces the public projection terminal for one
	// bounded, receipt-backed explanation request. It evaluates the exact
	// compiled value expression and exposes pre-reduction contributors without
	// changing ordinary dataframe execution.
	PhysicalCellTraceReturnOp PhysicalOperationKind = "CELL_TRACE_RETURN"
	// PhysicalTableShapeExclusionReturnOp replaces a grouped pivot and its
	// publication projection with a bounded diagnostic over the exact pivot
	// input rows.
	PhysicalTableShapeExclusionReturnOp PhysicalOperationKind = "TABLE_SHAPE_EXCLUSION_RETURN"
	// PhysicalGroupRowsOp emits one row per definition in a pinned immutable group revision.
	PhysicalGroupRowsOp PhysicalOperationKind = "GROUP_ROWS"
	// PhysicalGroupedPivotOp groups finalized output rows by authored scalar
	// keys and projects the frozen category list into named columns.
	PhysicalGroupedPivotOp PhysicalOperationKind = "GROUPED_PIVOT"
	// PhysicalUnpivotOp replaces selected scalar columns with one typed key/value
	// row per selected input column.
	PhysicalUnpivotOp PhysicalOperationKind = "UNPIVOT"
)

// PhysicalOperation is a tagged union. Exactly one payload matching Kind must
// be set. Source can be more specific than the plan-level provenance.
type PhysicalOperation struct {
	Kind                      PhysicalOperationKind
	Source                    PhysicalSource
	RootScan                  *PhysicalRootScan
	Traversal                 *PhysicalTraversal
	Filter                    *PhysicalFilter
	DerivedLet                *PhysicalDerivedLet
	ExpressionLet             *PhysicalExpressionLet
	Set                       *PhysicalSet
	Unnest                    *PhysicalUnnest
	Sort                      *PhysicalSort
	Limit                     *PhysicalLimit
	Return                    *PhysicalReturn
	PathSeed                  *PhysicalPathSeed
	PathExtend                *PhysicalPathExtend
	GraphReturn               *PhysicalGraphReturn
	CollectionScan            *PhysicalCollectionScan
	KeySetLookup              *PhysicalKeySetLookup
	DocumentLookup            *PhysicalDocumentLookup
	PopulationMappingReturn   *PhysicalPopulationMappingReturn
	CellTraceReturn           *PhysicalCellTraceReturn
	TableShapeExclusionReturn *PhysicalTableShapeExclusionReturn
	GroupRows                 *PhysicalGroupRows
	GroupedPivot              *PhysicalGroupedPivot
	Unpivot                   *PhysicalUnpivot
}

// PhysicalGroupRows is a typed source and terminal for grouped dataframe rows.
// Membership references remain independent of resource lookup so missing
// source documents do not erase group membership.
type PhysicalGroupRows struct {
	RevisionCollectionBindKey         string
	SelectionCollectionBindKey        string
	DefinitionsCollectionBindKey      string
	MembershipsCollectionBindKey      string
	SelectionMembersCollectionBindKey string
	ResourceCollectionBindKey         string
	RevisionIDBindKey                 string
	ProjectBindKey                    string
	DatasetGenerationBindKey          string
	ResourceTypeBindKey               string
	PolicyBindKey                     string
	AuthResourcePathsBindKey          string
	AuthUnrestrictedBindKey           string
	LimitBindKey                      string
	MemberValues                      []PhysicalGroupMemberValue
}

// PhysicalGroupMemberValue reduces a checked field over the authorized members
// of one pinned cohort, preserving the cohort's existing row identity.
type PhysicalGroupMemberValue struct {
	Output     string
	Kind       string
	Policy     string
	Expression PhysicalExpression
}

type PhysicalRootScan struct {
	Variable          string
	CollectionBindKey string
	// Population replaces the full root collection scan with an indexed scan
	// that starts from persisted selection members. The renderer deduplicates
	// RootKey before restoring Variable from CollectionBindKey.
	Population *PhysicalPopulationRootSource
	// CohortSource replaces a full scan with pinned assigned and selection
	// members when lowering proves the input rows retain exact root identity.
	CohortSource *PhysicalCohortRootSource
}

// PhysicalCohortRootSource bounds a typed cohort's source rows to the union
// of explicit memberships and pinned selection members. The latter preserves
// unassigned semantics; the former preserves accepted assignments even if a
// stored membership is outside the source selection.
type PhysicalCohortRootSource struct {
	CohortStageID                     string
	CohortInputStageID                string
	RootIdentityColumn                string
	RootResourceType                  string
	RevisionCollectionBindKey         string
	SelectionCollectionBindKey        string
	SelectionMembersCollectionBindKey string
	MembershipsCollectionBindKey      string
	RevisionIDBindKey                 string
	ProjectBindKey                    string
	DatasetGenerationBindKey          string
	ResourceTypeBindKey               string
	PolicyBindKey                     string
}

// PhysicalPopulationRootSource describes a membership-driven root scan. The
// member collection has a different trust boundary from FHIR resource
// collections, so its filters are kept separate from the scoped resource
// operations. CollectMembersVariable is empty for ordinary dataframe reads;
// population-mapping compilation sets it to retain member witnesses per root.
type PhysicalPopulationRootSource struct {
	MemberScan             PhysicalCollectionScan
	MemberFilters          []PhysicalFilter
	ResourceOperations     []PhysicalOperation
	RootKey                PhysicalValue
	MemberID               PhysicalValue
	CollectMembersVariable string
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
	// SourceTypeBindKey retains the typed FHIR resource at the source endpoint.
	// Ordinary forward traversal does not need it, but reverse semijoin plans
	// use the opposite edge discriminator to preserve the exact typed route.
	SourceTypeBindKey string
	TargetTypeBindKey string
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
