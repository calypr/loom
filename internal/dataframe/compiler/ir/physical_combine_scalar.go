package ir

import (
	"strings"
)

// ClickHouseCombineScalarBaseType returns the scalar base type accepted for a
// combine key or append input. KEY_JOIN, MEMBERSHIP, and APPEND accept nullable
// scalars; Membership's ordinary equality treats NULL as a nonmatch.
func ClickHouseCombineScalarBaseType(value string, kind PhysicalCombineKind) (string, bool) {
	physical := strings.TrimSpace(value)
	if physical == "" {
		return "", false
	}
	nullable := strings.HasPrefix(physical, "Nullable(") && strings.HasSuffix(physical, ")")
	if nullable {
		physical = strings.TrimSuffix(strings.TrimPrefix(physical, "Nullable("), ")")
	}
	if physical == "" || strings.HasPrefix(physical, "Nullable(") || strings.HasPrefix(physical, "Array(") {
		return "", false
	}
	if nullable && kind != PhysicalCombineKeyJoin && kind != PhysicalCombineMembership && kind != PhysicalCombineAppend {
		return "", false
	}
	switch kind {
	case PhysicalCombineKeyJoin, PhysicalCombineMembership:
		if physical == "String" || physical == "Bool" || strings.HasPrefix(physical, "Int") ||
			strings.HasPrefix(physical, "UInt") || strings.HasPrefix(physical, "Decimal") || strings.HasPrefix(physical, "Date") {
			return physical, true
		}
	case PhysicalCombineAppend:
		switch physical {
		case "String", "UUID", "Date", "DateTime64(3)", "Bool", "Int64", "Float64":
			return physical, true
		}
	}
	return "", false
}
