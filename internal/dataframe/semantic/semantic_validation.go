package semantic

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// ValidateSemanticGraph checks the schema and structural safety of an already
// constructed semantic plan. It performs no observed-data or authorization
// checks and does not mutate the plan.
func ValidateSemanticGraph(plan SemanticPlan) error {
	if strings.TrimSpace(plan.Root.ResourceType) == "" {
		return fmt.Errorf("semantic graph root resource type is required")
	}
	if plan.Root.Alias != "root" {
		return fmt.Errorf("semantic graph root alias must be %q, got %q", "root", plan.Root.Alias)
	}
	if strings.TrimSpace(plan.Root.EdgeLabel) != "" {
		return fmt.Errorf("semantic graph root must not declare edge label %q", plan.Root.EdgeLabel)
	}
	if plan.Root.MatchMode != "" {
		return fmt.Errorf("semantic graph root must not declare traversal match mode %q", plan.Root.MatchMode)
	}
	if !fhirschema.HasResource(plan.Root.ResourceType) {
		return fmt.Errorf("semantic graph root resource type %q is not represented by the active generated FHIR schema", plan.Root.ResourceType)
	}

	state := semanticValidationState{
		aliases: map[string]string{},
	}
	return state.validateChildren(plan.Root, []string{plan.Root.ResourceType})
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
		// Semantic plans are finite values: every child is explicitly present in
		// the request and therefore has a bounded traversal route. Repeating an
		// edge (including a self-reference) is valid and is left to the explicit
		// physical cost policy rather than an authoring-time hop or cycle cap.
		if err := s.validateChildren(child, append(path, edge)); err != nil {
			return err
		}
	}
	return nil
}
