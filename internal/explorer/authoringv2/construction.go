package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
)

const ConstructionVersion = 1

type ConstructionInputKind string

const (
	ConstructionInputSourceProjection ConstructionInputKind = "SOURCE_PROJECTION"
	ConstructionInputStepOutput       ConstructionInputKind = "STEP_OUTPUT"
	ConstructionInputTableRevision    ConstructionInputKind = "TABLE_REVISION"
	ConstructionInputWorkspaceOutput  ConstructionInputKind = "WORKSPACE_OUTPUT"
)

type ConstructionOperationKind string

const (
	ConstructionOperationPivot              ConstructionOperationKind = "PIVOT"
	ConstructionOperationDerive             ConstructionOperationKind = "DERIVE"
	ConstructionOperationFilter             ConstructionOperationKind = "FILTER"
	ConstructionOperationUnpivot            ConstructionOperationKind = "UNPIVOT"
	ConstructionOperationGroup              ConstructionOperationKind = "GROUP"
	ConstructionOperationCodedGroup         ConstructionOperationKind = "CODED_GROUP"
	ConstructionOperationCodedPivot         ConstructionOperationKind = "CODED_PIVOT"
	ConstructionOperationExpand             ConstructionOperationKind = "EXPAND"
	ConstructionOperationCombine            ConstructionOperationKind = "COMBINE"
	ConstructionOperationRelatedSource      ConstructionOperationKind = "RELATED_SOURCE"
	ConstructionOperationRelatedExpand      ConstructionOperationKind = "RELATED_EXPAND"
	ConstructionOperationRelatedEligibility ConstructionOperationKind = "RELATED_ELIGIBILITY"
	ConstructionOperationRelatedField       ConstructionOperationKind = "RELATED_FIELD"
)

// Construction stores the ordered, durable operations applied after the
// document's source projection. A zero-step construction is the identity plan
// over that source projection. A terminal Combine is a standalone plan over
// exact published table revisions. Version identifies this operation
// contract, independently of the workspace's older V2 semantics version.
type Construction struct {
	Version           int                            `json:"version"`
	Steps             []ConstructionStep             `json:"steps"`
	SourceProjections []ConstructionSourceProjection `json:"sourceProjections,omitempty"`
}

// ConstructionSourceProjection is a compiler-authorized source field needed
// by a construction operation but excluded from the document's public columns.
type ConstructionSourceProjection struct {
	ColumnID     string `json:"columnId"`
	OwnerStepID  string `json:"ownerStepId,omitempty"`
	OccurrenceID string `json:"occurrenceId"`
	FieldPath    string `json:"fieldPath"`
	FHIRType     string `json:"fhirType"`
	LogicalType  string `json:"logicalType"`
	Label        string `json:"label"`
}

// MarshalJSON keeps an empty construction as an explicit empty sequence at
// persistence and API boundaries. A nil slice can arrive from older drafts
// that encoded "steps": null, but clients consume this field as an array.
func (c Construction) MarshalJSON() ([]byte, error) {
	type wire Construction
	if c.Steps == nil {
		c.Steps = []ConstructionStep{}
	}
	return json.Marshal(wire(c))
}

// ConstructionStep is one saved analytical operation. Its Inputs identify
// the exact stage or immutable table revision it consumes. Outputs declare
// the complete resulting stage schema; IDs remain stable when names or labels
// change.
type ConstructionStep struct {
	ID          string                 `json:"id"`
	OwnerStepID string                 `json:"ownerStepId,omitempty"`
	Inputs      []ConstructionInputRef `json:"inputs"`
	Operation   ConstructionOperation  `json:"operation"`
	Outputs     []StageColumn          `json:"outputs"`
	RowValues   []ConstructionRowValue `json:"rowValues,omitempty"`
}

type ConstructionRowValuePolicy string

const (
	ConstructionRowValueAll ConstructionRowValuePolicy = "ALL"
	ConstructionRowValueOne ConstructionRowValuePolicy = "ONE"
)

// ConstructionRowValue populates a shaped row from the same records that
// contributed to its grouping or pivot. It does not alter row membership.
type ConstructionRowValue struct {
	InputColumnID  string                        `json:"inputColumnId,omitempty"`
	FieldSource    *ConstructionGroupFieldSource `json:"fieldSource,omitempty"`
	OutputColumnID string                        `json:"outputColumnId"`
	Policy         ConstructionRowValuePolicy    `json:"policy"`
}

