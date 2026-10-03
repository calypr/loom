package schema

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	generatedschema "github.com/calypr/loom/generated/fhirschema"
)

type DefinitionName string

type ResourceType string

type JSONType string

type BindingStrength string

const (
	JSONTypeObject JSONType = "object"
	JSONTypeArray  JSONType = "array"
)

// Index is an immutable view of FHIR definition metadata.
type Index struct {
	definitions map[DefinitionName]Definition
}

// DefinitionView reads a definition's elements without exposing its slices.
// Its zero value represents an empty definition.
type DefinitionView struct {
	requiredElements []string
	elements         []Element
}

// ElementListView reads a list of elements without exposing its backing slice.
type ElementListView struct {
	elements []Element
}

// ElementView reads one element without exposing its nested slices.
type ElementView struct {
	element *Element
}

// Definition contains one generated or synthetic FHIR definition.
type Definition struct {
	Name             DefinitionName
	Title            string
	Description      string
	RequiredElements []string
	Elements         []Element
}

// Element contains schema metadata for one property.
type Element struct {
	Name                 string
	JSONType             JSONType
	Format               string
	ReferencedType       DefinitionName
	Elements             []Element
	ItemJSONType         JSONType
	ItemFormat           string
	ArrayElementType     DefinitionName
	ArrayElements        []Element
	ChoiceGroup          string
	ChoiceGroupRequired  bool
	ElementRequired      bool
	RequiredChildren     []string
	BindingStrength      BindingStrength
	BindingURI           string
	BindingVersion       string
	BindingDescription   string
	ReferenceTargetTypes []ResourceType
	Title                string
	Description          string
}

// NewIndex copies definitions into an immutable lookup index.
func NewIndex(definitions []Definition) (*Index, error) {
	index := &Index{definitions: make(map[DefinitionName]Definition, len(definitions))}
	for _, definition := range definitions {
		if strings.TrimSpace(string(definition.Name)) == "" || string(definition.Name) != strings.TrimSpace(string(definition.Name)) {
			return nil, fmt.Errorf("definition name must be non-empty and trimmed")
		}
		if _, exists := index.definitions[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate definition %q", definition.Name)
		}
		if err := validateElementNames(definition.Name, definition.Elements); err != nil {
			return nil, err
		}
		index.definitions[definition.Name] = cloneDefinition(definition)
	}
	for _, definition := range definitions {
		if err := validateElementReferences(definition.Name, index.definitions[definition.Name].Elements, index.definitions); err != nil {
			return nil, err
		}
	}
	return index, nil
}

// Definition returns a copy of one definition. Changes to the returned slices
// do not alter the index.
func (i *Index) Definition(name DefinitionName) (Definition, bool) {
	if i == nil {
		return Definition{}, false
	}
	definition, ok := i.definitions[name]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(definition), true
}

// ReadDefinition returns a read-only view for traversal. Use Definition when
// callers need an independently mutable copy of all metadata.
func (i *Index) ReadDefinition(name DefinitionName) (DefinitionView, bool) {
	if i == nil {
		return DefinitionView{}, false
	}
	definition, ok := i.definitions[name]
	if !ok {
		return DefinitionView{}, false
	}
	return DefinitionView{
		requiredElements: definition.RequiredElements,
		elements:         definition.Elements,
	}, true
}

// RequiredElements returns the names of members required by this definition.
// The returned slice never aliases the immutable index.
func (v DefinitionView) RequiredElements() []string {
	return cloneStrings(v.requiredElements)
}

// Elements returns a read-only view of the definition's elements.
func (v DefinitionView) Elements() ElementListView {
	return ElementListView{elements: v.elements}
}

// Len returns the number of elements in the list.
func (v ElementListView) Len() int {
	return len(v.elements)
}

// At returns a read-only view of the indexed element.
func (v ElementListView) At(index int) (ElementView, bool) {
	return elementAt(v.elements, index)
}

// Name returns the element name.
func (v ElementView) Name() string {
	if v.element == nil {
		return ""
	}
	return v.element.Name
}

// JSONType returns the element's JSON type.
func (v ElementView) JSONType() JSONType {
	if v.element == nil {
		return ""
	}
	return v.element.JSONType
}

// ReferencedType returns the element's referenced definition, if any.
func (v ElementView) ReferencedType() DefinitionName {
	if v.element == nil {
		return ""
	}
	return v.element.ReferencedType
}

// ItemJSONType returns the JSON type of an array item.
func (v ElementView) ItemJSONType() JSONType {
	if v.element == nil {
		return ""
	}
	return v.element.ItemJSONType
}

// ArrayElementType returns the array item's referenced definition, if any.
func (v ElementView) ArrayElementType() DefinitionName {
	if v.element == nil {
		return ""
	}
	return v.element.ArrayElementType
}

// Elements returns a read-only view of inline object elements.
func (v ElementView) Elements() ElementListView {
	if v.element == nil {
		return ElementListView{}
	}
	return ElementListView{elements: v.element.Elements}
}

// ArrayElements returns a read-only view of inline array-item elements.
func (v ElementView) ArrayElements() ElementListView {
	if v.element == nil {
		return ElementListView{}
	}
	return ElementListView{elements: v.element.ArrayElements}
}

// Snapshot returns an independently mutable copy of the element metadata.
func (v ElementView) Snapshot() Element {
	if v.element == nil {
		return Element{}
	}
	return cloneElement(*v.element)
}

