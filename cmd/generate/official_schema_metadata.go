package main

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func augmentOfficialFHIRSchema(schema *Schema, definitionsPath string) error {
	definitions, version, err := readStructureDefinitions(definitionsPath)
	if err != nil {
		return err
	}
	if version != "5.0.0" {
		return fmt.Errorf("FHIR definitions version is %q, want 5.0.0", version)
	}
	byType := make(map[string]fhirStructureDefinition, len(definitions))
	for _, definition := range definitions {
		byType[definition.Type] = definition
	}
	for _, resourceType := range schema.RootResourceTypes {
		definition, found := byType[resourceType]
		if !found || definition.Kind != "resource" {
			return fmt.Errorf("FHIR StructureDefinitions are missing supported resource %q", resourceType)
		}
	}
	for _, definition := range definitions {
		if definition.Kind != "resource" && definition.Kind != "complex-type" {
			continue
		}
		name := officialFHIRGoDefinitionName(definition.Type)
		if schema.Defs[name] == nil {
			continue
		}
		if err := augmentFHIRDefinitionMetadata(schema, name, definition); err != nil {
			return fmt.Errorf("apply StructureDefinition metadata for %s: %w", definition.Type, err)
		}
	}
	return nil
}

func augmentFHIRDefinitionMetadata(schema *Schema, root string, definition fhirStructureDefinition) error {
	for _, element := range definition.Snapshot.Elements[1:] {
		parts := strings.Split(element.Path, ".")
		if len(parts) < 2 || parts[0] != root {
			continue
		}
		owner, err := schemaDefinitionForPath(schema, root, parts[:len(parts)-1])
		if err != nil {
			return fmt.Errorf("%s: %w", element.Path, err)
		}
		if owner == nil {
			continue
		}
		name := parts[len(parts)-1]
		if strings.HasSuffix(name, "[x]") {
			base := strings.TrimSuffix(name, "[x]")
			for _, choice := range element.Type {
				field := base + choiceSuffix(choice)
				property := owner.Properties[field]
				if property == nil {
					return fmt.Errorf("official JSON Schema field %s.%s is missing", owner.Title, field)
				}
				applyElementMetadata(property, base, element)
				property.ChoiceGroup = base
				property.ChoiceGroupRequired = element.Min > 0
				property.ElementRequired = element.Min > 0
				applyFHIRPrimitiveMetadata(property, choice.Code)
				applyReferenceTargets(property, choice.TargetProfile)
			}
			continue
		}
		property := owner.Properties[name]
		if property == nil {
			continue
		}
		applyElementMetadata(property, name, element)
		for _, elementType := range element.Type {
			applyFHIRPrimitiveMetadata(property, elementType.Code)
			applyReferenceTargets(property, elementType.TargetProfile)
		}
	}
	return nil
}

func schemaDefinitionForPath(schema *Schema, root string, parts []string) (*Definition, error) {
	definition := schema.Defs[root]
	if definition == nil {
		return nil, fmt.Errorf("official JSON Schema definition %q is missing", root)
	}
	for _, part := range parts[1:] {
		property := definition.Properties[part]
		if property == nil {
			return nil, fmt.Errorf("official JSON Schema property %s.%s is missing", definition.Title, part)
		}
		name := schemaDefinitionRefName(schema, property.Ref)
		if name == "" && property.Items != nil {
			name = schemaDefinitionRefName(schema, property.Items.Ref)
		}
		if name == "" {
			return nil, fmt.Errorf("official JSON Schema property %s.%s has no structured definition", definition.Title, part)
		}
		definition = schema.Defs[name]
		if definition == nil {
			return nil, fmt.Errorf("official JSON Schema definition %q is missing", name)
		}
	}
	return definition, nil
}

func applyElementMetadata(property *Property, name string, element fhirElementDefinition) {
	if property.Title == "" {
		property.Title = element.Short
	}
	if property.Description == "" {
		property.Description = element.Definition
	}
	if element.Min > 0 {
		property.ElementRequired = true
	}
	if strings.HasSuffix(name, "[x]") {
		property.ChoiceGroup = strings.TrimSuffix(name, "[x]")
		property.ChoiceGroupRequired = element.Min > 0
	}
	if element.Binding.Strength != "" {
		property.BindingStrength = BindingStrength(element.Binding.Strength)
		valueSet := element.Binding.ValueSet
		if separator := strings.LastIndexByte(valueSet, '|'); separator >= 0 {
			property.BindingVersion = valueSet[separator+1:]
			valueSet = valueSet[:separator]
		}
		property.BindingURI = valueSet
		property.BindingDescription = element.Binding.Description
	}
}

func applyFHIRPrimitiveMetadata(property *Property, primitive string) {
	value := property
	if property.Type == "array" && property.Items != nil {
		value = property.Items
	}
	if value.Ref != "" {
		return
	}
	if value.Type == nil {
		return
	}
	if typeName, ok := value.Type.(string); ok {
		normalizedType := normalizedFHIRPrimitiveType(primitive, typeName)
		value.Type = normalizedType
		if normalizedType != "string" {
			return
		}
	}
	value.Primitive = primitive
	if value.Format == "" {
		switch primitive {
		case "date":
			value.Format = "date"
		case "dateTime", "instant":
			value.Format = "date-time"
		case "time":
			value.Format = "time"
		}
	}
}

func applyReferenceTargets(property *Property, profiles []string) {
	if len(profiles) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(property.ReferenceTargetTypes)+len(profiles))
	for _, target := range property.ReferenceTargetTypes {
		seen[target] = struct{}{}
	}
	for _, profile := range profiles {
		target := profile
		if separator := strings.IndexByte(target, '|'); separator >= 0 {
			target = target[:separator]
		}
		if parsed, err := url.Parse(profile); err == nil && parsed.Path != "" {
			target = path.Base(strings.SplitN(parsed.Path, "|", 2)[0])
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		property.ReferenceTargetTypes = append(property.ReferenceTargetTypes, target)
	}
}
