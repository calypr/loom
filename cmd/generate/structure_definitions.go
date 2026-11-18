package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type fhirStructureDefinition struct {
	ResourceType string `json:"resourceType"`
	URL          string `json:"url"`
	Version      string `json:"version"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	Kind         string `json:"kind"`
	Derivation   string `json:"derivation"`
	Abstract     bool   `json:"abstract"`
	Snapshot     struct {
		Elements []fhirElementDefinition `json:"element"`
	} `json:"snapshot"`
}

type fhirElementDefinition struct {
	ID               string                     `json:"id"`
	Path             string                     `json:"path"`
	Short            string                     `json:"short"`
	Definition       string                     `json:"definition"`
	ContentReference string                     `json:"contentReference"`
	Min              int                        `json:"min"`
	Max              string                     `json:"max"`
	Type             []fhirElementType          `json:"type"`
	Binding          fhirElementBinding         `json:"binding"`
	Fixed            map[string]json.RawMessage `json:"fixed"`
}

type fhirElementType struct {
	Code          string   `json:"code"`
	Profile       []string `json:"profile"`
	TargetProfile []string `json:"targetProfile"`
	Extension     []struct {
		URL      string `json:"url"`
		ValueURL string `json:"valueUrl"`
	} `json:"extension"`
}

type fhirElementBinding struct {
	Strength    string `json:"strength"`
	ValueSet    string `json:"valueSet"`
	Description string `json:"description"`
}

const fhirDefinitionBase = "http://hl7.org/fhir/StructureDefinition"

func loadStructureDefinitions(path string) (*Schema, error) {
	definitions, version, err := readStructureDefinitions(path)
	if err != nil {
		return nil, err
	}
	if version != "5.0.0" {
		return nil, fmt.Errorf("FHIR definitions version is %q, want 5.0.0", version)
	}

	schema := &Schema{ID: fhirDefinitionBase, Defs: make(map[string]*Definition)}
	for _, structure := range definitions {
		if structure.Kind != "resource" && structure.Kind != "complex-type" {
			continue
		}
		if structure.Derivation != "specialization" || !strings.HasPrefix(structure.URL, "http://hl7.org/fhir/StructureDefinition/") {
			continue
		}
		if len(structure.Snapshot.Elements) == 0 {
			continue
		}
		if err := addStructureDefinition(schema, structure); err != nil {
			return nil, err
		}
	}
	if element := schema.Defs["Element"]; element != nil {
		copy := *element
		copy.Title = "FHIRPrimitiveExtension"
		copy.Description = "FHIR primitive element metadata used by Loom's generated Go model"
		copy.Properties = make(map[string]*Property, len(element.Properties))
		for name, property := range element.Properties {
			propertyCopy := *property
			copy.Properties[name] = &propertyCopy
		}
		schema.Defs["FHIRPrimitiveExtension"] = &copy
	}
	return schema, nil
}

func readStructureDefinitions(path string) ([]fhirStructureDefinition, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	var definitions []fhirStructureDefinition
	version := ""
	add := func(name string, source []byte) error {
		if name == "package.json" {
			var manifest struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal(source, &manifest); err != nil {
				return fmt.Errorf("decode FHIR package manifest: %w", err)
			}
			if manifest.Name != "hl7.fhir.r5.core" {
				return fmt.Errorf("FHIR package name is %q, want hl7.fhir.r5.core", manifest.Name)
			}
			version = manifest.Version
			return nil
		}
		if !strings.HasPrefix(filepath.Base(name), "StructureDefinition-") || filepath.Ext(name) != ".json" {
			return nil
		}
		var structure fhirStructureDefinition
		if err := json.Unmarshal(source, &structure); err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		if structure.ResourceType != "StructureDefinition" || structure.Version != "" && structure.Version != "5.0.0" {
			return fmt.Errorf("unexpected StructureDefinition metadata in %s", name)
		}
		definitions = append(definitions, structure)
		return nil
	}

	if info.IsDir() {
		manifest, err := os.ReadFile(filepath.Join(path, "package.json"))
		if err != nil {
			return nil, "", fmt.Errorf("read FHIR package manifest: %w", err)
		}
		if err := add("package.json", manifest); err != nil {
			return nil, "", err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, "", err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "StructureDefinition-") {
				continue
			}
			source, err := os.ReadFile(filepath.Join(path, entry.Name()))
			if err != nil {
				return nil, "", err
			}
			if err := add(entry.Name(), source); err != nil {
				return nil, "", err
			}
		}
	} else {
		file, err := os.Open(path)
		if err != nil {
			return nil, "", err
		}
		defer file.Close()
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return nil, "", fmt.Errorf("open FHIR package archive: %w", err)
		}
		defer compressed.Close()
		reader := tar.NewReader(compressed)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", fmt.Errorf("read FHIR package archive: %w", err)
			}
			if header.Typeflag != tar.TypeReg {
				continue
			}
			if header.Name != "package/package.json" && !strings.Contains(filepath.Base(header.Name), "StructureDefinition-") {
				continue
			}
			source, err := io.ReadAll(reader)
			if err != nil {
				return nil, "", fmt.Errorf("read %s from FHIR package: %w", header.Name, err)
			}
			name := filepath.Base(header.Name)
			if name == "package.json" {
				if err := add(name, source); err != nil {
					return nil, "", err
				}
			} else if err := add(name, source); err != nil {
				return nil, "", err
			}
		}
	}
	if version == "" || len(definitions) == 0 {
		return nil, "", fmt.Errorf("FHIR package %q is missing package metadata or StructureDefinitions", path)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Type < definitions[j].Type })
	return definitions, version, nil
}

func addStructureDefinition(schema *Schema, structure fhirStructureDefinition) error {
	root := structure.Type
	if root == "" || len(structure.Snapshot.Elements) == 0 {
		return nil
	}
	rootElement := structure.Snapshot.Elements[0]
	if rootElement.Path != root {
		return fmt.Errorf("StructureDefinition %s starts at %q, want %q", structure.URL, rootElement.Path, root)
	}
	rootDefinition := &Definition{
		ID:          structure.URL,
		Type:        "object",
		Title:       structure.Name,
		Description: structure.Description,
		Abstract:    structure.Abstract,
		Properties:  make(map[string]*Property),
	}
	schema.Defs[root] = rootDefinition
	ownedDefinitions := map[string]*Definition{root: rootDefinition}

	pathToDefinition := map[string]string{root: root}
	elements := structure.Snapshot.Elements
	for _, element := range elements[1:] {
		parts := strings.Split(element.Path, ".")
		if len(parts) < 2 || parts[0] != root {
			continue
		}
		propertyName, owner, err := structureElementOwner(root, elements, element.Path, pathToDefinition)
		if err != nil {
			return fmt.Errorf("%s: %w", structure.Type, err)
		}
		definition := schema.Defs[owner]
		if definition == nil {
			return fmt.Errorf("FHIR element %s has missing owner %s", element.Path, owner)
		}
		if strings.Contains(propertyName, "[x]") {
			for _, choice := range element.Type {
				name := strings.TrimSuffix(propertyName, "[x]") + choiceSuffix(choice)
				property := fhirProperty(choice, element)
				property.ChoiceGroup = strings.TrimSuffix(propertyName, "[x]")
				property.ChoiceGroupRequired = element.Min > 0
				putElementProperty(definition, name, property, false)
			}
			continue
		}
		if len(element.Type) == 1 && (isInlineBackbone(element.Type[0].Code) || element.Type[0].Code == "Element" && hasDescendantElement(elements, element.Path)) {
			childName := nestedDefinitionName(owner, propertyName)
			pathToDefinition[element.Path] = childName
			if schema.Defs[childName] == nil {
				childDefinition := &Definition{Type: "object", Title: childName, Description: "FHIR R5 backbone element " + element.Path, Properties: make(map[string]*Property)}
				schema.Defs[childName] = childDefinition
				ownedDefinitions[childName] = childDefinition
			}
			prop := &Property{Ref: fhirDefinitionBase + "/" + childName}
			if element.Max == "*" || maxCount(element.Max) > 1 {
				prop = &Property{Type: "array", Items: &Property{Ref: fhirDefinitionBase + "/" + childName}}
			}
			prop.Title = element.Short
			prop.Description = element.Definition
			prop.ElementRequired = element.Min > 0
			putElementProperty(schema.Defs[owner], propertyName, prop, element.Min > 0)
			continue
		}
		if len(element.Type) == 0 {
			if element.ContentReference != "" {
				referencedPath := strings.TrimPrefix(element.ContentReference, "#")
				referencedDefinition := pathToDefinition[referencedPath]
				if referencedDefinition == "" {
					referencedParts := strings.Split(referencedPath, ".")
					if len(referencedParts) > 1 {
						referencedDefinition = nestedDefinitionName(referencedParts[0], strings.Join(referencedParts[1:], "."))
					}
				}
				if referencedDefinition == "" {
					return fmt.Errorf("FHIR element %s has unresolved contentReference %q", element.Path, element.ContentReference)
				}
				property := &Property{Ref: fhirDefinitionBase + "/" + referencedDefinition}
				if element.Max == "*" || maxCount(element.Max) > 1 {
					property = &Property{Type: "array", Items: &Property{Ref: fhirDefinitionBase + "/" + referencedDefinition}}
				}
				property.Title = element.Short
				property.Description = element.Definition
				property.ElementRequired = element.Min > 0
				putElementProperty(definition, propertyName, property, element.Min > 0)
			}
			continue
		}
		property := fhirProperty(element.Type[0], element)
		if element.Max == "*" || maxCount(element.Max) > 1 {
			property = &Property{
				Type:                 "array",
				Items:                property,
				ElementRequired:      property.ElementRequired,
				BindingStrength:      property.BindingStrength,
				BindingURI:           property.BindingURI,
				BindingVersion:       property.BindingVersion,
				BindingDescription:   property.BindingDescription,
				ReferenceTargetTypes: property.ReferenceTargetTypes,
				Title:                property.Title,
				Description:          property.Description,
			}
		}
		putElementProperty(definition, propertyName, property, element.Min > 0)
	}

	rootDefinition.Properties["resourceType"] = &Property{Type: "string", Const: root}
	rootDefinition.Properties["fhir_comments"] = &Property{}
	rootDefinition.Properties["links"] = &Property{Type: "array", Items: &Property{Ref: "#/links"}}
	for _, definition := range ownedDefinitions {
		definition.Properties["resourceType"] = &Property{Type: "string", Const: definition.Title}
		definition.Properties["fhir_comments"] = &Property{}
		definition.Properties["links"] = &Property{Type: "array", Items: &Property{Ref: "#/links"}}
	}
	return nil
}

func structureElementOwner(root string, elements []fhirElementDefinition, elementPath string, pathToDefinition map[string]string) (string, string, error) {
	parts := strings.Split(elementPath, ".")
	propertyName := parts[len(parts)-1]
	ownerPath := root
	ownerName := root
	for i := 1; i < len(parts)-1; i++ {
		candidatePath := strings.Join(parts[:i+1], ".")
		if name, ok := pathToDefinition[candidatePath]; ok {
			ownerPath = candidatePath
			ownerName = name
		}
	}
	if ownerName == "" {
		return "", "", fmt.Errorf("could not find owner of %s", elementPath)
	}
	_ = ownerPath
	return propertyName, ownerName, nil
}

func nestedDefinitionName(owner, property string) string {
	return owner + toGoName(property)
}

func putElementProperty(definition *Definition, name string, property *Property, required bool) {
	if definition == nil || property == nil {
		return
	}
	if _, exists := definition.Properties[name]; exists {
		return
	}
	definition.Properties[name] = property
	if required {
		definition.Required = append(definition.Required, name)
	}
	if name != "id" && isPrimitiveFHIRProperty(property) {
		metadata := &Property{Ref: fhirDefinitionBase + "/FHIRPrimitiveExtension"}
		if property.Type == "array" {
			metadata = &Property{Type: "array", Items: &Property{Ref: fhirDefinitionBase + "/FHIRPrimitiveExtension"}}
		}
		definition.Properties["_"+name] = metadata
	}
}

func fhirProperty(elementType fhirElementType, element fhirElementDefinition) *Property {
	typeName := elementType.Code
	for _, extension := range elementType.Extension {
		if extension.URL == "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type" && extension.ValueURL != "" {
			typeName = extension.ValueURL
		}
	}
	if slash := strings.LastIndexByte(typeName, '/'); slash >= 0 {
		typeName = typeName[slash+1:]
	}
	property := &Property{}
	property.Title = element.Short
	property.Description = element.Definition
	property.ElementRequired = element.Min > 0
	switch typeName {
	case "base64Binary", "canonical", "code", "date", "dateTime", "id", "instant", "markdown", "oid", "string", "time", "uri", "url", "uuid", "xhtml":
		property.Type = "string"
		switch typeName {
		case "base64Binary":
			property.Format = "binary"
		case "date":
			property.Format = "date"
		case "dateTime":
			property.Format = "date-time"
		case "time":
			property.Format = "time"
		case "uuid":
			property.Format = "uuid"
		}
		if typeName == "id" {
			property.Pattern = "^[A-Za-z0-9\\-.]+$"
			property.MinLength = intPointer(1)
			property.MaxLength = intPointer(64)
		}
	case "boolean":
		property.Type = "boolean"
	case "decimal":
		property.Type = "number"
	case "integer", "integer64", "positiveInt", "unsignedInt":
		property.Type = "integer"
	default:
		property.Ref = fhirDefinitionBase + "/" + typeName
	}
	if len(elementType.TargetProfile) > 0 {
		for _, profile := range elementType.TargetProfile {
			if slash := strings.LastIndexByte(profile, '/'); slash >= 0 {
				property.ReferenceTargetTypes = append(property.ReferenceTargetTypes, profile[slash+1:])
			}
		}
	}
	if len(elementType.Profile) > 0 && typeName == "canonical" {
		property.BindingURI = elementType.Profile[0]
	}
	if element.Binding.ValueSet != "" {
		property.BindingURI = element.Binding.ValueSet
		if separator := strings.LastIndexByte(property.BindingURI, '|'); separator >= 0 {
			property.BindingVersion = property.BindingURI[separator+1:]
			property.BindingURI = property.BindingURI[:separator]
		}
		property.BindingStrength = BindingStrength(element.Binding.Strength)
		property.BindingDescription = element.Binding.Description
	}
	if raw, ok := element.Fixed[typeName]; ok {
		var value any
		if json.Unmarshal(raw, &value) == nil {
			property.Const = value
		}
	}
	return property
}

func isInlineBackbone(typeName string) bool {
	return typeName == "BackboneElement" || typeName == "BackboneType"
}

func hasDescendantElement(elements []fhirElementDefinition, elementPath string) bool {
	prefix := elementPath + "."
	for _, element := range elements {
		if strings.HasPrefix(element.Path, prefix) {
			return true
		}
	}
	return false
}

func choiceSuffix(elementType fhirElementType) string {
	name := elementType.Code
	for _, extension := range elementType.Extension {
		if extension.URL == "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type" && extension.ValueURL != "" {
			name = extension.ValueURL
		}
	}
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func isPrimitiveFHIRProperty(property *Property) bool {
	if property == nil {
		return false
	}
	if property.Type == "array" && property.Items != nil {
		_, ok := property.Items.Type.(string)
		return ok
	}
	_, ok := property.Type.(string)
	return ok
}

func maxCount(value string) int {
	if value == "*" {
		return 2
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 1
	}
	return count
}

func intPointer(value int) *int {
	return &value
}