func (value *ConstructionRowValue) UnmarshalJSON(raw []byte) error {
	var wire struct {
		InputColumnID  *string                       `json:"inputColumnId,omitempty"`
		FieldSource    *ConstructionGroupFieldSource `json:"fieldSource,omitempty"`
		OutputColumnID string                        `json:"outputColumnId"`
		Policy         ConstructionRowValuePolicy    `json:"policy"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	if (wire.InputColumnID != nil) == (wire.FieldSource != nil) {
		return fmt.Errorf("row value must contain exactly one of inputColumnId or fieldSource")
	}
	*value = ConstructionRowValue{
		OutputColumnID: wire.OutputColumnID, FieldSource: wire.FieldSource, Policy: wire.Policy,
	}
	if wire.InputColumnID != nil {
		value.InputColumnID = *wire.InputColumnID
	}
	return nil
}

// ConstructionInputRef is a closed source-stage, prior-stage, sibling-output,
// or immutable table-revision reference. A table input always names a
// concrete revision; floating references to a table's current head are not
// supported.
type ConstructionInputRef struct {
	Kind       ConstructionInputKind `json:"kind"`
	StepID     string                `json:"stepId,omitempty"`
	TableID    string                `json:"tableId,omitempty"`
	RevisionID string                `json:"revisionId,omitempty"`
	OutputID   string                `json:"outputId,omitempty"`
}

func (r *ConstructionInputRef) UnmarshalJSON(raw []byte) error {
	type wire ConstructionInputRef
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*r = ConstructionInputRef(value)
	return nil
}

func (r ConstructionInputRef) Validate() error {
	if string(r.Kind) != strings.TrimSpace(string(r.Kind)) {
		return fmt.Errorf("input kind must not contain surrounding whitespace")
	}
	switch r.Kind {
	case ConstructionInputSourceProjection:
		if r.StepID != "" || r.TableID != "" || r.RevisionID != "" || r.OutputID != "" {
			return fmt.Errorf("SOURCE_PROJECTION input does not accept step or table fields")
		}
	case ConstructionInputStepOutput:
		if !requiredID(r.StepID) || r.TableID != "" || r.RevisionID != "" || r.OutputID != "" {
			return fmt.Errorf("STEP_OUTPUT input requires only stepId")
		}
	case ConstructionInputTableRevision:
		if !requiredID(r.TableID) || !requiredID(r.RevisionID) || !requiredID(r.OutputID) || r.StepID != "" {
			return fmt.Errorf("TABLE_REVISION input requires tableId, revisionId, and outputId")
		}
	case ConstructionInputWorkspaceOutput:
		if !requiredID(r.OutputID) || r.StepID != "" || r.TableID != "" || r.RevisionID != "" {
			return fmt.Errorf("WORKSPACE_OUTPUT input requires only outputId")
		}
	default:
		return fmt.Errorf("unsupported input kind %q", r.Kind)
	}
	return nil
}

// StageColumn describes one column at a stage boundary. Type is advisory for
// source-derived operations, while terminal Combine requires a concrete scalar
// type and uses Nullable to declare left-join output behavior.
type StageColumn struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Label    string             `json:"label"`
	Type     string             `json:"type,omitempty"`
	Nullable bool               `json:"nullable,omitempty"`
	Table    *TablePresentation `json:"table,omitempty"`
}

type ConstructionOperation struct {
	Kind               ConstructionOperationKind       `json:"kind"`
	Pivot              *ConstructionPivot              `json:"pivot,omitempty"`
	Derive             *ConstructionDerive             `json:"derive,omitempty"`
	Filter             *ConstructionFilter             `json:"filter,omitempty"`
	Unpivot            *ConstructionUnpivot            `json:"unpivot,omitempty"`
	Group              *ConstructionGroup              `json:"group,omitempty"`
	CodedGroup         *ConstructionCodedGroup         `json:"codedGroup,omitempty"`
	CodedPivot         *ConstructionCodedPivot         `json:"codedPivot,omitempty"`
	Expand             *ConstructionExpand             `json:"expand,omitempty"`
	Combine            *ConstructionCombine            `json:"combine,omitempty"`
	RelatedSource      *ConstructionRelatedSource      `json:"relatedSource,omitempty"`
	RelatedExpand      *ConstructionRelatedExpand      `json:"relatedExpand,omitempty"`
	RelatedEligibility *ConstructionRelatedEligibility `json:"relatedEligibility,omitempty"`
	RelatedField       *ConstructionRelatedField       `json:"relatedField,omitempty"`
}

// ConstructionRelatedSource adds one compiler-authorized field from related
// resources to rows at the exact input stage. Route and field identity are
// persisted alongside the choice token so saved constructions remain
// self-contained after the capability snapshot expires.
type ConstructionRelatedSource struct {
	AnchorColumnID     string                             `json:"anchorColumnId"`
	ChoiceID           string                             `json:"choiceId"`
	SourceOccurrenceID string                             `json:"sourceOccurrenceId"`
	Source             ConstructionRelatedFieldSource     `json:"source"`
	Route              []capability.ConstructionRouteStep `json:"route"`
	ContributorRule    ConstructionRelatedContributorRule `json:"contributorRule"`
	Form               capability.ConstructionChoiceForm  `json:"form"`
	RowValuePolicy     ConstructionRowValuePolicy         `json:"rowValuePolicy,omitempty"`
	OutputColumnID     string                             `json:"outputColumnId"`
}

// ConstructionRelatedExpand emits one row per distinct resource reached by
// the exact, server-authorized route.
type ConstructionRelatedExpand struct {
	AnchorColumnID        string                             `json:"anchorColumnId"`
	ChoiceID              string                             `json:"choiceId"`
	TargetNodeID          string                             `json:"targetNodeId"`
	TargetResourceType    string                             `json:"targetResourceType"`
	Route                 []capability.ConstructionRouteStep `json:"route"`
	ContributorRule       ConstructionRelatedContributorRule `json:"contributorRule"`
	ContributorSource     *ConstructionRelatedFieldSource    `json:"contributorSource,omitempty"`
	ContributorChoiceID   string                             `json:"contributorChoiceId,omitempty"`
	EmptyPolicy           ConstructionExpandEmptyPolicy      `json:"emptyPolicy"`
	RelatedRecordColumnID string                             `json:"relatedRecordColumnId"`
}

// ConstructionRelatedEligibility filters rows by the number of distinct
// terminal resources reached through an exact authorized route. It preserves
// the input schema and row identity.
type ConstructionRelatedEligibility struct {
	AnchorColumnID      string                              `json:"anchorColumnId"`
	ChoiceID            string                              `json:"choiceId"`
	TargetNodeID        string                              `json:"targetNodeId"`
	TargetResourceType  string                              `json:"targetResourceType"`
	Route               []capability.ConstructionRouteStep  `json:"route"`
	ContributorRule     ConstructionRelatedContributorRule  `json:"contributorRule"`
	ContributorSource   *ConstructionRelatedFieldSource     `json:"contributorSource,omitempty"`
	ContributorChoiceID string                              `json:"contributorChoiceId,omitempty"`
	Match               ConstructionRelatedEligibilityMatch `json:"match"`
}

type ConstructionRelatedEligibilityMatch struct {
	Kind      string `json:"kind"`
	Threshold *int   `json:"threshold,omitempty"`
}

const (
	ConstructionRelatedEligibilityExists       = "EXISTS"
	ConstructionRelatedEligibilityAbsent       = "ABSENT"
	ConstructionRelatedEligibilityCountAtLeast = "COUNT_AT_LEAST"
)

// ConstructionRelatedField adds one scalar field from the exact terminal
// resource retained by a preceding RELATED_EXPAND stage.
type ConstructionRelatedField struct {
	ChoiceID       string                         `json:"choiceId"`
	Source         ConstructionRelatedFieldSource `json:"source"`
	OutputColumnID string                         `json:"outputColumnId"`
}

type ConstructionRelatedFieldSource struct {
	Kind               capability.ConstructionChoiceSourceKind `json:"kind"`
	CandidateID        string                                  `json:"candidateId"`
	NodeID             string                                  `json:"nodeId"`
	ResourceType       string                                  `json:"resourceType"`
	Path               string                                  `json:"path"`
	Cardinality        string                                  `json:"cardinality"`
	LogicalType        string                                  `json:"logicalType"`
	RepeatedBoundaries []capability.RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

type ConstructionRelatedContributorRule struct {
	Policy    string                `json:"policy"`
	Predicate *ContributorPredicate `json:"predicate,omitempty"`
}

const ConstructionRelatedAllMatches = "ALL_MATCHES"

func (o *ConstructionOperation) UnmarshalJSON(raw []byte) error {
	type wire ConstructionOperation
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*o = ConstructionOperation(value)
	return nil
}

// ConstructionCombine describes one standalone operation over exact published
// table revisions. InputColumnIDs refer to the schemas resolved for those
// revisions; OutputColumnIDs remain stable in the authored output schema.
type ConstructionCombine struct {
	Kind             ConstructionCombineKind             `json:"kind"`
	Keys             []ConstructionCombineKey            `json:"keys,omitempty"`
	Projections      []ConstructionCombineProjection     `json:"projections"`
	JoinType         ConstructionCombineJoinType         `json:"joinType,omitempty"`
	RightMatchPolicy ConstructionCombineRightMatchPolicy `json:"rightMatchPolicy,omitempty"`
	MembershipMode   ConstructionCombineMembershipMode   `json:"membershipMode,omitempty"`
}

type ConstructionCombineKind string

const (
	ConstructionCombineKeyJoin    ConstructionCombineKind = "KEY_JOIN"
	ConstructionCombineAppend     ConstructionCombineKind = "APPEND"
	ConstructionCombineMembership ConstructionCombineKind = "MEMBERSHIP"
)

type ConstructionCombineJoinType string

const (
	ConstructionCombineInnerJoin ConstructionCombineJoinType = "INNER"
	ConstructionCombineLeftJoin  ConstructionCombineJoinType = "LEFT"
)

type ConstructionCombineRightMatchPolicy string

const ConstructionCombinePreserveAllMatches ConstructionCombineRightMatchPolicy = "PRESERVE_ALL"

type ConstructionCombineMembershipMode string

const (
	ConstructionCombineIncludeMatches ConstructionCombineMembershipMode = "INCLUDE"
	ConstructionCombineExcludeMatches ConstructionCombineMembershipMode = "EXCLUDE"
)

type ConstructionCombineKey struct {
	LeftColumnID  string `json:"leftColumnId"`
	RightColumnID string `json:"rightColumnId"`
}

type ConstructionCombineProjection struct {
	OutputColumnID string `json:"outputColumnId"`
	InputIndex     int    `json:"inputIndex"`
	InputColumnID  string `json:"inputColumnId"`
}

type ConstructionPivot struct {
	ConstructionID         string                                  `json:"constructionId"`
	GroupKeyIDs            []string                                `json:"groupKeyIds"`
	CategoryColumnID       string                                  `json:"categoryColumnId"`
	ValueColumnID          string                                  `json:"valueColumnId"`
	Categories             []ConstructionPivotCategory             `json:"categories"`
	DuplicatePolicy        ConstructionPivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      ConstructionPivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy ConstructionPivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type ConstructionPivotDuplicatePolicy string

const (
	ConstructionPivotDuplicateError ConstructionPivotDuplicatePolicy = "ERROR"
	ConstructionPivotDuplicateSum   ConstructionPivotDuplicatePolicy = "SUM"
	ConstructionPivotDuplicateMin   ConstructionPivotDuplicatePolicy = "MIN"
	ConstructionPivotDuplicateMax   ConstructionPivotDuplicatePolicy = "MAX"
)

type ConstructionPivotMissingCellPolicy string

const (
	ConstructionPivotMissingNull  ConstructionPivotMissingCellPolicy = "NULL"
	ConstructionPivotMissingError ConstructionPivotMissingCellPolicy = "ERROR"
)

type ConstructionPivotUnlistedCategoryPolicy string

const (
	ConstructionPivotUnlistedError               ConstructionPivotUnlistedCategoryPolicy = "ERROR"
	ConstructionPivotUnlistedExcludeWithEvidence ConstructionPivotUnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
)

type ConstructionPivotCategory struct {
	Key            TableScalar `json:"key"`
	OutputColumnID string      `json:"outputColumnId"`
}

// ConstructionCodedPivot pivots a compiler-authorized coded value family
// directly from root resources. Choice IDs authorize a proposal and are
// cleared before the durable source and category facts are persisted.
type ConstructionCodedPivot struct {
	ConstructionID    string                             `json:"constructionId"`
	SourceChoiceID    string                             `json:"sourceChoiceId,omitempty"`
	Source            *ConstructionCodedPivotSource      `json:"source,omitempty"`
	Categories        []ConstructionCodedPivotCategory   `json:"categories"`
	DuplicatePolicy   ConstructionPivotDuplicatePolicy   `json:"duplicatePolicy"`
	MissingCellPolicy ConstructionPivotMissingCellPolicy `json:"missingCellPolicy"`
}

type ConstructionCodedPivotSource struct {
	Family      capability.SemanticFrameFamily     `json:"family"`
	CandidateID string                             `json:"candidateId"`
	NodeID      string                             `json:"nodeId"`
	FieldPath   string                             `json:"fieldPath"`
	Route       []capability.ConstructionRouteStep `json:"route"`
}

type ConstructionCodedPivotCategory struct {
	ChoiceID       string `json:"choiceId,omitempty"`
	System         string `json:"system,omitempty"`
	Code           string `json:"code,omitempty"`
	OutputColumnID string `json:"outputColumnId"`
}

type ConstructionDerive struct {
	ConstructionID       string                           `json:"constructionId"`
	OutputColumnID       string                           `json:"outputColumnId"`
	Operation            ConstructionDerivedOperation     `json:"operation"`
	Left                 ConstructionOperand              `json:"left"`
	Right                ConstructionOperand              `json:"right"`
	MissingInputPolicy   ConstructionMissingInputPolicy   `json:"missingInputPolicy"`
	DivisionByZeroPolicy ConstructionDivisionByZeroPolicy `json:"divisionByZeroPolicy,omitempty"`
}

type ConstructionDerivedOperation string

const (
	ConstructionDerivedAdd      ConstructionDerivedOperation = "ADD"
	ConstructionDerivedSubtract ConstructionDerivedOperation = "SUBTRACT"
	ConstructionDerivedMultiply ConstructionDerivedOperation = "MULTIPLY"
	ConstructionDerivedDivide   ConstructionDerivedOperation = "DIVIDE"
)

type ConstructionMissingInputPolicy string

const (
	ConstructionMissingInputPropagateNull ConstructionMissingInputPolicy = "PROPAGATE_NULL"
	ConstructionMissingInputError         ConstructionMissingInputPolicy = "ERROR"
)

type ConstructionDivisionByZeroPolicy string

const (
	ConstructionDivisionByZeroNull  ConstructionDivisionByZeroPolicy = "NULL"
	ConstructionDivisionByZeroError ConstructionDivisionByZeroPolicy = "ERROR"
)

type ConstructionOperand struct {
	Kind     ConstructionOperandKind `json:"kind"`
	ColumnID string                  `json:"columnId,omitempty"`
	Literal  *ConstructionLiteral    `json:"literal,omitempty"`
}

type ConstructionOperandKind string

const (
	ConstructionColumnOperand  ConstructionOperandKind = "COLUMN"
	ConstructionLiteralOperand ConstructionOperandKind = "LITERAL"
)

func (o *ConstructionOperand) UnmarshalJSON(raw []byte) error {
	type wire ConstructionOperand
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*o = ConstructionOperand(value)
	return nil
}

type ConstructionLiteral struct {
	Kind    ConstructionNumericKind `json:"kind"`
	Integer *int64                  `json:"integer,omitempty"`
	Decimal *float64                `json:"decimal,omitempty"`
}

type ConstructionNumericKind string

const (
	ConstructionNumericInteger ConstructionNumericKind = "INTEGER"
	ConstructionNumericDecimal ConstructionNumericKind = "DECIMAL"
)

func (l *ConstructionLiteral) UnmarshalJSON(raw []byte) error {
	type wire ConstructionLiteral
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*l = ConstructionLiteral(value)
	return nil
}

type ConstructionFilter struct {
	ColumnID string                     `json:"columnId"`
	Operator ConstructionFilterOperator `json:"operator"`
	Values   []FilterValue              `json:"values,omitempty"`
}

type ConstructionFilterOperator string

const (
	ConstructionFilterEquals      ConstructionFilterOperator = "EQUALS"
	ConstructionFilterNotEquals   ConstructionFilterOperator = "NOT_EQUALS"
	ConstructionFilterIn          ConstructionFilterOperator = "IN"
	ConstructionFilterExists      ConstructionFilterOperator = "EXISTS"
	ConstructionFilterMissing     ConstructionFilterOperator = "MISSING"
	ConstructionFilterContains    ConstructionFilterOperator = "CONTAINS_TEXT"
	ConstructionFilterGreaterThan ConstructionFilterOperator = "GT"
	ConstructionFilterGreaterEq   ConstructionFilterOperator = "GTE"
	ConstructionFilterLessThan    ConstructionFilterOperator = "LT"
	ConstructionFilterLessEq      ConstructionFilterOperator = "LTE"
)

type FilterValue struct {
	Kind     ConstructionFilterValueKind `json:"kind"`
	String   *string                     `json:"string,omitempty"`
	Code     *CodeValue                  `json:"code,omitempty"`
	Boolean  *bool                       `json:"boolean,omitempty"`
	Integer  *int64                      `json:"integer,omitempty"`
	Decimal  *float64                    `json:"decimal,omitempty"`
	Date     *string                     `json:"date,omitempty"`
	DateTime *string                     `json:"dateTime,omitempty"`
}

type ConstructionFilterValueKind string

const (
	ConstructionFilterString   ConstructionFilterValueKind = "STRING"
	ConstructionFilterCode     ConstructionFilterValueKind = "CODE"
	ConstructionFilterBoolean  ConstructionFilterValueKind = "BOOLEAN"
	ConstructionFilterInteger  ConstructionFilterValueKind = "INTEGER"
	ConstructionFilterDecimal  ConstructionFilterValueKind = "DECIMAL"
	ConstructionFilterDate     ConstructionFilterValueKind = "DATE"
	ConstructionFilterDateTime ConstructionFilterValueKind = "DATE_TIME"
)

func (v *FilterValue) UnmarshalJSON(raw []byte) error {
	type wire FilterValue
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*v = FilterValue(value)
	return nil
}

type CodeValue struct {
	System  string `json:"system,omitempty"`
	Code    string `json:"code"`
	Display string `json:"display,omitempty"`
}

type ConstructionUnpivot struct {
	ConstructionID      string                        `json:"constructionId"`
	Inputs              []ConstructionUnpivotInput    `json:"inputs"`
	KeyOutputColumnID   string                        `json:"keyOutputColumnId"`
	ValueOutputColumnID string                        `json:"valueOutputColumnId"`
	NullRowPolicy       ConstructionUnpivotNullPolicy `json:"nullRowPolicy"`
}

type ConstructionUnpivotNullPolicy string

const (
	ConstructionUnpivotDrop     ConstructionUnpivotNullPolicy = "DROP"
	ConstructionUnpivotPreserve ConstructionUnpivotNullPolicy = "PRESERVE"
)

type ConstructionUnpivotInput struct {
	ColumnID string      `json:"columnId"`
	Key      TableScalar `json:"key"`
}

// ConstructionGroup replaces a stage's rows with one row per distinct key
// tuple, or one table summary row when Keys is empty.
type ConstructionGroup struct {
	ConstructionID   string                            `json:"constructionId"`
	MissingKeyPolicy ConstructionGroupMissingKeyPolicy `json:"missingKeyPolicy"`
	Keys             []ConstructionGroupKey            `json:"keys,omitempty"`
	Aggregates       []ConstructionGroupAggregate      `json:"aggregates,omitempty"`
}

// ConstructionCodedGroup groups source rows by one correlated tuple from a
// repeated Coding path. The source facts are durable compiler inputs; ChoiceID
// is proposal authority only and is not required to recompile a saved step.
type ConstructionCodedGroup struct {
	ConstructionID                    string                            `json:"constructionId"`
	ChoiceID                          string                            `json:"choiceId,omitempty"`
	Source                            ConstructionCodedGroupSource      `json:"source"`
	MissingKeyPolicy                  ConstructionGroupMissingKeyPolicy `json:"missingKeyPolicy"`
	SystemOutputColumnID              string                            `json:"systemOutputColumnId"`
	VersionOutputColumnID             string                            `json:"versionOutputColumnId"`
	CodeOutputColumnID                string                            `json:"codeOutputColumnId"`
	DistinctSourceCountOutputColumnID string                            `json:"distinctSourceCountOutputColumnId"`
}

type ConstructionCodedGroupSource struct {
	OccurrenceID string                             `json:"occurrenceId"`
	ResourceType string                             `json:"resourceType"`
	CodingPath   string                             `json:"codingPath"`
	FHIRType     string                             `json:"fhirType"`
	Cardinality  string                             `json:"cardinality"`
	Shape        string                             `json:"shape"`
	Route        []capability.ConstructionRouteStep `json:"route"`
}

type ConstructionGroupMissingKeyPolicy string

const (
	ConstructionGroupMissingKeyGroup   ConstructionGroupMissingKeyPolicy = "GROUP"
	ConstructionGroupMissingKeyExclude ConstructionGroupMissingKeyPolicy = "EXCLUDE"
	ConstructionGroupMissingKeyError   ConstructionGroupMissingKeyPolicy = "ERROR"
)

func (policy ConstructionGroupMissingKeyPolicy) Normalized() ConstructionGroupMissingKeyPolicy {
	if policy == "" {
		return ConstructionGroupMissingKeyGroup
	}
	return policy
}

func (policy ConstructionGroupMissingKeyPolicy) Valid() bool {
	switch policy.Normalized() {
	case ConstructionGroupMissingKeyGroup, ConstructionGroupMissingKeyExclude, ConstructionGroupMissingKeyError:
		return true
	default:
		return false
	}
}

func (group *ConstructionGroup) UnmarshalJSON(raw []byte) error {
	type wire ConstructionGroup
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*group = ConstructionGroup(value)
	group.MissingKeyPolicy = group.MissingKeyPolicy.Normalized()
	return nil
}

func (group ConstructionGroup) MarshalJSON() ([]byte, error) {
	type wire ConstructionGroup
	group.MissingKeyPolicy = group.MissingKeyPolicy.Normalized()
	return json.Marshal(wire(group))
}

type ConstructionGroupKey struct {
	InputColumnID    string                             `json:"inputColumnId,omitempty"`
	FieldSource      *ConstructionGroupFieldSource      `json:"fieldSource,omitempty"`
	OutputColumnID   string                             `json:"outputColumnId"`
	SemanticCategory *ConstructionGroupSemanticCategory `json:"semanticCategory,omitempty"`
}

func (key *ConstructionGroupKey) UnmarshalJSON(raw []byte) error {
	var wire struct {
		InputColumnID    *string                            `json:"inputColumnId,omitempty"`
		FieldSource      *ConstructionGroupFieldSource      `json:"fieldSource,omitempty"`
		OutputColumnID   string                             `json:"outputColumnId"`
		SemanticCategory *ConstructionGroupSemanticCategory `json:"semanticCategory,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	sources := 0
	if wire.InputColumnID != nil {
		sources++
	}
	if wire.FieldSource != nil {
		sources++
	}
	if wire.SemanticCategory != nil {
		sources++
	}
	if sources != 1 {
		return fmt.Errorf("Group key must contain exactly one of inputColumnId, fieldSource, or semanticCategory")
	}
	*key = ConstructionGroupKey{OutputColumnID: wire.OutputColumnID, FieldSource: wire.FieldSource, SemanticCategory: wire.SemanticCategory}
	if wire.InputColumnID != nil {
		key.InputColumnID = *wire.InputColumnID
	}
	return nil
}

// ConstructionGroupFieldSource is a signed Group-key choice or its durable
// binding. A binding pins exactly one input provenance variant. Flat fields
// remain accepted for saved CURRENT_STAGE bindings and are preserved when
// those saved values cross the authoring boundary again.
type ConstructionGroupFieldSource struct {
	Kind          string                             `json:"kind"`
	ChoiceID      string                             `json:"choiceId,omitempty"`
	ValueKind     string                             `json:"valueKind,omitempty"`
	SourceStageID string                             `json:"sourceStageId,omitempty"`
	SourceShapeID string                             `json:"sourceShapeId,omitempty"`
	Input         *ConstructionGroupFieldSourceInput `json:"input,omitempty"`

	// Legacy flat CURRENT_STAGE fields. They are populated only when decoding
	// saved recipes that predate the input provenance sum.
	SourceRole        string `json:"-"`
	OccurrenceID      string `json:"-"`
	ResourceType      string `json:"-"`
	OwnerOccurrenceID string `json:"-"`
	OwnerResourceType string `json:"-"`
	CandidateID       string `json:"-"`
	CanonicalPath     string `json:"-"`
	BoundarySelector  string `json:"-"`
	legacyFlat        bool
}

type ConstructionGroupFieldSourceInput struct {
	Kind              string                                   `json:"kind"`
	CurrentStage      *ConstructionGroupFieldCurrentStage      `json:"currentStage,omitempty"`
	RelatedOccurrence *ConstructionGroupFieldRelatedOccurrence `json:"relatedOccurrence,omitempty"`
}

func (input *ConstructionGroupFieldSourceInput) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind              string                                   `json:"kind"`
		CurrentStage      *ConstructionGroupFieldCurrentStage      `json:"currentStage,omitempty"`
		RelatedOccurrence *ConstructionGroupFieldRelatedOccurrence `json:"relatedOccurrence,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	if (wire.CurrentStage == nil) == (wire.RelatedOccurrence == nil) {
		return fmt.Errorf("Group fieldSource input requires exactly one provenance payload")
	}
	switch wire.Kind {
	case "CURRENT_STAGE":
		if wire.CurrentStage == nil {
			return fmt.Errorf("CURRENT_STAGE Group fieldSource input requires currentStage")
		}
	case "RELATED_OCCURRENCE":
		if wire.RelatedOccurrence == nil {
			return fmt.Errorf("RELATED_OCCURRENCE Group fieldSource input requires relatedOccurrence")
		}
	default:
		return fmt.Errorf("unsupported Group fieldSource input kind %q", wire.Kind)
	}
	*input = ConstructionGroupFieldSourceInput(wire)
	return nil
}

type ConstructionGroupFieldCurrentStage struct {
	SourceRole        string `json:"sourceRole"`
	OccurrenceID      string `json:"occurrenceId"`
	OwnerResourceType string `json:"ownerResourceType"`
	ResourceType      string `json:"resourceType"`
	CandidateID       string `json:"candidateId,omitempty"`
	CanonicalPath     string `json:"canonicalPath"`
	BoundarySelector  string `json:"boundarySelector,omitempty"`
}

type ConstructionGroupFieldRelatedRouteStep struct {
	StepIndex        int    `json:"stepIndex"`
	FromOccurrenceID string `json:"fromOccurrenceId"`
	FromNodeID       string `json:"fromNodeId"`
	FromResourceType string `json:"fromResourceType"`
	EdgeID           string `json:"edgeId"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
	ToOccurrenceID   string `json:"toOccurrenceId"`
	ToNodeID         string `json:"toNodeId"`
	ToResourceType   string `json:"toResourceType"`
}

