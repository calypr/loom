package schema

import "strings"

type PrimitiveKind string

const (
	PrimitiveUnknown  PrimitiveKind = "unknown"
	PrimitiveString   PrimitiveKind = "string"
	PrimitiveBoolean  PrimitiveKind = "boolean"
	PrimitiveInteger  PrimitiveKind = "integer"
	PrimitiveDecimal  PrimitiveKind = "decimal"
	PrimitiveDate     PrimitiveKind = "date"
	PrimitiveDateTime PrimitiveKind = "date_time"
)

type TerminalScalarMetadata struct {
	Primitive PrimitiveKind
	Repeated  bool
}

func ResolveTerminalScalarMetadata(resourceType, canonicalPath string) (TerminalScalarMetadata, bool) {
	canonicalPath = CanonicalizePath(canonicalPath)
	resolved, ok := ResolvePath(resourceType, canonicalPath)
	if !ok {
		return TerminalScalarMetadata{}, false
	}
	repeated := strings.Contains(canonicalPath, "[]")
	property := resolved.Property
	if property.Kind == "array" {
		return TerminalScalarMetadata{
			Primitive: primitiveKind(property.ItemKind, property.ItemFormat),
			Repeated:  true,
		}, true
	}
	return TerminalScalarMetadata{
		Primitive: primitiveKind(property.Kind, property.Format),
		Repeated:  repeated,
	}, true
}

func primitiveKind(kind, format string) PrimitiveKind {
	if kind == "string" {
		switch format {
		case "date":
			return PrimitiveDate
		case "date-time":
			return PrimitiveDateTime
		}
	}
	switch kind {
	case "string":
		return PrimitiveString
	case "boolean":
		return PrimitiveBoolean
	case "integer":
		return PrimitiveInteger
	case "number":
		return PrimitiveDecimal
	default:
		return PrimitiveUnknown
	}
}
