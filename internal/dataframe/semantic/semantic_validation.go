package semantic

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// MaxSemanticTraversalDepth is the maximum number of relationship edges below
// a semantic-plan root. Keeping this small bounds compiler work and graph-query
// fanout until a cost model can safely authorize deeper plans.
const MaxSemanticTraversalDepth = 4

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
	return state.validateChildren(plan.Root, 0, []string{plan.Root.ResourceType}, map[string]bool{})
}

type semanticValidationState struct {
	aliases map[string]string
}

func (s *semanticValidationState) validateChildren(parent SemanticNode, depth int, path []string, edgesOnPath map[string]bool) error {
	for index, child := range parent.Children {
		childDepth := depth + 1
		location := fmt.Sprintf("%s.children[%d]", strings.Join(path, " -> "), index)
		if childDepth > MaxSemanticTraversalDepth {
			return fmt.Errorf("semantic traversal depth %d exceeds maximum %d at %s", childDepth, MaxSemanticTraversalDepth, location)
		}
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
		edgeKey := parent.ResourceType + "\x00" + child.EdgeLabel + "\x00" + child.ResourceType
		// A single self-reference (for example, Patient.link.other) is a
		// normal, finite FHIR relationship and must remain compilable. A route
		// that repeats the same relationship within one requested path is the
		// cycle that can accidentally multiply graph work; reject that while the
		// depth cap protects paths made of distinct relationships.
		if edgesOnPath[edgeKey] {
			return fmt.Errorf("semantic traversal cycle detected at %s: %s", location, strings.Join(append(path, edge), " -> "))
		}
		nextEdges := cloneTraversalPathSet(edgesOnPath)
		nextEdges[edgeKey] = true
		if err := s.validateChildren(child, childDepth, append(path, edge), nextEdges); err != nil {
			return err
		}
	}
	return nil
}

func cloneTraversalPathSet(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}
