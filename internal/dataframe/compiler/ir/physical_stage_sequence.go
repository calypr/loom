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
	NormalizedUnit      *unit.UnitIdentity
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
	PhysicalStageExpandOp             PhysicalStageOperationKind = "EXPAND"
	PhysicalStageRelatedSourceOp      PhysicalStageOperationKind = "RELATED_SOURCE"
	PhysicalStageRelatedExpandOp      PhysicalStageOperationKind = "RELATED_EXPAND"
	PhysicalStageRelatedEligibilityOp PhysicalStageOperationKind = "RELATED_ELIGIBILITY"
	PhysicalStageRelatedFieldOp       PhysicalStageOperationKind = "RELATED_FIELD"
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

// PhysicalStageRelatedExpand emits one row for each distinct terminal
// resource reached from a retained root key. The terminal _id and parent row
// identity are retained as hidden columns for downstream related-field stages.
type PhysicalStageRelatedExpand struct {
	AnchorColumnID         string
	AnchorKind             string
	AnchorNodeID           string
	AnchorResourceType     string
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
