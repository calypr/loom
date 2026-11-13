package arango

import (
	"encoding/json"
	"time"
)

func decode[T any](value any) (T, error) {
	var out T
	raw, err := json.Marshal(normalizeUpdatedAt(value))
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

func normalizeUpdatedAt(value any) any {
	row, ok := value.(map[string]any)
	if !ok {
		return value
	}
	normalized := make(map[string]any, len(row))
	for key, item := range row {
		normalized[key] = item
	}
	if timestamp, ok := numericTimestamp(row["updatedAt"]); ok {
		normalized["updatedAt"] = timestamp
	}
	return normalized
}

func numericTimestamp(value any) (string, bool) {
	var millis int64
	switch number := value.(type) {
	case float64:
		millis = int64(number)
	case float32:
		millis = int64(number)
	case int:
		millis = int64(number)
	case int64:
		millis = number
	case uint64:
		millis = int64(number)
	default:
		return "", false
	}
	return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano), true
}
func document(value any, k string) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	doc["_key"] = k
	return doc, nil
}
