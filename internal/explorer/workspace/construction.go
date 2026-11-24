package workspace

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
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

type Construction struct {
	Version           int                            `json:"version"`
	Steps             []ConstructionStep             `json:"steps"`
	SourceProjections []ConstructionSourceProjection `json:"sourceProjections,omitempty"`
}

type ConstructionSourceProjection struct {
	ColumnID     string `json:"columnId"`
	OwnerStepID  string `json:"ownerStepId,omitempty"`
	OccurrenceID string `json:"occurrenceId"`
	FieldPath    string `json:"fieldPath"`
	FHIRType     string `json:"fhirType"`
	LogicalType  string `json:"logicalType"`
	Label        string `json:"label"`
}

func (c Construction) MarshalJSON() ([]byte, error) {
	type wire Construction
	if c.Steps == nil {
		c.Steps = []ConstructionStep{}
	}
	return json.Marshal(wire(c))
}

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

type ConstructionCombine recipe.ConstructionCombine

type ConstructionCombineKind = recipe.ConstructionCombineKind

type ConstructionCombineJoinType = recipe.ConstructionCombineJoinType

type ConstructionCombineRightMatchPolicy = recipe.ConstructionCombineRightMatchPolicy

type ConstructionCombineMembershipMode = recipe.ConstructionCombineMembershipMode

type ConstructionCombineKey = recipe.ConstructionCombineKey

type ConstructionCombineProjection = recipe.ConstructionCombineProjection

const (
	ConstructionCombineKeyJoin    = recipe.ConstructionCombineKeyJoin
	ConstructionCombineAppend     = recipe.ConstructionCombineAppend
	ConstructionCombineMembership = recipe.ConstructionCombineMembership

	ConstructionCombineInnerJoin = recipe.ConstructionCombineInnerJoin
	ConstructionCombineLeftJoin  = recipe.ConstructionCombineLeftJoin

	ConstructionCombinePreserveAllMatches = recipe.ConstructionCombinePreserveAllMatches

	ConstructionCombineIncludeMatches = recipe.ConstructionCombineIncludeMatches
	ConstructionCombineExcludeMatches = recipe.ConstructionCombineExcludeMatches
)

