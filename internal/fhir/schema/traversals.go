package schema

import "strings"

// LookupTraversal returns a defensive copy of the generated traversal record.
func LookupTraversal(fromType, edgeLabel, toType string) (TraversalSpec, bool) {
	spec, ok := generatedTraversals[traversalKey(fromType, edgeLabel, toType)]
	if ok {
		return cloneTraversalSpec(spec), true
	}
	return lookupNestedTraversal(fromType, edgeLabel, toType)
}

func lookupNestedTraversal(fromType, edgeLabel, toType string) (TraversalSpec, bool) {
	for _, rule := range generatedNestedTraversals {
		if prefix, ok := nestedLabelPrefix(edgeLabel, rule.Relation); ok {
			if rule.TargetType == toType && schemaPathReachesNestedType(fromType, rule.NestedType, prefix) {
				return nestedTraversalSpec(rule, fromType, edgeLabel, toType, prefix), true
			}
			if rule.TargetType == fromType && schemaPathReachesNestedType(toType, rule.NestedType, prefix) {
				return nestedTraversalSpec(rule, fromType, edgeLabel, toType, prefix), true
			}
		}
		for _, backref := range rule.Backref {
			prefix, ok := nestedLabelPrefix(edgeLabel, backref)
			if ok && rule.TargetType == fromType && schemaPathReachesNestedType(toType, rule.NestedType, prefix) {
				return nestedTraversalSpec(rule, fromType, edgeLabel, toType, prefix), true
			}
		}
	}
	return TraversalSpec{}, false
}

func nestedLabelPrefix(edgeLabel, relation string) (string, bool) {
	if relation == "" {
		return "", false
	}
	suffix := "_" + relation
	if !strings.HasSuffix(edgeLabel, suffix) {
		return "", false
	}
	prefix := strings.TrimSuffix(edgeLabel, suffix)
	return prefix, prefix != ""
}

func schemaPathReachesNestedType(rootType, nestedType, expectedPath string) bool {
	if expectedPath == "" || !isFHIRResourceType(rootType) {
		return false
	}
	var visitDefinition func(string, string) bool
	var visitProperty func(generatedProperty, string) bool
	pathCanMatch := func(path string) bool {
		if path == "" {
			return true
		}
		return path == expectedPath || strings.HasPrefix(expectedPath, path+"_")
	}
	joinPath := func(path, field string) string {
		if path == "" {
			return field
		}
		return path + "_" + field
	}
	visitDefinition = func(typeName, path string) bool {
		if typeName == nestedType && path == expectedPath {
			return true
		}
		if !pathCanMatch(path) || (path != "" && isFHIRResourceType(typeName)) {
			return false
		}
		definition, ok := generatedDefinitions[typeName]
		if !ok {
			return false
		}
		for _, property := range definition.Properties {
			if visitProperty(property, joinPath(path, property.Name)) {
				return true
			}
		}
		return false
	}
	visitProperty = func(property generatedProperty, path string) bool {
		if !pathCanMatch(path) {
			return false
		}
		if property.Ref != "" && visitDefinition(property.Ref, path) {
			return true
		}
		if property.ItemRef != "" && visitDefinition(property.ItemRef, path) {
			return true
		}
		for _, child := range property.Properties {
			if visitProperty(child, joinPath(path, child.Name)) {
				return true
			}
		}
		for _, child := range property.ItemProperties {
			if visitProperty(child, joinPath(path, child.Name)) {
				return true
			}
		}
		return false
	}
	return visitDefinition(rootType, "")
}

func isFHIRResourceType(typeName string) bool {
	for _, resourceType := range generatedResourceTypes {
		if resourceType == typeName {
			return true
		}
	}
	return false
}

func nestedTraversalSpec(rule NestedTraversalRule, fromType, edgeLabel, toType, pathPrefix string) TraversalSpec {
	backrefs := make([]string, 0, len(rule.Backref))
	for _, backref := range rule.Backref {
		if pathPrefix == "" {
			backrefs = append(backrefs, backref)
		} else {
			backrefs = append(backrefs, pathPrefix+"_"+backref)
		}
	}
	return TraversalSpec{
		FromType:     fromType,
		EdgeLabel:    edgeLabel,
		ToType:       toType,
		Direction:    cloneStrings(rule.Direction),
		Multiplicity: cloneStrings(rule.Multiplicity),
		Backref:      backrefs,
		RegexMatch:   cloneStrings(rule.RegexMatch),
	}
}

func traversalKey(fromType, edgeLabel, toType string) string {
	return fromType + "|" + edgeLabel + "|" + toType
}

func cloneTraversalSpec(spec TraversalSpec) TraversalSpec {
	spec.Direction = cloneStrings(spec.Direction)
	spec.Multiplicity = cloneStrings(spec.Multiplicity)
	spec.Backref = cloneStrings(spec.Backref)
	spec.RegexMatch = cloneStrings(spec.RegexMatch)
	return spec
}
