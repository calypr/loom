package spec

import (
	"errors"
	"fmt"
	"strings"
	"time"

	fhir "github.com/calypr/loom/generated/fhir"
)

// FilterOperator is the closed set of operations accepted by the typed filter
// AST. Compilers must map these values to bound expressions rather than treat
// them as query fragments.
type FilterOperator string

const (
	FilterEquals      FilterOperator = "EQUALS"
	FilterNotEquals   FilterOperator = "NOT_EQUALS"
	FilterIn          FilterOperator = "IN"
	FilterExists      FilterOperator = "EXISTS"
	FilterMissing     FilterOperator = "MISSING"
	FilterContains    FilterOperator = "CONTAINS_TEXT"
	FilterGreaterThan FilterOperator = "GT"
	FilterGreaterEq   FilterOperator = "GTE"
	FilterLessThan    FilterOperator = "LT"
	FilterLessEq      FilterOperator = "LTE"
)

type FilterValueKind string

const (
	FilterString   FilterValueKind = "STRING"
	FilterCode     FilterValueKind = "CODE"
	FilterBoolean  FilterValueKind = "BOOLEAN"
	FilterInteger  FilterValueKind = "INTEGER"
	FilterDecimal  FilterValueKind = "DECIMAL"
	FilterDate     FilterValueKind = "DATE"
	FilterDateTime FilterValueKind = "DATE_TIME"
)

type ArrayQuantifier string

const (
	QuantifierAny  ArrayQuantifier = "ANY"
	QuantifierAll  ArrayQuantifier = "ALL"
	QuantifierNone ArrayQuantifier = "NONE"
)

// CodeValue preserves the terminology identity instead of filtering only on a
// human-readable display string.
type CodeValue struct {
	System  string `json:"system,omitempty"`
	Code    string `json:"code"`
	Display string `json:"display,omitempty"`
}

// FilterValue is a tagged value. Exactly the member selected by Kind must be
// populated. Pointer scalar fields preserve false, zero, and empty values.
type FilterValue struct {
	Kind     FilterValueKind `json:"kind"`
	String   *string         `json:"string,omitempty"`
	Code     *CodeValue      `json:"code,omitempty"`
	Boolean  *bool           `json:"boolean,omitempty"`
	Integer  *int64          `json:"integer,omitempty"`
	Decimal  *float64        `json:"decimal,omitempty"`
	Date     *string         `json:"date,omitempty"`
	DateTime *string         `json:"dateTime,omitempty"`
}

// TypedFilter identifies a field through a stable reference. It deliberately
// contains no AQL or selector expression supplied by the user.
type TypedFilter struct {
	FieldRef string `json:"fieldRef"`
	// Selector is a resolved, canonical FHIR selector. Product callers should
	// normally supply FieldRef and receive this selector from the semantic
	// registry; the compiler requires it before physical lowering.
	Selector   string          `json:"selector,omitempty"`
	FieldKind  FilterValueKind `json:"fieldKind"`
	Repeated   bool            `json:"repeated,omitempty"`
	Quantifier ArrayQuantifier `json:"quantifier,omitempty"`
	Operator   FilterOperator  `json:"operator"`
	Values     []FilterValue   `json:"values,omitempty"`
}

func (f TypedFilter) Validate() error {
	if strings.TrimSpace(f.FieldRef) == "" {
		return errors.New("filter fieldRef is required")
	}
	if !f.FieldKind.Valid() {
		return fmt.Errorf("unknown filter field kind %q", f.FieldKind)
	}
	if !f.Operator.Valid() {
		return fmt.Errorf("unknown filter operator %q", f.Operator)
	}
	if f.Repeated {
		if !f.Quantifier.Valid() {
			return errors.New("repeated filter requires ANY, ALL, or NONE quantifier")
		}
	} else if f.Quantifier != "" {
		return errors.New("array quantifier is only valid for repeated fields")
	}

	want := 1
	if f.Operator == FilterExists || f.Operator == FilterMissing {
		want = 0
	} else if f.Operator == FilterIn {
		if len(f.Values) == 0 {
			return errors.New("IN filter requires at least one value")
		}
		want = len(f.Values)
	}
	if len(f.Values) != want {
		return fmt.Errorf("operator %s requires %d value(s), got %d", f.Operator, want, len(f.Values))
	}
	if !OperatorSupportsKind(f.Operator, f.FieldKind) {
		return fmt.Errorf("operator %s is not compatible with %s", f.Operator, f.FieldKind)
	}
	for i, value := range f.Values {
		if err := value.Validate(); err != nil {
			return fmt.Errorf("filter value %d: %w", i, err)
		}
		if value.Kind != f.FieldKind {
			return fmt.Errorf("filter value %d kind %s does not match field kind %s", i, value.Kind, f.FieldKind)
		}
		if err := validateOrderedTemporalValue(f.Operator, value); err != nil {
			return fmt.Errorf("filter value %d: %w", i, err)
		}
	}
	return nil
}

