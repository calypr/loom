package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func addOfficialEdges(schema *Schema, definitionsPath string) error {
	definitions, version, err := readStructureDefinitions(definitionsPath)
	if err != nil {
		return err
	}
	if version != "5.0.0" {
		return fmt.Errorf("FHIR definitions version is %q, want 5.0.0", version)
	}

	definitionsByType := make(map[string]fhirStructureDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByType[definition.Type] = definition
	}
	resourceTypes := schemaFHIRRootResourceTypes(schema)
	resourceTypeSet := make(map[string]struct{}, len(resourceTypes))
	for _, resourceType := range resourceTypes {
		resourceTypeSet[resourceType] = struct{}{}
	}

	for _, resourceType := range resourceTypes {
		definition, ok := definitionsByType[resourceType]
		if !ok || definition.Kind != "resource" {
			return fmt.Errorf("official FHIR definitions are missing resource %q", resourceType)
		}
		visited := map[string]bool{resourceType: true}
		if err := addDefinitionEdges(schema, definitionsByType, resourceTypeSet, resourceType, definition, nil, nil, false, visited); err != nil {
			return fmt.Errorf("derive edges for %s: %w", resourceType, err)
		}
	}
	return nil
}

func addDefinitionEdges(
	schema *Schema,
	definitions map[string]fhirStructureDefinition,
	resourceTypes map[string]struct{},
	sourceResource string,
	definition fhirStructureDefinition,
	prefix []string,
	labelPrefix []string,
	prefixRepeated bool,
	visited map[string]bool,
) error {
	elements := definition.Snapshot.Elements
	if len(elements) == 0 {
		return nil
	}
	elementsByPath := make(map[string]fhirElementDefinition, len(elements))
	for _, element := range elements {
		elementsByPath[element.Path] = element
	}
	root := definition.Type
	for _, element := range elements[1:] {
		parts := strings.Split(element.Path, ".")
		if len(parts) < 2 || parts[0] != root {
			continue
		}
		path, label, repeated, err := definitionElementPath(root, parts[1:], elementsByPath)
		if err != nil {
			return err
		}
		fullPath := append(append([]string(nil), prefix...), path...)
		fullLabel := append(append([]string(nil), labelPrefix...), label...)
		if prefixRepeated || repeated {
			repeated = true
		}
		if len(element.Type) == 0 {
			continue
		}
		for _, elementType := range element.Type {
			propertyPath := append([]string(nil), fullPath...)
			propertyLabel := append([]string(nil), fullLabel...)
			if strings.Contains(parts[len(parts)-1], "[x]") {
				name := strings.TrimSuffix(parts[len(parts)-1], "[x]") + choiceSuffix(elementType)
				propertyIndex := len(propertyPath) - 1
				if propertyPath[propertyIndex] == "-" {
					propertyIndex--
				}
				propertyPath[propertyIndex] = name
				propertyLabel[len(propertyLabel)-1] = name
			}
			switch elementType.Code {
			case "Reference":
				targets := officialReferenceTargets(elementType.TargetProfile, resourceTypes)
				if definition.Type == "CodeableReference" && len(propertyLabel) > 0 && propertyLabel[len(propertyLabel)-1] == "reference" {
					propertyLabel = propertyLabel[:len(propertyLabel)-1]
				}
				for _, target := range targets.types {
					if err := addOfficialEdge(schema, sourceResource, propertyPath, propertyLabel, target, repeated || hasRepeatedCardinality(element.Max), targets.multiple); err != nil {
						return err
					}
				}
			case "CodeableReference":
				if len(elementType.TargetProfile) > 0 {
					targets := officialReferenceTargets(elementType.TargetProfile, resourceTypes)
					for _, target := range targets.types {
						if err := addCodeableReferenceEdge(schema, sourceResource, propertyPath, propertyLabel, target, repeated || hasRepeatedCardinality(element.Max), targets.multiple); err != nil {
							return err
						}
					}
				}
			}
		}

		if hasDescendantElement(elements, element.Path) {
			continue
		}
		for _, elementType := range element.Type {
			if elementType.Code == "Reference" || elementType.Code == "Extension" ||
				(elementType.Code == "CodeableReference" && len(elementType.TargetProfile) > 0) {
				continue
			}
			child, ok := definitions[elementType.Code]
			if !ok || child.Kind != "complex-type" || visited[elementType.Code] {
				continue
			}
			childPrefix := append([]string(nil), fullPath...)
			childLabels := append([]string(nil), fullLabel...)
			if strings.Contains(parts[len(parts)-1], "[x]") {
				name := strings.TrimSuffix(parts[len(parts)-1], "[x]") + choiceSuffix(elementType)
				propertyIndex := len(childPrefix) - 1
				if childPrefix[propertyIndex] == "-" {
					propertyIndex--
				}
				childPrefix[propertyIndex] = name
				childLabels[len(childLabels)-1] = name
			}
			childVisited := cloneVisited(visited)
			childVisited[elementType.Code] = true
			if err := addDefinitionEdges(
				schema,
				definitions,
				resourceTypes,
				sourceResource,
				child,
				childPrefix,
				childLabels,
				repeated || hasRepeatedCardinality(element.Max),
				childVisited,
			); err != nil {
				return err
			}
		}
	}
	_ = prefixRepeated
	return nil
}

func definitionElementPath(root string, parts []string, elements map[string]fhirElementDefinition) ([]string, []string, bool, error) {
	path := make([]string, 0, len(parts)*2)
	label := make([]string, 0, len(parts))
	repeated := false
	current := root
	for _, part := range parts {
		current += "." + part
		path = append(path, part)
		label = append(label, part)
		element, ok := elements[current]
		if !ok {
			return nil, nil, false, fmt.Errorf("StructureDefinition path %q is missing its parent element", current)
		}
		if hasRepeatedCardinality(element.Max) {
			path = append(path, "-")
			repeated = true
		}
	}
	return path, label, repeated, nil
}

