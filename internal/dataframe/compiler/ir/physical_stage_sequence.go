package ir

import "github.com/calypr/loom/internal/dataframe/unit"

const PreviewSourceResourceIDColumn = "__loom_source_resource_id"

type PhysicalStageSequence struct {
	SourceStageID                 string
	SourceRowIdentity             string
	SourceColumns                 []PhysicalStageColumn
	Stages                        []PhysicalConstructionStage
	FinalStageID                  string
	FinalRowIdentity              string
	FinalColumns                  []PhysicalStageColumn
	PreviewLimitBindKey           string
	PreviewSourceWindowByRootID   bool
	PreviewTerminalPivotWindow    bool
	OutputAuthResourcePathBindKey string
	CellTraceReturn               *PhysicalCellTraceReturn
	RowLineageReturn              *PhysicalRowLineageReturn
	PopulationMappingReturn       *PhysicalStagePopulationMappingReturn
}

type PhysicalStagePopulationMappingReturn struct {
	SourceRootKeyColumn        string
	SourceMemberIDsColumn      string
	FinalRootContributorColumn string
	FinalRootContributorsMany  bool
	RowIdentityColumn          string
}

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
	IdentityKeyBindKeys      []string
}

type PhysicalStageColumn struct {
	ID                          string
	Name                        string
	Label                       string
	Kind                        string
	Cardinality                 string
	Nullable                    bool
	Internal                    bool
	Identity                    bool
	RelatedRecordAnchor         *PhysicalStageRelatedRecordAnchor
	RootContributorResourceType string
	NormalizedUnit              *unit.UnitIdentity
	PresenceCompanionName       string
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
	SemanticKey                 *PhysicalStageGroupSemanticKey
	CodingKey                   *PhysicalStageGroupCodingKey
	RelatedOccurrence           *PhysicalStageGroupRelatedOccurrenceAdapter
	Aggregates                  []PhysicalStageGroupAggregate
	RowValues                   []PhysicalStageRowValue
}

type PhysicalStageCohortGroup struct {
	Rows                        PhysicalGroupRows
	ContributorInputColumn      string
	ContributorInputMany        bool
	RootContributorOutputColumn string
	RootContributorVariable     string
	PreserveMissingMembers      bool
}

type PhysicalStageRowValue struct {
	InputColumn string
	InputKind   string
	InputMany   bool
	FieldSource *PhysicalStageGroupFieldSource
	Output      string
	Policy      string
	Variable    string
}

type PhysicalStageCodedGroup struct {
	RootCollectionBindKey string
	ConstructionIDBindKey string
	OccurrenceIDBindKey   string
	CodingPathBindKey     string
	ResourceType          string
	SourceIdentityColumn  string
	GroupRowsVariable     string
	SourceRowsUnique      bool
	CodingPath            string
	PathSegments          []PhysicalCodedGroupPathSegment
	MissingKeyPolicy      PhysicalStageGroupMissingKeyPolicy
	SystemOutputColumn    string
	VersionOutputColumn   string
	CodeOutputColumn      string
	CountOutputColumn     string
	SystemVariable        string
	VersionVariable       string
	CodeVariable          string
	CountVariable         string
	IdentityVariable      string
	RowValues             []PhysicalStageRowValue
	RowValueProjections   []PhysicalProjection
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
	InputColumn   string
	OutputColumn  string
	Variable      string
	Kind          string
	InputInternal bool
	FieldSource   *PhysicalStageGroupFieldSource
}

type PhysicalStageGroupFieldSource struct {
	Kind PhysicalStageGroupFieldSourceKind
	PhysicalStageGroupCurrentStageFieldSource
	RelatedOccurrence *PhysicalStageGroupRelatedOccurrenceFieldSource
}

type PhysicalStageGroupFieldSourceKind string

const (
	PhysicalStageGroupFieldSourceCurrentStage      PhysicalStageGroupFieldSourceKind = "CURRENT_STAGE"
	PhysicalStageGroupFieldSourceRelatedOccurrence PhysicalStageGroupFieldSourceKind = "RELATED_OCCURRENCE"
)