func (op FilterOperator) Valid() bool {
	switch op {
	case FilterEquals, FilterNotEquals, FilterIn, FilterExists, FilterMissing,
		FilterContains, FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
		return true
	default:
		return false
	}
}

func (kind FilterValueKind) Valid() bool {
	switch kind {
	case FilterString, FilterCode, FilterBoolean, FilterInteger, FilterDecimal, FilterDate, FilterDateTime:
		return true
	default:
		return false
	}
}

func (q ArrayQuantifier) Valid() bool {
	return q == QuantifierAny || q == QuantifierAll || q == QuantifierNone
}

func OperatorSupportsKind(op FilterOperator, kind FilterValueKind) bool {
	if !op.Valid() || !kind.Valid() {
		return false
	}
	switch op {
	case FilterExists, FilterMissing, FilterEquals, FilterNotEquals, FilterIn:
		return true
	case FilterContains:
		return kind == FilterString
	case FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
		return kind == FilterInteger || kind == FilterDecimal || kind == FilterDate || kind == FilterDateTime
	default:
		return false
	}
}

func (v FilterValue) Validate() error {
	if !v.Kind.Valid() {
		return fmt.Errorf("unknown value kind %q", v.Kind)
	}
	populated := 0
	for _, present := range []bool{v.String != nil, v.Code != nil, v.Boolean != nil, v.Integer != nil, v.Decimal != nil, v.Date != nil, v.DateTime != nil} {
		if present {
			populated++
		}
	}
	if populated != 1 {
		return fmt.Errorf("exactly one typed value member is required, got %d", populated)
	}
	switch v.Kind {
	case FilterString:
		if v.String == nil {
			return errors.New("STRING requires string")
		}
	case FilterCode:
		if v.Code == nil || strings.TrimSpace(v.Code.Code) == "" {
			return errors.New("CODE requires a non-empty code")
		}
	case FilterBoolean:
		if v.Boolean == nil {
			return errors.New("BOOLEAN requires boolean")
		}
	case FilterInteger:
		if v.Integer == nil {
			return errors.New("INTEGER requires integer")
		}
	case FilterDecimal:
		if v.Decimal == nil {
			return errors.New("DECIMAL requires decimal")
		}
	case FilterDate:
		if v.Date == nil {
			return errors.New("DATE requires date")
		}
		if err := fhir.ValidateFhirDate(*v.Date); err != nil {
			return fmt.Errorf("DATE must use a valid FHIR date: %w", err)
		}
	case FilterDateTime:
		if v.DateTime == nil {
			return errors.New("DATE_TIME requires dateTime")
		}
		if err := fhir.ValidateFhirDateTime(*v.DateTime); err != nil {
			return fmt.Errorf("DATE_TIME must use a valid FHIR date-time: %w", err)
		}
	}
	return nil
}

// Ordered comparison has point-in-time semantics, unlike exact FHIR temporal
// matching which can legitimately use partial precision (for example, 2024).
// Require full values before emitting DATE_TIMESTAMP so offsets are normalized
// in AQL rather than compared lexically.
func validateOrderedTemporalValue(operator FilterOperator, value FilterValue) error {
	switch operator {
	case FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
	default:
		return nil
	}
	switch value.Kind {
	case FilterDate:
		if value.Date == nil || len(*value.Date) != len("2006-01-02") {
			return errors.New("ordered DATE comparison requires full YYYY-MM-DD precision")
		}
	case FilterDateTime:
		if value.DateTime == nil || !strings.Contains(*value.DateTime, "T") {
			return errors.New("ordered DATE_TIME comparison requires a full RFC3339 timestamp")
		}
		if _, err := time.Parse(time.RFC3339Nano, *value.DateTime); err != nil {
			return fmt.Errorf("ordered DATE_TIME comparison requires RFC3339 timestamp: %w", err)
		}
	}
	return nil
}
