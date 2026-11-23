package capabilitydb

import (
	"context"
	"encoding/json"
	"time"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

func decode[T any](value any) (T, error) {
	return store.DecodeValue[T](normalizeUpdatedAt(value))
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
	switch typed := value.(type) {
	case int64:
		millis = typed
	case int:
		millis = int64(typed)
	case float64:
		millis = int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return "", false
		}
		millis = parsed
	default:
		return "", false
	}
	if millis <= 0 {
		return "", false
	}
	return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano), true
}
