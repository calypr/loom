package spec

import (
	"errors"
	"fmt"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"

	"strings"
	"time"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	fhir "github.com/calypr/loom/fhir/model"
)

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

type FilterValueCountRule uint8

const (
	FilterValueCountInvalid FilterValueCountRule = iota
	FilterValueCountExactlyOne
	FilterValueCountOneOrMore
	FilterValueCountNone
)

func (op FilterOperator) ValueCountRule() FilterValueCountRule {
	switch op {
	case FilterExists, FilterMissing:
		return FilterValueCountNone
	case FilterIn:
		return FilterValueCountOneOrMore
	case FilterEquals, FilterNotEquals, FilterContains, FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
		return FilterValueCountExactlyOne
	default:
		return FilterValueCountInvalid
	}
}

func (rule FilterValueCountRule) Accepts(count int) bool {
	switch rule {
	case FilterValueCountExactlyOne:
		return count == 1
	case FilterValueCountOneOrMore:
		return count >= 1
	case FilterValueCountNone:
		return count == 0
	default:
		return false
	}
}

type ArrayQuantifier string

const (
	QuantifierAny  ArrayQuantifier = "ANY"
	QuantifierAll  ArrayQuantifier = "ALL"
	QuantifierNone ArrayQuantifier = "NONE"
)

type CodeValue struct {
	System  string `json:"system,omitempty"`
	Code    string `json:"code"`
	Display string `json:"display,omitempty"`
	Version string `json:"version,omitempty"`
}

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

type TypedFilter struct {
	FieldRef string `json:"fieldRef"`

	Selector    string                         `json:"selector,omitempty"`
	FieldKind   FilterValueKind                `json:"fieldKind"`
	Repeated    bool                           `json:"repeated,omitempty"`
	Quantifier  ArrayQuantifier                `json:"quantifier,omitempty"`
	Operator    FilterOperator                 `json:"operator"`
	Values      []FilterValue                  `json:"values,omitempty"`
	Correlation *fhirbinding.CorrelatedBinding `json:"correlation,omitempty"`
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

	countRule := f.Operator.ValueCountRule()
	if !countRule.Accepts(len(f.Values)) {
		switch countRule {
		case FilterValueCountNone:
			return fmt.Errorf("operator %s requires 0 value(s), got %d", f.Operator, len(f.Values))
		case FilterValueCountOneOrMore:
			return errors.New("IN filter requires at least one value")
		default:
			return fmt.Errorf("operator %s requires 1 value(s), got %d", f.Operator, len(f.Values))
		}
	}
	if !OperatorSupportsKind(f.Operator, f.FieldKind) {
		return fmt.Errorf("operator %s is not compatible with %s", f.Operator, f.FieldKind)
	}
	for i, value := range f.Values {
		requireCode := f.Correlation == nil || f.Correlation.SelectionScope != fhirbinding.CorrelatedSelectionScopeSystemVersion
		if err := value.validate(requireCode); err != nil {
			return fmt.Errorf("filter value %d: %w", i, err)
		}
		if value.Kind != f.FieldKind {
			return fmt.Errorf("filter value %d kind %s does not match field kind %s", i, value.Kind, f.FieldKind)
		}
		if err := validateOrderedTemporalValue(f.Operator, value); err != nil {
			return fmt.Errorf("filter value %d: %w", i, err)
		}
	}
	if f.Correlation != nil && f.FieldKind != FilterCode {
		return errors.New("correlated filter requires CODE field kind")
	}
	if f.Correlation != nil {
		if f.Operator != FilterEquals {
			return fmt.Errorf("correlated filter supports only EQUALS, got %s", f.Operator)
		}
		if f.Quantifier != "" && f.Quantifier != QuantifierAny {
			return fmt.Errorf("correlated filter supports only ANY quantifier, got %s", f.Quantifier)
		}
		if len(f.Values) != 1 || f.Values[0].Code == nil || strings.TrimSpace(f.Values[0].Code.System) == "" {
			return errors.New("correlated filter requires one CODE value with system")
		}
		code := f.Values[0].Code
		if err := fhirbinding.ValidateCorrelatedKey(f.Correlation.SelectionScope, fhirbinding.CorrelatedKey{System: code.System, Code: code.Code, Version: code.Version}); err != nil {
			return err
		}
	}
	return nil
}

func (op FilterOperator) Valid() bool {
	return op.ValueCountRule() != FilterValueCountInvalid
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
	return v.validate(true)
}

func (v FilterValue) validate(requireCode bool) error {
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
		if v.Code == nil || (requireCode && strings.TrimSpace(v.Code.Code) == "") {
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

func ValidateTypedFilterForResource(resourceType string, filter TypedFilter) error {
	if err := filter.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(filter.Selector) == "" {
		return fmt.Errorf("filter %q requires a resolved selector", filter.FieldRef)
	}
	selector, err := ParseSelector(filter.Selector)
	if err != nil {
		return fmt.Errorf("filter %q selector: %w", filter.FieldRef, err)
	}
	if _, _, err := selectorCardinality(resourceType, selector); err != nil {
		return fmt.Errorf("filter %q selector: %w", filter.FieldRef, err)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, selector.CanonicalPath())
	if !ok {
		return fmt.Errorf("filter selector %q is not represented by generated resource type %q", filter.Selector, resourceType)
	}
	if filter.Repeated != metadata.Repeated {
		return fmt.Errorf("filter selector %q repeated=%t does not match generated cardinality repeated=%t", filter.Selector, filter.Repeated, metadata.Repeated)
	}
	if !filterKindMatchesGeneratedPrimitive(filter.FieldKind, metadata.Primitive, selector.CanonicalPath()) {
		return fmt.Errorf("filter selector %q has generated primitive %q, incompatible with filter value kind %q", filter.Selector, metadata.Primitive, filter.FieldKind)
	}
	if filter.FieldKind == FilterCode {
		for _, value := range filter.Values {
			if value.Code == nil {
				continue
			}
			if filter.Correlation != nil {
				if strings.TrimSpace(value.Code.System) == "" {
					return fmt.Errorf("filter %q correlated CODE requires a non-empty system", filter.FieldRef)
				}
				checked, err := fhirbinding.ValidateCorrelatedBinding(resourceType, *filter.Correlation)
				if err != nil {
					return fmt.Errorf("filter %q correlated binding: %w", filter.FieldRef, err)
				}
				if checked.VersionSelector == nil && value.Code.Version != "" {
					return fmt.Errorf("filter %q supplies a version without a correlated versionPath", filter.FieldRef)
				}
				continue
			}
			if strings.TrimSpace(value.Code.System) != "" || strings.TrimSpace(value.Code.Display) != "" || value.Code.Version != "" {
				return fmt.Errorf("filter %q supplies code system/display/version without a paired Coding binding", filter.FieldRef)
			}
		}
	}
	return nil
}

func filterKindMatchesGeneratedPrimitive(kind FilterValueKind, primitive fhirschema.PrimitiveKind, canonicalPath string) bool {
	switch primitive {
	case fhirschema.PrimitiveString:
		if kind == FilterString {
			return true
		}

		return kind == FilterCode && (canonicalPath == "code" || strings.HasSuffix(canonicalPath, ".code"))
	case fhirschema.PrimitiveBoolean:
		return kind == FilterBoolean
	case fhirschema.PrimitiveInteger:
		return kind == FilterInteger
	case fhirschema.PrimitiveDecimal:
		return kind == FilterDecimal
	case fhirschema.PrimitiveDate:
		return kind == FilterDate
	case fhirschema.PrimitiveDateTime:
		return kind == FilterDateTime
	default:
		return false
	}
}
