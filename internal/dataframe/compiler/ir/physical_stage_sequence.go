package ir

import "github.com/calypr/loom/internal/dataframe/unit"

const PreviewSourceResourceIDColumn = "__loom_source_resource_id"

type PhysicalStageSequence struct {
	SourceStageID       string
	SourceRowIdentity   string
	SourceColumns       []PhysicalStageColumn
	Stages              []PhysicalConstructionStage
	FinalStageID        string
	FinalRowIdentity    string
	FinalColumns        []PhysicalStageColumn
	PreviewLimitBindKey string
	// PreviewSourceWindowByRootID is set only when lowering proves the terminal
	// Pivot group key is the direct scalar root FHIR id and the source has root
	// row identity. Preview compilation may then bound the root scan before the
	// source projection is materialized. Full execution ignores this hint.
	PreviewSourceWindowByRootID bool
	// PreviewTerminalPivotWindow is set only when a preview ends at a
	// nonunique Pivot. Rendering may select complete groups by their final
	// grouped-row identity after COLLECT and before computing Pivot cells.
	PreviewTerminalPivotWindow bool
	// OutputAuthResourcePathBindKey adds the exact bound authorization path to
	// the private prefix result as hidden row metadata. It is populated only by
	// the typed composite Combine boundary.
	OutputAuthResourcePathBindKey string
	CellTraceReturn               *PhysicalCellTraceReturn
	RowLineageReturn              *PhysicalRowLineageReturn
	PopulationMappingReturn       *PhysicalStagePopulationMappingReturn
}

// PhysicalStagePopulationMappingReturn maps the final root-contributor keys
// back to the exact selected member IDs captured in the original source rows.
type PhysicalStagePopulationMappingReturn struct {
	SourceRootKeyColumn        string
	SourceMemberIDsColumn      string
	FinalRootContributorColumn string
	FinalRootContributorsMany  bool
	RowIdentityColumn          string
}

// PhysicalRowLineageReturn selects source records for one final construction
// row. Trace carries exact owner identities for authored RELATED_EXPAND stages;
// Group terminals retain their existing typed path. It is diagnostic metadata
// and never attaches contributors to ordinary preview rows.
type PhysicalRowLineageReturn struct {
	RowIDBindKey        string
	OffsetBindKey       string
	LimitBindKey        string
	FetchLimitBindKey   string
	ResourceType        string
	ResourceIDColumn    string
	OccurrenceKeyColumn string
	Trace               *PhysicalRowLineageTrace
}

// PhysicalRowLineageTrace carries exact compiler-decoded identities for the
// row-changing construction stages that own authored contributor occurrences.
// Stages are ordered from the source toward the final output row.
type PhysicalRowLineageTrace struct {
	RootKeyBindKey string
	Stages         []PhysicalRowLineageStageMatch
}

type PhysicalRowLineageStageMatch struct {
	StageID                  string
	Kind                     PhysicalStageOperationKind
	StageRowIDBindKey        string
	RelatedTerminalIDBindKey string
	RelatedRowKind           string
	// IdentityKeyBindKeys carries decoded keys for a GROUP or PIVOT preimage
	// owner, in the order of that owner's typed key list.
	IdentityKeyBindKeys []string
}

type PhysicalStageColumn struct {
	ID                  string
	Name                string
	Label               string
	Kind                string
	Cardinality         string
	Nullable            bool
	Internal            bool
	Identity            bool
	RelatedRecordAnchor *PhysicalStageRelatedRecordAnchor
	// RootContributorResourceType marks a compiler-owned root identity or
	// deduplicated set of root identities retained through a reshape.
	RootContributorResourceType string
	NormalizedUnit              *unit.UnitIdentity
}

type PhysicalStageRelatedRecordAnchor struct {
	NodeID       string
	ResourceType string
}

type PhysicalConstructionStage struct {
	ID                string
	InputStageID      string
	Kind              PhysicalStageOperationKind
	InputRowVariable  string
	OutputRowVariable string
	InputColumns      []PhysicalStageColumn
	OutputColumns     []PhysicalStageColumn
	InputProjections  []PhysicalProjection
	OutputProjections []PhysicalProjection
	DerivedLets       []PhysicalOperation
	Filter            *PhysicalFilter
	Group             *PhysicalStageGroup
	CodedGroup        *PhysicalStageCodedGroup
	CohortGroup       *PhysicalStageCohortGroup
	Expand            *PhysicalStageExpand
	GroupedPivot      *PhysicalGroupedPivot
	Unpivot           *PhysicalUnpivot
	RelatedSource     *PhysicalStageRelatedSource
	RelatedExpand     *PhysicalStageRelatedExpand
	RelatedField      *PhysicalStageRelatedField
	RowIdentityColumn string
}

type PhysicalStageOperationKind string

const (
	PhysicalStageDeriveOp             PhysicalStageOperationKind = "DERIVE"
	PhysicalStageFilterOp             PhysicalStageOperationKind = "FILTER"
	PhysicalStagePivotOp              PhysicalStageOperationKind = "PIVOT"
	PhysicalStageUnpivotOp            PhysicalStageOperationKind = "UNPIVOT"
	PhysicalStageGroupOp              PhysicalStageOperationKind = "GROUP"
	PhysicalStageCodedGroupOp         PhysicalStageOperationKind = "CODED_GROUP"
	PhysicalStageCohortGroupOp        PhysicalStageOperationKind = "COHORT_GROUP"
	PhysicalStageExpandOp             PhysicalStageOperationKind = "EXPAND"
	PhysicalStageRelatedSourceOp      PhysicalStageOperationKind = "RELATED_SOURCE"
	PhysicalStageRelatedExpandOp      PhysicalStageOperationKind = "RELATED_EXPAND"
	PhysicalStageRelatedEligibilityOp PhysicalStageOperationKind = "RELATED_ELIGIBILITY"
	PhysicalStageRelatedFieldOp       PhysicalStageOperationKind = "RELATED_FIELD"
)

