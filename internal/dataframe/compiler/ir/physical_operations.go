package ir

import (
	"regexp"

	"github.com/calypr/loom/internal/dataframe/spec"
)

const PhysicalCodedPivotSourcePayloadColumn = "__loom_coded_pivot_source_payload"

type PhysicalSet struct {
	Variable          string
	Kind              PhysicalSetKind
	Subplan           PhysicalSubplan
	Unique            bool
	Output            *PhysicalSetOutput
	Projection        *PhysicalSetProjection
	SourceSetVariable string
	ItemVariable      string
	SortByKey         bool
	Reduction         *PhysicalSetReduction
	Prepared          *PhysicalPreparedSet
}

type PhysicalSetKind string

const (
	PhysicalNodeSetKind     PhysicalSetKind = "NODE_SET"
	PhysicalNodePathSetKind PhysicalSetKind = "NODE_PATH"
)

type PhysicalPathNode struct {
	Alias        string
	ResourceType string
	Value        PhysicalValue
}

type PhysicalPathRelationship struct {
	Alias            string
	LabelBindKey     string
	FromResourceType string
	ToResourceType   string
}

type PhysicalPathSeed struct {
	Variable   string
	Node       PhysicalPathNode
	RouteOrder int
}

type PhysicalPathExtend struct {
	Variable       string
	SourceVariable string
	SourcePath     []string
	Traversal      PhysicalTraversal
	Node           PhysicalPathNode
	Relationship   PhysicalPathRelationship
	MatchMode      string
	RouteOrder     int
	Scope          []PhysicalOperation
}

type PhysicalGraphReturn struct {
	PathSets     []string
	LimitBindKey string
}

type PhysicalPopulationMappingReturn struct {
	Members          PhysicalExpression
	IdentityParts    []PhysicalPopulationMappingIdentityPart
	ExplicitIdentity *PhysicalExpression
}

type PhysicalPopulationMappingIdentityPart struct {
	Name       string
	Expression PhysicalExpression
}

type PhysicalCellTraceReturn struct {
	Value             PhysicalExpression
	Contribution      *PhysicalCellTraceContribution
	Reshape           *PhysicalCellTraceReshape
	Construction      *PhysicalCellTraceConstruction
	IdentityParts     []PhysicalPopulationMappingIdentityPart
	ExplicitIdentity  *PhysicalExpression
	OffsetBindKey     string
	LimitBindKey      string
	FetchLimitBindKey string
	OmissionCode      string
	RowExists         bool
}

type PhysicalCellTraceConstruction struct {
	FinalStageID      string
	RowIdentityColumn string
	RowIdentityFields []string
	OutputColumnID    string
	OutputColumn      string
	ProducerStageID   string
	ConstructionID    string
	Operation         string
	Inputs            []PhysicalCellTraceConstructionInput
	RelatedSource     *PhysicalCellTraceRelatedSource
	OmissionCode      string
}

type PhysicalCellTraceConstructionInput struct {
	StageID          string
	ColumnID         string
	Column           string
	FinalValueColumn string
}

type PhysicalCellTraceRelatedSource struct {
	InputRowVariable string
	AnchorColumn     string
	ResourceType     string
	Subplan          PhysicalSubplan
}

type PhysicalTableShapeExclusionReturn struct {
	Pivot             PhysicalGroupedPivot
	OffsetBindKey     string
	LimitBindKey      string
	FetchLimitBindKey string
}

type PhysicalCellTraceReshape struct {
	OutputVariable string
	Sources        []PhysicalCellTraceReshapeSource
	OmissionCode   string
}

type PhysicalCellTraceReshapeSourceKind string

const (
	PhysicalCellTracePivotGroupKey PhysicalCellTraceReshapeSourceKind = "PIVOT_GROUP_KEY"
	PhysicalCellTracePivotCell     PhysicalCellTraceReshapeSourceKind = "PIVOT_CELL"
	PhysicalCellTraceUnpivotValue  PhysicalCellTraceReshapeSourceKind = "UNPIVOT_VALUE"
	PhysicalCellTraceUnpivotField  PhysicalCellTraceReshapeSourceKind = "UNPIVOT_PASSTHROUGH"
)

