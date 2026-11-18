package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

type officialFHIRJSONSchema struct {
	ID          string                     `json:"id"`
	Definitions map[string]officialFHIRDef `json:"definitions"`
}

type officialFHIRDef struct {
	Type                 string                          `json:"type"`
	Description          string                          `json:"description"`
	Properties           map[string]officialFHIRProperty `json:"properties"`
	Items                *officialFHIRProperty           `json:"items"`
	Required             []string                        `json:"required"`
	AdditionalProperties bool                            `json:"additionalProperties"`
	Pattern              string                          `json:"pattern"`
	MinLength            *int                            `json:"minLength"`
	MaxLength            *int                            `json:"maxLength"`
	Minimum              *float64                        `json:"minimum"`
	ExclusiveMinimum     *float64                        `json:"exclusiveMinimum"`
	Const                any                             `json:"const"`
	Enum                 []string                        `json:"enum"`
	Format               string                          `json:"format"`
	Ref                  string                          `json:"$ref"`
	OneOf                []officialFHIRProperty          `json:"oneOf"`
}

type officialFHIRProperty struct {
	Type                 string                          `json:"type"`
	Description          string                          `json:"description"`
	Properties           map[string]officialFHIRProperty `json:"properties"`
	Items                *officialFHIRProperty           `json:"items"`
	Required             []string                        `json:"required"`
	AdditionalProperties bool                            `json:"additionalProperties"`
	Pattern              string                          `json:"pattern"`
	MinLength            *int                            `json:"minLength"`
	MaxLength            *int                            `json:"maxLength"`
	Minimum              *float64                        `json:"minimum"`
	ExclusiveMinimum     *float64                        `json:"exclusiveMinimum"`
	Const                any                             `json:"const"`
	Enum                 []string                        `json:"enum"`
	Format               string                          `json:"format"`
	Ref                  string                          `json:"$ref"`
	OneOf                []officialFHIRProperty          `json:"oneOf"`
}

func loadOfficialFHIRJSONSchema(schemaPath, resourceTypesPath string) (*Schema, error) {
	resourceTypes, err := readSupportedFHIRResourceTypes(resourceTypesPath)
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read official FHIR JSON Schema %q: %w", schemaPath, err)
	}
	var official officialFHIRJSONSchema
	if err := json.Unmarshal(contents, &official); err != nil {
		return nil, fmt.Errorf("decode official FHIR JSON Schema %q: %w", schemaPath, err)
	}
	if official.ID != "http://hl7.org/fhir/json-schema/5.0" {
		return nil, fmt.Errorf("FHIR JSON Schema id is %q, want http://hl7.org/fhir/json-schema/5.0", official.ID)
	}
	if official.Definitions == nil {
		return nil, fmt.Errorf("FHIR JSON Schema has no definitions")
	}
	for _, resourceType := range resourceTypes {
		definition, found := official.Definitions[resourceType]
		if !found || definition.Type != "object" {
			return nil, fmt.Errorf("FHIR JSON Schema has no object definition for supported resource %q", resourceType)
		}
	}

	definitionNames, err := officialDefinitionClosure(official.Definitions, resourceTypes)
	if err != nil {
		return nil, err
	}
	schema := &Schema{
		ID:                official.ID,
		FHIRVersion:       "5.0.0",
		RootResourceTypes: resourceTypes,
		Defs:              make(map[string]*Definition, len(definitionNames)),
	}
	digest := sha256.Sum256(contents)
	schema.SourceSHA256 = hex.EncodeToString(digest[:])
	schema.Defs["ResourceList"] = &Definition{
		ID:          "#/definitions/ResourceList",
		Type:        "object",
		Title:       "ResourceList",
		Description: "A contained FHIR resource. Its subtype is preserved in the raw JSON object.",
		Abstract:    true,
		Properties:  make(map[string]*Property),
	}
	for _, name := range definitionNames {
		if name == "ResourceList" {
			continue
		}
		definition := official.Definitions[name]
		if definition.Type != "object" || definition.Properties == nil {
			continue
		}
		goName := officialFHIRGoDefinitionName(name)
		converted := &Definition{
			ID:                   "#/definitions/" + goName,
			Type:                 definition.Type,
			Title:                goName,
			Description:          definition.Description,
			Properties:           make(map[string]*Property, len(definition.Properties)),
			Required:             append([]string(nil), definition.Required...),
			AdditionalProperties: definition.AdditionalProperties,
		}
		propertyNames := make([]string, 0, len(definition.Properties))
		for propertyName := range definition.Properties {
			propertyNames = append(propertyNames, propertyName)
		}
		sort.Strings(propertyNames)
		for _, propertyName := range propertyNames {
			property, err := convertOfficialFHIRProperty(official.Definitions, definition.Properties[propertyName])
			if err != nil {
				return nil, fmt.Errorf("convert FHIR field %s.%s: %w", name, propertyName, err)
			}
			converted.Properties[propertyName] = property
		}
		schema.Defs[goName] = converted
	}
	return schema, nil
}

func readSupportedFHIRResourceTypes(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read supported FHIR resource types %q: %w", path, err)
	}
	seen := make(map[string]struct{})
	var resourceTypes []string
	for _, line := range strings.Split(string(contents), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("supported FHIR resource type %q is listed more than once", name)
		}
		seen[name] = struct{}{}
		resourceTypes = append(resourceTypes, name)
	}
	if len(resourceTypes) == 0 {
		return nil, fmt.Errorf("supported FHIR resource type list is empty")
	}
	sort.Strings(resourceTypes)
	return resourceTypes, nil
}

