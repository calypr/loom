package semantic

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type RepeatedBoundary struct {
	CanonicalPath string
	OwnerPath     string
	Index         int
}

func decodeObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("walk FHIR path %q: decode object: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("walk FHIR path %q: expected an object", path)
	}
	return object, nil
}

func isNull(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func appendRepeatedBoundary(boundaries []RepeatedBoundary, next RepeatedBoundary) []RepeatedBoundary {
	copyOfBoundaries := make([]RepeatedBoundary, len(boundaries)+1)
	copy(copyOfBoundaries, boundaries)
	copyOfBoundaries[len(boundaries)] = next
	return copyOfBoundaries
}

func cloneRepeatedBoundaries(boundaries []RepeatedBoundary) []RepeatedBoundary {
	return append([]RepeatedBoundary(nil), boundaries...)
}