type PhysicalCellTraceReshapeSource struct {
	Kind                  PhysicalCellTraceReshapeSourceKind
	GroupRowsVariable     string
	InputRowVariable      string
	OneInputRowPerGroup   bool
	SourceColumn          string
	SourcePresenceField   string
	CategoryColumn        string
	CategoryPresenceField string
	CategoryType          string
	Category              *PhysicalGroupedPivotCategory
	ValueColumn           string
	ValueColumnType       string
	DuplicatePolicy       string
	OmissionCode          string
}

const (
	PhysicalCellTraceSourceDocumentField      = "__loom_trace_source_document"
	PhysicalCellTraceSourcePresencePrefix     = "__loom_trace_source_presence_"
	PhysicalCellTraceSourcePresenceField      = "__loom_trace_source_presence"
	PhysicalCellTraceSourceSupportedField     = "__loom_trace_source_supported"
	PhysicalCellTracePassSourceDocumentField  = "__loom_trace_pass_source_document"
	PhysicalCellTracePassSourcePresenceField  = "__loom_trace_pass_source_presence"
	PhysicalCellTracePassSourceSupportedField = "__loom_trace_pass_source_supported"
	PhysicalCellTraceGeneratedKeyOmission     = "TABLE_SHAPE_GENERATED_KEY_HAS_NO_SOURCE_CELL"
	PhysicalCellTraceSourceOmission           = "TABLE_SHAPE_SOURCE_LINEAGE_UNAVAILABLE"
	PhysicalCellTraceDerivedOmission          = "TABLE_SHAPE_DERIVED_LINEAGE_UNAVAILABLE"
	PhysicalCellTraceDerivedLiteralOnly       = "TABLE_SHAPE_DERIVED_HAS_NO_SOURCE_CELL"
)

type PhysicalCellTraceContribution struct {
	SetVariable string
	ValueField  string
	Lossy       bool
}

const (
	PhysicalCellTraceValueField            = "__loom_trace_value"
	PhysicalCellTraceContributionsField    = "__loom_trace_contributions"
	PhysicalCellTraceIdentityPartsField    = "__loom_trace_identity_parts"
	PhysicalCellTraceExplicitIdentityField = "__loom_trace_explicit_identity"
	PhysicalCellTraceStatusField           = "__loom_trace_status"
	PhysicalCellTraceHasMoreField          = "__loom_trace_has_more"
	PhysicalCellTraceOmissionField         = "__loom_trace_omission"
)

const (
	PhysicalTableShapeExclusionResourceTypeField         = "__loom_table_shape_exclusion_resource_type"
	PhysicalTableShapeExclusionResourceIDField           = "__loom_table_shape_exclusion_resource_id"
	PhysicalTableShapeExclusionIdentityStatusField       = "__loom_table_shape_exclusion_identity_status"
	PhysicalTableShapeExclusionCategoryValueField        = "__loom_table_shape_exclusion_category_value"
	PhysicalTableShapeExclusionCategoryPresentField      = "__loom_table_shape_exclusion_category_present"
	PhysicalTableShapeExclusionCategoryTypeField         = "__loom_table_shape_exclusion_category_type"
	PhysicalTableShapeExclusionOutputRowIDField          = "__loom_table_shape_exclusion_output_row_id"
	PhysicalTableShapeExclusionReasonField               = "__loom_table_shape_exclusion_reason"
	PhysicalTableShapeExclusionOmissionField             = "__loom_table_shape_exclusion_omission"
	PhysicalTableShapeExclusionReasonUnlistedCategory    = "UNLISTED_CATEGORY"
	PhysicalTableShapeExclusionIdentityExact             = "EXACT"
	PhysicalTableShapeExclusionIdentityUnavailable       = "UNAVAILABLE"
	PhysicalTableShapeExclusionSourceIdentityUnavailable = "TABLE_SHAPE_SOURCE_IDENTITY_UNAVAILABLE"
)

const (
	PhysicalPopulationMappingMemberField           = "__loom_population_member"
	PhysicalPopulationMappingIdentityPartsField    = "__loom_population_identity_parts"
	PhysicalPopulationMappingExplicitIdentityField = "__loom_population_explicit_identity"
)

type PhysicalUnnestEmptyPolicy string

const (
	PhysicalUnnestError          PhysicalUnnestEmptyPolicy = "ERROR"
	PhysicalUnnestExclude        PhysicalUnnestEmptyPolicy = "EXCLUDE"
	PhysicalUnnestPreserveParent PhysicalUnnestEmptyPolicy = "PRESERVE_PARENT"
)

