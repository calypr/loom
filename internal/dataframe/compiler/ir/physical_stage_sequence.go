package ir

import "github.com/calypr/loom/internal/dataframe/unit"

type PhysicalStageSequence struct {
	SourceStageID          string
	SourceRowIdentity     string
	SourceColumns         []PhysicalStageColumn
	Stages                []PhysicalConstructionStage
	FinalStageID          string
	FinalRowIdentity      string
	FinalColumns          []PhysicalStageColumn
	PreviewLimitBindKey   string
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
	ID                 string
	InputStageID       string
	Kind               PhysicalStageOperationKind
	InputRowVariable   string
	OutputRowVariable  string
	InputColumns       []PhysicalStageColumn
	OutputColumns      []PhysicalStageColumn
	InputProjections   []PhysicalProjection
	OutputProjections  []PhysicalProjection
	DerivedLets        []PhysicalOperation
	Filter             *PhysicalFilter
	GroupedPivot       *PhysicalGroupedPivot
	Unpivot            *PhysicalUnpivot
	RowIdentityColumn  string
}

type PhysicalStageOperationKind string

const (
	PhysicalStageDeriveOp  PhysicalStageOperationKind = "DERIVE"
	PhysicalStageFilterOp  PhysicalStageOperationKind = "FILTER"
	PhysicalStagePivotOp   PhysicalStageOperationKind = "PIVOT"
	PhysicalStageUnpivotOp PhysicalStageOperationKind = "UNPIVOT"
)
