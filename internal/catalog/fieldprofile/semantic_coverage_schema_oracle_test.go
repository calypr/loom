package fieldprofile

import (
	"fmt"
	"github.com/calypr/loom/internal/fhir/schema"
	"sort"
	"strings"
)

type SemanticCodeableConceptTemplate struct {
	ResourceType        string
	CanonicalPath       string
	DeclaringType       string
	DefinitionPath      []string
	ChoiceGroup         string
	Repeated            bool
	RecursiveDefinition string
	RecursivePath       string
}

func DiscoverSemanticCodeableConceptTemplates(index *schema.Index) ([]SemanticCodeableConceptTemplate, error) {
	if index == nil {
		return nil, fmt.Errorf("discover CodeableConcept templates: schema index is nil")
	}
	var templates []SemanticCodeableConceptTemplate
	for _, resourceType := range schema.ResourceTypes() {
		root := schema.DefinitionName(resourceType)
		if _, ok := index.ReadDefinition(root); !ok {
			return nil, fmt.Errorf("discover CodeableConcept templates: generated resource %q is missing from schema index", resourceType)
		}
		walk := semanticCoverageSchemaWalk{
			index:        index,
			resourceType: resourceType,
		}
		if err := walk.definition(root, "", nil, nil); err != nil {
			return nil, fmt.Errorf("discover CodeableConcept templates from %q: %w", resourceType, err)
		}
		for _, template := range walk.templates {
			for _, cycle := range walk.cycles {
				if containsString(template.DefinitionPath, cycle.definition) {
					template.RecursiveDefinition = string(cycle.definition)
					template.RecursivePath = cycle.path
					break
				}
			}
			templates = append(templates, template)
		}
	}
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].ResourceType != templates[j].ResourceType {
			return templates[i].ResourceType < templates[j].ResourceType
		}
		if templates[i].CanonicalPath != templates[j].CanonicalPath {
			return templates[i].CanonicalPath < templates[j].CanonicalPath
		}
		return templates[i].DeclaringType < templates[j].DeclaringType
	})
	return templates, nil
}

type semanticCoverageSchemaCycle struct {
	definition schema.DefinitionName
	path       string
}

type semanticCoverageSchemaWalk struct {
	index        *schema.Index
	resourceType string
	templates    []SemanticCodeableConceptTemplate
	cycles       []semanticCoverageSchemaCycle
}

func (w *semanticCoverageSchemaWalk) definition(
	name schema.DefinitionName,
	prefix string,
	stack []schema.DefinitionName,
	definitionPath []string,
) error {
	view, ok := w.index.ReadDefinition(name)
	if !ok {
		return fmt.Errorf("definition %q is missing from schema index", name)
	}
	stack = append(append([]schema.DefinitionName(nil), stack...), name)
	definitionPath = append(append([]string(nil), definitionPath...), string(name))
	return w.elements(view.Elements(), name, prefix, stack, definitionPath)
}

func (w *semanticCoverageSchemaWalk) elements(
	elements schema.ElementListView,
	declaringType schema.DefinitionName,
	prefix string,
	stack []schema.DefinitionName,
	definitionPath []string,
) error {
	for index := 0; index < elements.Len(); index++ {
		element, ok := elements.At(index)
		if !ok {
			return fmt.Errorf("schema element %d is unavailable in %q", index, declaringType)
		}
		metadata := element.Snapshot()
		path := joinCoveragePath(prefix, element.Name())
		referencedType := element.ReferencedType()
		inline := element.Elements()
		if element.JSONType() == schema.JSONTypeArray {
			path += "[]"
			referencedType = element.ArrayElementType()
			inline = element.ArrayElements()
		}
		if referencedType == "CodeableConcept" {
			w.templates = append(w.templates, SemanticCodeableConceptTemplate{
				ResourceType:   w.resourceType,
				CanonicalPath:  path,
				DeclaringType:  string(declaringType),
				DefinitionPath: append([]string(nil), definitionPath...),
				ChoiceGroup:    metadata.ChoiceGroup,
				Repeated:       strings.Contains(path, "[]"),
			})
			continue
		}
		if referencedType != "" {
			if containsDefinition(stack, referencedType) {
				w.cycles = append(w.cycles, semanticCoverageSchemaCycle{definition: referencedType, path: path})
				continue
			}
			if err := w.definition(referencedType, path, stack, definitionPath); err != nil {
				return err
			}
			continue
		}
		if inline.Len() > 0 {
			if err := w.elements(inline, declaringType, path, stack, definitionPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsDefinition(stack []schema.DefinitionName, candidate schema.DefinitionName) bool {
	for _, definition := range stack {
		if definition == candidate {
			return true
		}
	}
	return false
}

func containsString(values []string, candidate schema.DefinitionName) bool {
	for _, value := range values {
		if value == string(candidate) {
			return true
		}
	}
	return false
}
