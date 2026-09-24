package schema

import "strings"

// PrimitiveKind is the primitive shape reliably represented by generated
// schema metadata. Date and date-time are exposed only when the source graph
// schema declares the corresponding JSON Schema format; code semantics remain
// distinct from arbitrary generated strings.
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

// TerminalScalarMetadata describes the terminal property of a canonical FHIR
// path without exposing generator-private property types. Primitive is unknown
// when the terminal is an object or an array of objects.
type TerminalScalarMetadata struct {
	Primitive PrimitiveKind
	Repeated  bool
}

// ResolveTerminalLogicalType includes complete structured values without
// classifying an object as a scalar or selecting one of its members.
func ResolveTerminalLogicalType(resourceType, canonicalPath string) (string, bool) {
	metadata, ok := ResolveTerminalScalarMetadata(resourceType, canonicalPath)
	if !ok {
		return "", false
	}
	if metadata.Primitive != PrimitiveUnknown {
		return string(metadata.Primitive), true
	}
	shape, ok := ResolveFieldSemantics(resourceType, canonicalPath)
	if ok && (shape.Kind == FieldKindObject || shape.Kind == FieldKindArray && shape.ElementKind == FieldKindObject) {
		return "object", true
	}
	return "", false
}

// ResolveTerminalScalarMetadata resolves compiler-facing primitive and
// repetition facts from the active generated definition.
func ResolveTerminalScalarMetadata(resourceType, canonicalPath string) (TerminalScalarMetadata, bool) {
	canonicalPath = CanonicalizePath(canonicalPath)
	resolved, ok := ResolvePath(resourceType, canonicalPath)
	if !ok {
		return TerminalScalarMetadata{}, false
	}
	// A scalar below any repeated parent can yield more than one value per
	// resource. Looking at only the terminal generated property would classify
	// component[].valueInteger as scalar, which would let the compiler apply an
	// incorrect scalar filter or projection.
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
