package proto

import "fmt"

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func int64Value(value any) (int64, error) {
	switch v := value.(type) {
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func int64Must(value any) int64 {
	v, _ := int64Value(value)
	return v
}
