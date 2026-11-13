package ir

type PhysicalPlan struct {
	Version                     int
	Engine                      PhysicalEngine
	Source                      PhysicalSource
	RootOccurrenceID            string
	BindVars                    map[string]any
	Operations                  []PhysicalOperation
	ClickHouseCombine           *PhysicalClickHouseCombine
	ClickHousePrefix            *PhysicalClickHousePrefix
	StageSequence               *PhysicalStageSequence
	PreviewSourceWindowByRootID bool
	DeferredExpressionLets      []PhysicalOperation
	AppliedRules                []string
	SharedTraversalCount        int
	OptimizationPolicy          PhysicalOptimizationReport
	RequiredMatchReuseCount     int
}

const PopulationMappingMembersVariable = "__loom_population_members_value"
const PhysicalPopulationMappingMembersColumn = "__loom_population_member_ids"

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
	PhysicalRootScanOp                  PhysicalOperationKind = "ROOT_SCAN"
	PhysicalTraversalOp                 PhysicalOperationKind = "TRAVERSAL"
	PhysicalFilterOp                    PhysicalOperationKind = "FILTER"
	PhysicalDerivedLetOp                PhysicalOperationKind = "DERIVED_LET"
	PhysicalExpressionLetOp             PhysicalOperationKind = "EXPRESSION_LET"
	PhysicalSetOp                       PhysicalOperationKind = "SET"
	PhysicalUnnestOp                    PhysicalOperationKind = "UNNEST"
	PhysicalSortOp                      PhysicalOperationKind = "SORT"
	PhysicalLimitOp                     PhysicalOperationKind = "LIMIT"
	PhysicalReturnOp                    PhysicalOperationKind = "RETURN"
	PhysicalPathSeedOp                  PhysicalOperationKind = "PATH_SEED"
	PhysicalPathExtendOp                PhysicalOperationKind = "PATH_EXTEND"
	PhysicalGraphReturnOp               PhysicalOperationKind = "GRAPH_RETURN"
	PhysicalCollectionScanOp            PhysicalOperationKind = "COLLECTION_SCAN"
	PhysicalKeySetLookupOp              PhysicalOperationKind = "KEY_SET_LOOKUP"
	PhysicalDocumentLookupOp            PhysicalOperationKind = "DOCUMENT_LOOKUP"
	PhysicalPopulationMappingReturnOp   PhysicalOperationKind = "POPULATION_MAPPING_RETURN"
	PhysicalCellTraceReturnOp           PhysicalOperationKind = "CELL_TRACE_RETURN"
	PhysicalTableShapeExclusionReturnOp PhysicalOperationKind = "TABLE_SHAPE_EXCLUSION_RETURN"
	PhysicalGroupRowsOp                 PhysicalOperationKind = "GROUP_ROWS"
	PhysicalGroupedPivotOp              PhysicalOperationKind = "GROUPED_PIVOT"
	PhysicalUnpivotOp                   PhysicalOperationKind = "UNPIVOT"
)

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

type PhysicalGroupRows struct {
	RevisionCollectionBindKey         string
	SelectionCollectionBindKey        string
	DefinitionsCollectionBindKey      string
	MembershipsCollectionBindKey      string
	SelectionMembersCollectionBindKey string
	ResourceCollectionBindKey         string
	RevisionIDBindKey                 string
	ProjectBindKey                    string
	ResourceProjectBindKey            string
	DatasetGenerationBindKey          string
	ResourceTypeBindKey               string
	PolicyBindKey                     string
	AuthResourcePathsBindKey          string
	AuthUnrestrictedBindKey           string
	LimitBindKey                      string
	MemberValues                      []PhysicalGroupMemberValue
	CellTrace                         *PhysicalGroupRowsCellTrace
}

type PhysicalGroupRowsCellTrace struct {
	OutputColumn      string
	Cardinality       PhysicalCardinality
	OffsetBindKey     string
	LimitBindKey      string
	FetchLimitBindKey string
}

type PhysicalGroupMemberValue struct {
	Output     string
	Kind       string
	Policy     string
	Expression PhysicalExpression
}

type PhysicalRootScan struct {
	Variable          string
	CollectionBindKey string
	PageCandidateSeed *PhysicalRootPageCandidateSeed
	Population        *PhysicalPopulationRootSource
	CohortSource      *PhysicalCohortRootSource
}

type PhysicalRootPageCandidateSeed struct {
	Subplan PhysicalSubplan
}

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
	ResourceProjectBindKey            string
	DatasetGenerationBindKey          string
	ResourceTypeBindKey               string
	PolicyBindKey                     string
}

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
	SourceTypeBindKey     string
	TargetTypeBindKey     string
	EdgeTargetTypeField   string
	Strategy              PhysicalTraversalStrategy
	EndpointField         string
	EndpointJoinField     string
	EndpointIndexFields   []string
}
