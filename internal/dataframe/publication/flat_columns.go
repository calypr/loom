package publication

import (
	"fmt"
	"strings"
	"unicode"
)

// FlatColumnName is the stable column contract for Explorer-facing dataframe
// indexes. Root fields carry the root resource/table prefix; traversal fields
// already carry their alias path and remain unchanged.
func FlatColumnName(resourceType, name string) string {
	if name == "" || name == "auth_resource_path" || name == "_key" || strings.HasPrefix(name, "__loom_") || strings.Contains(name, "__") {
		return name
	}
	prefix := flatResourcePrefix(resourceType)
	if prefix == "" || strings.HasPrefix(name, prefix+"_") {
		return name
	}
	return prefix + "_" + name
}

func flatResourcePrefix(resourceType string) string {
	var builder strings.Builder
	for index, r := range strings.TrimSpace(resourceType) {
		if unicode.IsUpper(r) && index > 0 {
			builder.WriteByte('_')
		}
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

// QualifyFlatRow applies the same naming contract as FlatColumnName and
// rejects source fields that would collapse onto one published column.
func QualifyFlatRow(resourceType string, row map[string]any) (map[string]any, error) {
	if row == nil {
		return nil, nil
	}
	result := make(map[string]any, len(row))
	sources := make(map[string]string, len(row))
	for name, value := range row {
		qualified := FlatColumnName(resourceType, name)
		if previous, exists := sources[qualified]; exists && previous != name {
			return nil, fmt.Errorf("flat column naming collision for %q", qualified)
		}
		sources[qualified] = name
		result[qualified] = value
	}
	return result, nil
}
