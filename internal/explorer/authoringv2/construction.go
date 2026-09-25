package authoringv2

import (
	"fmt"
	"strings"
)

const ConstructionVersion = 1

type ConstructionInputKind string

const (
	ConstructionInputSourceProjection ConstructionInputKind = "SOURCE_PROJECTION"
	ConstructionInputStepOutput       ConstructionInputKind = "STEP_OUTPUT"
	ConstructionInputTableRevision    ConstructionInputKind = "TABLE_REVISION"
)

type ConstructionOperationKind string

const (
	ConstructionOperationPivot   ConstructionOperationKind = "PIVOT"
	ConstructionOperationDerive  ConstructionOperationKind = "DERIVE"
	ConstructionOperationFilter  ConstructionOperationKind = "FILTER"
	ConstructionOperationUnpivot ConstructionOperationKind = "UNPIVOT"
)

// Construction stores the ordered, durable operations applied after the
// document's source projection. A zero-step construction is the identity plan
// over that source projection. Version identifies this operation contract,
// independently of the workspace's older V2 semantics version.
type Construction struct {
	Version int                `json:"version"`
	Steps   []ConstructionStep `json:"steps"`
}

// ConstructionStep is one saved analytical operation. Its Inputs identify
// the exact stage or immutable table revision it consumes. Outputs declare
// the complete resulting stage schema; IDs remain stable when names or labels
// change.
type ConstructionStep struct {
	ID        string                 `json:"id"`
	Inputs    []ConstructionInputRef `json:"inputs"`
	Operation ConstructionOperation  `json:"operation"`
	Outputs   []StageColumn          `json:"outputs"`
}

// ConstructionInputRef is a closed source-stage, prior-stage, or immutable
// table-revision reference. A table input always names a concrete revision;
// floating references to a table's current head are not supported.
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
	default:
		return fmt.Errorf("unsupported input kind %q", r.Kind)
	}
	return nil
}

// StageColumn describes one column at a stage boundary. Type is advisory; an
// empty value or INFER lets the compiler resolve it from typed source and
// operator semantics.
type StageColumn struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
}

type ConstructionOperation struct {
	Kind    ConstructionOperationKind `json:"kind"`
	Pivot   *ConstructionPivot        `json:"pivot,omitempty"`
	Derive  *ConstructionDerive       `json:"derive,omitempty"`
	Filter  *ConstructionFilter       `json:"filter,omitempty"`
	Unpivot *ConstructionUnpivot      `json:"unpivot,omitempty"`
}

func (o *ConstructionOperation) UnmarshalJSON(raw []byte) error {
	type wire ConstructionOperation
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	*o = ConstructionOperation(value)
	return nil
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

func requiredID(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value)
}