type PhysicalStageGroup struct {
	GroupRowsVariable           string
	IdentityVariable            string
	ConstructionIDBindKey       string
	MissingKeyPolicy            PhysicalStageGroupMissingKeyPolicy
	RootContributorInputColumn  string
	RootContributorInputMany    bool
	RootContributorOutputColumn string
	RootContributorVariable     string
	Keys                        []PhysicalStageGroupKey
	Aggregates                  []PhysicalStageGroupAggregate
	RowValues                   []PhysicalStageRowValue
}

// PhysicalStageCohortGroup inserts a pinned explicit-cohort boundary into an
// ordinary construction sequence. Contributor columns are compiler-proven
// root storage identities retained by the preceding stage.
type PhysicalStageCohortGroup struct {
	Rows                        PhysicalGroupRows
	ContributorInputColumn      string
	ContributorInputMany        bool
	RootContributorOutputColumn string
	RootContributorVariable     string
	PreserveMissingMembers      bool
}

// PhysicalStageRowValue reduces values from contributors retained by a shape
// operation's existing COLLECT.
type PhysicalStageRowValue struct {
	InputColumn string
	InputKind   string
	InputMany   bool
	Output      string
	Policy      string
	Variable    string
}

// PhysicalStageCodedGroup groups root records by the tuple from one generated
// repeated Coding path. It retains no coding ordinal or display value.
type PhysicalStageCodedGroup struct {
	RootCollectionBindKey string
	ConstructionIDBindKey string
	OccurrenceIDBindKey   string
	CodingPathBindKey     string
	ResourceType          string
	SourceIdentityColumn  string
	GroupRowsVariable     string
	// SourceRowsUnique is set only after lowering proves that the direct source
	// scan yields at most one row per root _key. CODED_GROUP uses it to count
	// locally deduplicated tuples with SUM(1), without retaining contributor IDs.
	SourceRowsUnique    bool
	CodingPath          string
	PathSegments        []PhysicalCodedGroupPathSegment
	MissingKeyPolicy    PhysicalStageGroupMissingKeyPolicy
	SystemOutputColumn  string
	VersionOutputColumn string
	CodeOutputColumn    string
	CountOutputColumn   string
	SystemVariable      string
	VersionVariable     string
	CodeVariable        string
	CountVariable       string
	IdentityVariable    string
	RowValues           []PhysicalStageRowValue
	RowValueProjections []PhysicalProjection
}

type PhysicalCodedGroupPathSegment struct {
	Name     string
	Repeated bool
}

type PhysicalStageGroupMissingKeyPolicy string

const (
	PhysicalStageGroupMissingKeyGroup   PhysicalStageGroupMissingKeyPolicy = "GROUP"
	PhysicalStageGroupMissingKeyExclude PhysicalStageGroupMissingKeyPolicy = "EXCLUDE"
	PhysicalStageGroupMissingKeyError   PhysicalStageGroupMissingKeyPolicy = "ERROR"
)

type PhysicalStageGroupKey struct {
	InputColumn  string
	OutputColumn string
	Variable     string
	Kind         string
}

type PhysicalStageGroupAggregate struct {
	Operation   string
	InputColumn string
	Output      string
	Variable    string
	InputKind   string
	OutputKind  string
}

type PhysicalStageExpand struct {
	ItemsVariable         string
	IndexVariable         string
	ItemVariable          string
	IdentityVariable      string
	InputColumn           string
	OutputColumn          string
	OrdinalColumn         string
	InputKind             string
	ConstructionIDBindKey string
	EmptyPolicy           PhysicalUnnestEmptyPolicy
}

type PhysicalStageRelatedSource struct {
	AnchorColumnID string
	// RootContributorColumn identifies compiler-proven root records when the
	// signed row anchor is a shaped identity rather than the root document key.
	RootContributorColumn string
	RootResourceType      string
	OutputColumnID        string
	CandidateID           string
	SourceOccurrenceID    string
	ResourceType          string
	Path                  string
	LogicalType           string
	Form                  string
	ContributorPolicy     string
}

// PhysicalStageRelatedExpand emits one row for each distinct terminal
// resource reached from a retained root key. The terminal _id and parent row
// identity are retained as hidden columns for downstream related-field stages.
type PhysicalStageRelatedExpand struct {
	AnchorColumnID         string
	AnchorKind             string
	AnchorNodeID           string
	AnchorResourceType     string
	RootContributorColumn  string
	RootResourceType       string
	RelatedRecordColumnID  string
	TargetNodeID           string
	TargetResourceType     string
	ParentIdentityColumn   string
	ParentIdentityColumnID string
	TerminalIdentityColumn string
	RelatedRecordsVariable string
	IndexVariable          string
	ItemVariable           string
	IdentityVariable       string
	ConstructionIDBindKey  string
	EmptyPolicy            PhysicalUnnestEmptyPolicy
	RelatedRecords         PhysicalSubplan
	Route                  []PhysicalStageRelatedRouteStep
}

type PhysicalStageRelatedField struct {
	ActiveRecordColumn string
	CandidateID        string
	TargetNodeID       string
	TargetResourceType string
	OutputColumnID     string
	LogicalType        string
	Path               []string
	Nullable           bool
}

type PhysicalStageRelatedRouteStep struct {
	EdgeID           string
	FromNodeID       string
	ToNodeID         string
	FromResourceType string
	ToResourceType   string
	Relationship     string
	StorageDirection string
	MatchMode        string
}