type ConstructionPivot struct {
	ConstructionID   string                      `json:"constructionId"`
	GroupKeyIDs      []string                    `json:"groupKeyIds"`
	CategoryColumnID string                      `json:"categoryColumnId"`
	ValueColumnID    string                      `json:"valueColumnId"`
	Categories       []ConstructionPivotCategory `json:"categories"`

	Discovery              *ConstructionPivotDiscovery             `json:"discovery,omitempty"`
	DuplicatePolicy        ConstructionPivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      ConstructionPivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy ConstructionPivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type ConstructionPivotDiscovery struct {
	MaxCategories int `json:"maxCategories"`
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
	ColumnID string              `json:"columnId"`
	Operator spec.FilterOperator `json:"operator"`
	Values   []FilterValue       `json:"values,omitempty"`
}

const (
	ConstructionFilterEquals      = spec.FilterEquals
	ConstructionFilterNotEquals   = spec.FilterNotEquals
	ConstructionFilterIn          = spec.FilterIn
	ConstructionFilterExists      = spec.FilterExists
	ConstructionFilterMissing     = spec.FilterMissing
	ConstructionFilterContains    = spec.FilterContains
	ConstructionFilterGreaterThan = spec.FilterGreaterThan
	ConstructionFilterGreaterEq   = spec.FilterGreaterEq
	ConstructionFilterLessThan    = spec.FilterLessThan
	ConstructionFilterLessEq      = spec.FilterLessEq
)

type FilterValue struct {
	Kind     spec.FilterValueKind `json:"kind"`
	String   *string              `json:"string,omitempty"`
	Code     *CodeValue           `json:"code,omitempty"`
	Boolean  *bool                `json:"boolean,omitempty"`
	Integer  *int64               `json:"integer,omitempty"`
	Decimal  *float64             `json:"decimal,omitempty"`
	Date     *string              `json:"date,omitempty"`
	DateTime *string              `json:"dateTime,omitempty"`
}

const (
	ConstructionFilterString   = spec.FilterString
	ConstructionFilterCode     = spec.FilterCode
	ConstructionFilterBoolean  = spec.FilterBoolean
	ConstructionFilterInteger  = spec.FilterInteger
	ConstructionFilterDecimal  = spec.FilterDecimal
	ConstructionFilterDate     = spec.FilterDate
	ConstructionFilterDateTime = spec.FilterDateTime
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

type ConstructionGroup struct {
	ConstructionID   string                            `json:"constructionId"`
	MissingKeyPolicy ConstructionGroupMissingKeyPolicy `json:"missingKeyPolicy"`
	Keys             []ConstructionGroupKey            `json:"keys,omitempty"`
	Aggregates       []ConstructionGroupAggregate      `json:"aggregates,omitempty"`
}

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

type ConstructionGroupFieldSource struct {
	Kind          string                             `json:"kind"`
	ChoiceID      string                             `json:"choiceId,omitempty"`
	ValueKind     string                             `json:"valueKind,omitempty"`
	SourceStageID string                             `json:"sourceStageId,omitempty"`
	SourceShapeID string                             `json:"sourceShapeId,omitempty"`
	Input         *ConstructionGroupFieldSourceInput `json:"input,omitempty"`

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

type ConstructionGroupFieldCurrentStage = recipe.ConstructionGroupCurrentStageFieldSource

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

type ConstructionGroupAggregate = recipe.ConstructionGroupAggregate

type ConstructionGroupAggregateOp = recipe.ConstructionGroupAggregateOp

const (
	ConstructionGroupCountRows     = recipe.ConstructionGroupCountRows
	ConstructionGroupCountNonNull  = recipe.ConstructionGroupCountNonNull
	ConstructionGroupCountDistinct = recipe.ConstructionGroupCountDistinct
	ConstructionGroupSum           = recipe.ConstructionGroupSum
	ConstructionGroupMean          = recipe.ConstructionGroupMean
)

type ConstructionExpand struct {
	ConstructionID  string                           `json:"constructionId"`
	InputColumnID   string                           `json:"inputColumnId"`
	OutputColumnID  string                           `json:"outputColumnId"`
	OrdinalColumnID string                           `json:"ordinalColumnId,omitempty"`
	EmptyPolicy     ConstructionExpandEmptyPolicy    `json:"emptyPolicy,omitempty"`
	ExpandedField   *ConstructionExpandedFieldSource `json:"expandedField,omitempty"`
}

type ConstructionExpandedFieldSource struct {
	Path        string `json:"path"`
	Cardinality string `json:"cardinality"`
	FHIRType    string `json:"fhirType,omitempty"`
	LogicalType string `json:"logicalType"`
}

const ConstructionExpandedFieldCardinalityMany = "many"

func ValidateConstructionExpandedFieldSource(field *ConstructionExpandedFieldSource) error {
	if field == nil {
		return nil
	}
	if !requiredID(field.Path) {
		return fmt.Errorf("expandedField.path is required")
	}
	if field.Cardinality != ConstructionExpandedFieldCardinalityMany {
		return fmt.Errorf("expandedField.cardinality must be many")
	}
	if !requiredID(field.LogicalType) {
		return fmt.Errorf("expandedField.logicalType is required")
	}
	return nil
}

type ConstructionExpandEmptyPolicy string

const (
	ConstructionExpandEmptyError          ConstructionExpandEmptyPolicy = "ERROR"
	ConstructionExpandEmptyExclude        ConstructionExpandEmptyPolicy = "EXCLUDE"
	ConstructionExpandEmptyPreserveParent ConstructionExpandEmptyPolicy = "PRESERVE_PARENT"
)

func requiredID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func (o ConstructionOperation) inputColumnIDs() []string {
	ids := make([]string, 0)
	add := func(id string) {
		if !requiredID(id) {
			return
		}
		for _, present := range ids {
			if present == id {
				return
			}
		}
		ids = append(ids, id)
	}
	switch o.Kind {
	case ConstructionOperationPivot:
		if o.Pivot != nil {
			for _, id := range o.Pivot.GroupKeyIDs {
				add(id)
			}
			add(o.Pivot.CategoryColumnID)
			add(o.Pivot.ValueColumnID)
		}
	case ConstructionOperationDerive:
		if o.Derive != nil {
			if o.Derive.Left.Kind == ConstructionColumnOperand {
				add(o.Derive.Left.ColumnID)
			}
			if o.Derive.Right.Kind == ConstructionColumnOperand {
				add(o.Derive.Right.ColumnID)
			}
		}
	case ConstructionOperationFilter:
		if o.Filter != nil {
			add(o.Filter.ColumnID)
		}
	case ConstructionOperationUnpivot:
		if o.Unpivot != nil {
			for _, input := range o.Unpivot.Inputs {
				add(input.ColumnID)
			}
		}
	case ConstructionOperationGroup:
		if o.Group != nil {
			for _, key := range o.Group.Keys {
				add(key.InputColumnID)
			}
			for _, aggregate := range o.Group.Aggregates {
				add(aggregate.InputColumnID)
			}
		}
	case ConstructionOperationExpand:
		if o.Expand != nil {
			add(o.Expand.InputColumnID)
		}
	case ConstructionOperationRelatedSource:
		if o.RelatedSource != nil {
			if o.RelatedSource.AnchorColumnID != "_key" && o.RelatedSource.AnchorColumnID != "__loom_row_id" {
				add(o.RelatedSource.AnchorColumnID)
			}
		}
	}
	return ids
}

func (s ConstructionStep) inputColumnIDs() []string {
	ids := s.Operation.inputColumnIDs()
	for _, value := range s.RowValues {
		if value.InputColumnID != "" && !containsConstructionColumnID(ids, value.InputColumnID) {
			ids = append(ids, value.InputColumnID)
		}
	}
	return ids
}

func (c *Construction) Validate(sourceColumns []Column) error {
	if c == nil {
		return nil
	}
	if err := validateIndependentRelatedExpansions(c.Steps); err != nil {
		return err
	}
	if c.Version != ConstructionVersion {
		return fmt.Errorf("unsupported construction version %d", c.Version)
	}
	if hasCombineOperation(c.Steps) {
		if len(c.SourceProjections) != 0 {
			return fmt.Errorf("COMBINE cannot declare source projections")
		}
		if len(c.Steps) != 1 {
			return fmt.Errorf("COMBINE must be the only construction step")
		}
		if len(sourceColumns) != 0 {
			return fmt.Errorf("COMBINE cannot declare source projection columns")
		}
		return validateConstructionCombineStep(c.Steps[0])
	}
	if err := validateConstructionSourceProjections(c, sourceColumns); err != nil {
		return err
	}
	if _, err := sourceStageColumns(sourceColumns); err != nil {
		return err
	}
	if len(c.Steps) == 0 {
		return nil
	}
	source, err := constructionSourceColumnsWithChildrenAndProjections(sourceColumns, c.SourceProjections, c.Steps[0].inputColumnIDs(), c.Steps[0].Outputs)
	if err != nil {
		return fmt.Errorf("source projection: %w", err)
	}
	stepIDs := make(map[string]bool, len(c.Steps))
	for i, step := range c.Steps {
		if !requiredID(step.ID) {
			return fmt.Errorf("steps[%d].id is required", i)
		}
		if stepIDs[step.ID] {
			return fmt.Errorf("duplicate step id %q", step.ID)
		}
		stepIDs[step.ID] = true
		inputColumns, err := constructionStepInputSchema(c.Steps, source, i)
		if err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
		if err := validateConstructionStep(step, inputColumns); err != nil {
			return fmt.Errorf("steps[%d]: %w", i, err)
		}
	}
	return validateConstructionStepOwnership(c)
}

func (c *Construction) ValidateWithExplicitGroups(sourceColumns []Column, groups *GroupedRows) error {
	if groups == nil {
		return c.Validate(sourceColumns)
	}
	if c == nil {
		if groups.AfterStepID != "" {
			return fmt.Errorf("group rows afterStepId %q does not name a construction step", groups.AfterStepID)
		}
		return nil
	}
	if err := validateIndependentRelatedExpansions(c.Steps); err != nil {
		return err
	}
	if c.Version != ConstructionVersion {
		return fmt.Errorf("unsupported construction version %d", c.Version)
	}
	if len(c.Steps) > 128 {
		return fmt.Errorf("construction steps must contain at most 128 entries")
	}
	if hasCombineOperation(c.Steps) {
		return fmt.Errorf("explicit groups cannot be composed with COMBINE")
	}
	if err := validateConstructionSourceProjections(c, sourceColumns); err != nil {
		return err
	}
	source, err := sourceStageColumns(sourceColumns)
	if err != nil {
		return err
	}
	anchorIndex := -1
	if groups.AfterStepID != "" {
		anchorIndex = findConstructionStep(c.Steps, groups.AfterStepID)
		if anchorIndex < 0 {
			return fmt.Errorf("group rows afterStepId %q does not name a construction step", groups.AfterStepID)
		}
	}
	cohortColumns, err := explicitGroupStageColumns(sourceColumns, groups)
	if err != nil {
		return fmt.Errorf("group rows stage: %w", err)
	}
	if len(c.Steps) == 0 {
		if groups.AfterStepID != "" {
			return fmt.Errorf("group rows afterStepId %q does not name a construction step", groups.AfterStepID)
		}
		return nil
	}
	if anchorIndex >= 0 {
		first := c.Steps[0]
		source, err = constructionSourceColumnsWithChildrenAndProjections(sourceColumns, c.SourceProjections, first.inputColumnIDs(), first.Outputs)
		if err != nil {
			return fmt.Errorf("source projection: %w", err)
		}
	}
	priorStepID := ""
	priorColumns := source
	if anchorIndex < 0 {
		priorStepID = "group_rows"
		priorColumns = cohortColumns
	}
	stepIDs := make(map[string]bool, len(c.Steps))
	for index, step := range c.Steps {
		if index == anchorIndex+1 && anchorIndex >= 0 {
			priorStepID = "group_rows"
			priorColumns = cohortColumns
		}
		if !requiredID(step.ID) {
			return fmt.Errorf("steps[%d].id is required", index)
		}
		if step.ID == "source_projection" || step.ID == "group_rows" || stepIDs[step.ID] {
			return fmt.Errorf("steps[%d].id is reserved or duplicated", index)
		}
		stepIDs[step.ID] = true
		if len(step.Inputs) != 1 {
			return fmt.Errorf("steps[%d].inputs must contain exactly one stage reference", index)
		}
		input := step.Inputs[0]
		if err := input.Validate(); err != nil {
			return fmt.Errorf("steps[%d].inputs[0]: %w", index, err)
		}
		switch input.Kind {
		case ConstructionInputSourceProjection:
			if index != 0 || priorStepID != "" {
				return fmt.Errorf("steps[%d].inputs[0] must reference the source projection only before the cohort boundary", index)
			}
		case ConstructionInputStepOutput:
			if input.StepID != priorStepID || priorStepID == "" {
				return fmt.Errorf("steps[%d].inputs[0] must reference the immediately preceding effective stage", index)
			}
		case ConstructionInputTableRevision:
			return fmt.Errorf("steps[%d].inputs[0] table revision inputs are not supported by this compiler", index)
		default:
			return fmt.Errorf("steps[%d].inputs[0] has unsupported kind %q", index, input.Kind)
		}
		if (step.Operation.Kind == ConstructionOperationCodedGroup || step.Operation.Kind == ConstructionOperationCodedPivot) &&
			(index != 0 || input.Kind != ConstructionInputSourceProjection || priorStepID != "") {
			return fmt.Errorf("steps[%d] %s currently requires the direct source projection stage; prior construction stages are not supported", index, step.Operation.Kind)
		}
		if err := validateConstructionStep(step, priorColumns); err != nil {
			return fmt.Errorf("steps[%d]: %w", index, err)
		}
		priorStepID = step.ID
		priorColumns = step.Outputs
	}
	return validateConstructionStepOwnership(c)
}

func (combine ConstructionCombine) Validate(inputCount int, outputs []StageColumn) error {
	if inputCount < 2 {
		return fmt.Errorf("combine requires at least two table inputs")
	}
	if len(outputs) == 0 {
		return fmt.Errorf("combine output schema is required")
	}
	outputIDs := make(map[string]bool, len(outputs))
	for _, output := range outputs {
		if !requiredID(output.ID) || outputIDs[output.ID] {
			return fmt.Errorf("combine output IDs must be non-empty and unique")
		}
		outputIDs[output.ID] = true
	}
	if len(combine.Projections) == 0 {
		return fmt.Errorf("combine projections are required")
	}
	for index, projection := range combine.Projections {
		if !requiredID(projection.OutputColumnID) || !outputIDs[projection.OutputColumnID] {
			return fmt.Errorf("projection %d references unknown output column %q", index, projection.OutputColumnID)
		}
		if projection.InputIndex < 0 || projection.InputIndex >= inputCount {
			return fmt.Errorf("projection %d inputIndex is out of range", index)
		}
		if !requiredID(projection.InputColumnID) {
			return fmt.Errorf("projection %d inputColumnId is required", index)
		}
	}
	validateKeys := func() error {
		if len(combine.Keys) == 0 {
			return fmt.Errorf("combine requires at least one key pair")
		}
		leftIDs, rightIDs := map[string]bool{}, map[string]bool{}
		for index, key := range combine.Keys {
			if !requiredID(key.LeftColumnID) || !requiredID(key.RightColumnID) {
				return fmt.Errorf("keys[%d] requires leftColumnId and rightColumnId", index)
			}
			if leftIDs[key.LeftColumnID] || rightIDs[key.RightColumnID] {
				return fmt.Errorf("key column IDs must be unique on each input")
			}
			leftIDs[key.LeftColumnID], rightIDs[key.RightColumnID] = true, true
		}
		return nil
	}
	validateProjections := func(allowAppendMappings bool) error {
		pairs := make(map[string]bool, len(combine.Projections))
		projectedOutputs := make(map[string]bool, len(outputs))
		for _, projection := range combine.Projections {
			pair := fmt.Sprintf("%d\x00%s", projection.InputIndex, projection.OutputColumnID)
			if pairs[pair] {
				return fmt.Errorf("duplicate projection for input %d and output %q", projection.InputIndex, projection.OutputColumnID)
			}
			pairs[pair] = true
			if !allowAppendMappings && projectedOutputs[projection.OutputColumnID] {
				return fmt.Errorf("output %q has more than one input projection", projection.OutputColumnID)
			}
			projectedOutputs[projection.OutputColumnID] = true
		}
		for _, output := range outputs {
			if allowAppendMappings {
				mappedCount := 0
				missingInput := -1
				for inputIndex := 0; inputIndex < inputCount; inputIndex++ {
					if pairs[fmt.Sprintf("%d\x00%s", inputIndex, output.ID)] {
						mappedCount++
					} else if missingInput < 0 {
						missingInput = inputIndex
					}
				}
				if mappedCount == 0 {
					return fmt.Errorf("append output %q is not mapped from any input", output.ID)
				}
				if missingInput >= 0 {
					if !output.Nullable {
						return fmt.Errorf("append output %q must be nullable when input %d is absent", output.ID, missingInput)
					}
					if !constructionCombineNullableScalarType(output.Type) {
						return fmt.Errorf("append output %q must have a concrete scalar type when an input is absent", output.ID)
					}
				}
				continue
			}
			if !projectedOutputs[output.ID] {
				return fmt.Errorf("output %q is not projected", output.ID)
			}
		}
		return nil
	}

	switch combine.Kind {
	case ConstructionCombineKeyJoin:
		if inputCount != 2 {
			return fmt.Errorf("key join requires exactly two table inputs")
		}
		if combine.JoinType != ConstructionCombineInnerJoin && combine.JoinType != ConstructionCombineLeftJoin {
			return fmt.Errorf("key join type must be INNER or LEFT")
		}
		if combine.RightMatchPolicy != ConstructionCombinePreserveAllMatches {
			return fmt.Errorf("key join rightMatchPolicy must be PRESERVE_ALL")
		}
		if combine.MembershipMode != "" {
			return fmt.Errorf("key join does not accept membershipMode")
		}
		if err := validateKeys(); err != nil {
			return err
		}
		return validateProjections(false)
	case ConstructionCombineAppend:
		if len(combine.Keys) != 0 || combine.JoinType != "" || combine.RightMatchPolicy != "" || combine.MembershipMode != "" {
			return fmt.Errorf("append does not accept keys, joinType, rightMatchPolicy, or membershipMode")
		}
		return validateProjections(true)
	case ConstructionCombineMembership:
		if inputCount != 2 {
			return fmt.Errorf("membership requires exactly two table inputs")
		}
		if combine.MembershipMode != ConstructionCombineIncludeMatches && combine.MembershipMode != ConstructionCombineExcludeMatches {
			return fmt.Errorf("membership mode must be INCLUDE or EXCLUDE")
		}
		if combine.JoinType != "" || combine.RightMatchPolicy != "" {
			return fmt.Errorf("membership does not accept joinType or rightMatchPolicy")
		}
		if err := validateKeys(); err != nil {
			return err
		}
		for _, projection := range combine.Projections {
			if projection.InputIndex != 0 {
				return fmt.Errorf("membership projections must preserve the left input")
			}
		}
		return validateProjections(false)
	default:
		return fmt.Errorf("unsupported combine kind %q", combine.Kind)
	}
}

func (v FilterValue) Validate() error {
	payloads := 0
	for _, present := range []bool{v.String != nil, v.Code != nil, v.Boolean != nil, v.Integer != nil, v.Decimal != nil, v.Date != nil, v.DateTime != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("filter value must contain exactly one value payload")
	}
	switch v.Kind {
	case ConstructionFilterString:
		if v.String == nil {
			return fmt.Errorf("STRING value requires string")
		}
	case ConstructionFilterCode:
		if v.Code == nil || !requiredID(v.Code.Code) {
			return fmt.Errorf("CODE value requires a non-empty code")
		}
	case ConstructionFilterBoolean:
		if v.Boolean == nil {
			return fmt.Errorf("BOOLEAN value requires boolean")
		}
	case ConstructionFilterInteger:
		if v.Integer == nil {
			return fmt.Errorf("INTEGER value requires integer")
		}
	case ConstructionFilterDecimal:
		if v.Decimal == nil || math.IsNaN(*v.Decimal) || math.IsInf(*v.Decimal, 0) {
			return fmt.Errorf("DECIMAL value requires a finite decimal")
		}
	case ConstructionFilterDate:
		if v.Date == nil || strings.TrimSpace(*v.Date) == "" {
			return fmt.Errorf("DATE value requires a date")
		}
	case ConstructionFilterDateTime:
		if v.DateTime == nil || strings.TrimSpace(*v.DateTime) == "" {
			return fmt.Errorf("DATE_TIME value requires a dateTime")
		}
	default:
		return fmt.Errorf("unsupported filter value kind %q", v.Kind)
	}
	return nil
}
