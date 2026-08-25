package explorer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// canonicalJSONBytes returns compact JSON with deterministic object-key
// ordering. It normalizes raw JSON fields before they participate in a
// content address, so persistence stores that materialize objects and then
// reconstruct them cannot change the digest merely by changing key order.
func canonicalJSONBytes(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing JSON: %w", err)
	}

	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return canonical, nil
}
