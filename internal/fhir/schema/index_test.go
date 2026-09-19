package schema

import (
	"strings"
	"testing"
)

func TestNewIndexCopiesDefinitionsAndElements(t *testing.T) {
	targets := []ResourceType{"FutureTarget"}
	definitions := []Definition{
		{
			Name:             "FutureRoot",
			RequiredElements: []string{"identifier"},
			Elements: []Element{{
				Name:                 "identifier",
				JSONType:             JSONTypeArray,
				ArrayElementType:     "Identifier",
				ChoiceGroup:          "identity",
				ChoiceGroupRequired:  true,
				ElementRequired:      true,
				RequiredChildren:     []string{"system"},
				BindingStrength:      "required",
				BindingURI:           "https://example.test/identifiers",
				BindingVersion:       "1",
				BindingDescription:   "Identifier binding",
				ReferenceTargetTypes: targets,
				Title:                "Identifier",
				Description:          "A future identifier",
			}},
		},
		{Name: "Identifier"},
	}

	index, err := NewIndex(definitions)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	targets[0] = "ChangedTarget"
	definitions[0].RequiredElements[0] = "changed"
	definitions[0].Elements[0].Title = "changed"

	definition, ok := index.Definition("FutureRoot")
	if !ok {
		t.Fatal("FutureRoot was not indexed")
	}
	element := definition.Elements[0]
	if definition.RequiredElements[0] != "identifier" || element.Title != "Identifier" || element.ReferenceTargetTypes[0] != "FutureTarget" {
		t.Fatalf("index retained caller mutations: %#v, %#v", definition, element)
	}
	definition.RequiredElements[0] = "changed again"
	definition.Elements[0].ReferenceTargetTypes[0] = "changed again"

	definition, ok = index.Definition("FutureRoot")
	if !ok {
		t.Fatal("FutureRoot disappeared after reading a copy")
	}
	if definition.RequiredElements[0] != "identifier" || definition.Elements[0].ReferenceTargetTypes[0] != "FutureTarget" {
		t.Fatalf("returned mutations changed the index: %#v", definition)
	}
}

func TestNewIndexRejectsDuplicateDefinitionsAndElements(t *testing.T) {
	tests := []struct {
		name        string
		definitions []Definition
	}{
		{
			name:        "duplicate definitions",
			definitions: []Definition{{Name: "Root"}, {Name: "Root"}},
		},
		{
			name: "duplicate elements",
			definitions: []Definition{{Name: "Root", Elements: []Element{
				{Name: "id"},
				{Name: "id"},
			}}},
		},
		{
			name:        "empty definition name",
			definitions: []Definition{{}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewIndex(test.definitions); err == nil {
				t.Fatal("NewIndex accepted invalid definitions")
			}
		})
	}
}

func TestNewIndexRejectsDanglingReferences(t *testing.T) {
	tests := []struct {
		name        string
		definitions []Definition
	}{
		{
			name: "object reference",
			definitions: []Definition{{
				Name: "Root",
				Elements: []Element{{
					Name:           "value",
					ReferencedType: "MissingObject",
				}},
			}},
		},
		{
			name: "array reference",
			definitions: []Definition{{
				Name: "Root",
				Elements: []Element{{
					Name:             "items",
					JSONType:         JSONTypeArray,
					ArrayElementType: "MissingArrayItem",
				}},
			}},
		},
		{
			name: "nested inline object reference",
			definitions: []Definition{{
				Name: "Root",
				Elements: []Element{{
					Name: "backbone",
					Elements: []Element{{
						Name:           "value",
						ReferencedType: "MissingNestedObject",
					}},
				}},
			}},
		},
		{
			name: "nested inline array reference",
			definitions: []Definition{{
				Name: "Root",
				Elements: []Element{{
					Name: "backbone",
					ArrayElements: []Element{{
						Name:             "items",
						JSONType:         JSONTypeArray,
						ArrayElementType: "MissingNestedArrayItem",
					}},
				}},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewIndex(test.definitions); err == nil {
				t.Fatal("NewIndex accepted a dangling definition reference")
			} else if !strings.Contains(err.Error(), "Missing") {
				t.Fatalf("NewIndex error = %v, want missing definition context", err)
			}
		})
	}
}

func TestReadDefinitionUsesImmutableViews(t *testing.T) {
	index, err := NewIndex([]Definition{{
		Name: "Root",
		Elements: []Element{{
			Name: "identifier",
			Elements: []Element{{
				Name: "system",
			}},
		}},
	}})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}

	definition, ok := index.ReadDefinition("Root")
	elements := definition.Elements()
	if !ok || elements.Len() != 1 {
		t.Fatalf("ReadDefinition = %#v, %v", definition, ok)
	}
	element, ok := elements.At(0)
	if !ok || element.Name() != "identifier" || element.Elements().Len() != 1 {
		t.Fatalf("read-only element view did not retain metadata: %#v, %v", element, ok)
	}

	snapshot := element.Snapshot()
	snapshot.Name = "changed"
	snapshot.Elements[0].Name = "changed"
	again, ok := elements.At(0)
	child, childOK := again.Elements().At(0)
	if !ok || !childOK || again.Name() != "identifier" || child.Name() != "system" {
		t.Fatal("mutating an element snapshot changed the read-only index view")
	}
}