type PhysicalUnnestOwner struct {
	OccurrenceID  string
	ResourceType  string
	RootVariable  string
	OwnerVariable string
	Route         []PhysicalUnnestRouteStep
}

type PhysicalUnnestRouteStep struct {
	OccurrenceID string
	Traversal    PhysicalTraversal
	Scope        []PhysicalOperation
}

type PhysicalUnnestAncestor struct {
	StepIndex int
	Variable  string
}

type PhysicalUnnest struct {
	Owner           PhysicalUnnestOwner
	OutputVariable  string
	Ordinality      string
	HasItemVariable string
	Expression      PhysicalExpression
	Ancestors       []PhysicalUnnestAncestor
	EmptyPolicy     PhysicalUnnestEmptyPolicy
}

func PhysicalUnnestSortKeys(unnest PhysicalUnnest) []PhysicalValue {
	keys := []PhysicalValue{{Variable: unnest.Owner.RootVariable, Path: []string{"_key"}}}
	for _, step := range unnest.Owner.Route {
		if step.Traversal.EdgeVariable != "" {
			keys = append(keys, PhysicalValue{Variable: step.Traversal.EdgeVariable, Path: []string{"_key"}})
		}
	}
	if unnest.Owner.OwnerVariable != unnest.Owner.RootVariable {
		keys = append(keys, PhysicalValue{Variable: unnest.Owner.OwnerVariable, Path: []string{"_key"}})
	}
	keys = append(keys, PhysicalValue{Variable: unnest.HasItemVariable})
	if unnest.Ordinality != "" {
		keys = append(keys, PhysicalValue{Variable: unnest.Ordinality})
	}
	return keys
}

type PhysicalSetProjection struct {
	Fields []PhysicalSetProjectionField
}

type PhysicalSetProjectionField struct {
	Name          string
	ResourceType  string
	Selector      spec.Selector
	ExecutionMode PhysicalSelectorExecutionMode
	Demand        PhysicalSelectorValueDemand
}

type PhysicalSelectorValueDemand string

const (
	PhysicalSelectorAllValues  PhysicalSelectorValueDemand = ""
	PhysicalSelectorFirstValue PhysicalSelectorValueDemand = "FIRST_ONLY"
)

type PhysicalSetReduction struct {
	Variable          string
	SourceSetVariable string
	Fields            []PhysicalSetReductionField
}

type PhysicalSetReductionField struct {
	Name        string
	SourceField string
	Mode        PhysicalSetReductionMode
}

type PhysicalSetReductionMode string

const (
	PhysicalSetReductionFirst    PhysicalSetReductionMode = "FIRST"
	PhysicalSetReductionAll      PhysicalSetReductionMode = "ALL"
	PhysicalSetReductionDistinct PhysicalSetReductionMode = "DISTINCT"
)

type PhysicalSetOutputField string

const (
	PhysicalSetGraphIDField      PhysicalSetOutputField = "_id"
	PhysicalSetKeyField          PhysicalSetOutputField = "_key"
	PhysicalSetIDField           PhysicalSetOutputField = "id"
	PhysicalSetResourceTypeField PhysicalSetOutputField = "resourceType"
	PhysicalSetPayloadField      PhysicalSetOutputField = "payload"
)

type PhysicalSetOutput struct {
	Fields []PhysicalSetOutputField
}

type PhysicalSubplan struct {
	Captures   []string
	Operations []PhysicalOperation
	Return     PhysicalExpression
	Sort       *PhysicalValue
	Unique     bool
	DistinctBy *PhysicalValue
}

type PhysicalCollectionScan struct {
	Variable          string
	CollectionBindKey string
}

type PhysicalKeySetLookup struct {
	Variable          string
	KeyVariable       string
	CollectionBindKey string
	Keys              PhysicalValue
}

type PhysicalDocumentLookup struct {
	Variable          string
	CollectionBindKey string
	ExactID           PhysicalValue
}

type PhysicalPredicate struct {
	Operator       string
	Left           PhysicalValue
	LeftExpression *PhysicalExpression
	Right          *PhysicalValue
	Quantifier     spec.ArrayQuantifier
	ValueKind      spec.FilterValueKind
	Correlation    *PhysicalCorrelation
}

type PhysicalPredicateKind string

