package publication

import "strings"

type ColumnCapabilities struct {
	Logical      string
	Nullable     bool
	Repeated     bool
	Filterable   bool
	Sortable     bool
	Aggregatable bool
}

func ColumnCapabilitiesFromClickHouse(clickHouseType string) ColumnCapabilities {
	typ := strings.TrimSpace(clickHouseType)
	result := ColumnCapabilities{}
	for {
		switch {
		case strings.HasPrefix(typ, "Nullable(") && strings.HasSuffix(typ, ")"):
			result.Nullable = true
			typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Nullable("), ")")
		case strings.HasPrefix(typ, "Array(") && strings.HasSuffix(typ, ")"):
			result.Repeated = true
			typ = strings.TrimSuffix(strings.TrimPrefix(typ, "Array("), ")")
		default:
			result.Logical = clickHouseLogicalColumnType(typ)
			result.Filterable = true
			result.Sortable = !result.Repeated
			result.Aggregatable = !result.Repeated && result.Logical != "json"
			return result
		}
	}
}

func ResolveColumnCapabilities(clickHouseType, logical string, nullable, repeated bool) ColumnCapabilities {
	result := ColumnCapabilitiesFromClickHouse(clickHouseType)
	if logical != "" {
		result.Logical = logical
	}
	result.Nullable = result.Nullable || nullable
	result.Repeated = result.Repeated || repeated
	result.Sortable = !result.Repeated
	result.Aggregatable = !result.Repeated && result.Logical != "json"
	return result
}

func clickHouseLogicalColumnType(typ string) string {
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
