package db

import (
	"crypto/sha256"
	"encoding/hex"

	"encoding/json"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

func Decode[T any](value any) (T, error) {
	return arangostore.DecodeValue[T](NormalizeUpdatedAt(value))
}

func NormalizeUpdatedAt(value any) any {
	row, ok := value.(map[string]any)
	if !ok {
		return value
	}
	normalized := make(map[string]any, len(row))
	for key, item := range row {
		normalized[key] = item
	}
	if timestamp, ok := NumericTimestamp(row["updatedAt"]); ok {
		normalized["updatedAt"] = timestamp
	}
	return normalized
}

func NumericTimestamp(value any) (string, bool) {
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
func Document(value any, k string) (map[string]any, error) {
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

func Key(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}