type ConstructionGroupFieldRelatedOccurrence struct {
	AnchorOccurrenceID string                                   `json:"anchorOccurrenceId"`
	AnchorNodeID       string                                   `json:"anchorNodeId"`
	AnchorResourceType string                                   `json:"anchorResourceType"`
	Route              []ConstructionGroupFieldRelatedRouteStep `json:"route"`
	TargetOccurrenceID string                                   `json:"targetOccurrenceId"`
	TargetNodeID       string                                   `json:"targetNodeId"`
	TargetResourceType string                                   `json:"targetResourceType"`
	ResourceType       string                                   `json:"resourceType"`
	CandidateID        string                                   `json:"candidateId,omitempty"`
	CanonicalPath      string                                   `json:"canonicalPath,omitempty"`
	EmptyPolicy        string                                   `json:"emptyPolicy"`
	Multiplicity       string                                   `json:"multiplicity"`
	CodingSelector     string                                   `json:"codingSelector,omitempty"`
	CodingEmptyPolicy  string                                   `json:"codingEmptyPolicy,omitempty"`
	OrdinalSemantics   string                                   `json:"ordinalSemantics,omitempty"`
}

func (source *ConstructionGroupFieldSource) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind              string                             `json:"kind"`
		ChoiceID          *string                            `json:"choiceId,omitempty"`
		ValueKind         *string                            `json:"valueKind,omitempty"`
		SourceStageID     *string                            `json:"sourceStageId,omitempty"`
		SourceShapeID     *string                            `json:"sourceShapeId,omitempty"`
		Input             *ConstructionGroupFieldSourceInput `json:"input,omitempty"`
		SourceRole        *string                            `json:"sourceRole,omitempty"`
		OccurrenceID      *string                            `json:"occurrenceId,omitempty"`
		ResourceType      *string                            `json:"resourceType,omitempty"`
		OwnerOccurrenceID *string                            `json:"ownerOccurrenceId,omitempty"`
		OwnerResourceType *string                            `json:"ownerResourceType,omitempty"`
		CandidateID       *string                            `json:"candidateId,omitempty"`
		CanonicalPath     *string                            `json:"canonicalPath,omitempty"`
		BoundarySelector  *string                            `json:"boundarySelector,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	flatPresent := wire.SourceRole != nil || wire.OccurrenceID != nil || wire.ResourceType != nil ||
		wire.OwnerOccurrenceID != nil || wire.OwnerResourceType != nil || wire.CandidateID != nil ||
		wire.CanonicalPath != nil || wire.BoundarySelector != nil
	decoded := ConstructionGroupFieldSource{Kind: wire.Kind}
	if wire.ChoiceID != nil {
		decoded.ChoiceID = *wire.ChoiceID
	}
	if wire.ValueKind != nil {
		decoded.ValueKind = *wire.ValueKind
	}
	if wire.SourceStageID != nil {
		decoded.SourceStageID = *wire.SourceStageID
	}
	if wire.SourceShapeID != nil {
		decoded.SourceShapeID = *wire.SourceShapeID
	}
	switch wire.Kind {
	case "CHOICE":
		if decoded.ChoiceID == "" || decoded.ValueKind != "" || decoded.SourceStageID != "" || decoded.SourceShapeID != "" || wire.Input != nil || flatPresent {
			return fmt.Errorf("Group fieldSource CHOICE requires only choiceId")
		}
	case "BINDING":
		if decoded.SourceStageID == "" || decoded.SourceShapeID == "" || decoded.ValueKind == "" || decoded.ChoiceID != "" {
			return fmt.Errorf("Group fieldSource BINDING requires valueKind, sourceStageId, and sourceShapeId only")
		}
		if wire.Input != nil && flatPresent {
			return fmt.Errorf("Group fieldSource BINDING cannot combine input with legacy flat fields")
		}
		if wire.Input != nil {
			decoded.Input = wire.Input
			if wire.Input.Kind == "CURRENT_STAGE" && wire.Input.CurrentStage != nil {
				decoded.setLegacyCurrentStage(wire.Input.CurrentStage)
			}
		} else if flatPresent {
			if wire.OwnerOccurrenceID != nil && wire.OccurrenceID != nil && *wire.OwnerOccurrenceID != *wire.OccurrenceID {
				return fmt.Errorf("legacy CURRENT_STAGE binding owner occurrence must match its field occurrence")
			}
			current := &ConstructionGroupFieldCurrentStage{}
			setString := func(target *string, value *string) {
				if value != nil {
					*target = *value
				}
			}
			setString(&current.SourceRole, wire.SourceRole)
			setString(&current.OccurrenceID, wire.OccurrenceID)
			setString(&current.OwnerResourceType, wire.OwnerResourceType)
			setString(&current.ResourceType, wire.ResourceType)
			setString(&current.CandidateID, wire.CandidateID)
			setString(&current.CanonicalPath, wire.CanonicalPath)
			setString(&current.BoundarySelector, wire.BoundarySelector)
			decoded.Input = &ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: current}
			decoded.legacyFlat = true
			decoded.setLegacyCurrentStage(current)
		} else {
			return fmt.Errorf("Group fieldSource BINDING requires one input provenance")
		}
		if err := decoded.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported Group fieldSource kind %q", wire.Kind)
	}
	*source = decoded
	return nil
}

func (source ConstructionGroupFieldSource) MarshalJSON() ([]byte, error) {
	if source.Kind == "CHOICE" {
		if source.ChoiceID == "" || source.ValueKind != "" || source.SourceStageID != "" || source.SourceShapeID != "" || source.Input != nil {
			return nil, fmt.Errorf("Group fieldSource CHOICE requires only choiceId")
		}
		return json.Marshal(struct {
			Kind     string `json:"kind"`
			ChoiceID string `json:"choiceId"`
		}{Kind: source.Kind, ChoiceID: source.ChoiceID})
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	if source.legacyFlat || source.Input == nil && source.hasLegacyCurrentStage() {
		return json.Marshal(struct {
			Kind              string `json:"kind"`
			ValueKind         string `json:"valueKind"`
			SourceStageID     string `json:"sourceStageId"`
			SourceShapeID     string `json:"sourceShapeId"`
			SourceRole        string `json:"sourceRole"`
			OccurrenceID      string `json:"occurrenceId"`
			ResourceType      string `json:"resourceType"`
			OwnerOccurrenceID string `json:"ownerOccurrenceId,omitempty"`
			OwnerResourceType string `json:"ownerResourceType"`
			CandidateID       string `json:"candidateId,omitempty"`
			CanonicalPath     string `json:"canonicalPath,omitempty"`
			BoundarySelector  string `json:"boundarySelector,omitempty"`
		}{
			Kind: source.Kind, ValueKind: source.ValueKind, SourceStageID: source.SourceStageID,
			SourceShapeID: source.SourceShapeID, SourceRole: source.SourceRole,
			OccurrenceID: source.OccurrenceID, ResourceType: source.ResourceType,
			OwnerOccurrenceID: source.OwnerOccurrenceID, OwnerResourceType: source.OwnerResourceType,
			CandidateID: source.CandidateID, CanonicalPath: source.CanonicalPath,
			BoundarySelector: source.BoundarySelector,
		})
	}
	input := source.Input
	if input == nil {
		input = &ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: source.legacyCurrentStage()}
	}
	return json.Marshal(struct {
		Kind          string                             `json:"kind"`
		ValueKind     string                             `json:"valueKind"`
		SourceStageID string                             `json:"sourceStageId"`
		SourceShapeID string                             `json:"sourceShapeId"`
		Input         *ConstructionGroupFieldSourceInput `json:"input"`
	}{Kind: source.Kind, ValueKind: source.ValueKind, SourceStageID: source.SourceStageID, SourceShapeID: source.SourceShapeID, Input: input})
}

func (source ConstructionGroupFieldSource) Validate() error {
	if source.Kind != "BINDING" || source.ChoiceID != "" || source.SourceStageID == "" || source.SourceShapeID == "" || source.ValueKind == "" {
		return fmt.Errorf("Group fieldSource BINDING requires exact stage, shape, valueKind, and input provenance")
	}
	if source.ValueKind != "SCALAR" && source.ValueKind != "CODING_IDENTITY" {
		return fmt.Errorf("unsupported Group fieldSource valueKind %q", source.ValueKind)
	}
	input := source.Input
	if input == nil && source.hasLegacyCurrentStage() {
		if source.OwnerOccurrenceID != "" && source.OwnerOccurrenceID != source.OccurrenceID {
			return fmt.Errorf("legacy CURRENT_STAGE binding owner occurrence must match its field occurrence")
		}
		input = &ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: source.legacyCurrentStage()}
	}
	if input == nil {
		return fmt.Errorf("Group fieldSource BINDING requires one input provenance")
	}
	switch input.Kind {
	case "CURRENT_STAGE":
		current := input.CurrentStage
		if current == nil || input.RelatedOccurrence != nil || current.OccurrenceID == "" || current.OwnerResourceType == "" || current.ResourceType == "" || current.SourceRole == "" {
			return fmt.Errorf("CURRENT_STAGE Group fieldSource requires one exact currentStage source")
		}
		if current.SourceRole != "ROOT" && current.SourceRole != "OWNER" && current.SourceRole != "ANCESTOR" && current.SourceRole != "CURRENT_ITEM" {
			return fmt.Errorf("unsupported CURRENT_STAGE sourceRole %q", current.SourceRole)
		}
		if source.ValueKind == "SCALAR" && (current.CandidateID == "" || current.CanonicalPath == "") {
			return fmt.Errorf("scalar CURRENT_STAGE binding requires candidateId and canonicalPath")
		}
		if source.ValueKind == "CODING_IDENTITY" && (current.SourceRole != "CURRENT_ITEM" || current.CandidateID != "" || current.CanonicalPath == "" || current.BoundarySelector != "") {
			return fmt.Errorf("Coding identity CURRENT_STAGE binding requires a current item path without candidateId or redundant boundarySelector")
		}
		if current.SourceRole == "ANCESTOR" && current.BoundarySelector == "" {
			return fmt.Errorf("ANCESTOR CURRENT_STAGE binding requires boundarySelector")
		}
	case "RELATED_OCCURRENCE":
		related := input.RelatedOccurrence
		if related == nil || input.CurrentStage != nil || related.AnchorOccurrenceID == "" || related.AnchorNodeID == "" || related.AnchorResourceType == "" ||
			related.TargetOccurrenceID == "" || related.TargetNodeID == "" || related.TargetResourceType == "" || related.ResourceType == "" ||
			len(related.Route) == 0 || related.Multiplicity != "DISTINCT_TARGET_PER_INPUT" {
			return fmt.Errorf("RELATED_OCCURRENCE Group fieldSource requires an exact route, anchor, target, and DISTINCT_TARGET_PER_INPUT multiplicity")
		}
		if related.Route[0].FromOccurrenceID != related.AnchorOccurrenceID || related.Route[0].FromNodeID != related.AnchorNodeID ||
			related.Route[0].FromResourceType != related.AnchorResourceType ||
			related.Route[len(related.Route)-1].ToOccurrenceID != related.TargetOccurrenceID ||
			related.Route[len(related.Route)-1].ToNodeID != related.TargetNodeID ||
			related.Route[len(related.Route)-1].ToResourceType != related.TargetResourceType {
			return fmt.Errorf("RELATED_OCCURRENCE route endpoints must match its exact anchor and target")
		}
		for index, step := range related.Route {
			if step.StepIndex != index || step.FromOccurrenceID == "" || step.FromNodeID == "" || step.FromResourceType == "" ||
				step.EdgeID == "" || step.Relationship == "" || step.ToOccurrenceID == "" || step.ToNodeID == "" || step.ToResourceType == "" ||
				(step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND") ||
				(step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED") ||
				(index > 0 && (related.Route[index-1].ToOccurrenceID != step.FromOccurrenceID || related.Route[index-1].ToNodeID != step.FromNodeID || related.Route[index-1].ToResourceType != step.FromResourceType)) {
				return fmt.Errorf("RELATED_OCCURRENCE route step %d is invalid or disconnected", index)
			}
		}
		if related.EmptyPolicy != "PRESERVE_PARENT" && related.EmptyPolicy != "EXCLUDE" && related.EmptyPolicy != "ERROR" {
			return fmt.Errorf("unsupported RELATED_OCCURRENCE emptyPolicy %q", related.EmptyPolicy)
		}
		if source.ValueKind == "SCALAR" {
			if related.CandidateID == "" || related.CanonicalPath == "" || related.CodingSelector != "" || related.OrdinalSemantics != "" || related.CodingEmptyPolicy != "" {
				return fmt.Errorf("scalar RELATED_OCCURRENCE binding requires only candidateId and canonicalPath")
			}
		} else if related.CandidateID != "" || related.CanonicalPath != "" || related.CodingSelector == "" || related.OrdinalSemantics == "" || related.CodingEmptyPolicy == "" {
			return fmt.Errorf("Coding identity RELATED_OCCURRENCE binding requires Coding selector, empty policy, and ordinal semantics")
		}
		if related.CodingEmptyPolicy != "" && related.CodingEmptyPolicy != "GROUP" && related.CodingEmptyPolicy != "EXCLUDE" && related.CodingEmptyPolicy != "ERROR" {
			return fmt.Errorf("unsupported Coding identity empty policy %q", related.CodingEmptyPolicy)
		}
	default:
		return fmt.Errorf("unsupported Group fieldSource input kind %q", input.Kind)
	}
	return nil
}

func (source *ConstructionGroupFieldSource) setLegacyCurrentStage(current *ConstructionGroupFieldCurrentStage) {
	if source == nil || current == nil {
		return
	}
	source.SourceRole = current.SourceRole
	source.OccurrenceID = current.OccurrenceID
	source.ResourceType = current.ResourceType
	source.OwnerResourceType = current.OwnerResourceType
	source.CandidateID = current.CandidateID
	source.CanonicalPath = current.CanonicalPath
	source.BoundarySelector = current.BoundarySelector
	source.OwnerOccurrenceID = current.OccurrenceID
}

func (source ConstructionGroupFieldSource) hasLegacyCurrentStage() bool {
	return source.SourceRole != "" || source.OccurrenceID != "" || source.ResourceType != "" ||
		source.OwnerOccurrenceID != "" || source.OwnerResourceType != "" || source.CandidateID != "" ||
		source.CanonicalPath != "" || source.BoundarySelector != ""
}

func (source ConstructionGroupFieldSource) legacyCurrentStage() *ConstructionGroupFieldCurrentStage {
	return &ConstructionGroupFieldCurrentStage{
		SourceRole: source.SourceRole, OccurrenceID: source.OccurrenceID,
		OwnerResourceType: source.OwnerResourceType, ResourceType: source.ResourceType,
		CandidateID: source.CandidateID, CanonicalPath: source.CanonicalPath,
		BoundarySelector: source.BoundarySelector,
	}
}

// ConstructionGroupSemanticCategory is either an ephemeral CHOICE supplied
// by current stage capabilities or the durable server-resolved BINDING.
// Version is an exact string; the empty string means versionless, not any.
type ConstructionGroupSemanticCategory struct {
	Kind         string `json:"kind"`
	ChoiceID     string `json:"choiceId,omitempty"`
	BindingID    string `json:"bindingId,omitempty"`
	RuleVersion  string `json:"ruleVersion,omitempty"`
	FamilyID     string `json:"familyId,omitempty"`
	SourceKind   string `json:"sourceKind,omitempty"`
	System       string `json:"system,omitempty"`
	Version      string `json:"version,omitempty"`
	ResourceType string `json:"resourceType,omitempty"`
	CodingPath   string `json:"codingPath,omitempty"`
}

type ConstructionGroupAggregate struct {
	Operation      ConstructionGroupAggregateOp `json:"operation"`
	InputColumnID  string                       `json:"inputColumnId,omitempty"`
	OutputColumnID string                       `json:"outputColumnId"`
}

type ConstructionGroupAggregateOp string

const (
	ConstructionGroupCountRows     ConstructionGroupAggregateOp = "COUNT_ROWS"
	ConstructionGroupCountNonNull  ConstructionGroupAggregateOp = "COUNT_NON_NULL"
	ConstructionGroupCountDistinct ConstructionGroupAggregateOp = "COUNT_DISTINCT"
	ConstructionGroupSum           ConstructionGroupAggregateOp = "SUM"
	ConstructionGroupMean          ConstructionGroupAggregateOp = "MEAN"
)

// ConstructionExpand replaces one public list column with its items and, if
// requested, the zero-based position of each item.
type ConstructionExpand struct {
	ConstructionID  string                        `json:"constructionId"`
	InputColumnID   string                        `json:"inputColumnId"`
	OutputColumnID  string                        `json:"outputColumnId"`
	OrdinalColumnID string                        `json:"ordinalColumnId,omitempty"`
	EmptyPolicy     ConstructionExpandEmptyPolicy `json:"emptyPolicy,omitempty"`
}

type ConstructionExpandEmptyPolicy string

const (
	ConstructionExpandEmptyError          ConstructionExpandEmptyPolicy = "ERROR"
	ConstructionExpandEmptyExclude        ConstructionExpandEmptyPolicy = "EXCLUDE"
	ConstructionExpandEmptyPreserveParent ConstructionExpandEmptyPolicy = "PRESERVE_PARENT"
)

func requiredID(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value)
}
