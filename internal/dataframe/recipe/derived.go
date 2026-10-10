package recipe

// DerivedColumn is one named arithmetic projection over values already
// present in an output row. It contains no FHIR selectors or backend syntax.
type DerivedColumn struct {
	ConstructionID       string               `json:"constructionId"`
	Name                 string               `json:"name"`
	Label                string               `json:"label"`
	Operation            DerivedOperation     `json:"operation"`
	Left                 DerivedOperand       `json:"left"`
	Right                DerivedOperand       `json:"right"`
	MissingInputPolicy   MissingInputPolicy   `json:"missingInputPolicy"`
	DivisionByZeroPolicy DivisionByZeroPolicy `json:"divisionByZeroPolicy,omitempty"`
}

type DerivedOperation string

const (
	DerivedAdd      DerivedOperation = "ADD"
	DerivedSubtract DerivedOperation = "SUBTRACT"
	DerivedMultiply DerivedOperation = "MULTIPLY"
	DerivedDivide   DerivedOperation = "DIVIDE"
)

type MissingInputPolicy string

const (
	MissingInputPropagateNull MissingInputPolicy = "PROPAGATE_NULL"
	MissingInputError         MissingInputPolicy = "ERROR"
)

type DivisionByZeroPolicy string

const (
	DivisionByZeroNull  DivisionByZeroPolicy = "NULL"
	DivisionByZeroError DivisionByZeroPolicy = "ERROR"
)

// DerivedOperand is either a reference to a named output column or a typed
// numeric literal. Validate enforces the tagged union at the recipe boundary.
type DerivedOperand struct {
	Kind    DerivedOperandKind `json:"kind"`
	Column  string             `json:"column,omitempty"`
	Literal *DerivedLiteral    `json:"literal,omitempty"`
}

type DerivedOperandKind string

const (
	DerivedColumnOperand  DerivedOperandKind = "COLUMN"
	DerivedLiteralOperand DerivedOperandKind = "LITERAL"
)

type DerivedLiteral struct {
	Kind    NumericKind `json:"kind"`
	Integer *int64      `json:"integer,omitempty"`
	Decimal *float64    `json:"decimal,omitempty"`
}

type NumericKind string

const (
	NumericInteger NumericKind = "INTEGER"
	NumericDecimal NumericKind = "DECIMAL"
)
