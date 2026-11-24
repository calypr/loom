package arango

import (
	"encoding/json"
	"fmt"
)

func decodeSemanticRow(row map[string]any, target any, domain string) error {
	encoded, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("encode %s row: %w", domain, err)
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return fmt.Errorf("decode %s row: %w", domain, err)
	}
	return nil
}
