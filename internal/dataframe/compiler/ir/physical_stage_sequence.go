package ir

import "github.com/calypr/loom/internal/dataframe/unit"

type PhysicalStageSequence struct {
	SourceStageID       string
	SourceRowIdentity   string
	SourceColumns       []PhysicalStageColumn
	Stages              []PhysicalConstructionStage
	FinalStageID        string
	FinalRowIdentity    string
	FinalColumns        []PhysicalStageColumn
	PreviewLimitBindKey string
	// OutputAuthResourcePathBindKey adds the exact bound authorization path to
	// the private prefix result as hidden row metadata. It is populated only by
	// the typed composite Combine boundary.
	OutputAuthResourcePathBindKey string
	CellTraceReturn               *PhysicalCellTraceReturn
}

type PhysicalStageColumn struct {
	ID             string
	Name           string
	Label          string
	Kind           string
	Cardinality    string
	Nullable       bool
	Internal       bool
	Identity       bool
	NormalizedUnit *unit.UnitIdentity
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
	Expand            *PhysicalStageExpand
	GroupedPivot      *PhysicalGroupedPivot
	Unpivot           *PhysicalUnpivot
	RelatedSource     *PhysicalStageRelatedSource
	RowIdentityColumn string
}

type PhysicalStageOperationKind string

const (
	PhysicalStageDeriveOp        PhysicalStageOperationKind = "DERIVE"
	PhysicalStageFilterOp        PhysicalStageOperationKind = "FILTER"
	PhysicalStagePivotOp         PhysicalStageOperationKind = "PIVOT"
	PhysicalStageUnpivotOp       PhysicalStageOperationKind = "UNPIVOT"
	PhysicalStageGroupOp         PhysicalStageOperationKind = "GROUP"
	PhysicalStageExpandOp        PhysicalStageOperationKind = "EXPAND"
	PhysicalStageRelatedSourceOp PhysicalStageOperationKind = "RELATED_SOURCE"
)

type PhysicalStageGroup struct {
	GroupRowsVariable     string
	IdentityVariable      string
	ConstructionIDBindKey string
	MissingKeyPolicy      PhysicalStageGroupMissingKeyPolicy
	Keys                  []PhysicalStageGroupKey
	Aggregates            []PhysicalStageGroupAggregate
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
	AnchorColumnID     string
	OutputColumnID     string
	CandidateID        string
	SourceOccurrenceID string
	ResourceType       string
	Path               string
	LogicalType        string
	Form               string
	ContributorPolicy  string
}
