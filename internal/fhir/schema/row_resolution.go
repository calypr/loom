package schema

import (
	"fmt"
	"strings"
)

type RowCardinality string

const (
	RowCardinalityOne  RowCardinality = "ONE"
	RowCardinalityMany RowCardinality = "MANY"
)

type RowPathShape string

const (
	RowPathScalar  RowPathShape = "SCALAR"
	RowPathObject  RowPathShape = "OBJECT"
	RowPathArray   RowPathShape = "ARRAY"
	RowPathUnknown RowPathShape = "UNKNOWN"
)

// RowPathFacts contains detached structural facts used to mint row choices.
type RowPathFacts struct {
	ResourceType  DefinitionName
	CanonicalPath string
	FHIRType      string
	Cardinality   RowCardinality
	Shape         RowPathShape
	Reference     bool
	Title         string
	Description   string
}

// ResolveRowPath resolves one canonical path against immutable schema metadata.
func (i *Index) ResolveRowPath(resourceType DefinitionName, path string) (RowPathFacts, error) {
	segments, err := parseRowPath(path)
	if err != nil {
		return RowPathFacts{}, err
	}
	if i == nil {
		return RowPathFacts{}, fmt.Errorf("schema index is unavailable")
	}
	root, ok := i.definitions[resourceType]
	if !ok {
		return RowPathFacts{}, fmt.Errorf("schema definition %q is unavailable", resourceType)
	}

	current := root.Elements
	cardinality := RowCardinalityOne
	var terminal Element
	for index, segment := range segments {
		element, found := findElement(current, segment.name)
		if !found {
			return RowPathFacts{}, fmt.Errorf("schema path %q is unknown at %q", path, segment.name)
		}
		terminal = element
		if element.JSONType == JSONTypeArray {
			if !segment.repeated {
				return RowPathFacts{}, fmt.Errorf("schema path %q must mark repeated member %q with []", path, segment.name)
			}
			cardinality = RowCardinalityMany
		} else if segment.repeated {
			return RowPathFacts{}, fmt.Errorf("schema path %q marks non-repeated member %q with []", path, segment.name)
		}
		if index == len(segments)-1 {
			break
		}
		// Reference payload members such as reference and display are fields on
		// the same JSON object. Only an explicit construction route follows the
		// referenced resource itself.
		switch element.JSONType {
		case JSONTypeArray:
			current, err = i.elementChildren(element, true)
		case JSONTypeObject:
			current, err = i.elementChildren(element, false)
		default:
			return RowPathFacts{}, fmt.Errorf("schema path %q traverses scalar member %q", path, segment.name)
		}
		if err != nil {
			return RowPathFacts{}, fmt.Errorf("schema path %q: %w", path, err)
		}
	}

	facts := RowPathFacts{
		ResourceType: resourceType, CanonicalPath: path, Cardinality: cardinality,
		Title: terminal.Title, Description: terminal.Description,
	}
	if len(terminal.ReferenceTargetTypes) != 0 {
		facts.Reference = true
	}
	switch terminal.JSONType {
	case JSONTypeArray:
		facts.Shape = RowPathArray
		facts.FHIRType = string(terminal.ArrayElementType)
		if facts.FHIRType == "" {
			facts.FHIRType = rowFHIRType(terminal.ItemJSONType, terminal.ItemFormat)
		}
		if len(terminal.ReferenceTargetTypes) != 0 {
			facts.Reference = true
		}
	case JSONTypeObject:
		facts.Shape = RowPathObject
		facts.FHIRType = string(terminal.ReferencedType)
		if facts.FHIRType == "" {
			facts.FHIRType = "object"
		}
	case "string", "boolean", "integer", "number":
		facts.Shape = RowPathScalar
		facts.FHIRType = rowFHIRType(terminal.JSONType, terminal.Format)
	default:
		facts.Shape = RowPathUnknown
	}
	if facts.FHIRType == "" || facts.Shape == RowPathUnknown {
		return RowPathFacts{}, fmt.Errorf("schema path %q has an unsupported generated shape", path)
	}
	return facts, nil
}

type rowPathSegment struct {
	name     string
	repeated bool
}

func parseRowPath(path string) ([]rowPathSegment, error) {
	if path == "" || strings.TrimSpace(path) != path {
		return nil, fmt.Errorf("schema path must be non-empty and canonical")
	}
	parts := strings.Split(path, ".")
	segments := make([]rowPathSegment, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || strings.ContainsAny(part, "[]") && !strings.HasSuffix(part, "[]") {
			return nil, fmt.Errorf("schema path %q is not canonical", path)
		}
		segment := rowPathSegment{name: part}
		if strings.HasSuffix(part, "[]") {
			segment.name = strings.TrimSuffix(part, "[]")
			segment.repeated = true
		}
		if segment.name == "" || strings.ContainsAny(segment.name, "[]") {
			return nil, fmt.Errorf("schema path %q is not canonical", path)
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func findElement(elements []Element, name string) (Element, bool) {
	for _, element := range elements {
		if element.Name == name {
			return element, true
		}
	}
	return Element{}, false
}

func (i *Index) elementChildren(element Element, arrayItem bool) ([]Element, error) {
	if arrayItem {
		if element.ArrayElementType != "" {
			definition, ok := i.definitions[element.ArrayElementType]
			if !ok {
				return nil, fmt.Errorf("array element definition %q is unavailable", element.ArrayElementType)
			}
			return definition.Elements, nil
		}
		return element.ArrayElements, nil
	}
	if element.ReferencedType != "" {
		definition, ok := i.definitions[element.ReferencedType]
		if !ok {
			return nil, fmt.Errorf("referenced definition %q is unavailable", element.ReferencedType)
		}
		return definition.Elements, nil
	}
	return element.Elements, nil
}

func rowFHIRType(kind JSONType, format string) string {
	switch format {
	case "date":
		return "date"
	case "date-time":
		return "dateTime"
	case "uri":
		return "uri"
	case "uuid":
		return "uuid"
	case "time":
		return "time"
	case "binary":
		return "base64Binary"
	}
	switch kind {
	case "string":
		return "string"
	case "boolean":
		return "boolean"
	case "integer":
		return "integer"
	case "number":
		return "decimal"
	default:
		return ""
	}
}
