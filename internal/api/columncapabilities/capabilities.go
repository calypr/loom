// Package columncapabilities defines the transport-neutral capability policy
// derived from a published ClickHouse column type.
package columncapabilities

import "strings"

// Tuple is the shared selector capability contract for a published column.
type Tuple struct {
	Logical      string
	Nullable     bool
	Repeated     bool
	Filterable   bool
	Sortable     bool
	Aggregatable bool
}

// FromClickHouse classifies nested Nullable and Array wrappers before
// applying the primitive type policy. Wrapper order is intentionally
// insignificant because ClickHouse can emit either nesting order.
func FromClickHouse(clickHouseType string) Tuple {
	typ := strings.TrimSpace(clickHouseType)
	result := Tuple{}
	for {
		switch {
		case strings.HasPrefix(typ, "Nullable(") && strings.HasSuffix(typ, ")"):
			result.Nullable = true
			typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Nullable("), ")")
		case strings.HasPrefix(typ, "Array(") && strings.HasSuffix(typ, ")"):
			result.Repeated = true
			typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Array("), ")")
		default:
			result.Logical = logicalType(typ)
			result.Filterable = true
			result.Sortable = !result.Repeated
			result.Aggregatable = !result.Repeated && result.Logical != "json"
			return result
		}
	}
}

func logicalType(typ string) string {
	switch {
	case strings.HasPrefix(typ, "Bool"):
		return "boolean"
	case strings.HasPrefix(typ, "Int"), strings.HasPrefix(typ, "UInt"):
		return "integer"
	case strings.HasPrefix(typ, "Float"), strings.HasPrefix(typ, "Decimal"):
		return "number"
	case strings.HasPrefix(typ, "Date"):
		return "date"
	case strings.HasPrefix(typ, "String"), strings.HasPrefix(typ, "FixedString"), strings.HasPrefix(typ, "UUID"):
		return "string"
	default:
		return "json"
	}
}
