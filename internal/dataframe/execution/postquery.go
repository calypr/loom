package execution

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

const (
	exactUUIDOperationKey = "__loom_exact_uuid_operation"
	exactUUIDArgsKey      = "__loom_exact_uuid_args"
	postQueryCallKey      = "__loom_postquery_call"
	postQueryArgsKey      = "__loom_postquery_args"
	postQueryTargetKey    = "__loom_postquery_target"
)

// DynamicDriftError means a runtime key was not part of the dynamic-column
// schema frozen from the field catalog. It carries schema metadata only, so
// transport adapters can report the mismatch without exposing row values.
type DynamicDriftError struct {
	DynamicName    string
	Key            string
	FrozenKeyCount int
}

func (e *DynamicDriftError) Error() string {
	if e == nil {
		return "dynamic column schema drift"
	}
	return fmt.Sprintf("dynamic map %q emitted unexpected key %q", e.DynamicName, e.Key)
}

func materializePostQueryRowWithChecks(row map[string]any, checks map[string]map[string]DynamicColumnCheck) (map[string]any, error) {
	value, err := materializePostQueryValue(row)
	if err != nil {
		return nil, err
	}
	result, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("post-query row is not an object")
	}
	if err := validateDynamicDrift(result, checks); err != nil {
		return nil, err
	}
	delete(result, "__loom_dynamic_runtime_keys")
	return result, nil
}

func validateDynamicDrift(row map[string]any, checks map[string]map[string]DynamicColumnCheck) error {
	if len(checks) == 0 {
		return nil
	}
	observed, ok := row["__loom_dynamic_runtime_keys"].(map[string]any)
	if !ok {
		return fmt.Errorf("dynamic runtime key metadata is missing")
	}
	for dynamicName, values := range observed {
		allowed := checks[dynamicName]
		items, err := dynamicRuntimeKeys(values)
		if err != nil {
			return fmt.Errorf("dynamic map %q runtime key metadata is malformed", dynamicName)
		}
		for _, value := range items {
			key := fmt.Sprint(value)
			column, ok := allowed[key]
			if !ok {
				if dynamicFamilyAllowsUnknownKeys(allowed) {
					continue
				}
				return &DynamicDriftError{DynamicName: dynamicName, Key: key, FrozenKeyCount: len(allowed)}
			}
			if actual, exists := row[column.ColumnName]; exists && !dynamicValueMatches(actual, column.ValueType) {
				return fmt.Errorf("dynamic map %q column %q has incompatible value type %q", dynamicName, column.ColumnName, column.ValueType)
			}
		}
	}
	return nil
}

func dynamicFamilyAllowsUnknownKeys(checks map[string]DynamicColumnCheck) bool {
	for _, check := range checks {
		if check.AllowUnknownKeys {
			return true
		}
	}
	return false
}

func dynamicRuntimeKeys(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case []string:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = item
		}
		return result, nil
	default:
		return nil, fmt.Errorf("expected an array")
	}
}

func dynamicValueMatches(value any, logicalType string) bool {
	if value == nil || logicalType == "" || logicalType == "unknown" {
		return true
	}
	switch logicalType {
	case "string", "code", "uuid", "date", "datetime":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return math.Trunc(typed) == typed
		case float32:
			return math.Trunc(float64(typed)) == float64(typed)
		case string:
			_, err := strconv.ParseInt(typed, 10, 64)
			return err == nil
		default:
			return false
		}
	case "decimal":
		switch typed := value.(type) {
		case float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case string:
			_, err := strconv.ParseFloat(typed, 64)
			return err == nil
		default:
			return false
		}
	default:
		return true
	}
}

func materializePostQueryValue(value any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		if operation, ok := typed[exactUUIDOperationKey].(string); ok {
			args, ok := typed[exactUUIDArgsKey].([]any)
			if !ok {
				return nil, fmt.Errorf("exact UUID marker arguments are malformed")
			}
			resolvedArgs := make([]any, len(args))
			for index, arg := range args {
				resolved, err := materializePostQueryValue(arg)
				if err != nil {
					return nil, err
				}
				resolvedArgs[index] = resolved
			}
			return computeNamedUUID(operation, resolvedArgs)
		}
		if operation, ok := typed[postQueryCallKey].(string); ok {
			args, ok := typed[postQueryArgsKey].([]any)
			if !ok {
				return nil, fmt.Errorf("post-query call arguments are malformed")
			}
			resolvedArgs := make([]any, len(args))
			for index, arg := range args {
				resolved, err := materializePostQueryValue(arg)
				if err != nil {
					return nil, err
				}
				resolvedArgs[index] = resolved
			}
			target, _ := typed[postQueryTargetKey].(string)
			return evaluatePostQueryCall(operation, target, resolvedArgs)
		}
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			resolved, err := materializePostQueryValue(item)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			resolved, err := materializePostQueryValue(item)
			if err != nil {
				return nil, err
			}
			result[index] = resolved
		}
		return result, nil
	default:
		return value, nil
	}
}

