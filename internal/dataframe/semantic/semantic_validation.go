package semantic

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func ValidateSemanticGraph(root SemanticNode) error {
	if strings.TrimSpace(root.ResourceType) == "" {
		return fmt.Errorf("semantic graph root resource type is required")
	}
	if root.Alias != "root" {
		return fmt.Errorf("semantic graph root alias must be %q, got %q", "root", root.Alias)
	}
	if strings.TrimSpace(root.EdgeLabel) != "" {
		return fmt.Errorf("semantic graph root must not declare edge label %q", root.EdgeLabel)
	}
	if root.MatchMode != "" {
		return fmt.Errorf("semantic graph root must not declare traversal match mode %q", root.MatchMode)
	}
	if !fhirschema.HasResource(root.ResourceType) {
		return fmt.Errorf("semantic graph root resource type %q is not represented by the active generated FHIR schema", root.ResourceType)
	}

	state := semanticValidationState{
		aliases: map[string]string{},
	}
	return state.validateChildren(root, []string{root.ResourceType})
}

type semanticValidationState struct {
	aliases map[string]string
}

func (s *semanticValidationState) validateChildren(parent SemanticNode, path []string) error {
	for index, child := range parent.Children {
		location := fmt.Sprintf("%s.children[%d]", strings.Join(path, " -> "), index)
		alias := strings.TrimSpace(child.Alias)
		if alias == "" {
			return fmt.Errorf("semantic traversal alias is required at %s", location)
		}
		if alias == "root" {
			return fmt.Errorf("semantic traversal alias %q is reserved for the root at %s", alias, location)
		}
		if prior, exists := s.aliases[alias]; exists {
			return fmt.Errorf("semantic traversal alias %q is not unique: used at %s and %s", alias, prior, location)
		}
		s.aliases[alias] = location
		if err := child.MatchMode.Validate(); err != nil {
			return fmt.Errorf("semantic traversal %s -> %s (%s) at %s: %w", parent.ResourceType, child.ResourceType, child.EdgeLabel, location, err)
		}

		if !fhirschema.HasResource(child.ResourceType) {
			return fmt.Errorf("semantic traversal target resource type %q is not represented by the active generated FHIR schema at %s", child.ResourceType, location)
		}
		if strings.TrimSpace(child.EdgeLabel) == "" {
			return fmt.Errorf("semantic traversal edge label is required for %s -> %s at %s", parent.ResourceType, child.ResourceType, location)
		}
		if _, ok := fhirschema.LookupTraversal(parent.ResourceType, child.EdgeLabel, child.ResourceType); !ok {
			return fmt.Errorf("semantic traversal %s -> %s (%s) at %s is not represented by the active generated FHIR schema", parent.ResourceType, child.ResourceType, child.EdgeLabel, location)
		}

		edge := fmt.Sprintf("%s -[%s]-> %s", parent.ResourceType, child.EdgeLabel, child.ResourceType)
		if err := s.validateChildren(child, append(path, edge)); err != nil {
			return err
		}
	}
	return nil
}