// Definitions returns all definitions in name order. Each definition owns
// its slices and can be changed without altering the index.
func (i *Index) Definitions() []Definition {
	if i == nil || len(i.definitions) == 0 {
		return []Definition{}
	}
	names := make([]DefinitionName, 0, len(i.definitions))
	for name := range i.definitions {
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool { return names[a] < names[b] })

	definitions := make([]Definition, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, cloneDefinition(i.definitions[name]))
	}
	return definitions
}

var (
	generatedIndexOnce sync.Once
	generatedIndex     *Index
	generatedIndexErr  error
)

// GeneratedIndex returns the immutable index built from generated FHIR metadata.
func GeneratedIndex() (*Index, error) {
	generatedIndexOnce.Do(func() {
		generatedIndex, generatedIndexErr = newGeneratedIndex()
	})
	return generatedIndex, generatedIndexErr
}

func newGeneratedIndex() (*Index, error) {
	names := make([]string, 0, len(generatedDefinitions))
	for name := range generatedDefinitions {
		names = append(names, name)
	}
	sort.Strings(names)

	definitions := make([]Definition, 0, len(names))
	for _, name := range names {
		generated := generatedDefinitions[name]
		definitions = append(definitions, Definition{
			Name:             DefinitionName(name),
			Title:            generated.Title,
			Description:      generated.Description,
			RequiredElements: cloneStrings(generated.Required),
			Elements:         generatedElements(generated.Properties),
		})
	}
	return NewIndex(definitions)
}

func generatedElements(properties []generatedschema.Property) []Element {
	elements := make([]Element, 0, len(properties))
	for _, property := range properties {
		targetTypes := make([]ResourceType, len(property.ReferenceTargetTypes))
		for i, targetType := range property.ReferenceTargetTypes {
			targetTypes[i] = ResourceType(targetType)
		}
		elements = append(elements, Element{
			Name:                 property.Name,
			JSONType:             JSONType(property.Kind),
			Format:               property.Format,
			ReferencedType:       DefinitionName(property.Ref),
			Elements:             generatedElements(property.Properties),
			ItemJSONType:         JSONType(property.ItemKind),
			ItemFormat:           property.ItemFormat,
			ArrayElementType:     DefinitionName(property.ItemRef),
			ArrayElements:        generatedElements(property.ItemProperties),
			ChoiceGroup:          property.ChoiceGroup,
			ChoiceGroupRequired:  property.ChoiceGroupRequired,
			ElementRequired:      property.ElementRequired,
			RequiredChildren:     cloneStrings(property.RequiredChildren),
			BindingStrength:      BindingStrength(property.BindingStrength),
			BindingURI:           property.BindingURI,
			BindingVersion:       property.BindingVersion,
			BindingDescription:   property.BindingDescription,
			ReferenceTargetTypes: targetTypes,
			Title:                property.Title,
			Description:          property.Description,
		})
	}
	return elements
}

func validateElementNames(definitionName DefinitionName, elements []Element) error {
	seen := make(map[string]struct{}, len(elements))
	for _, element := range elements {
		if strings.TrimSpace(element.Name) == "" || element.Name != strings.TrimSpace(element.Name) {
			return fmt.Errorf("definition %q has an empty or untrimmed element name", definitionName)
		}
		if _, exists := seen[element.Name]; exists {
			return fmt.Errorf("definition %q has duplicate element %q", definitionName, element.Name)
		}
		seen[element.Name] = struct{}{}
		if err := validateElementNames(definitionName, element.Elements); err != nil {
			return err
		}
		if err := validateElementNames(definitionName, element.ArrayElements); err != nil {
			return err
		}
	}
	return nil
}

func validateElementReferences(definitionName DefinitionName, elements []Element, definitions map[DefinitionName]Definition) error {
	return validateElementReferencesAt(definitionName, elements, definitions, "")
}

func validateElementReferencesAt(definitionName DefinitionName, elements []Element, definitions map[DefinitionName]Definition, prefix string) error {
	for _, element := range elements {
		path := element.Name
		if prefix != "" {
			path = prefix + "." + element.Name
		}
		if element.ReferencedType != "" {
			if _, exists := definitions[element.ReferencedType]; !exists {
				return fmt.Errorf("definition %q element %q references missing definition %q", definitionName, path, element.ReferencedType)
			}
		}
		if element.ArrayElementType != "" {
			if _, exists := definitions[element.ArrayElementType]; !exists {
				return fmt.Errorf("definition %q element %q has array element reference to missing definition %q", definitionName, path, element.ArrayElementType)
			}
		}
		if err := validateElementReferencesAt(definitionName, element.Elements, definitions, path); err != nil {
			return err
		}
		if err := validateElementReferencesAt(definitionName, element.ArrayElements, definitions, path+"[]"); err != nil {
			return err
		}
	}
	return nil
}

func elementAt(elements []Element, index int) (ElementView, bool) {
	if index < 0 || index >= len(elements) {
		return ElementView{}, false
	}
	return ElementView{element: &elements[index]}, true
}

func cloneDefinition(definition Definition) Definition {
	definition.RequiredElements = cloneStrings(definition.RequiredElements)
	definition.Elements = cloneElements(definition.Elements)
	return definition
}

func cloneElements(elements []Element) []Element {
	if len(elements) == 0 {
		return nil
	}
	cloned := make([]Element, len(elements))
	for i, element := range elements {
		cloned[i] = cloneElement(element)
	}
	return cloned
}

func cloneElement(element Element) Element {
	element.Elements = cloneElements(element.Elements)
	element.ArrayElements = cloneElements(element.ArrayElements)
	element.RequiredChildren = cloneStrings(element.RequiredChildren)
	element.ReferenceTargetTypes = append([]ResourceType(nil), element.ReferenceTargetTypes...)
	return element
}