type PhysicalStageGroupCurrentStageFieldSource struct {
	SourceStageID     string
	SourceShapeID     string
	Role              string
	OccurrenceID      string
	OwnerResourceType string
	ResourceType      string
	CandidateID       string
	CanonicalPath     string
	BoundarySelector  string
	SourceVariable    string
}

type PhysicalStageGroupRelatedOccurrenceFieldSource struct {
	RouteID            string
	SourceStageID      string
	SourceShapeID      string
	AnchorOccurrenceID string
	AnchorNodeID       string
	AnchorResourceType string
	Route              []PhysicalStageGroupRelatedRouteStep
	TargetOccurrenceID string
	TargetNodeID       string
	TargetResourceType string
	ResourceType       string
	CandidateID        string
	CanonicalPath      string
	EmptyPolicy        string
	Multiplicity       string
	CodingSelector     string
	CodingEmptyPolicy  string
	OrdinalSemantics   string
	ProjectionColumn   string
	MembershipColumn   string
}

type PhysicalStageGroupRelatedOccurrenceAdapter struct {
	RouteID                string
	RelatedRecords         PhysicalStageRelatedExpand
	ScalarProjections      []PhysicalStageGroupRelatedProjection
	CodingSelector         string
	CodingArrayColumn      string
	CodingEmptyPolicy      PhysicalUnnestEmptyPolicy
	OrdinalSemantics       string
	CodingUnnest           *PhysicalUnnest
	CodingMembershipColumn string
	OccurrenceKeyColumn    string
	RouteHasItemVariable   string
}

type PhysicalStageGroupRelatedProjection struct {
	InputColumn       string
	ResultField       string
	OwnerResourceType string
	ValueResourceType string
	CandidateID       string
	CanonicalPath     string
}

type PhysicalStageGroupRelatedRouteStep struct {
	StepIndex        int
	FromOccurrenceID string
	FromNodeID       string
	FromResourceType string
	EdgeID           string
	Relationship     string
	StorageDirection string
	MatchMode        string
	ToOccurrenceID   string
	ToNodeID         string
	ToResourceType   string
}

type PhysicalStageGroupCodingKey struct {
	OutputColumn              string
	InputTupleVariable        string
	TupleVariable             string
	LabelVariable             string
	MembershipColumn          string
	SourceStageID             string
	SourceShapeID             string
	SourceOccurrenceKeyColumn string
	OwnerOccurrenceID         string
	OwnerResourceType         string
	CodingPath                string
	SourceUnnest              *PhysicalUnnest
	RelatedSource             *PhysicalStageGroupRelatedOccurrenceFieldSource
}

type PhysicalStageGroupSemanticKey struct {
	OutputColumn          string
	TupleVariable         string
	LabelVariable         string
	SourceRootVariable    string
	SourceIdentityColumn  string
	RootCollectionBindKey string
	ResourceType          string
	CodingPath            string
	PathSegments          []PhysicalCodedGroupPathSegment
	BindingID             string
	RuleVersion           string
	FamilyID              string
	System                string
	Version               string
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
	AnchorColumnID        string
	RootContributorColumn string
	RootResourceType      string
	OutputColumnID        string
	CandidateID           string
	SourceOccurrenceID    string
	ResourceType          string
	Path                  string
	LogicalType           string
	Form                  string
	RowValuePolicy        string
	ContributorPolicy     string
}

type PhysicalStageRelatedExpand struct {
	RouteID                string
	Multiplicity           string
	AnchorColumnID         string
	AnchorKind             string
	AnchorOccurrenceID     string
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
	StepIndex        int
	FromOccurrenceID string
	EdgeID           string
	FromNodeID       string
	ToNodeID         string
	ToOccurrenceID   string
	FromResourceType string
	ToResourceType   string
	Relationship     string
	StorageDirection string
	MatchMode        string
}
