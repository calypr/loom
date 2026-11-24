package reference

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type Value struct {
	Data    any
	Present bool
}

type Row map[string]Value

func path(value any, selector string) Value {
	parts := strings.Split(strings.TrimPrefix(selector, "root."), ".")
	current := Value{Data: value, Present: true}
	for _, part := range parts {
		if part == "" || !current.Present || current.Data == nil {
			return Value{}
		}
		object, ok := current.Data.(map[string]any)
		if !ok {
			return Value{}
		}
		child, exists := object[part]
		current = Value{Data: child, Present: exists}
	}
	return current
}

func numeric(value any) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case jsonNumber:
		f, err := strconv.ParseFloat(string(n), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

type jsonNumber string

func equal(left, right any) bool {
	if a, ok := numeric(left); ok {
		if b, ok := numeric(right); ok {
			return a == b
		}
	}
	return reflect.DeepEqual(left, right)
}

func scalarValue(value recipe.TableScalar) any {
	switch value.Kind {
	case recipe.TableScalarString:
		if value.String != nil {
			return *value.String
		}
	case recipe.TableScalarInteger:
		if value.Integer != nil {
			return float64(*value.Integer)
		}
	case recipe.TableScalarDecimal:
		if value.Decimal != nil {
			return *value.Decimal
		}
	case recipe.TableScalarBoolean:
		if value.Boolean != nil {
			return *value.Boolean
		}
	}
	return nil
}

func literalValue(value recipe.DerivedLiteral) any {
	if value.Kind == recipe.NumericInteger && value.Integer != nil {
		return float64(*value.Integer)
	}
	if value.Kind == recipe.NumericDecimal && value.Decimal != nil {
		return *value.Decimal
	}
	return nil
}

func expressionValue(document map[string]any, expr recipe.Expression) (Value, error) {
	if expr.Select != "" {
		return path(document, expr.Select), nil
	}
	if expr.Literal != nil {
		var literal any
		decoder := json.NewDecoder(strings.NewReader(string(expr.Literal)))
		decoder.UseNumber()
		if err := decoder.Decode(&literal); err != nil {
			return Value{}, fmt.Errorf("decode literal: %w", err)
		}
		return Value{Data: literal, Present: true}, nil
	}
	return Value{}, fmt.Errorf("reference interpreter supports selector and literal expressions, got call %q", expr.Call)
}

func applyFilter(value Value, filter recipe.ConstructionFilter) (bool, error) {
	if filter.Operator == recipe.FilterExists {
		return value.Present, nil
	}
	if filter.Operator == recipe.FilterMissing {
		return !value.Present, nil
	}
	if !value.Present || value.Data == nil {
		return false, nil
	}
	if len(filter.Values) == 0 {
		return false, fmt.Errorf("filter %s requires a value", filter.Operator)
	}
	want := filterValue(filter.Values[0])
	switch filter.Operator {
	case recipe.FilterEquals:
		return equal(value.Data, want), nil
	case recipe.FilterNotEquals:
		return !equal(value.Data, want), nil
	case recipe.FilterIn:
		for _, item := range filter.Values {
			if equal(value.Data, filterValue(item)) {
				return true, nil
			}
		}
		return false, nil
	case recipe.FilterGreaterThan, recipe.FilterGreaterEq, recipe.FilterLessThan, recipe.FilterLessEq:
		left, lok := numeric(value.Data)
		right, rok := numeric(want)
		if !lok || !rok {
			return false, fmt.Errorf("reference filter %s needs numeric operands", filter.Operator)
		}
		switch filter.Operator {
		case recipe.FilterGreaterThan:
			return left > right, nil
		case recipe.FilterGreaterEq:
			return left >= right, nil
		case recipe.FilterLessThan:
			return left < right, nil
		default:
			return left <= right, nil
		}
	default:
		return false, fmt.Errorf("reference interpreter does not support filter operator %q", filter.Operator)
	}
}

func filterValue(value spec.FilterValue) any {
	switch value.Kind {
	case recipe.FilterString:
		if value.String != nil {
			return *value.String
		}
	case recipe.FilterCode:
		if value.Code != nil {
			return value.Code.Code
		}
	case recipe.FilterDate:
		if value.Date != nil {
			return *value.Date
		}
	case recipe.FilterDateTime:
		if value.DateTime != nil {
			return *value.DateTime
		}
	case recipe.FilterBoolean:
		if value.Boolean != nil {
			return *value.Boolean
		}
	case recipe.FilterInteger:
		if value.Integer != nil {
			return float64(*value.Integer)
		}
	case recipe.FilterDecimal:
		if value.Decimal != nil {
			return *value.Decimal
		}
	default:
		return nil
	}
	return nil
}

func derive(left, right Value, operation recipe.DerivedOperation, missing recipe.MissingInputPolicy, zero recipe.DivisionByZeroPolicy) (Value, error) {
	if !left.Present || !right.Present || left.Data == nil || right.Data == nil {
		if missing == recipe.MissingInputError {
			return Value{}, fmt.Errorf("derived operation %s has a missing input", operation)
		}
		return Value{Present: true}, nil
	}
	a, aok := numeric(left.Data)
	b, bok := numeric(right.Data)
	if !aok || !bok {
		return Value{}, fmt.Errorf("derived operation %s has nonnumeric inputs", operation)
	}
	var result float64
	switch operation {
	case recipe.DerivedAdd:
		result = a + b
	case recipe.DerivedSubtract:
		result = a - b
	case recipe.DerivedMultiply:
		result = a * b
	case recipe.DerivedDivide:
		if b == 0 {
			if zero == recipe.DivisionByZeroError {
				return Value{}, fmt.Errorf("derived operation DIVIDE by zero")
			}
			return Value{Present: true}, nil
		}
		result = a / b
	default:
		return Value{}, fmt.Errorf("unsupported derived operation %q", operation)
	}
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return Value{}, fmt.Errorf("derived operation %s produced a non-finite value", operation)
	}
	return Value{Data: result, Present: true}, nil
}
