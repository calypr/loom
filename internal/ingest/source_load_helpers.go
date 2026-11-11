package ingest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func newSourceLeaseOwner() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("create source lease owner: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func numericInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}
