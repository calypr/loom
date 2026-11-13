package schema

import (
	"fmt"
	"sort"
	"strings"
)

type FieldKind string

const (
	FieldKindUnknown FieldKind = "unknown"
	FieldKindScalar  FieldKind = "scalar"
	FieldKindObject  FieldKind = "object"
	FieldKindArray   FieldKind = "array"
)

type FieldSemantics struct {
	Kind        FieldKind
	ElementKind FieldKind
	Reference   string
}

type TraversalDirection string

const (
	TraversalOutbound TraversalDirection = "OUTBOUND"
	TraversalInbound  TraversalDirection = "INBOUND"
	TraversalAny      TraversalDirection = "ANY"
)

type TraversalMultiplicity string

const (
	TraversalOne  TraversalMultiplicity = "ONE"
	TraversalMany TraversalMultiplicity = "MANY"
)

type CompilerTraversal struct {
	FromType     string
	EdgeLabel    string
	ToType       string
	Direction    TraversalDirection
	Multiplicity TraversalMultiplicity
}

func ResourceTypes() []string {
	out := append([]string(nil), generatedResourceTypes...)
	sort.Strings(out)
	return out
}

func ConcreteResourceType(resourceType string) (string, bool) {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return "", false
	}
	index := sort.SearchStrings(generatedResourceTypes, resourceType)
	if index < len(generatedResourceTypes) && generatedResourceTypes[index] == resourceType {
		return resourceType, true
	}
	for _, candidate := range generatedResourceTypes {
		if strings.EqualFold(candidate, resourceType) {
			return candidate, true
		}
	}
	return "", false
}

func CanonicalResourceType(resourceType string) (string, bool) {
	return ConcreteResourceType(resourceType)
}

func HasResource(resourceType string) bool {
	_, ok := ConcreteResourceType(resourceType)
	return ok
}

func DefinitionExists(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	_, ok := generatedDefinitions[name]
	return ok
}

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