func officialDefinitionClosure(definitions map[string]officialFHIRDef, resourceTypes []string) ([]string, error) {
	seen := make(map[string]struct{})
	var visitProperty func(officialFHIRProperty) error
	var visitDefinition func(string) error
	visitProperty = func(property officialFHIRProperty) error {
		if property.Ref != "" {
			name, ok := officialFHIRDefinitionName(property.Ref)
			if !ok {
				return fmt.Errorf("unsupported official FHIR JSON Schema reference %q", property.Ref)
			}
			if name == "ResourceList" {
				seen[name] = struct{}{}
				return nil
			}
			return visitDefinition(name)
		}
		if property.Items != nil {
			if err := visitProperty(*property.Items); err != nil {
				return err
			}
		}
		for _, child := range property.Properties {
			if err := visitProperty(child); err != nil {
				return err
			}
		}
		for _, child := range property.OneOf {
			if err := visitProperty(child); err != nil {
				return err
			}
		}
		return nil
	}
	visitDefinition = func(name string) error {
		if _, exists := seen[name]; exists {
			return nil
		}
		definition, found := definitions[name]
		if !found {
			return fmt.Errorf("official FHIR JSON Schema reference names missing definition %q", name)
		}
		seen[name] = struct{}{}
		if definition.Ref != "" {
			if err := visitProperty(officialFHIRProperty{Ref: definition.Ref}); err != nil {
				return err
			}
		}
		if definition.Items != nil {
			if err := visitProperty(*definition.Items); err != nil {
				return err
			}
		}
		for _, child := range definition.Properties {
			if err := visitProperty(child); err != nil {
				return err
			}
		}
		for _, child := range definition.OneOf {
			if err := visitProperty(child); err != nil {
				return err
			}
		}
		return nil
	}
	for _, resourceType := range resourceTypes {
		if err := visitDefinition(resourceType); err != nil {
			return nil, err
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func officialFHIRDefinitionName(ref string) (string, bool) {
	const prefix = "#/definitions/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return strings.NewReplacer("~1", "/", "~0", "~").Replace(name), true
}

func convertOfficialFHIRProperty(definitions map[string]officialFHIRDef, source officialFHIRProperty) (*Property, error) {
	property := &Property{
		Type:             source.Type,
		Items:            nil,
		Properties:       make(map[string]*Property, len(source.Properties)),
		Required:         append([]string(nil), source.Required...),
		Pattern:          source.Pattern,
		MinLength:        source.MinLength,
		MaxLength:        source.MaxLength,
		Minimum:          source.Minimum,
		ExclusiveMinimum: source.ExclusiveMinimum,
		Const:            source.Const,
		EnumValues:       append([]string(nil), source.Enum...),
		Format:           source.Format,
		Title:            "",
		Description:      source.Description,
	}
	if source.Ref != "" {
		name, ok := officialFHIRDefinitionName(source.Ref)
		if !ok {
			return nil, fmt.Errorf("unsupported official FHIR JSON Schema reference %q", source.Ref)
		}
		definition, found := definitions[name]
		if !found {
			return nil, fmt.Errorf("official FHIR JSON Schema reference names missing definition %q", name)
		}
		if name == "ResourceList" {
			property.Type = nil
			property.Ref = "#/definitions/ResourceList"
			return property, nil
		}
		primitiveType := definition.Type
		if name == "xhtml" {
			primitiveType = "string"
		}
		if primitiveType != "" && primitiveType != "object" {
			property.Type = normalizedFHIRPrimitiveType(name, primitiveType)
			property.Primitive = name
			pattern := definition.Pattern
			if property.Type != "string" {
				pattern = ""
			}
			property.Pattern = chooseString(property.Pattern, pattern)
			property.MinLength = chooseInt(property.MinLength, definition.MinLength)
			property.MaxLength = chooseInt(property.MaxLength, definition.MaxLength)
			property.Minimum = chooseFloat(property.Minimum, definition.Minimum)
			property.ExclusiveMinimum = chooseFloat(property.ExclusiveMinimum, definition.ExclusiveMinimum)
			property.EnumValues = append(property.EnumValues, definition.Enum...)
			property.Format = chooseString(property.Format, definition.Format)
			return property, nil
		}
		property.Ref = "#/definitions/" + officialFHIRGoDefinitionName(name)
		property.Type = nil
		return property, nil
	}
	if source.Type == "" && (len(source.Enum) > 0 || source.Const != nil) {
		property.Type = "string"
	}
	if property.Type != "string" {
		property.Pattern = ""
	}
	if source.Type == "array" && source.Items != nil {
		items, err := convertOfficialFHIRProperty(definitions, *source.Items)
		if err != nil {
			return nil, err
		}
		property.Items = items
	}
	for name, child := range source.Properties {
		converted, err := convertOfficialFHIRProperty(definitions, child)
		if err != nil {
			return nil, err
		}
		property.Properties[name] = converted
	}
	if len(property.Properties) == 0 {
		property.Properties = nil
	}
	return property, nil
}

func normalizedFHIRPrimitiveType(name, jsonType string) string {
	switch name {
	case "integer", "positiveInt", "unsignedInt":
		return "integer"
	case "integer64":
		return "string"
	default:
		return jsonType
	}
}

func officialFHIRGoDefinitionName(name string) string {
	var result strings.Builder
	capitalizeNext := true
	for _, character := range name {
		if character == '_' {
			capitalizeNext = true
			continue
		}
		if capitalizeNext {
			character = unicode.ToUpper(character)
			capitalizeNext = false
		}
		result.WriteRune(character)
	}
	return result.String()
}

func chooseString(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

func chooseInt(first, second *int) *int {
	if first != nil {
		return first
	}
	return second
}

func chooseFloat(first, second *float64) *float64 {
	if first != nil {
		return first
	}
	return second
}
