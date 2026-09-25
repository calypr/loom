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
	RowIdentityColumn string
}

type PhysicalStageOperationKind string

const (
	PhysicalStageDeriveOp  PhysicalStageOperationKind = "DERIVE"
	PhysicalStageFilterOp  PhysicalStageOperationKind = "FILTER"
	PhysicalStagePivotOp   PhysicalStageOperationKind = "PIVOT"
	PhysicalStageUnpivotOp PhysicalStageOperationKind = "UNPIVOT"
	PhysicalStageGroupOp   PhysicalStageOperationKind = "GROUP"
	PhysicalStageExpandOp  PhysicalStageOperationKind = "EXPAND"
)

type PhysicalStageGroup struct {
	GroupRowsVariable     string
	IdentityVariable      string
	ConstructionIDBindKey string
	Keys                  []PhysicalStageGroupKey
	Aggregates            []PhysicalStageGroupAggregate
}

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
