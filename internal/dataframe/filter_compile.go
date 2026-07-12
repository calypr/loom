package dataframe

import (
	"fmt"
	"strings"
)

func (c *compiler) compileTypedFilters(payloadVar string, filters []TypedFilter) (string, error) {
	if len(filters) == 0 {
		return "true", nil
	}
	parts := make([]string, 0, len(filters))
	for _, filter := range filters {
		expr, err := c.compileTypedFilter(payloadVar, filter)
		if err != nil {
			return "", err
		}
		parts = append(parts, "("+expr+")")
	}
	return strings.Join(parts, " AND "), nil
}

func (c *compiler) compileTypedFilter(payloadVar string, filter TypedFilter) (string, error) {
	if err := filter.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(filter.Selector) == "" {
		return "", fmt.Errorf("filter %q requires a resolved selector", filter.FieldRef)
	}
	selector, err := ParseSelector(filter.Selector)
	if err != nil {
		return "", fmt.Errorf("filter %q selector: %w", filter.FieldRef, err)
	}
	values := compileSelectorArrayExpr(payloadVar, selector, c)
	if filter.Operator == FilterExists {
		return fmt.Sprintf("LENGTH(%s) > 0", values), nil
	}
	if filter.Operator == FilterMissing {
		return fmt.Sprintf("LENGTH(%s) == 0", values), nil
	}

	match, err := c.compileTypedFilterMatch("__value", filter)
	if err != nil {
		return "", err
	}
	quantifier := filter.Quantifier
	if !filter.Repeated {
		quantifier = QuantifierAny
	}
	switch quantifier {
	case QuantifierAny:
		return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER %s LIMIT 1 RETURN 1) > 0", values, match), nil
	case QuantifierNone:
		return fmt.Sprintf("LENGTH(FOR __value IN %s FILTER %s LIMIT 1 RETURN 1) == 0", values, match), nil
	case QuantifierAll:
		return fmt.Sprintf("LENGTH(%s) > 0 AND LENGTH(FOR __value IN %s FILTER NOT (%s) LIMIT 1 RETURN 1) == 0", values, values, match), nil
	default:
		return "", fmt.Errorf("filter %q has unsupported quantifier %q", filter.FieldRef, quantifier)
	}
}

func (c *compiler) compileTypedFilterMatch(valueVar string, filter TypedFilter) (string, error) {
	if filter.Operator == FilterIn {
		values := make([]any, 0, len(filter.Values))
		for _, value := range filter.Values {
			literal, err := filterLiteral(value)
			if err != nil {
				return "", err
			}
			values = append(values, literal)
		}
		bind := c.newBind("filter_in", values)
		return fmt.Sprintf("POSITION(@%s, %s, true)", bind, valueVar), nil
	}
	if len(filter.Values) != 1 {
		return "", fmt.Errorf("filter %q requires one value", filter.FieldRef)
	}
	literal, err := filterLiteral(filter.Values[0])
	if err != nil {
		return "", err
	}
	bind := c.newBind("filter_value", literal)
	left := valueVar
	right := "@" + bind
	if filter.FieldKind == FilterDate || filter.FieldKind == FilterDateTime {
		switch filter.Operator {
		case FilterGreaterThan, FilterGreaterEq, FilterLessThan, FilterLessEq:
			left = "DATE_TIMESTAMP(" + valueVar + ")"
			right = "DATE_TIMESTAMP(@" + bind + ")"
		}
	}
	switch filter.Operator {
	case FilterEquals:
		return fmt.Sprintf("%s == @%s", left, bind), nil
	case FilterNotEquals:
		return fmt.Sprintf("%s != @%s", left, bind), nil
	case FilterContains:
		return fmt.Sprintf("CONTAINS(TO_STRING(%s), @%s)", valueVar, bind), nil
	case FilterGreaterThan:
		return fmt.Sprintf("%s > %s", left, right), nil
	case FilterGreaterEq:
		return fmt.Sprintf("%s >= %s", left, right), nil
	case FilterLessThan:
		return fmt.Sprintf("%s < %s", left, right), nil
	case FilterLessEq:
		return fmt.Sprintf("%s <= %s", left, right), nil
	default:
		return "", fmt.Errorf("filter %q uses unsupported operator %q", filter.FieldRef, filter.Operator)
	}
}

func filterLiteral(value FilterValue) (any, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	switch value.Kind {
	case FilterString:
		return *value.String, nil
	case FilterCode:
		return value.Code.Code, nil
	case FilterBoolean:
		return *value.Boolean, nil
	case FilterInteger:
		return *value.Integer, nil
	case FilterDecimal:
		return *value.Decimal, nil
	case FilterDate:
		return *value.Date, nil
	case FilterDateTime:
		return *value.DateTime, nil
	default:
		return nil, fmt.Errorf("unsupported filter value kind %q", value.Kind)
	}
}
