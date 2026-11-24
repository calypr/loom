package recipe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

type Filter struct {
	Select     string          `json:"select"`
	FieldRef   string          `json:"fieldRef,omitempty"`
	Operator   FilterOperator  `json:"operator"`
	Quantifier ArrayQuantifier `json:"quantifier,omitempty"`
	Values     []FilterValue   `json:"values,omitempty"`
}

type FilterOperator = spec.FilterOperator

const (
	FilterEquals      = spec.FilterEquals
	FilterNotEquals   = spec.FilterNotEquals
	FilterIn          = spec.FilterIn
	FilterExists      = spec.FilterExists
	FilterMissing     = spec.FilterMissing
	FilterContains    = spec.FilterContains
	FilterGreaterThan = spec.FilterGreaterThan
	FilterGreaterEq   = spec.FilterGreaterEq
	FilterLessThan    = spec.FilterLessThan
	FilterLessEq      = spec.FilterLessEq
)

type ArrayQuantifier = spec.ArrayQuantifier

const (
	QuantifierAny  = spec.QuantifierAny
	QuantifierAll  = spec.QuantifierAll
	QuantifierNone = spec.QuantifierNone
)

type FilterValueKind = spec.FilterValueKind

const (
	FilterString   = spec.FilterString
	FilterCode     = spec.FilterCode
	FilterBoolean  = spec.FilterBoolean
	FilterInteger  = spec.FilterInteger
	FilterDecimal  = spec.FilterDecimal
	FilterDate     = spec.FilterDate
	FilterDateTime = spec.FilterDateTime
)

type CodeValue = spec.CodeValue

type FilterValue = spec.FilterValue

func (f Filter) Validate() error { return f.validateAt("$") }

func (f Filter) validateAt(path string) error {
	if strings.TrimSpace(f.Select) == "" {
		return validationError("required", path+".select", "select is required")
	}
	if !f.Operator.Valid() {
		return validationError("invalid_filter_operator", path+".operator", fmt.Sprintf("unsupported operator %q", f.Operator))
	}
	if f.Quantifier != "" && !f.Quantifier.Valid() {
		return validationError("invalid_filter_quantifier", path+".quantifier", fmt.Sprintf("unsupported quantifier %q", f.Quantifier))
	}
	countRule := f.Operator.ValueCountRule()
	if !countRule.Accepts(len(f.Values)) {
		message := "operator requires exactly one value"
		switch countRule {
		case spec.FilterValueCountNone:
			message = "EXISTS and MISSING do not accept values"
		case spec.FilterValueCountOneOrMore:
			message = "IN requires at least one value"
		}
		return validationError("invalid_filter_values", path+".values", message)
	}
	var kind FilterValueKind
	for i, value := range f.Values {
		if err := value.Validate(); err != nil {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d]", path, i), err.Error())
		}
		if i == 0 {
			kind = value.Kind
		} else if value.Kind != kind {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d].kind", path, i), "all values must use the same kind")
		}
		if !spec.OperatorSupportsKind(f.Operator, value.Kind) {
			return validationError("invalid_filter_value", fmt.Sprintf("%s.values[%d].kind", path, i), fmt.Sprintf("operator %s is incompatible with %s", f.Operator, value.Kind))
		}
	}
	return nil
}

func validateRichFilterAt(filter Filter, path string, allowQuantifier bool) error {
	if err := filter.validateAt(path); err != nil {
		return err
	}
	if filter.Quantifier != "" && !allowQuantifier {
		return validationError("unsupported_filter_quantifier", path+".quantifier", "rich shaping predicates do not support quantifiers")
	}
	switch filter.Operator {
	case FilterExists:
		return nil
	case FilterEquals:
		if len(filter.Values) != 1 || (filter.Values[0].Kind != FilterString && filter.Values[0].Kind != FilterCode) {
			return validationError("unsupported_filter_value", path+".values", "rich shaping equality supports STRING and CODE values")
		}
		return nil
	default:
		return validationError("unsupported_filter_operator", path+".operator", "rich shaping predicates support only EXISTS and EQUALS")
	}
}
