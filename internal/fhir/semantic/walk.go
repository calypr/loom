package semantic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/calypr/loom/internal/fhir/schema"
)

// Occurrence describes one object reached through a generated type reference.
// RawJSON contains a copy of the exact JSON value for that object.
type Occurrence struct {
	RootDefinition     schema.DefinitionName
	ReferencedType     schema.DefinitionName
	CanonicalPath      string
	OwnerPath          string
	RepeatedBoundaries []RepeatedBoundary
	Element            schema.Element
	RawJSON            json.RawMessage
}

// RepeatedBoundary records one concrete array item along an occurrence path.
type RepeatedBoundary struct {
	CanonicalPath string
	OwnerPath     string
	Index         int
}

// Walk reports referenced datatype objects found under root. Paths use [] for
// repeated boundaries and indexes in OwnerPath to retain each concrete owner.
func Walk(index *schema.Index, root schema.DefinitionName, payload []byte, visit func(Occurrence) error) error {
	if index == nil {
		return fmt.Errorf("walk FHIR definition %q: schema index is nil", root)
	}
	if visit == nil {
		return fmt.Errorf("walk FHIR definition %q: visitor is nil", root)
	}
	definition, ok := index.Definition(root)
	if !ok {
		return fmt.Errorf("walk FHIR definition %q: definition is not in the schema index", root)
	}
	object, err := decodeObject(payload, "root")
	if err != nil {
		return err
	}
	return walkElements(index, root, definition.Elements, object, "", "", nil, visit)
}

func walkElements(
	index *schema.Index,
	root schema.DefinitionName,
	elements []schema.Element,
	object map[string]json.RawMessage,
	canonicalPrefix string,
	ownerPrefix string,
	repeated []RepeatedBoundary,
	visit func(Occurrence) error,
) error {
	for _, element := range elements {
		value, exists := object[element.Name]
		if !exists {
			continue
		}
		canonicalPath := joinPath(canonicalPrefix, element.Name)
		ownerPath := joinPath(ownerPrefix, element.Name)
		if isArray(element) {
			if isNull(value) {
				continue
			}
			var items []json.RawMessage
			if err := json.Unmarshal(value, &items); err != nil {
				return fmt.Errorf("walk FHIR path %q: decode array: %w", ownerPath, err)
			}
			for itemIndex, item := range items {
				if !isObjectArrayElement(element) || isNull(item) {
					continue
				}
				itemCanonicalPath := canonicalPath + "[]"
				itemOwnerPath := ownerPath + "[" + strconv.Itoa(itemIndex) + "]"
				boundaries := appendRepeatedBoundary(repeated, RepeatedBoundary{
					CanonicalPath: itemCanonicalPath,
					OwnerPath:     itemOwnerPath,
					Index:         itemIndex,
				})
				if err := walkObjectValue(index, root, element.ArrayElementType, element.ArrayElements, element, item, itemCanonicalPath, itemOwnerPath, boundaries, visit); err != nil {
					return err
				}
			}
			continue
		}
		if !isObject(element) {
			continue
		}
		if isNull(value) {
			continue
		}
		if err := walkObjectValue(index, root, element.ReferencedType, element.Elements, element, value, canonicalPath, ownerPath, repeated, visit); err != nil {
			return err
		}
	}
	return nil
}

func walkObjectValue(
	index *schema.Index,
	root schema.DefinitionName,
	referencedType schema.DefinitionName,
	inlineElements []schema.Element,
	element schema.Element,
	raw json.RawMessage,
	canonicalPath string,
	ownerPath string,
	repeated []RepeatedBoundary,
	visit func(Occurrence) error,
) error {
	object, err := decodeObject(raw, ownerPath)
	if err != nil {
		return err
	}
	if referencedType != "" {
		definition, ok := index.Definition(referencedType)
		if !ok {
			return nil
		}
		occurrence := Occurrence{
			RootDefinition:     root,
			ReferencedType:     referencedType,
			CanonicalPath:      canonicalPath,
			OwnerPath:          ownerPath,
			RepeatedBoundaries: cloneRepeatedBoundaries(repeated),
			Element:            cloneElement(element),
			RawJSON:            append(json.RawMessage(nil), raw...),
		}
		if err := visit(occurrence); err != nil {
			return err
		}
		return walkElements(index, root, definition.Elements, object, canonicalPath, ownerPath, repeated, visit)
	}
	return walkElements(index, root, inlineElements, object, canonicalPath, ownerPath, repeated, visit)
}

func decodeObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("walk FHIR path %q: decode object: %w", path, err)
	}
	if object == nil {
		return nil, fmt.Errorf("walk FHIR path %q: expected an object", path)
	}
	return object, nil
}

func isArray(element schema.Element) bool {
	return element.JSONType == schema.JSONTypeArray
}

func isObject(element schema.Element) bool {
	return element.JSONType == schema.JSONTypeObject || element.ReferencedType != "" || len(element.Elements) > 0
}

func isObjectArrayElement(element schema.Element) bool {
	return element.ArrayElementType != "" || element.ItemJSONType == schema.JSONTypeObject || len(element.ArrayElements) > 0
}

func isNull(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func appendRepeatedBoundary(boundaries []RepeatedBoundary, next RepeatedBoundary) []RepeatedBoundary {
	copyOfBoundaries := make([]RepeatedBoundary, len(boundaries)+1)
	copy(copyOfBoundaries, boundaries)
	copyOfBoundaries[len(boundaries)] = next
	return copyOfBoundaries
}

func cloneRepeatedBoundaries(boundaries []RepeatedBoundary) []RepeatedBoundary {
	return append([]RepeatedBoundary(nil), boundaries...)
}

func cloneElement(element schema.Element) schema.Element {
	element.Elements = cloneElements(element.Elements)
	element.ArrayElements = cloneElements(element.ArrayElements)
	element.RequiredChildren = append([]string(nil), element.RequiredChildren...)
	element.ReferenceTargetTypes = append([]schema.ResourceType(nil), element.ReferenceTargetTypes...)
	return element
}

func cloneElements(elements []schema.Element) []schema.Element {
	if len(elements) == 0 {
		return nil
	}
	cloned := make([]schema.Element, len(elements))
	for i, element := range elements {
		cloned[i] = cloneElement(element)
	}
	return cloned
}