func addOfficialEdge(schema *Schema, source string, path, label []string, target string, repeated, multipleTargets bool) error {
	linkPath := append(append([]string(nil), path...), "reference")
	if err := validateOfficialEdgePath(schema, source, linkPath); err != nil {
		return fmt.Errorf("%s.%s to %s: %w", source, strings.Join(label, "."), target, err)
	}
	joinLabel := officialEdgeLabel(label, target, multipleTargets)
	if joinLabel == "" {
		return fmt.Errorf("FHIR reference in %s has an empty edge label", source)
	}
	definition := schema.Defs[source]
	definition.Links = appendUniqueOfficialLink(definition.Links, Link{
		Rel: joinLabel,
		TargetHints: TargetHints{
			Direction:    []string{"outbound"},
			Multiplicity: []string{officialMultiplicity(repeated)},
			RegexMatch:   []string{target + "/*"},
		},
		TargetSchema:     TargetSchema{Ref: fhirDefinitionBase + "/" + target},
		TemplatePointers: TemplatePointers{ID: "/" + strings.Join(linkPath, "/")},
	})
	return nil
}

func addCodeableReferenceEdge(schema *Schema, source string, path, label []string, target string, repeated, multipleTargets bool) error {
	linkPath := append(append([]string(nil), path...), "reference", "reference")
	if err := validateOfficialEdgePath(schema, source, linkPath); err != nil {
		return fmt.Errorf("%s.%s to %s: %w", source, strings.Join(label, "."), target, err)
	}
	joinLabel := officialEdgeLabel(label, target, multipleTargets)
	if joinLabel == "" {
		return fmt.Errorf("FHIR CodeableReference in %s has an empty edge label", source)
	}
	definition := schema.Defs[source]
	definition.Links = appendUniqueOfficialLink(definition.Links, Link{
		Rel: joinLabel,
		TargetHints: TargetHints{
			Direction:    []string{"outbound"},
			Multiplicity: []string{officialMultiplicity(repeated)},
			RegexMatch:   []string{target + "/*"},
		},
		TargetSchema:     TargetSchema{Ref: fhirDefinitionBase + "/" + target},
		TemplatePointers: TemplatePointers{ID: "/" + strings.Join(linkPath, "/")},
	})
	return nil
}

type officialReferenceTargetSet struct {
	types    []string
	multiple bool
}

func officialReferenceTargets(profiles []string, resourceTypes map[string]struct{}) officialReferenceTargetSet {
	if len(profiles) == 0 {
		targets := make([]string, 0, len(resourceTypes))
		for resourceType := range resourceTypes {
			targets = append(targets, resourceType)
		}
		sort.Strings(targets)
		return officialReferenceTargetSet{types: targets, multiple: true}
	}
	targets := map[string]struct{}{}
	profileTypes := map[string]struct{}{}
	anyResource := false
	for _, profile := range profiles {
		profile = strings.SplitN(profile, "|", 2)[0]
		target := profile[strings.LastIndex(profile, "/")+1:]
		profileTypes[target] = struct{}{}
		if target == "Resource" {
			anyResource = true
			for resourceType := range resourceTypes {
				targets[resourceType] = struct{}{}
			}
			continue
		}
		if _, supported := resourceTypes[target]; supported {
			targets[target] = struct{}{}
		}
	}
	result := make([]string, 0, len(targets))
	for target := range targets {
		result = append(result, target)
	}
	sort.Strings(result)
	return officialReferenceTargetSet{types: result, multiple: anyResource || len(profileTypes) > 1}
}

func officialEdgeLabel(parts []string, target string, multipleTargets bool) string {
	label := strings.Join(parts, "_")
	if multipleTargets {
		label += "_" + target
	}
	return label
}

func hasRepeatedCardinality(max string) bool {
	if max == "*" {
		return true
	}
	count, err := strconv.Atoi(max)
	return err == nil && count > 1
}

func officialMultiplicity(repeated bool) string {
	if repeated {
		return "has_many"
	}
	return "has_one"
}

func appendUniqueOfficialLink(links []Link, candidate Link) []Link {
	for _, link := range links {
		if link.Rel == candidate.Rel && link.TargetSchema.Ref == candidate.TargetSchema.Ref && link.TemplatePointers.ID == candidate.TemplatePointers.ID {
			return links
		}
	}
	return append(links, candidate)
}

func validateOfficialEdgePath(schema *Schema, resource string, path []string) error {
	currentType := resource
	var property *Property
	for _, part := range path {
		if part == "-" {
			if property == nil || propertyType(property) != "array" || property.Items == nil {
				return fmt.Errorf("path crosses a non-array property")
			}
			property = property.Items
			currentType = officialPropertyType(property)
			continue
		}
		definition := schema.Defs[currentType]
		if definition == nil {
			return fmt.Errorf("schema definition %q is missing", currentType)
		}
		property = definition.Properties[part]
		if property == nil {
			return fmt.Errorf("schema property %q is missing from %q", part, currentType)
		}
		currentType = officialPropertyType(property)
	}
	if property == nil || propertyType(property) != "string" {
		return fmt.Errorf("reference path does not end at a string")
	}
	return nil
}

func officialPropertyType(property *Property) string {
	if property.Ref != "" {
		return refName(property.Ref)
	}
	if property.Items != nil && property.Items.Ref != "" {
		return refName(property.Items.Ref)
	}
	return ""
}

func cloneVisited(visited map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(visited)+1)
	for name, present := range visited {
		copy[name] = present
	}
	return copy
}
