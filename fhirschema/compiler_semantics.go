package fhirschema

import (
	"fmt"
	"sort"
	"strings"
)

// FieldKind is the compiler-facing shape of a FHIR field. It deliberately
// hides the generated schema representation so callers do not depend on
// generator-private types.
type FieldKind string

const (
	FieldKindUnknown FieldKind = "unknown"
	FieldKindScalar  FieldKind = "scalar"
	FieldKindObject  FieldKind = "object"
	FieldKindArray   FieldKind = "array"
)

// FieldSemantics describes the terminal field at a canonical FHIR path.
// ElementKind and Reference are populated for arrays and objects when the
// generated metadata provides that information.
type FieldSemantics struct {
	Kind        FieldKind
	ElementKind FieldKind
	Reference   string
}

// TraversalDirection is the FHIR reference direction declared by generated
// graph metadata. It is not automatically the physical AQL direction: the
// stored fhir_edge layout and catalog traversal contract decide that lowering
// concern.
type TraversalDirection string

const (
	TraversalOutbound TraversalDirection = "OUTBOUND"
	TraversalInbound  TraversalDirection = "INBOUND"
	TraversalAny      TraversalDirection = "ANY"
)

// TraversalMultiplicity describes whether one source may resolve to one or
// multiple targets. Unknown generated values are rejected rather than guessed.
type TraversalMultiplicity string

const (
	TraversalOne  TraversalMultiplicity = "ONE"
	TraversalMany TraversalMultiplicity = "MANY"
)

// CompilerTraversal is an immutable compiler-facing traversal description.
type CompilerTraversal struct {
	FromType     string
	EdgeLabel    string
	ToType       string
	Direction    TraversalDirection
	Multiplicity TraversalMultiplicity
}

// ResourceTypes returns a sorted copy of the generated FHIR resource types.
func ResourceTypes() []string {
	out := append([]string(nil), generatedResourceTypes...)
	sort.Strings(out)
	return out
}

// HasResource reports whether resourceType is a generated FHIR resource type.
func HasResource(resourceType string) bool {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return false
	}
	index := sort.SearchStrings(generatedResourceTypes, resourceType)
	return index < len(generatedResourceTypes) && generatedResourceTypes[index] == resourceType
}

// ResourceExists is the explicit predicate form of HasResource.
func ResourceExists(resourceType string) bool { return HasResource(resourceType) }

// ResolveFieldSemantics resolves a canonical path using generated schema
// metadata and returns only stable, compiler-safe values.
func ResolveFieldSemantics(resourceType, canonicalPath string) (FieldSemantics, bool) {
	resolved, ok := ResolvePath(resourceType, canonicalPath)
	if !ok {
		return FieldSemantics{}, false
	}
	property := resolved.Property
	semantics := FieldSemantics{Kind: normalizeFieldKind(property.Kind)}
	switch semantics.Kind {
	case FieldKindObject:
		semantics.Reference = property.Ref
	case FieldKindArray:
		semantics.ElementKind = normalizeFieldKind(property.ItemKind)
		semantics.Reference = property.ItemRef
	}
	return semantics, true
}

// ResolveCompilerTraversal looks up and normalizes generated traversal
// metadata. Conflicting or unknown generated values return an error so the
// compiler cannot silently choose unsafe graph semantics.
func ResolveCompilerTraversal(fromType, edgeLabel, toType string) (CompilerTraversal, bool, error) {
	spec, ok := LookupTraversal(fromType, edgeLabel, toType)
	if !ok {
		return CompilerTraversal{}, false, nil
	}
	direction, err := normalizeTraversalDirection(spec.Direction)
	if err != nil {
		return CompilerTraversal{}, true, err
	}
	multiplicity, err := normalizeTraversalMultiplicity(spec.Multiplicity)
	if err != nil {
		return CompilerTraversal{}, true, err
	}
	return CompilerTraversal{
		FromType:     spec.FromType,
		EdgeLabel:    spec.EdgeLabel,
		ToType:       spec.ToType,
		Direction:    direction,
		Multiplicity: multiplicity,
	}, true, nil
}

func normalizeFieldKind(kind string) FieldKind {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "object":
		return FieldKindObject
	case "array":
		return FieldKindArray
	case "", "null":
		return FieldKindUnknown
	default:
		return FieldKindScalar
	}
}

func normalizeTraversalDirection(values []string) (TraversalDirection, error) {
	seen := map[TraversalDirection]bool{}
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "outbound", "out":
			seen[TraversalOutbound] = true
		case "inbound", "in":
			seen[TraversalInbound] = true
		case "any", "both":
			seen[TraversalAny] = true
		case "":
		default:
			return "", fmt.Errorf("unsupported traversal direction %q", value)
		}
	}
	if seen[TraversalAny] || (seen[TraversalOutbound] && seen[TraversalInbound]) {
		return TraversalAny, nil
	}
	if seen[TraversalOutbound] {
		return TraversalOutbound, nil
	}
	if seen[TraversalInbound] {
		return TraversalInbound, nil
	}
	return "", fmt.Errorf("traversal direction is missing")
}

func normalizeTraversalMultiplicity(values []string) (TraversalMultiplicity, error) {
	seenOne := false
	seenMany := false
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "has_one", "one", "0..1", "1..1":
			seenOne = true
		case "has_many", "many", "0..*", "1..*", "*":
			seenMany = true
		case "":
		default:
			return "", fmt.Errorf("unsupported traversal multiplicity %q", value)
		}
	}
	if seenMany {
		return TraversalMany, nil
	}
	if seenOne {
		return TraversalOne, nil
	}
	return "", fmt.Errorf("traversal multiplicity is missing")
}