const (
	PhysicalComparisonPredicate PhysicalPredicateKind = "COMPARISON"
	PhysicalAllPredicate        PhysicalPredicateKind = "ALL"
	PhysicalAnyPredicate        PhysicalPredicateKind = "ANY"
	PhysicalNotPredicate        PhysicalPredicateKind = "NOT"
	PhysicalExistsPredicate     PhysicalPredicateKind = "EXISTS"
)

type PhysicalPredicateExpression struct {
	Kind       PhysicalPredicateKind
	Comparison *PhysicalPredicate
	Children   []PhysicalPredicateExpression
	Exists     *PhysicalSubplan
}

type PhysicalFilter struct {
	Predicate  PhysicalPredicate
	Expression *PhysicalPredicateExpression
}

type PhysicalDerivedLet struct {
	Variable string
	Operator string
	Inputs   []PhysicalValue
}

type PhysicalExpressionLet struct {
	Variable   string
	Expression PhysicalExpression
}

type PhysicalSort struct {
	Keys []PhysicalValue
}

type PhysicalLimit struct {
	BindKey string
}

type PhysicalProjection struct {
	Name           string
	Hidden         bool
	Value          PhysicalValue
	Expression     *PhysicalExpression
	Presence       *PhysicalProjectionPresence
	PresenceOutput bool
}

type PhysicalProjectionPresence struct {
	Source PhysicalValue
	Paths  [][]string
}

type PhysicalReturn struct {
	Projections []PhysicalProjection
}

type PhysicalGroupedPivot struct {
	ConstructionID              string
	InputRowVariable            string
	GroupRowsVariable           string
	OutputRowVariable           string
	OneInputRowPerGroup         bool
	InputProjections            []PhysicalProjection
	GroupKeys                   []PhysicalGroupedPivotKey
	CategoryColumn              string
	CategoryPresenceColumn      string
	CategoryPresence            *PhysicalProjectionPresence
	CategoryPresenceFromInput   bool
	CategoryType                string
	ValueColumn                 string
	ValueType                   string
	Categories                  []PhysicalGroupedPivotCategory
	RowValues                   []PhysicalStageRowValue
	RootContributorInputColumn  string
	RootContributorInputMany    bool
	RootContributorOutputColumn string
	RootContributorVariable     string
	CodedCorrelation            *PhysicalCorrelation
	CodedCategories             []PhysicalGroupedCodedPivotCategory
	CodedSourceVariable         string
	ConstructionIDBindKey       string
	UnlistedEvidenceColumn      string
	DuplicatePolicy             string
	MissingCellPolicy           string
	UnlistedCategoryPolicy      string
}

type PhysicalGroupedPivotKey struct {
	Column   string
	Output   string
	Variable string
	Kind     string
	Hidden   bool
}

type PhysicalGroupedPivotCategory struct {
	Output       string
	MatchKind    PhysicalPivotCategoryMatchKind
	ValueBindKey string
	ValueKind    string
}

type PhysicalGroupedCodedPivotCategory struct {
	Output        string
	SystemBindKey string
	CodeBindKey   string
}

type PhysicalPivotCategoryMatchKind string

const (
	PhysicalPivotCategoryValueMatch   PhysicalPivotCategoryMatchKind = "VALUE"
	PhysicalPivotCategoryNullMatch    PhysicalPivotCategoryMatchKind = "NULL"
	PhysicalPivotCategoryMissingMatch PhysicalPivotCategoryMatchKind = "MISSING"
)

type PhysicalUnpivot struct {
	ConstructionID        string
	InputRowVariable      string
	SlotVariable          string
	OutputRowVariable     string
	InputProjections      []PhysicalProjection
	Inputs                []PhysicalUnpivotInput
	PreservedOutputs      []PhysicalUnpivotOutput
	KeyOutput             string
	KeyType               string
	ValueOutput           string
	ValueType             string
	IdentityParts         []PhysicalUnpivotIdentityPart
	ConstructionIDBindKey string
	NullRowPolicy         string
}

type PhysicalUnpivotOutput struct {
	InputColumn  string
	OutputColumn string
}

type PhysicalUnpivotInput struct {
	Column     string
	KeyBindKey string
	KeyKind    string
	ValueKind  string
}

type PhysicalUnpivotIdentityPart struct {
	Name  string
	Value PhysicalValue
}

var (
	physicalVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	physicalBindKeyPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	physicalPathPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)