func evaluatePostQueryCall(operation, target string, args []any) (any, error) {
	if operation == "uuid3" || operation == "uuid5" {
		return computeNamedUUID(operation, args)
	}
	isNull := func(value any) bool { return value == nil }
	switch operation {
	case "canonical_json":
		if len(args) != 1 {
			return nil, fmt.Errorf("canonical_json requires one argument")
		}
		encoded, err := json.Marshal(args[0])
		if err != nil {
			return nil, fmt.Errorf("canonical_json: %w", err)
		}
		return string(encoded), nil
	case "coalesce", "fallback":
		for _, value := range args {
			if !isNull(value) {
				return value, nil
			}
		}
		return nil, nil
	case "first":
		if len(args) != 1 {
			return nil, fmt.Errorf("first requires one argument")
		}
		if values, ok := args[0].([]any); ok {
			if len(values) == 0 {
				return nil, nil
			}
			return values[0], nil
		}
		return args[0], nil
	case "all":
		return args, nil
	case "concat":
		var result string
		for _, value := range args {
			if value != nil {
				result += fmt.Sprint(value)
			}
		}
		return result, nil
	case "join":
		if len(args) != 2 {
			return nil, fmt.Errorf("join requires two arguments")
		}
		values, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf("join requires an array")
		}
		separator := fmt.Sprint(args[1])
		parts := make([]string, len(values))
		for index, value := range values {
			parts[index] = fmt.Sprint(value)
		}
		return strings.Join(parts, separator), nil
	case "cast":
		if len(args) != 1 {
			return nil, fmt.Errorf("cast requires one argument")
		}
		return castPostQueryValue(args[0], target)
	case "if":
		if len(args) != 3 {
			return nil, fmt.Errorf("if requires three arguments")
		}
		if truthyPostQuery(args[0]) {
			return args[1], nil
		}
		return args[2], nil
	case "case":
		for index := 0; index+1 < len(args); index += 2 {
			if truthyPostQuery(args[index]) {
				return args[index+1], nil
			}
		}
		if len(args)%2 == 1 {
			return args[len(args)-1], nil
		}
		return nil, nil
	case "not":
		if len(args) != 1 {
			return nil, fmt.Errorf("not requires one argument")
		}
		return !truthyPostQuery(args[0]), nil
	case "and", "or":
		if len(args) < 2 {
			return nil, fmt.Errorf("%s requires two arguments", operation)
		}
		result := operation == "and"
		for _, value := range args {
			if operation == "and" {
				result = result && truthyPostQuery(value)
			} else {
				result = result || truthyPostQuery(value)
			}
		}
		return result, nil
	case "eq", "neq", "gt", "gte", "lt", "lte":
		if len(args) != 2 {
			return nil, fmt.Errorf("%s requires two arguments", operation)
		}
		left, right := fmt.Sprint(args[0]), fmt.Sprint(args[1])
		switch operation {
		case "eq":
			return reflect.DeepEqual(args[0], args[1]) || left == right, nil
		case "neq":
			return !(reflect.DeepEqual(args[0], args[1]) || left == right), nil
		case "gt":
			return left > right, nil
		case "gte":
			return left >= right, nil
		case "lt":
			return left < right, nil
		default:
			return left <= right, nil
		}
	case "contains":
		if len(args) != 2 {
			return nil, fmt.Errorf("contains requires two arguments")
		}
		return strings.Contains(fmt.Sprint(args[0]), fmt.Sprint(args[1])), nil
	default:
		return nil, fmt.Errorf("unsupported nested post-query call %q", operation)
	}
}

func truthyPostQuery(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	default:
		return true
	}
}

func castPostQueryValue(value any, target string) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch target {
	case "string", "code", "uuid", "date", "date_time":
		return fmt.Sprint(value), nil
	case "integer":
		return fmt.Sprint(value), nil
	case "decimal":
		return fmt.Sprint(value), nil
	case "boolean":
		return truthyPostQuery(value), nil
	default:
		return nil, fmt.Errorf("unsupported nested cast target %q", target)
	}
}