func TestGeneratedIndexRetainsSemanticMetadata(t *testing.T) {
	index, err := GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}

	var (
		definitionRequired    bool
		definitionTitle       bool
		definitionDescription bool
		choiceGroup           bool
		choiceRequired        bool
		elementRequired       bool
		bindingStrength       bool
		bindingURI            bool
		bindingVersion        bool
		bindingDescription    bool
		referenceTargets      bool
		elementTitle          bool
		elementDescription    bool
	)
	for _, definition := range index.Definitions() {
		definitionRequired = definitionRequired || len(definition.RequiredElements) > 0
		definitionTitle = definitionTitle || definition.Title != ""
		definitionDescription = definitionDescription || definition.Description != ""
		for _, element := range definition.Elements {
			checkElementMetadata(element, &choiceGroup, &choiceRequired, &elementRequired, &bindingStrength, &bindingURI, &bindingVersion, &bindingDescription, &referenceTargets, &elementTitle, &elementDescription)
		}
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{name: "definition required elements", ok: definitionRequired},
		{name: "definition title", ok: definitionTitle},
		{name: "definition description", ok: definitionDescription},
		{name: "choice group", ok: choiceGroup},
		{name: "required choice group", ok: choiceRequired},
		{name: "required element", ok: elementRequired},
		{name: "binding strength", ok: bindingStrength},
		{name: "binding URI", ok: bindingURI},
		{name: "binding version", ok: bindingVersion},
		{name: "binding description", ok: bindingDescription},
		{name: "reference target types", ok: referenceTargets},
		{name: "element title", ok: elementTitle},
		{name: "element description", ok: elementDescription},
	}
	for _, check := range checks {
		if !check.ok {
			t.Errorf("generated schema omitted %s metadata", check.name)
		}
	}
}

func checkElementMetadata(
	element Element,
	choiceGroup, choiceRequired, elementRequired *bool,
	bindingStrength, bindingURI, bindingVersion, bindingDescription *bool,
	referenceTargets, title, description *bool,
) {
	*choiceGroup = *choiceGroup || element.ChoiceGroup != ""
	*choiceRequired = *choiceRequired || element.ChoiceGroupRequired
	*elementRequired = *elementRequired || element.ElementRequired
	*bindingStrength = *bindingStrength || element.BindingStrength != ""
	*bindingURI = *bindingURI || element.BindingURI != ""
	*bindingVersion = *bindingVersion || element.BindingVersion != ""
	*bindingDescription = *bindingDescription || element.BindingDescription != ""
	*referenceTargets = *referenceTargets || len(element.ReferenceTargetTypes) > 0
	*title = *title || element.Title != ""
	*description = *description || element.Description != ""
	for _, child := range element.Elements {
		checkElementMetadata(child, choiceGroup, choiceRequired, elementRequired, bindingStrength, bindingURI, bindingVersion, bindingDescription, referenceTargets, title, description)
	}
	for _, child := range element.ArrayElements {
		checkElementMetadata(child, choiceGroup, choiceRequired, elementRequired, bindingStrength, bindingURI, bindingVersion, bindingDescription, referenceTargets, title, description)
	}
}
